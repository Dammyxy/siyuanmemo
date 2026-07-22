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

package symemo

import (
	"encoding/json"
	"time"
)

const (
	SupportedElementSpec = 1
	SupportedPayloadSpec = 1
	SupportedEventSpec   = 1
	SupportedConfigSpec  = 1
)

type Element struct {
	Spec            int            `json:"spec"`
	ID              string         `json:"id"`
	Type            string         `json:"type"`
	Title           string         `json:"title,omitempty"`
	ProcessingState string         `json:"processingState"`
	PayloadSpec     int            `json:"payloadSpec"`
	Payload         ElementPayload `json:"payload"`
	Relations       []Relation     `json:"relations,omitempty"`
	Children        []Element      `json:"children,omitempty"`
}

func (element Element) MarshalJSON() ([]byte, error) {
	type elementAlias Element
	payload, err := marshalElementPayload(element.Type, element.Payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		elementAlias
		Payload json.RawMessage `json:"payload"`
	}{elementAlias: elementAlias(element), Payload: payload})
}

func isSupportedElementType(elementType string) bool {
	switch elementType {
	case "item", "topic", "concept":
		return true
	default:
		return false
	}
}

func marshalElementPayload(elementType string, payload ElementPayload) (json.RawMessage, error) {
	if !isSupportedElementType(elementType) && len(payload.Raw) > 0 {
		return payload.Raw, nil
	}
	return json.Marshal(payload)
}

type ElementPayload struct {
	Kind     string          `json:"kind,omitempty"`
	Prompt   string          `json:"prompt,omitempty"`
	Answer   string          `json:"answer,omitempty"`
	Material *TopicMaterial  `json:"material,omitempty"`
	Raw      json.RawMessage `json:"-"`
}

type ItemPayload = ElementPayload

func (payload *ElementPayload) UnmarshalJSON(data []byte) error {
	type payloadAlias ElementPayload
	var decoded payloadAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*payload = ElementPayload(decoded)
	payload.Raw = append(payload.Raw[:0], data...)
	return nil
}

func (payload ElementPayload) MarshalJSON() ([]byte, error) {
	if payload.Kind == "" && payload.Prompt == "" && payload.Answer == "" && payload.Material == nil && len(payload.Raw) > 0 {
		return payload.Raw, nil
	}
	type payloadAlias ElementPayload
	return json.Marshal(payloadAlias(payload))
}

type TopicMaterial struct {
	Kind             string          `json:"kind"`
	HTML             string          `json:"html,omitempty"`
	BlockID          string          `json:"blockId,omitempty"`
	SourceNotebookID string          `json:"sourceNotebookId,omitempty"`
	Raw              json.RawMessage `json:"-"`
}

func (material *TopicMaterial) UnmarshalJSON(data []byte) error {
	type materialAlias TopicMaterial
	var decoded materialAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*material = TopicMaterial(decoded)
	material.Raw = append(material.Raw[:0], data...)
	return nil
}

func (material TopicMaterial) MarshalJSON() ([]byte, error) {
	if !isSupportedTopicMaterialKind(material.Kind) && len(material.Raw) > 0 {
		return material.Raw, nil
	}
	type materialAlias TopicMaterial
	return json.Marshal(materialAlias(material))
}

func isSupportedTopicMaterialKind(kind string) bool {
	return kind == "html" || kind == "siyuanBlock"
}

type StorageKind string

const (
	StorageKindInternal     StorageKind = "internal"
	StorageKindRootDocument StorageKind = "rootDocument"
)

type SourceMode string

const (
	SourceModeHTML    SourceMode = "html"
	SourceModeBlock   SourceMode = "block"
	SourceModeOpaque  SourceMode = "opaque"
	SourceModeUnknown SourceMode = "unknown"
)

type SupportStatus string

const (
	SupportStatusSupported           SupportStatus = "supported"
	SupportStatusUnsupportedReadOnly SupportStatus = "unsupportedReadOnly"
)

type MaterialSourceStatus string

const (
	MaterialSourceAvailable   MaterialSourceStatus = "available"
	MaterialSourceUnavailable MaterialSourceStatus = "unavailable"
	MaterialSourceUnresolved  MaterialSourceStatus = "unresolved"
)

type ElementMaterialDiagnostic struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

