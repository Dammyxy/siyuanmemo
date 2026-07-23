// SiYuan - Refactor your thinking
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/88250/gulu"
	"github.com/gin-gonic/gin"
	"github.com/siyuan-note/logging"
	"github.com/siyuan-note/siyuan/kernel/model"
	"github.com/siyuan-note/siyuan/kernel/model/symemo"
	"github.com/siyuan-note/siyuan/kernel/util"
)

var symemoIsBooted = util.IsBooted
var symemoBootProgress = func() int { return int(util.GetBootProgress()) }
var symemoBootMessage = func(progress int) string {
	return fmt.Sprintf(model.Conf.Language(74), progress)
}
var symemoQuery = model.QuerySymemo
var symemoRunLearningAction = model.RunSymemoLearningAction
var symemoCreateElement = model.CreateSymemoElement

var symemoLogError = func(code string, cause error) {
	logging.LogErrorf("SiYuanMemo request failed [code=%s]: %s", code, cause)
}

const (
	symemoInvalidRequestCode = "invalid-request"
	symemoInternalErrorCode  = "internal-error"
)

var symemoSafeMessages = map[string]string{
	symemoInvalidRequestCode:                          "Invalid request.",
	symemoInternalErrorCode:                           "SiYuanMemo could not complete the request.",
	string(symemo.ErrUnsupportedOperation):            "This operation is not supported.",
	string(symemo.ErrInvalidSessionPhase):             "This learning action is not available in the current phase.",
	string(symemo.ErrTargetMismatch):                  "The requested Element is not the current learning target.",
	string(symemo.ErrUnsupportedGrade):                "The grade is not supported.",
	string(symemo.ErrAuthoritativeElementUnavailable): "The learning Element is unavailable.",
	string(symemo.ErrUnsupportedAlgorithmState):       "The scheduling state is not supported.",
	string(symemo.ErrInvalidAlgorithmOutput):          "The scheduling result is invalid.",
	string(symemo.ErrDurableWriteFailed):              "The review could not be saved.",
	string(symemo.ErrProjectionRefreshFailed):         "The review was saved, but its schedule could not be refreshed.",
	string(symemo.ErrQueueAdvanceFailed):              "The review was saved, but the learning queue could not advance.",
	string(symemo.ErrHistoryRequiresRepair):           "The review history requires repair.",
	string(symemo.ErrInvalidCreateCommand):            "The Element could not be created.",
	string(symemo.ErrElementWritePartial):             "The Element could not be created.",
	string(symemo.ErrElementNotFound):                 "The Element was not found.",
	string(symemo.ErrElementSourceUnavailable):        "The Element source is unavailable.",
	string(symemo.ErrElementSourceAmbiguous):          "The Element source is ambiguous.",
	string(symemo.ErrProjectionRebuildFailed):         "The Element index could not be rebuilt.",
}

func symemoSafeMessage(code string) string {
	if code == string(symemo.ErrAuthoritativeElementUnavailable) && model.Conf != nil {
		return model.Conf.Language(325)
	}
	return symemoSafeMessages[code]
}

func registerSymemoRoutes(ginServer *gin.Engine) {
	ginServer.Handle("POST", "/api/symemo/getElementSubset", model.CheckAuth, model.CheckAdminRole, getSymemoElementSubset)
	ginServer.Handle("POST", "/api/symemo/getElementTree", model.CheckAuth, model.CheckAdminRole, getSymemoElementTree)
	ginServer.Handle("POST", "/api/symemo/getElement", model.CheckAuth, model.CheckAdminRole, getSymemoElement)
	ginServer.Handle("POST", "/api/symemo/getElementSourceDiagnostics", model.CheckAuth, model.CheckAdminRole, getSymemoElementSourceDiagnostics)
	ginServer.Handle("POST", "/api/symemo/createHTMLTopic", model.CheckAuth, model.CheckAdminRole, model.CheckReadonly, createHTMLTopic)
	ginServer.Handle("POST", "/api/symemo/startLearning", model.CheckAuth, model.CheckAdminRole, startSymemoLearning)
	ginServer.Handle("POST", "/api/symemo/showAnswer", model.CheckAuth, model.CheckAdminRole, showSymemoAnswer)
	ginServer.Handle("POST", "/api/symemo/gradeItem", model.CheckAuth, model.CheckAdminRole, model.CheckReadonly, gradeSymemoItem)
	ginServer.Handle("POST", "/api/symemo/nextTopic", model.CheckAuth, model.CheckAdminRole, model.CheckReadonly, nextSymemoTopic)
	ginServer.Handle("POST", "/api/symemo/acceptLearningStage", model.CheckAuth, model.CheckAdminRole, acceptSymemoLearningStage)
	ginServer.Handle("POST", "/api/symemo/declineLearningStage", model.CheckAuth, model.CheckAdminRole, declineSymemoLearningStage)
	ginServer.Handle("POST", "/api/symemo/gradeDrill", model.CheckAuth, model.CheckAdminRole, model.CheckReadonly, gradeSymemoDrill)
	ginServer.Handle("POST", "/api/symemo/stopLearning", model.CheckAuth, model.CheckAdminRole, stopSymemoLearning)
	ginServer.Handle("POST", "/api/symemo/getCurrentLearningSession", model.CheckAuth, model.CheckAdminRole, getSymemoCurrentSession)
}

