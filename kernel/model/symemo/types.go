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

import "time"

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
	Payload         ItemPayload    `json:"payload"`
	Relations       []Relation     `json:"relations,omitempty"`
	Children        []ChildElement `json:"children,omitempty"`
}

type ItemPayload struct {
	Kind   string `json:"kind"`
	Prompt string `json:"prompt"`
	Answer string `json:"answer"`
}

type ChildElement struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type Relation struct {
	Spec            int    `json:"spec"`
	Type            string `json:"type"`
	TargetElementID string `json:"targetElementId"`
}

type ReviewTarget struct {
	Kind                        string               `json:"kind"`
	ElementID                   string               `json:"elementId"`
	Prompt                      string               `json:"prompt"`
	Answer                      string               `json:"answer,omitempty"`
	DueAt                       time.Time            `json:"dueAt"`
	PriorityPosition            float64              `json:"priorityPosition"`
	ObservedBaseSchedulingEvent string               `json:"observedBaseSchedulingEventId,omitempty"`
	ObservedProjection          SchedulingProjection `json:"observedProjection"`
	LearningDate                string               `json:"learningDate"`
}

type ReviewTargetSummary struct {
	Kind             string    `json:"kind"`
	ElementID        string    `json:"elementId"`
	Prompt           string    `json:"prompt"`
	DueAt            time.Time `json:"dueAt"`
	PriorityPosition float64   `json:"priorityPosition"`
}

type SessionStatus string

const (
	SessionActive    SessionStatus = "active"
	SessionCompleted SessionStatus = "completed"
)

type SessionPhase string

const (
	PhaseQuestion SessionPhase = "question"
	PhaseAnswer   SessionPhase = "answer"
	PhaseComplete SessionPhase = "completed"
)

type SessionState struct {
	SessionID              string                `json:"sessionId,omitempty"`
	Status                 SessionStatus         `json:"status"`
	Phase                  SessionPhase          `json:"phase"`
	Current                *ReviewTarget         `json:"current,omitempty"`
	RemainingElementIDs    []string              `json:"remainingElementIds,omitempty"`
	LastProjection         *SchedulingProjection `json:"lastProjection,omitempty"`
	PendingAcceptedEventID string                `json:"pendingAcceptedEventId,omitempty"`
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
	RawGrade                    int        `json:"rawGrade"`
	Passed                      bool       `json:"passed"`
	RatingLabel                 GradeLabel `json:"ratingLabel"`
	RatingMapping               string     `json:"ratingMapping"`
	ReviewAt                    time.Time  `json:"reviewAt"`
	LearningDate                string     `json:"learningDate"`
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
	ElementID         string                             `json:"elementId"`
	LifecycleState    string                             `json:"lifecycleState"`
	AdoptedTerminalID string                             `json:"adoptedTerminalEventId,omitempty"`
	DueAt             time.Time                          `json:"dueAt"`
	IntervalDays      int                                `json:"intervalDays"`
	Repetitions       int                                `json:"repetitions"`
	Lapses            int                                `json:"lapses"`
	LastReviewAt      *time.Time                         `json:"lastReviewAt,omitempty"`
	LastLearningDate  string                             `json:"lastLearningDate,omitempty"`
	LastRawGrade      *int                               `json:"lastRawGrade,omitempty"`
	LastPassed        *bool                              `json:"lastPassed,omitempty"`
	ActiveAlgorithm   string                             `json:"activeAlgorithm,omitempty"`
	AlgorithmStates   map[string]VersionedAlgorithmState `json:"algorithmStates,omitempty"`
	PriorityPosition  float64                            `json:"priorityPosition"`
}

type SchedulingEvent struct {
	Spec                int                  `json:"spec"`
	EventID             string               `json:"eventId"`
	OccurredAt          time.Time            `json:"occurredAt"`
	Type                string               `json:"type"`
	ElementID           string               `json:"elementId"`
	SessionID           string               `json:"sessionId,omitempty"`
	BaseEventID         string               `json:"baseSchedulingEventId,omitempty"`
	ReviewKind          string               `json:"reviewKind,omitempty"`
	RawGrade            *int                 `json:"rawGrade,omitempty"`
	Passed              *bool                `json:"passed,omitempty"`
	RatingLabel         GradeLabel           `json:"ratingLabel,omitempty"`
	RatingMapping       string               `json:"ratingMapping,omitempty"`
	LearningDate        string               `json:"learningDate,omitempty"`
	AlgorithmDecision   AlgorithmDecision    `json:"algorithmDecision,omitempty"`
	AlgorithmCandidates []AlgorithmCandidate `json:"algorithmCandidates,omitempty"`
	Before              SchedulingProjection `json:"before"`
	After               SchedulingProjection `json:"after"`
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
	SourcePath string `json:"sourcePath"`
	ElementID  string `json:"elementId,omitempty"`
	Code       string `json:"code"`
	Reason     string `json:"reason"`
}

type projectionBuild struct {
	Elements          map[string]Element
	Projections       map[string]SchedulingProjection
	EventDiagnostics  []EventDiagnostic
	SourceDiagnostics []ElementSourceDiagnostic
}

type LearningActionKind string

const (
	ActionStart      LearningActionKind = "Start"
	ActionShowAnswer LearningActionKind = "ShowAnswer"
	ActionGradeItem  LearningActionKind = "GradeItem"
)

type LearningAction struct {
	Kind      LearningActionKind `json:"kind"`
	ElementID string             `json:"elementId,omitempty"`
	RawGrade  *int               `json:"rawGrade,omitempty"`
	EventID   string             `json:"eventId,omitempty"`
}

type QueryKind string

const (
	QueryElementSubset  QueryKind = "GetElementSubset"
	QueryCurrentSession QueryKind = "GetCurrentLearningSession"
)

type Query struct {
	Kind   QueryKind `json:"kind"`
	Subset string    `json:"subset,omitempty"`
}

type QueryResult struct {
	Subset  string                `json:"subset,omitempty"`
	Items   []ReviewTargetSummary `json:"items,omitempty"`
	Session *SessionState         `json:"session,omitempty"`
}

type LearningResult struct {
	ReviewAccepted bool                  `json:"reviewAccepted"`
	EventID        string                `json:"eventId,omitempty"`
	RawGrade       *int                  `json:"rawGrade,omitempty"`
	Passed         *bool                 `json:"passed,omitempty"`
	RatingLabel    GradeLabel            `json:"ratingLabel,omitempty"`
	RatingMapping  string                `json:"ratingMapping,omitempty"`
	Decision       *AlgorithmDecision    `json:"algorithmDecision,omitempty"`
	Candidates     []AlgorithmCandidate  `json:"algorithmCandidates,omitempty"`
	Projection     *SchedulingProjection `json:"projection,omitempty"`
	Session        *SessionState         `json:"session,omitempty"`
}

type CreateElementCommand struct{ Kind string }
type ChangeElementCommand struct{ Kind string }
type SendToNoteCommand struct{ Kind string }
type CreateElementResult struct{}
type ChangeElementResult struct{}
type SendToNoteResult struct{}