type ElementTreeNode struct {
	ElementID                string                     `json:"elementId"`
	Type                     string                     `json:"type"`
	Title                    string                     `json:"title,omitempty"`
	ProcessingState          string                     `json:"processingState,omitempty"`
	ParentElementID          string                     `json:"parentElementId,omitempty"`
	RootElementID            string                     `json:"rootElementId"`
	StorageKind              StorageKind                `json:"storageKind"`
	SourcePath               string                     `json:"sourcePath"`
	SortRank                 *int                       `json:"sortRank,omitempty"`
	SourceMode               SourceMode                 `json:"sourceMode"`
	SupportStatus            SupportStatus              `json:"supportStatus"`
	BlockID                  string                     `json:"blockId,omitempty"`
	SourceNotebookID         string                     `json:"sourceNotebookId,omitempty"`
	CurrentNotebookID        string                     `json:"currentNotebookId,omitempty"`
	CurrentPath              string                     `json:"currentPath,omitempty"`
	MaterialSourceStatus     *MaterialSourceStatus      `json:"materialSourceStatus,omitempty"`
	MaterialSourceDiagnostic *ElementMaterialDiagnostic `json:"materialSourceDiagnostic,omitempty"`
	ScheduleSummary          *ElementScheduleSummary    `json:"scheduleSummary,omitempty"`
	Children                 []ElementTreeNode          `json:"children,omitempty"`
}

type ElementEnvelope Element

type ElementReadView struct {
	ElementEnvelope
	ParentElementID          string                     `json:"parentElementId,omitempty"`
	RootElementID            string                     `json:"rootElementId"`
	StorageKind              StorageKind                `json:"storageKind"`
	SourcePath               string                     `json:"sourcePath"`
	SourceMode               SourceMode                 `json:"sourceMode"`
	SupportStatus            SupportStatus              `json:"supportStatus"`
	BlockID                  string                     `json:"blockId,omitempty"`
	SourceNotebookID         string                     `json:"sourceNotebookId,omitempty"`
	CurrentNotebookID        string                     `json:"currentNotebookId,omitempty"`
	CurrentPath              string                     `json:"currentPath,omitempty"`
	MaterialSourceStatus     *MaterialSourceStatus      `json:"materialSourceStatus,omitempty"`
	MaterialSourceDiagnostic *ElementMaterialDiagnostic `json:"materialSourceDiagnostic,omitempty"`
	ScheduleProjection       *SchedulingProjection      `json:"scheduleProjection,omitempty"`
}

func (view ElementReadView) MarshalJSON() ([]byte, error) {
	type viewAlias ElementReadView
	payload, err := marshalElementPayload(view.Type, view.Payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		viewAlias
		Payload json.RawMessage `json:"payload"`
	}{viewAlias: viewAlias(view), Payload: payload})
}

type ElementScheduleSummary struct {
	ScheduleProfile      string     `json:"scheduleProfile"`
	AcceptedReviewAction string     `json:"acceptedReviewAction,omitempty"`
	LifecycleState       string     `json:"lifecycleState,omitempty"`
	DueAt                *time.Time `json:"dueAt,omitempty"`
	PriorityPosition     *float64   `json:"priorityPosition,omitempty"`
}

type Relation struct {
	Spec            int    `json:"spec"`
	Type            string `json:"type"`
	TargetElementID string `json:"targetElementId"`
}

type ReviewTarget struct {
	Kind                         string               `json:"kind"`
	ElementID                    string               `json:"elementId"`
	Prompt                       string               `json:"prompt"`
	Answer                       string               `json:"answer,omitempty"`
	DueAt                        time.Time            `json:"dueAt"`
	DueLearningDay               string               `json:"dueLearningDay,omitempty"`
	PriorityPosition             float64              `json:"priorityPosition"`
	ObservedBaseSchedulingEvent  string               `json:"observedBaseSchedulingEventId,omitempty"`
	ObservedProjection           SchedulingProjection `json:"observedProjection"`
	ObservedFinalDrillProjection FinalDrillProjection `json:"observedFinalDrillProjection,omitempty"`
	LearningDate                 string               `json:"learningDate"`
	LearningDayID                string               `json:"learningDayId,omitempty"`
}