type symemoSubsetRequest struct {
	Subset string `json:"subset" binding:"required"`
}

type symemoElementRequest struct {
	ElementID string `json:"elementId" binding:"required"`
}

type symemoElementTreeRequest struct {
	RootElementID          string `json:"rootElementId"`
	IncludeScheduleSummary bool   `json:"includeScheduleSummary"`
}

type symemoElementDiagnosticsRequest struct {
	ElementID  string `json:"elementId"`
	SourcePath string `json:"sourcePath"`
}

type symemoCreateHTMLTopicRequest struct {
	Title string `json:"title"`
	HTML  string `json:"html"`
}

type symemoGradeRequest struct {
	ElementID string `json:"elementId" binding:"required"`
	RawGrade  *int   `json:"rawGrade" binding:"required"`
	EventID   string `json:"eventId" binding:"required"`
}

type symemoNextTopicRequest struct {
	ElementID string `json:"elementId" binding:"required"`
	EventID   string `json:"eventId" binding:"required"`
}

type symemoStageRequest struct {
	Stage symemo.LearningStage `json:"stage" binding:"required"`
}

func getSymemoElementSubset(c *gin.Context) {
	var request symemoSubsetRequest
	if !bindSymemoRequest(c, &request) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoQuery(c, symemo.Query{Kind: symemo.QueryElementSubset, Subset: request.Subset})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result)
}

func getSymemoElementTree(c *gin.Context) {
	var request symemoElementTreeRequest
	if !bindSymemoRequest(c, &request) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoQuery(c, symemo.Query{Kind: symemo.QueryElementTree, RootElementID: request.RootElementID, IncludeScheduleSummary: request.IncludeScheduleSummary})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result)
}

func ensureSymemoBooted(c *gin.Context) bool {
	if symemoIsBooted() {
		return true
	}
	writeSymemoFailure(c, symemoBootMessage(symemoBootProgress()), map[string]any{"closeTimeout": 5000})
	return false
}

func getSymemoElement(c *gin.Context) {
	var request symemoElementRequest
	if !bindSymemoRequest(c, &request) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoQuery(c, symemo.Query{Kind: symemo.QueryElement, ElementID: request.ElementID})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, redactSymemoElementAnswer(result.Element))
}

func getSymemoElementSourceDiagnostics(c *gin.Context) {
	var request symemoElementDiagnosticsRequest
	if !bindSymemoRequest(c, &request) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoQuery(c, symemo.Query{Kind: symemo.QueryElementSourceDiagnostics, ElementID: request.ElementID, SourcePath: request.SourcePath})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, struct {
		Diagnostics []symemo.ElementSourceDiagnostic `json:"diagnostics"`
	}{Diagnostics: result.Diagnostics})
}
func createHTMLTopic(c *gin.Context) {
	var request symemoCreateHTMLTopicRequest
	if !bindCreateHTMLTopicRequest(c, &request) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoCreateElement(c, symemo.CreateElementCommand{Kind: symemo.CreateElementAddNewTopic, AddNewTopic: symemo.AddNewTopicCommand{Title: request.Title, HTML: request.HTML}})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result)
}

func redactSymemoElementAnswer(element *symemo.ElementReadView) *symemo.ElementReadView {
	if element == nil || element.Type != "item" {
		return element
	}
	redacted := *element
	redacted.Payload.Answer = ""
	return &redacted
}

func startSymemoLearning(c *gin.Context) {
	if !bindSymemoEmptyRequest(c) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoRunLearningAction(c, symemo.LearningAction{Kind: symemo.ActionStart})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result.Session)
}

func showSymemoAnswer(c *gin.Context) {
	var request symemoElementRequest
	if !bindSymemoRequest(c, &request) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoRunLearningAction(c, symemo.LearningAction{Kind: symemo.ActionShowAnswer, ElementID: request.ElementID})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result.Session)
}

func gradeSymemoItem(c *gin.Context) {
	var request symemoGradeRequest
	if !bindSymemoRequest(c, &request) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoRunLearningAction(c, symemo.LearningAction{Kind: symemo.ActionGradeItem, ElementID: request.ElementID, RawGrade: request.RawGrade, EventID: request.EventID})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result)
}

