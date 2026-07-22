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
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/siyuan-note/siyuan/kernel/model"
	"github.com/siyuan-note/siyuan/kernel/model/symemo"
	"github.com/siyuan-note/siyuan/kernel/util"
)

const symemoFixtureElementID = "20260719010101-abcdefg"

func TestSymemoRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := gin.New()
	registerSymemoRoutes(server)
	want := map[string]bool{
		"POST /api/symemo/getElementSubset":            false,
		"POST /api/symemo/getElementTree":              false,
		"POST /api/symemo/getElement":                  false,
		"POST /api/symemo/getElementSourceDiagnostics": false,
		"POST /api/symemo/startLearning":               false,
		"POST /api/symemo/showAnswer":                  false,
		"POST /api/symemo/gradeItem":                   false,
		"POST /api/symemo/nextTopic":                   false,
		"POST /api/symemo/acceptLearningStage":         false,
		"POST /api/symemo/declineLearningStage":        false,
		"POST /api/symemo/gradeDrill":                  false,
		"POST /api/symemo/stopLearning":                false,
		"POST /api/symemo/getCurrentLearningSession":   false,
	}
	for _, route := range server.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
		if strings.Contains(route.Path, "executeCommand") {
			t.Fatalf("generic command route is forbidden: %s", route.Path)
		}
		if strings.Contains(route.Path, "/api/symemo/") && (strings.Contains(strings.ToLower(route.Path), "rebuild") || strings.Contains(strings.ToLower(route.Path), "refresh")) {
			t.Fatalf("projection maintenance route is forbidden: %s", route.Path)
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("missing route %s", route)
		}
	}
}

func TestSymemoAuthoritativeElementUnavailableMessageUsesKernelLanguage(t *testing.T) {
	previousConf, previousLangs := model.Conf, util.Langs
	model.Conf = &model.AppConf{Lang: "symemo-test"}
	util.Langs = map[string]map[int]string{
		"symemo-test": {325: "Localized learning Element unavailable."},
		"en":          {325: "English learning Element unavailable."},
	}
	t.Cleanup(func() {
		model.Conf, util.Langs = previousConf, previousLangs
	})

	if got := symemoSafeMessage(string(symemo.ErrAuthoritativeElementUnavailable)); got != "Localized learning Element unavailable." {
		t.Fatalf("localized authoritative Element unavailable message = %q", got)
	}
	model.Conf = nil
	if got := symemoSafeMessage(string(symemo.ErrAuthoritativeElementUnavailable)); got != "The learning Element is unavailable." {
		t.Fatalf("authoritative Element unavailable fallback = %q", got)
	}
}