type ReviewTargetSummary struct {
	Kind             string    `json:"kind"`
	ElementID        string    `json:"elementId"`
	Prompt           string    `json:"prompt"`
	DueAt            time.Time `json:"dueAt"`
	DueLearningDay   string    `json:"dueLearningDay,omitempty"`
	PriorityPosition float64   `json:"priorityPosition"`
	LearningDayID    string    `json:"learningDayId,omitempty"`
}

type SessionStatus string

const (
	SessionActive    SessionStatus = "active"
	SessionCompleted SessionStatus = "completed"
)

type SessionPhase string

const (
	PhaseQuestion     SessionPhase = "question"
	PhaseAnswer       SessionPhase = "answer"
	PhaseConfirmation SessionPhase = "confirmation"
	PhaseComplete     SessionPhase = "completed"
)

type LearningStage string

const (
	StageOutstanding LearningStage = "outstanding"
	StagePending     LearningStage = "pending"
	StageFinalDrill  LearningStage = "finalDrill"
	StageCompleted   LearningStage = "completed"
)

type StageConfirmation struct {
	Stage LearningStage `json:"stage"`
}

type SessionState struct {
	SessionID                string                `json:"sessionId,omitempty"`
	Status                   SessionStatus         `json:"status"`
	Stage                    LearningStage         `json:"stage,omitempty"`
	Phase                    SessionPhase          `json:"phase"`
	Current                  *ReviewTarget         `json:"current,omitempty"`
	RemainingElementIDs      []string              `json:"remainingElementIds,omitempty"`
	Confirmation             *StageConfirmation    `json:"confirmation,omitempty"`
	AnswerVisible            bool                  `json:"answerVisible"`
	LastProjection           *SchedulingProjection `json:"lastProjection,omitempty"`
	LastFinalDrillProjection *FinalDrillProjection `json:"lastFinalDrillProjection,omitempty"`
	PendingAcceptedEventID   string                `json:"pendingAcceptedEventId,omitempty"`
}

type GradeLabel string

const (
	RatingAgain GradeLabel = "Again"
	RatingHard  GradeLabel = "Hard"
	RatingGood  GradeLabel = "Good"
	RatingEasy  GradeLabel = "Easy"
)

type NormalizedReview struct {
	ElementID                   string     `json:"elementId"`
	TargetKind                  string     `json:"targetKind,omitempty"`
	ActionKind                  string     `json:"actionKind,omitempty"`
	RawGrade                    int        `json:"rawGrade"`
	Passed                      bool       `json:"passed"`
	RatingLabel                 GradeLabel `json:"ratingLabel"`
	RatingMapping               string     `json:"ratingMapping"`
	ReviewAt                    time.Time  `json:"reviewAt"`
	LearningDate                string     `json:"learningDate"`
	LearningDayID               string     `json:"learningDayId,omitempty"`
	SessionID                   string     `json:"sessionId"`
	EventID                     string     `json:"eventId"`
	ObservedBaseSchedulingEvent string     `json:"observedBaseSchedulingEventId,omitempty"`
}

type VersionedAlgorithmState struct {
	Algorithm     string `json:"algorithm"`
	SchemaVersion int    `json:"schemaVersion"`
	State         any    `json:"state"`
}

type AlgorithmCandidate struct {
	Algorithm                  string                  `json:"algorithm"`
	AlgorithmVersion           string                  `json:"algorithmVersion"`
	StateSchemaVersion         int                     `json:"stateSchemaVersion"`
	Status                     string                  `json:"status"`
	ValidationReason           string                  `json:"validationReason,omitempty"`
	PredictedRecallBeforeGrade *float64                `json:"predictedRecallBeforeGrade,omitempty"`
	NextIntervalDays           int                     `json:"nextIntervalDays"`
	NextDueAt                  time.Time               `json:"nextDueAt"`
	Difficulty                 *float64                `json:"difficulty,omitempty"`
	Stability                  *float64                `json:"stability,omitempty"`
	Retrievability             *float64                `json:"retrievability,omitempty"`
	NextState                  VersionedAlgorithmState `json:"nextState"`
}

type AlgorithmDecision struct {
	Policy            string   `json:"policy"`
	Winner            string   `json:"winner"`
	FallbackReason    string   `json:"fallbackReason,omitempty"`
	EnabledAlgorithms []string `json:"enabledAlgorithms"`
}