func nextSymemoTopic(c *gin.Context) {
	var request symemoNextTopicRequest
	if !bindSymemoRequest(c, &request) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoRunLearningAction(c, symemo.LearningAction{Kind: symemo.ActionNextTopic, ElementID: request.ElementID, EventID: request.EventID})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result)
}

func acceptSymemoLearningStage(c *gin.Context) {
	var request symemoStageRequest
	if !bindSymemoRequest(c, &request) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoRunLearningAction(c, symemo.LearningAction{Kind: symemo.ActionAcceptStageTransition, Stage: request.Stage})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result.Session)
}

func declineSymemoLearningStage(c *gin.Context) {
	var request symemoStageRequest
	if !bindSymemoRequest(c, &request) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoRunLearningAction(c, symemo.LearningAction{Kind: symemo.ActionDeclineStageTransition, Stage: request.Stage})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result.Session)
}

func gradeSymemoDrill(c *gin.Context) {
	var request symemoGradeRequest
	if !bindSymemoRequest(c, &request) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoRunLearningAction(c, symemo.LearningAction{Kind: symemo.ActionGradeDrill, ElementID: request.ElementID, RawGrade: request.RawGrade, EventID: request.EventID})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result)
}

func stopSymemoLearning(c *gin.Context) {
	if !bindSymemoEmptyRequest(c) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoRunLearningAction(c, symemo.LearningAction{Kind: symemo.ActionStop})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result.Session)
}

func getSymemoCurrentSession(c *gin.Context) {
	if !bindSymemoEmptyRequest(c) {
		return
	}
	if !ensureSymemoBooted(c) {
		return
	}
	result, err := symemoQuery(c, symemo.Query{Kind: symemo.QueryCurrentSession})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result.Session)
}

func bindSymemoRequest(c *gin.Context, request any) bool {
	if err := c.ShouldBindJSON(request); err != nil {
		writeSymemoFailure(c, symemoSafeMessage(symemoInvalidRequestCode), map[string]any{"errorCode": symemoInvalidRequestCode, "retryable": false})
		return false
	}
	return true
}
func bindCreateHTMLTopicRequest(c *gin.Context, request *symemoCreateHTMLTopicRequest) bool {
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(&raw); err != nil || len(raw) != 2 {
		writeSymemoFailure(c, symemoSafeMessage(symemoInvalidRequestCode), map[string]any{"errorCode": symemoInvalidRequestCode, "retryable": false})
		return false
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeSymemoFailure(c, symemoSafeMessage(symemoInvalidRequestCode), map[string]any{"errorCode": symemoInvalidRequestCode, "retryable": false})
		return false
	}
	title, hasTitle := raw["title"]
	html, hasHTML := raw["html"]
	if !hasTitle || !hasHTML || json.Unmarshal(title, &request.Title) != nil || json.Unmarshal(html, &request.HTML) != nil {
		writeSymemoFailure(c, symemoSafeMessage(symemoInvalidRequestCode), map[string]any{"errorCode": symemoInvalidRequestCode, "retryable": false})
		return false
	}
	return true
}

func bindSymemoEmptyRequest(c *gin.Context) bool {
	if c.Request.ContentLength == 0 {
		return true
	}
	var request map[string]any
	return bindSymemoRequest(c, &request)
}

func writeSymemoSuccess(c *gin.Context, data any) {
	result := gulu.Ret.NewResult()
	result.Data = data
	c.JSON(http.StatusOK, result)
}

func writeSymemoError(c *gin.Context, err error) {
	if domainErr, ok := symemo.AsDomainError(err); ok {
		code := string(domainErr.Code)
		message := symemoSafeMessage(code)
		_, known := symemoSafeMessages[code]
		if !known {
			symemoLogError(symemoInternalErrorCode, err)
			writeSymemoFailure(c, symemoSafeMessage(symemoInternalErrorCode), map[string]any{"errorCode": symemoInternalErrorCode, "retryable": false})
			return
		}
		if domainErr.Cause != nil {
			symemoLogError(code, domainErr.Cause)
		}
		writeSymemoFailure(c, message, map[string]any{"errorCode": domainErr.Code, "retryable": domainErr.Retryable, "createAccepted": domainErr.CreateAccepted, "reviewAccepted": domainErr.ReviewAccepted, "elementId": domainErr.ElementID, "eventId": domainErr.EventID, "acceptedEventId": domainErr.AcceptedEventID, "session": domainErr.Session})
		return
	}
	symemoLogError(symemoInternalErrorCode, err)
	writeSymemoFailure(c, symemoSafeMessage(symemoInternalErrorCode), map[string]any{"errorCode": symemoInternalErrorCode, "retryable": false})
}

func writeSymemoFailure(c *gin.Context, message string, data map[string]any) {
	result := gulu.Ret.NewResult()
	result.Code = -1
	result.Msg = message
	result.Data = data
	c.JSON(http.StatusOK, result)
}