func TestSymemoPreBootRequestUsesNativeProgressWithoutRuntimeCall(t *testing.T) {
	previousBooted, previousProgress, previousMessage := symemoIsBooted, symemoBootProgress, symemoBootMessage
	previousQuery := symemoQuery
	symemoIsBooted = func() bool { return false }
	symemoBootProgress = func() int { return 42 }
	symemoBootMessage = func(progress int) string { return fmt.Sprintf("Loading %d%%", progress) }
	queryCalls := 0
	symemoQuery = func(context.Context, symemo.Query) (symemo.QueryResult, error) {
		queryCalls++
		return symemo.QueryResult{}, nil
	}
	t.Cleanup(func() {
		symemoIsBooted, symemoBootProgress, symemoBootMessage = previousBooted, previousProgress, previousMessage
		symemoQuery = previousQuery
	})

	response := invokeSymemoHandler(t, getSymemoElementTree, `{}`)
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			CloseTimeout int `json:"closeTimeout"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != -1 || envelope.Msg != "Loading 42%" || envelope.Data.CloseTimeout != 5000 || queryCalls != 0 {
		t.Fatalf("pre-boot response=%#v queryCalls=%d", envelope, queryCalls)
	}
}

func TestSymemoHandlersUseRuntimeFacadeExactlyOnce(t *testing.T) {
	previousBooted := symemoIsBooted
	previousQuery, previousLearningAction := symemoQuery, symemoRunLearningAction
	symemoIsBooted = func() bool { return true }
	queryCalls, learningActionCalls := 0, 0
	symemoQuery = func(context.Context, symemo.Query) (symemo.QueryResult, error) {
		queryCalls++
		return symemo.QueryResult{}, nil
	}
	symemoRunLearningAction = func(context.Context, symemo.LearningAction) (symemo.LearningResult, error) {
		learningActionCalls++
		return symemo.LearningResult{}, nil
	}
	t.Cleanup(func() {
		symemoIsBooted = previousBooted
		symemoQuery, symemoRunLearningAction = previousQuery, previousLearningAction
	})

	tests := []struct {
		name        string
		handler     gin.HandlerFunc
		body        string
		wantQueries int
		wantActions int
	}{
		{"element subset", getSymemoElementSubset, `{"subset":"due"}`, 1, 0},
		{"element tree", getSymemoElementTree, `{}`, 1, 0},
		{"element", getSymemoElement, `{"elementId":"20260719010101-abcdefg"}`, 1, 0},
		{"source diagnostics", getSymemoElementSourceDiagnostics, `{}`, 1, 0},
		{"start learning", startSymemoLearning, `{}`, 0, 1},
		{"show answer", showSymemoAnswer, `{"elementId":"20260719010101-abcdefg"}`, 0, 1},
		{"grade item", gradeSymemoItem, `{"elementId":"20260719010101-abcdefg","rawGrade":4,"eventId":"event-1"}`, 0, 1},
		{"next topic", nextSymemoTopic, `{"elementId":"20260719020101-topicaa","eventId":"event-2"}`, 0, 1},
		{"accept learning stage", acceptSymemoLearningStage, `{"stage":"pending"}`, 0, 1},
		{"decline learning stage", declineSymemoLearningStage, `{"stage":"finalDrill"}`, 0, 1},
		{"grade drill", gradeSymemoDrill, `{"elementId":"20260719010101-abcdefg","rawGrade":4,"eventId":"event-3"}`, 0, 1},
		{"stop learning", stopSymemoLearning, `{}`, 0, 1},
		{"current session", getSymemoCurrentSession, `{}`, 1, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queryCalls, learningActionCalls = 0, 0
			response := invokeSymemoHandler(t, test.handler, test.body)
			if code := envelopeCode(t, response); code != 0 {
				t.Fatalf("envelope code = %d body=%s", code, response.Body.String())
			}
			if queryCalls != test.wantQueries || learningActionCalls != test.wantActions {
				t.Fatalf("facade calls query=%d action=%d, want query=%d action=%d", queryCalls, learningActionCalls, test.wantQueries, test.wantActions)
			}
		})
	}
}

func TestSymemoReadRoutesDoNotMutateReviewsOrSession(t *testing.T) {
	storageRoot := installSymemoFixtureWorkspace(t)
	beforeReviews := snapshotSymemoDirectory(t, filepath.Join(storageRoot, "reviews"))
	beforeSession := envelopeData(t, invokeSymemoHandler(t, getSymemoCurrentSession, `{}`))

	for _, request := range []struct {
		handler gin.HandlerFunc
		body    string
	}{
		{getSymemoElementSubset, `{"subset":"due"}`},
		{getSymemoElementTree, `{}`},
		{getSymemoElement, `{"elementId":"` + symemoFixtureElementID + `"}`},
		{getSymemoElementSourceDiagnostics, `{}`},
		{getSymemoCurrentSession, `{}`},
	} {
		response := invokeSymemoHandler(t, request.handler, request.body)
		if code := envelopeCode(t, response); code != 0 {
			t.Fatalf("read route failed: %s", response.Body.String())
		}
	}
	afterSession := envelopeData(t, invokeSymemoHandler(t, getSymemoCurrentSession, `{}`))
	if string(afterSession) != string(beforeSession) {
		t.Fatalf("read routes changed session\nafter=%s\nbefore=%s", afterSession, beforeSession)
	}
	afterReviews := snapshotSymemoDirectory(t, filepath.Join(storageRoot, "reviews"))
	if string(afterReviews) != string(beforeReviews) {
		t.Fatal("read routes changed review history")
	}
}

func TestSymemoAcceptedTriggerRecoveryAndSubsequentLatch(t *testing.T) {
	previousBooted := symemoIsBooted
	previousQuery, previousLearningAction := symemoQuery, symemoRunLearningAction
	symemoIsBooted = func() bool { return true }
	eventID := "20260721120000-api-latch"
	session := &symemo.SessionState{Status: symemo.SessionActive, Phase: symemo.PhaseAnswer, PendingAcceptedEventID: eventID}
	symemoRunLearningAction = func(context.Context, symemo.LearningAction) (symemo.LearningResult, error) {
		return symemo.LearningResult{}, &symemo.DomainError{Code: symemo.ErrProjectionRefreshFailed, Retryable: true, ReviewAccepted: true, AcceptedEventID: eventID, Session: session}
	}
	symemoQuery = func(context.Context, symemo.Query) (symemo.QueryResult, error) {
		return symemo.QueryResult{}, &symemo.DomainError{Code: symemo.ErrProjectionRebuildFailed}
	}
	t.Cleanup(func() {
		symemoIsBooted = previousBooted
		symemoQuery, symemoRunLearningAction = previousQuery, previousLearningAction
	})

	trigger := invokeSymemoHandler(t, gradeSymemoItem, `{"elementId":"`+symemoFixtureElementID+`","rawGrade":4,"eventId":"`+eventID+`"}`)
	var triggerEnvelope struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ErrorCode      string               `json:"errorCode"`
			Retryable      bool                 `json:"retryable"`
			ReviewAccepted bool                 `json:"reviewAccepted"`
			AcceptedEvent  string               `json:"acceptedEventId"`
			Session        *symemo.SessionState `json:"session"`
		} `json:"data"`
	}
	if err := json.Unmarshal(trigger.Body.Bytes(), &triggerEnvelope); err != nil {
		t.Fatal(err)
	}
	if triggerEnvelope.Code != -1 || triggerEnvelope.Msg != "The review was saved, but its schedule could not be refreshed." || triggerEnvelope.Data.ErrorCode != string(symemo.ErrProjectionRefreshFailed) || !triggerEnvelope.Data.Retryable || !triggerEnvelope.Data.ReviewAccepted || triggerEnvelope.Data.AcceptedEvent != eventID || triggerEnvelope.Data.Session == nil || triggerEnvelope.Data.Session.PendingAcceptedEventID != eventID {
		t.Fatalf("accepted trigger envelope = %#v", triggerEnvelope)
	}

	latched := invokeSymemoHandler(t, getSymemoCurrentSession, `{}`)
	assertSymemoFailure(t, latched, string(symemo.ErrProjectionRebuildFailed), "The Element index could not be rebuilt.")
	if strings.Contains(latched.Body.String(), eventID) || strings.Contains(latched.Body.String(), "completed") {
		t.Fatalf("latched response exposed prior data: %s", latched.Body.String())
	}
}

func TestSymemoNextTopicTransportUsesElementUnavailableAndQueueAdvanceContracts(t *testing.T) {
	previousBooted := symemoIsBooted
	previousLearningAction := symemoRunLearningAction
	symemoIsBooted = func() bool { return true }
	session := &symemo.SessionState{
		SessionID: "topic-transport-session",
		Status:    symemo.SessionActive,
		Stage:     symemo.StageOutstanding,
		Phase:     symemo.PhaseQuestion,
		Current:   &symemo.ReviewTarget{Kind: "element.topic", ElementID: symemoFixtureElementID},
	}
	symemoRunLearningAction = func(_ context.Context, action symemo.LearningAction) (symemo.LearningResult, error) {
		switch action.EventID {
		case "topic-target-unavailable":
			return symemo.LearningResult{}, &symemo.DomainError{Code: symemo.ErrAuthoritativeElementUnavailable, Session: session}
		case "topic-queue-advance-failed":
			failed := *session
			failed.PendingAcceptedEventID = action.EventID
			return symemo.LearningResult{}, &symemo.DomainError{Code: symemo.ErrQueueAdvanceFailed, Retryable: true, ReviewAccepted: true, AcceptedEventID: action.EventID, Session: &failed}
		default:
			return symemo.LearningResult{}, nil
		}
	}
	t.Cleanup(func() {
		symemoIsBooted = previousBooted
		symemoRunLearningAction = previousLearningAction
	})

	unavailable := invokeSymemoHandler(t, nextSymemoTopic, `{"elementId":"`+symemoFixtureElementID+`","eventId":"topic-target-unavailable"}`)
	assertSymemoFailure(t, unavailable, string(symemo.ErrAuthoritativeElementUnavailable), "The learning Element is unavailable.")

	advance := invokeSymemoHandler(t, nextSymemoTopic, `{"elementId":"`+symemoFixtureElementID+`","eventId":"topic-queue-advance-failed"}`)
	assertSymemoFailure(t, advance, string(symemo.ErrQueueAdvanceFailed), "The review was saved, but the learning queue could not advance.")
	var envelope struct {
		Data struct {
			ReviewAccepted bool                 `json:"reviewAccepted"`
			AcceptedEvent  string               `json:"acceptedEventId"`
			Session        *symemo.SessionState `json:"session"`
		} `json:"data"`
	}
	if err := json.Unmarshal(advance.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Data.ReviewAccepted || envelope.Data.AcceptedEvent != "topic-queue-advance-failed" || envelope.Data.Session == nil || envelope.Data.Session.PendingAcceptedEventID != "topic-queue-advance-failed" {
		t.Fatalf("Topic queue-advance envelope = %#v", envelope)
	}
}

func TestSymemoHandlersRemainTransportOnly(t *testing.T) {
	data, err := os.ReadFile("symemo.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, forbidden := range []string{"SchedulingLedger", "filelock.", "memo.db", "/api/symemo/executeCommand", "github.com/siyuan-note/riff", "GetSymemoEngine", "kernel-not-ready", "*symemo.Engine", "symemo.NewEngine(", "model.InitSymemo("} {
		if strings.Contains(source, forbidden) {
			t.Errorf("transport adapter contains forbidden workflow dependency %q", forbidden)
		}
	}
	registrations := []string{
		`ginServer.Handle("POST", "/api/symemo/getElementSubset", model.CheckAuth, model.CheckAdminRole, getSymemoElementSubset)`,
		`ginServer.Handle("POST", "/api/symemo/getElementTree", model.CheckAuth, model.CheckAdminRole, getSymemoElementTree)`,
		`ginServer.Handle("POST", "/api/symemo/getElement", model.CheckAuth, model.CheckAdminRole, getSymemoElement)`,
		`ginServer.Handle("POST", "/api/symemo/getElementSourceDiagnostics", model.CheckAuth, model.CheckAdminRole, getSymemoElementSourceDiagnostics)`,
		`ginServer.Handle("POST", "/api/symemo/startLearning", model.CheckAuth, model.CheckAdminRole, startSymemoLearning)`,
		`ginServer.Handle("POST", "/api/symemo/showAnswer", model.CheckAuth, model.CheckAdminRole, showSymemoAnswer)`,
		`ginServer.Handle("POST", "/api/symemo/gradeItem", model.CheckAuth, model.CheckAdminRole, model.CheckReadonly, gradeSymemoItem)`,
		`ginServer.Handle("POST", "/api/symemo/nextTopic", model.CheckAuth, model.CheckAdminRole, model.CheckReadonly, nextSymemoTopic)`,
		`ginServer.Handle("POST", "/api/symemo/acceptLearningStage", model.CheckAuth, model.CheckAdminRole, acceptSymemoLearningStage)`,
		`ginServer.Handle("POST", "/api/symemo/declineLearningStage", model.CheckAuth, model.CheckAdminRole, declineSymemoLearningStage)`,
		`ginServer.Handle("POST", "/api/symemo/gradeDrill", model.CheckAuth, model.CheckAdminRole, model.CheckReadonly, gradeSymemoDrill)`,
		`ginServer.Handle("POST", "/api/symemo/stopLearning", model.CheckAuth, model.CheckAdminRole, stopSymemoLearning)`,
		`ginServer.Handle("POST", "/api/symemo/getCurrentLearningSession", model.CheckAuth, model.CheckAdminRole, getSymemoCurrentSession)`,
	}
	for _, registration := range registrations {
		if !strings.Contains(source, registration) {
			t.Errorf("missing protected route registration %q", registration)
		}
	}
}

func TestSymemoTransportEnvelopeAndAnswerRedaction(t *testing.T) {
	installSymemoFixtureWorkspace(t)

	due := invokeSymemoHandler(t, getSymemoElementSubset, `{"subset":"due"}`)
	if code := envelopeCode(t, due); code != 0 {
		t.Fatalf("due envelope code = %d", code)
	}
	dueData := envelopeData(t, due)
	if strings.Contains(string(dueData), `"answer"`) {
		t.Fatalf("due response reveals answer: %s", dueData)
	}

	start := invokeSymemoHandler(t, startSymemoLearning, `{}`)
	if code := envelopeCode(t, start); code != 0 {
		t.Fatalf("start envelope code = %d", code)
	}
	startData := envelopeData(t, start)
	if strings.Contains(string(startData), `"answer"`) {
		t.Fatalf("question-phase response reveals answer: %s", startData)
	}

	mismatch := invokeSymemoHandler(t, showSymemoAnswer, `{"elementId":"other"}`)
	if code := envelopeCode(t, mismatch); code != -1 {
		t.Fatalf("target mismatch envelope code = %d", code)
	}
	var failure struct {
		ErrorCode string `json:"errorCode"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(envelopeData(t, mismatch), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.ErrorCode != string(symemo.ErrTargetMismatch) || failure.Retryable {
		t.Fatalf("target mismatch payload = %#v", failure)
	}

	shown := invokeSymemoHandler(t, showSymemoAnswer, `{"elementId":"`+symemoFixtureElementID+`"}`)
	if code := envelopeCode(t, shown); code != 0 || !strings.Contains(string(envelopeData(t, shown)), `"answer"`) {
		t.Fatalf("show answer envelope = %s", shown.Body.String())
	}

	graded := invokeSymemoHandler(t, gradeSymemoItem, `{"elementId":"`+symemoFixtureElementID+`","rawGrade":4,"eventId":"20260719090000-api-review"}`)
	if code := envelopeCode(t, graded); code != 0 {
		t.Fatalf("grade envelope = %s", graded.Body.String())
	}
	var gradeData struct {
		ReviewAccepted bool `json:"reviewAccepted"`
		RawGrade       int  `json:"rawGrade"`
	}
	if err := json.Unmarshal(envelopeData(t, graded), &gradeData); err != nil {
		t.Fatal(err)
	}
	if !gradeData.ReviewAccepted || gradeData.RawGrade != 4 {
		t.Fatalf("grade payload = %#v", gradeData)
	}

	current := invokeSymemoHandler(t, getSymemoCurrentSession, `{}`)
	currentData := string(envelopeData(t, current))
	if code := envelopeCode(t, current); code != 0 || !strings.Contains(currentData, `"phase":"confirmation"`) || !strings.Contains(currentData, `"stage":"pending"`) {
		t.Fatalf("current session envelope = %s", current.Body.String())
	}
}

func TestSymemoElementTreeRoutes(t *testing.T) {
	installSymemoFixtureWorkspace(t)
	response := invokeSymemoHandler(t, getSymemoElementTree, `{"rootElementId":"20260720010101-rootaaa","includeScheduleSummary":true}`)
	if code := envelopeCode(t, response); code != 0 {
		t.Fatalf("tree envelope code = %d body=%s", code, response.Body.String())
	}
	var data struct {
		Nodes []symemo.ElementTreeNode `json:"nodes"`
	}
	if err := json.Unmarshal(envelopeData(t, response), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Nodes) != 1 || data.Nodes[0].ElementID != "20260720010101-rootaaa" {
		t.Fatalf("tree response nodes = %#v", data.Nodes)
	}
	if len(data.Nodes[0].Children) == 0 || data.Nodes[0].Children[0].ElementID != "20260720010102-itemaaa" {
		t.Fatalf("tree children = %#v", data.Nodes[0].Children)
	}
	foundFuture := false
	foundInvalidBlock := false
	for _, child := range data.Nodes[0].Children {
		if child.ElementID == "20260720010106-futurex" && child.SupportStatus == symemo.SupportStatusUnsupportedReadOnly {
			foundFuture = true
		}
		if child.ElementID == "20260720010105-badblok" && child.MaterialSourceDiagnostic != nil && child.MaterialSourceStatus == nil {
			foundInvalidBlock = true
		}
	}
	if !foundFuture || !foundInvalidBlock {
		t.Fatalf("tree response missing future or blocked material child: %#v", data.Nodes[0].Children)
	}
}

func TestSymemoElementTreeMissingScopedRootUsesStableDomainError(t *testing.T) {
	installSymemoFixtureWorkspace(t)
	logs := captureSymemoErrorLogs(t)
	response := invokeSymemoHandler(t, getSymemoElementTree, `{"rootElementId":"20260720999999-missing"}`)
	assertSymemoFailure(t, response, "element-not-found", "The Element was not found.")
	if len(*logs) != 0 {
		t.Fatalf("missing scoped root logs=%#v", *logs)
	}
}

func TestSymemoElementReadRoutes(t *testing.T) {
	storageRoot := installSymemoFixtureWorkspace(t)
	elementsRoot := filepath.Join(storageRoot, "elements")
	unavailableID := "20260720030201-unavail"
	if err := os.WriteFile(filepath.Join(elementsRoot, unavailableID+".sme"), []byte(`{"spec":1,"id":`), 0644); err != nil {
		t.Fatal(err)
	}
	duplicateID := "20260720010102-itemaaa"
	duplicate := `{"spec":1,"id":"` + duplicateID + `","type":"item","processingState":"processed","payloadSpec":1,"payload":{"kind":"qa","prompt":"duplicate","answer":"hidden"},"children":[]}`
	if err := os.WriteFile(filepath.Join(elementsRoot, duplicateID+".sme"), []byte(duplicate), 0644); err != nil {
		t.Fatal(err)
	}
	restartSymemoRuntime(t)

	known := invokeSymemoHandler(t, getSymemoElement, `{"elementId":"`+symemoFixtureElementID+`"}`)
	if code := envelopeCode(t, known); code != 0 {
		t.Fatalf("known Element response = %s", known.Body.String())
	}
	knownData := envelopeData(t, known)
	if !strings.Contains(string(knownData), `"id":"`+symemoFixtureElementID+`"`) || !strings.Contains(string(knownData), `"supportStatus":"supported"`) || strings.Contains(string(knownData), `"answer"`) {
		t.Fatalf("known Element response was malformed or revealed its answer: %s", knownData)
	}

	future := invokeSymemoHandler(t, getSymemoElement, `{"elementId":"20260720010106-futurex"}`)
	futureData := envelopeData(t, future)
	if code := envelopeCode(t, future); code != 0 || !strings.Contains(string(futureData), `"supportStatus":"unsupportedReadOnly"`) || !strings.Contains(string(futureData), `"kept":true`) {
		t.Fatalf("future Element response = %s", future.Body.String())
	}

	invalid := invokeSymemoHandler(t, getSymemoElement, `{"elementId":"20260720010105-badblok"}`)
	invalidData := envelopeData(t, invalid)
	if code := envelopeCode(t, invalid); code != 0 || !strings.Contains(string(invalidData), `"code":"invalid-block-reference"`) || strings.Contains(string(invalidData), `"materialSourceStatus"`) {
		t.Fatalf("invalid material Element response = %s", invalid.Body.String())
	}

	missing := invokeSymemoHandler(t, getSymemoElement, `{"elementId":"20260720999999-missing"}`)
	assertSymemoFailure(t, missing, string(symemo.ErrElementNotFound), "The Element was not found.")
	unavailable := invokeSymemoHandler(t, getSymemoElement, `{"elementId":"`+unavailableID+`"}`)
	assertSymemoFailure(t, unavailable, string(symemo.ErrElementSourceUnavailable), "The Element source is unavailable.")
	ambiguous := invokeSymemoHandler(t, getSymemoElement, `{"elementId":"`+duplicateID+`"}`)
	assertSymemoFailure(t, ambiguous, string(symemo.ErrElementSourceAmbiguous), "The Element source is ambiguous.")
}

func TestSymemoElementDiagnosticRoutes(t *testing.T) {
	storageRoot := installSymemoFixtureWorkspace(t)
	elementsRoot := filepath.Join(storageRoot, "elements")
	duplicateID := "20260720010102-itemaaa"
	duplicate := `{"spec":1,"id":"` + duplicateID + `","type":"item","processingState":"processed","payloadSpec":1,"payload":{"kind":"qa","prompt":"duplicate","answer":"hidden"},"children":[]}`
	if err := os.WriteFile(filepath.Join(elementsRoot, duplicateID+".sme"), []byte(duplicate), 0644); err != nil {
		t.Fatal(err)
	}
	restartSymemoRuntime(t)

	filtered := invokeSymemoHandler(t, getSymemoElementSourceDiagnostics, `{"elementId":"`+duplicateID+`"}`)
	filteredData := envelopeData(t, filtered)
	if code := envelopeCode(t, filtered); code != 0 || !strings.Contains(string(filteredData), `"code":"duplicate-element-id"`) || !strings.Contains(string(filteredData), `"relatedPaths"`) {
		t.Fatalf("filtered diagnostics response = %s", filtered.Body.String())
	}

	none := invokeSymemoHandler(t, getSymemoElementSourceDiagnostics, `{"sourcePath":"../../outside.sme"}`)
	if noneData := envelopeData(t, none); envelopeCode(t, none) != 0 || string(noneData) != `{"diagnostics":[]}` {
		t.Fatalf("unmatched diagnostics response = %s", none.Body.String())
	}
}

func TestSymemoMalformedRequestIsSanitizedWithoutLogging(t *testing.T) {
	logs := captureSymemoErrorLogs(t)
	response := invokeSymemoHandler(t, showSymemoAnswer, `{"elementId":`)
	assertSymemoFailure(t, response, "invalid-request", "Invalid request.")
	if strings.Contains(response.Body.String(), "unexpected EOF") || len(*logs) != 0 {
		t.Fatalf("malformed response=%s logs=%#v", response.Body.String(), *logs)
	}
}

func TestSymemoExpectedDomainFailureUsesStableMessageWithoutLogging(t *testing.T) {
	installSymemoFixtureWorkspace(t)
	logs := captureSymemoErrorLogs(t)
	invokeSymemoHandler(t, startSymemoLearning, `{}`)
	response := invokeSymemoHandler(t, showSymemoAnswer, `{"elementId":"other"}`)
	assertSymemoFailure(t, response, string(symemo.ErrTargetMismatch), "The requested Element is not the current learning target.")
	if len(*logs) != 0 {
		t.Fatalf("expected domain failure logs=%#v", *logs)
	}
}

func TestSymemoWrappedFailureIsSanitizedAndLoggedOnce(t *testing.T) {
	storageRoot := installSymemoFixtureWorkspace(t)
	logs := captureSymemoErrorLogs(t)
	invokeSymemoHandler(t, startSymemoLearning, `{}`)
	invokeSymemoHandler(t, showSymemoAnswer, `{"elementId":"`+symemoFixtureElementID+`"}`)

	reviewPath := filepath.Join(storageRoot, "reviews", "2026-07.smr")
	if err := os.Remove(reviewPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(reviewPath, 0755); err != nil {
		t.Fatal(err)
	}
	response := invokeSymemoHandler(t, gradeSymemoItem, `{"elementId":"`+symemoFixtureElementID+`","rawGrade":4,"eventId":"safe-error-review"}`)
	assertSymemoFailure(t, response, string(symemo.ErrHistoryRequiresRepair), "The review history requires repair.")
	if strings.Contains(response.Body.String(), reviewPath) || strings.Contains(response.Body.String(), "directory") {
		t.Fatalf("wrapped failure leaked internals: %s", response.Body.String())
	}
	if len(*logs) != 1 || (*logs)[0].code != string(symemo.ErrHistoryRequiresRepair) || !strings.Contains((*logs)[0].cause, "2026-07.smr") {
		t.Fatalf("wrapped failure logs=%#v", *logs)
	}
}

func TestSymemoUnexpectedRouteFailureIsSanitizedAndLoggedOnce(t *testing.T) {
	logs := captureSymemoErrorLogs(t)
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("blocked"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := installSymemoRuntimePaths(t, root, blocker); err == nil {
		t.Fatal("expected Runtime initialization failure")
	}

	response := invokeSymemoHandler(t, getSymemoCurrentSession, `{}`)
	assertSymemoFailure(t, response, string(symemo.ErrProjectionRebuildFailed), "The Element index could not be rebuilt.")
	if strings.Contains(response.Body.String(), blocker) || strings.Contains(response.Body.String(), "not a directory") {
		t.Fatalf("unexpected failure leaked internals: %s", response.Body.String())
	}
	if len(*logs) != 1 || (*logs)[0].code != string(symemo.ErrProjectionRebuildFailed) || !strings.Contains((*logs)[0].cause, "not-a-directory") {
		t.Fatalf("unexpected failure logs=%#v", *logs)
	}
}

func installSymemoFixtureWorkspace(t *testing.T) string {
	t.Helper()
	workspaceRoot := t.TempDir()
	storageRoot := filepath.Join(workspaceRoot, "data", "storage", "siyuanmemo")
	sourceRoot := filepath.Join("..", "model", "symemo", "testdata")
	if err := filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(storageRoot, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	}); err != nil {
		t.Fatal(err)
	}
	if err := installSymemoRuntimePaths(t, filepath.Join(workspaceRoot, "data"), filepath.Join(workspaceRoot, "temp")); err != nil {
		t.Fatal(err)
	}
	return storageRoot
}

func installSymemoRuntimePaths(t *testing.T, dataDir, tempDir string) error {
	t.Helper()
	if err := model.CloseSymemoEngine(); err != nil {
		t.Fatal(err)
	}
	previousDataDir, previousTempDir := util.DataDir, util.TempDir
	previousBooted := symemoIsBooted
	util.DataDir, util.TempDir = dataDir, tempDir
	symemoIsBooted = func() bool { return true }
	initErr := model.InitSymemo()
	t.Cleanup(func() {
		if err := model.CloseSymemoEngine(); err != nil {
			t.Error(err)
		}
		util.DataDir, util.TempDir = previousDataDir, previousTempDir
		symemoIsBooted = previousBooted
	})
	return initErr
}

func restartSymemoRuntime(t *testing.T) {
	t.Helper()
	if err := model.CloseSymemoEngine(); err != nil {
		t.Fatal(err)
	}
	if err := model.InitSymemo(); err != nil {
		t.Fatal(err)
	}
}

func snapshotSymemoDirectory(t *testing.T, root string) []byte {
	t.Helper()
	files := map[string][]byte{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)], err = os.ReadFile(path)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func invokeSymemoHandler(t *testing.T, handler gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	server := gin.New()
	server.POST("/", handler)
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, body = %s", response.Code, response.Body.String())
	}
	return response
}

func envelopeCode(t *testing.T, response *httptest.ResponseRecorder) int {
	t.Helper()
	var envelope struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Code
}

func envelopeData(t *testing.T, response *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func assertSymemoFailure(t *testing.T, response *httptest.ResponseRecorder, errorCode, message string) {
	t.Helper()
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ErrorCode string `json:"errorCode"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != -1 || envelope.Msg != message || envelope.Data.ErrorCode != errorCode {
		t.Fatalf("failure envelope=%#v", envelope)
	}
}

type capturedSymemoError struct {
	code  string
	cause string
}

func captureSymemoErrorLogs(t *testing.T) *[]capturedSymemoError {
	t.Helper()
	logs := []capturedSymemoError{}
	previous := symemoLogError
	symemoLogError = func(code string, cause error) {
		logs = append(logs, capturedSymemoError{code: code, cause: cause.Error()})
	}
	t.Cleanup(func() { symemoLogError = previous })
	return &logs
}