type SchedulingProjection struct {
	ElementID            string                             `json:"elementId"`
	ScheduleProfile      string                             `json:"scheduleProfile,omitempty"`
	AcceptedReviewAction string                             `json:"acceptedReviewAction,omitempty"`
	LifecycleState       string                             `json:"lifecycleState"`
	AdoptedTerminalID    string                             `json:"adoptedTerminalEventId,omitempty"`
	DueAt                time.Time                          `json:"dueAt"`
	DueLearningDay       string                             `json:"dueLearningDay,omitempty"`
	IntervalDays         int                                `json:"intervalDays"`
	Repetitions          int                                `json:"repetitions"`
	Lapses               int                                `json:"lapses"`
	LastReviewAt         *time.Time                         `json:"lastReviewAt,omitempty"`
	LastLearningDate     string                             `json:"lastLearningDate,omitempty"`
	LastRawGrade         *int                               `json:"lastRawGrade,omitempty"`
	LastPassed           *bool                              `json:"lastPassed,omitempty"`
	ActiveAlgorithm      string                             `json:"activeAlgorithm,omitempty"`
	AlgorithmStates      map[string]VersionedAlgorithmState `json:"algorithmStates,omitempty"`
	PriorityPosition     float64                            `json:"priorityPosition"`
}

type FinalDrillProjection struct {
	ElementID              string `json:"elementId"`
	Member                 bool   `json:"member"`
	LastActivityDay        string `json:"lastActivityDay,omitempty"`
	AdmissionEventID       string `json:"admissionEventId,omitempty"`
	AdoptedTerminalEventID string `json:"adoptedTerminalEventId,omitempty"`
	Expired                bool   `json:"expired,omitempty"`
}

func cloneSchedulingProjection(projection SchedulingProjection) (SchedulingProjection, error) {
	data, err := json.Marshal(projection)
	if err != nil {
		return SchedulingProjection{}, err
	}
	var cloned SchedulingProjection
	if err = json.Unmarshal(data, &cloned); err != nil {
		return SchedulingProjection{}, err
	}
	return cloned, nil
}

type SchedulingEvent struct {
	Spec                      int                  `json:"spec"`
	EventID                   string               `json:"eventId"`
	OccurredAt                time.Time            `json:"occurredAt"`
	Type                      string               `json:"type"`
	ElementID                 string               `json:"elementId"`
	SessionID                 string               `json:"sessionId,omitempty"`
	BaseEventID               string               `json:"baseSchedulingEventId,omitempty"`
	ReviewKind                string               `json:"reviewKind,omitempty"`
	RawGrade                  *int                 `json:"rawGrade,omitempty"`
	Passed                    *bool                `json:"passed,omitempty"`
	RatingLabel               GradeLabel           `json:"ratingLabel,omitempty"`
	RatingMapping             string               `json:"ratingMapping,omitempty"`
	LearningDate              string               `json:"learningDate,omitempty"`
	LearningDayID             string               `json:"learningDayId,omitempty"`
	TopicPolicyVersion        string               `json:"topicPolicyVersion,omitempty"`
	TopicInitialIntervalDays  int                  `json:"topicInitialIntervalDays,omitempty"`
	TopicPreviousIntervalDays int                  `json:"topicPreviousIntervalDays,omitempty"`
	TopicEffectiveAFactor     float64              `json:"topicEffectiveAFactor,omitempty"`
	TopicNextIntervalDays     int                  `json:"topicNextIntervalDays,omitempty"`
	TopicMinimumIntervalDays  int                  `json:"topicMinimumIntervalDays,omitempty"`
	TopicMaximumIntervalDays  int                  `json:"topicMaximumIntervalDays,omitempty"`
	TopicSkipPolicy           string               `json:"topicSkipPolicy,omitempty"`
	TopicSeed                 string               `json:"topicSeed,omitempty"`
	DrillEffect               string               `json:"drillEffect,omitempty"`
	DrillAdmissionEventID     string               `json:"drillAdmissionEventId,omitempty"`
	BaseDrillEventID          string               `json:"baseDrillEventId,omitempty"`
	AlgorithmDecision         AlgorithmDecision    `json:"algorithmDecision,omitempty"`
	AlgorithmCandidates       []AlgorithmCandidate `json:"algorithmCandidates,omitempty"`
	Before                    SchedulingProjection `json:"before"`
	After                     SchedulingProjection `json:"after"`
}

