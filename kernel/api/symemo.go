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
	"net/http"

	"github.com/88250/gulu"
	"github.com/gin-gonic/gin"
	"github.com/siyuan-note/logging"
	"github.com/siyuan-note/siyuan/kernel/model"
	"github.com/siyuan-note/siyuan/kernel/model/symemo"
)

var symemoLogError = func(code string, cause error) {
	logging.LogErrorf("SiYuanMemo request failed [code=%s]: %s", code, cause)
}

const (
	symemoInvalidRequestCode = "invalid-request"
	symemoInternalErrorCode  = "internal-error"
)

var symemoSafeMessages = map[string]string{
	symemoInvalidRequestCode:                       "Invalid request.",
	symemoInternalErrorCode:                        "SiYuanMemo could not complete the request.",
	string(symemo.ErrUnsupportedOperation):         "This operation is not supported.",
	string(symemo.ErrInvalidSessionPhase):          "This learning action is not available in the current phase.",
	string(symemo.ErrTargetMismatch):               "The requested Element is not the current learning target.",
	string(symemo.ErrUnsupportedGrade):             "The grade is not supported.",
	string(symemo.ErrAuthoritativeItemUnavailable): "The learning Item is unavailable.",
	string(symemo.ErrUnsupportedAlgorithmState):    "The scheduling state is not supported.",
	string(symemo.ErrInvalidAlgorithmOutput):       "The scheduling result is invalid.",
	string(symemo.ErrDurableWriteFailed):           "The review could not be saved.",
	string(symemo.ErrProjectionRefreshFailed):      "The review was saved, but its schedule could not be refreshed.",
	string(symemo.ErrQueueAdvanceFailed):           "The review was saved, but the learning queue could not advance.",
	string(symemo.ErrHistoryRequiresRepair):        "The review history requires repair.",
	string(symemo.ErrElementNotFound):              "The Element was not found.",
	string(symemo.ErrElementSourceUnavailable):     "The Element source is unavailable.",
	string(symemo.ErrElementSourceAmbiguous):       "The Element source is ambiguous.",
}

func registerSymemoRoutes(ginServer *gin.Engine) {
	ginServer.Handle("POST", "/api/symemo/getElementSubset", model.CheckAuth, model.CheckAdminRole, getSymemoElementSubset)
	ginServer.Handle("POST", "/api/symemo/getElementTree", model.CheckAuth, model.CheckAdminRole, getSymemoElementTree)
	ginServer.Handle("POST", "/api/symemo/getElement", model.CheckAuth, model.CheckAdminRole, getSymemoElement)
	ginServer.Handle("POST", "/api/symemo/getElementSourceDiagnostics", model.CheckAuth, model.CheckAdminRole, getSymemoElementSourceDiagnostics)
	ginServer.Handle("POST", "/api/symemo/startLearning", model.CheckAuth, model.CheckAdminRole, startSymemoLearning)
	ginServer.Handle("POST", "/api/symemo/showAnswer", model.CheckAuth, model.CheckAdminRole, showSymemoAnswer)
	ginServer.Handle("POST", "/api/symemo/gradeItem", model.CheckAuth, model.CheckAdminRole, model.CheckReadonly, gradeSymemoItem)
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

type symemoGradeRequest struct {
	ElementID string `json:"elementId" binding:"required"`
	RawGrade  *int   `json:"rawGrade" binding:"required"`
	EventID   string `json:"eventId" binding:"required"`
}

func getSymemoElementSubset(c *gin.Context) {
	var request symemoSubsetRequest
	if !bindSymemoRequest(c, &request) {
		return
	}
	engine, err := model.GetSymemoEngine()
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	result, err := engine.Query(c, symemo.Query{Kind: symemo.QueryElementSubset, Subset: request.Subset})
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
	engine, err := model.GetSymemoEngine()
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	result, err := engine.Query(c, symemo.Query{Kind: symemo.QueryElementTree, RootElementID: request.RootElementID, IncludeScheduleSummary: request.IncludeScheduleSummary})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result)
}

func getSymemoElement(c *gin.Context) {
	var request symemoElementRequest
	if !bindSymemoRequest(c, &request) {
		return
	}
	engine, err := model.GetSymemoEngine()
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	result, err := engine.Query(c, symemo.Query{Kind: symemo.QueryElement, ElementID: request.ElementID})
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
	engine, err := model.GetSymemoEngine()
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	result, err := engine.Query(c, symemo.Query{Kind: symemo.QueryElementSourceDiagnostics, ElementID: request.ElementID, SourcePath: request.SourcePath})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, struct {
		Diagnostics []symemo.ElementSourceDiagnostic `json:"diagnostics"`
	}{Diagnostics: result.Diagnostics})
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
	engine, err := model.GetSymemoEngine()
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	result, err := engine.RunLearningAction(c, symemo.LearningAction{Kind: symemo.ActionStart})
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
	engine, err := model.GetSymemoEngine()
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	result, err := engine.RunLearningAction(c, symemo.LearningAction{Kind: symemo.ActionShowAnswer, ElementID: request.ElementID})
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
	engine, err := model.GetSymemoEngine()
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	result, err := engine.RunLearningAction(c, symemo.LearningAction{Kind: symemo.ActionGradeItem, ElementID: request.ElementID, RawGrade: request.RawGrade, EventID: request.EventID})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result)
}

func getSymemoCurrentSession(c *gin.Context) {
	if !bindSymemoEmptyRequest(c) {
		return
	}
	engine, err := model.GetSymemoEngine()
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	result, err := engine.Query(c, symemo.Query{Kind: symemo.QueryCurrentSession})
	if err != nil {
		writeSymemoError(c, err)
		return
	}
	writeSymemoSuccess(c, result.Session)
}

func bindSymemoRequest(c *gin.Context, request any) bool {
	if err := c.ShouldBindJSON(request); err != nil {
		writeSymemoFailure(c, symemoSafeMessages[symemoInvalidRequestCode], map[string]any{"errorCode": symemoInvalidRequestCode, "retryable": false})
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
		message, known := symemoSafeMessages[code]
		if !known {
			symemoLogError(symemoInternalErrorCode, err)
			writeSymemoFailure(c, symemoSafeMessages[symemoInternalErrorCode], map[string]any{"errorCode": symemoInternalErrorCode, "retryable": false})
			return
		}
		if domainErr.Cause != nil {
			symemoLogError(code, domainErr.Cause)
		}
		writeSymemoFailure(c, message, map[string]any{"errorCode": domainErr.Code, "retryable": domainErr.Retryable, "reviewAccepted": domainErr.ReviewAccepted, "acceptedEventId": domainErr.AcceptedEventID, "session": domainErr.Session})
		return
	}
	symemoLogError(symemoInternalErrorCode, err)
	writeSymemoFailure(c, symemoSafeMessages[symemoInternalErrorCode], map[string]any{"errorCode": symemoInternalErrorCode, "retryable": false})
}

func writeSymemoFailure(c *gin.Context, message string, data map[string]any) {
	result := gulu.Ret.NewResult()
	result.Code = -1
	result.Msg = message
	result.Data = data
	c.JSON(http.StatusOK, result)
}