type EventFile struct {
	Spec   int               `json:"spec"`
	Month  string            `json:"month"`
	Events []SchedulingEvent `json:"events"`
}

type EventDiagnostic struct {
	EventID        string `json:"eventId"`
	PayloadHash    string `json:"payloadHash"`
	Classification string `json:"classification"`
	Reason         string `json:"reason,omitempty"`
	DuplicateCount int    `json:"duplicateCount,omitempty"`
}

type ElementSourceDiagnostic struct {
	SourcePath   string   `json:"sourcePath"`
	ElementID    string   `json:"elementId,omitempty"`
	Code         string   `json:"code"`
	Reason       string   `json:"reason"`
	RelatedPaths []string `json:"relatedPaths,omitempty"`
}

type projectionBuild struct {
	Elements                 map[string]Element
	Tree                     []ElementTreeNode
	Projections              map[string]SchedulingProjection
	FinalDrillProjections    map[string]FinalDrillProjection
	HistoryEventFingerprints []string
	EventDiagnostics         []EventDiagnostic
	SourceDiagnostics        []ElementSourceDiagnostic
}

type LearningActionKind string

const (
	ActionStart                  LearningActionKind = "Start"
	ActionShowAnswer             LearningActionKind = "ShowAnswer"
	ActionGradeItem              LearningActionKind = "GradeItem"
	ActionNextTopic              LearningActionKind = "NextTopic"
	ActionAcceptStageTransition  LearningActionKind = "AcceptStageTransition"
	ActionDeclineStageTransition LearningActionKind = "DeclineStageTransition"
	ActionGradeDrill             LearningActionKind = "GradeDrill"
	ActionStop                   LearningActionKind = "Stop"
)

type LearningAction struct {
	Kind      LearningActionKind `json:"kind"`
	ElementID string             `json:"elementId,omitempty"`
	RawGrade  *int               `json:"rawGrade,omitempty"`
	EventID   string             `json:"eventId,omitempty"`
	Stage     LearningStage      `json:"stage,omitempty"`
}

type QueryKind string

const (
	QueryElementSubset            QueryKind = "GetElementSubset"
	QueryCurrentSession           QueryKind = "GetCurrentLearningSession"
	QueryElementTree              QueryKind = "GetElementTree"
	QueryElement                  QueryKind = "GetElement"
	QueryElementSourceDiagnostics QueryKind = "GetElementSourceDiagnostics"
)

type Query struct {
	Kind                   QueryKind `json:"kind"`
	Subset                 string    `json:"subset,omitempty"`
	RootElementID          string    `json:"rootElementId,omitempty"`
	ElementID              string    `json:"elementId,omitempty"`
	SourcePath             string    `json:"sourcePath,omitempty"`
	IncludeScheduleSummary bool      `json:"includeScheduleSummary,omitempty"`
}

type QueryResult struct {
	Subset      string                    `json:"subset,omitempty"`
	Items       []ReviewTargetSummary     `json:"items,omitempty"`
	Session     *SessionState             `json:"session,omitempty"`
	Nodes       []ElementTreeNode         `json:"nodes,omitempty"`
	Element     *ElementReadView          `json:"element,omitempty"`
	Diagnostics []ElementSourceDiagnostic `json:"diagnostics,omitempty"`
}

type LearningResult struct {
	ReviewAccepted       bool                  `json:"reviewAccepted"`
	EventID              string                `json:"eventId,omitempty"`
	RawGrade             *int                  `json:"rawGrade,omitempty"`
	Passed               *bool                 `json:"passed,omitempty"`
	RatingLabel          GradeLabel            `json:"ratingLabel,omitempty"`
	RatingMapping        string                `json:"ratingMapping,omitempty"`
	Decision             *AlgorithmDecision    `json:"algorithmDecision,omitempty"`
	Candidates           []AlgorithmCandidate  `json:"algorithmCandidates,omitempty"`
	Projection           *SchedulingProjection `json:"projection,omitempty"`
	FinalDrillProjection *FinalDrillProjection `json:"finalDrillProjection,omitempty"`
	Session              *SessionState         `json:"session,omitempty"`
}

type CreateElementCommand struct{ Kind string }
type ChangeElementCommand struct{ Kind string }
type SendToNoteCommand struct{ Kind string }
type CreateElementResult struct{}
type ChangeElementResult struct{}
type SendToNoteResult struct{}
