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
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrUnsupportedOperation         ErrorCode = "unsupported-operation"
	ErrInvalidSessionPhase          ErrorCode = "invalid-session-phase"
	ErrTargetMismatch               ErrorCode = "target-mismatch"
	ErrUnsupportedGrade             ErrorCode = "unsupported-grade"
	ErrAuthoritativeItemUnavailable ErrorCode = "authoritative-item-unavailable"
	ErrUnsupportedAlgorithmState    ErrorCode = "unsupported-algorithm-state"
	ErrInvalidAlgorithmOutput       ErrorCode = "invalid-algorithm-output"
	ErrDurableWriteFailed           ErrorCode = "durable-write-failed"
	ErrProjectionRefreshFailed      ErrorCode = "projection-refresh-failed"
	ErrQueueAdvanceFailed           ErrorCode = "queue-advance-failed"
	ErrHistoryRequiresRepair        ErrorCode = "history-requires-repair"
	ErrElementNotFound              ErrorCode = "element-not-found"
	ErrElementSourceUnavailable     ErrorCode = "element-source-unavailable"
	ErrElementSourceAmbiguous       ErrorCode = "element-source-ambiguous"
	ErrProjectionRebuildFailed      ErrorCode = "projection-rebuild-failed"
)

type DomainError struct {
	Code            ErrorCode
	Message         string
	Retryable       bool
	ReviewAccepted  bool
	AcceptedEventID string
	Session         *SessionState
	Cause           error
}

func (e *DomainError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Code)
}

func (e *DomainError) Unwrap() error { return e.Cause }

func domainError(code ErrorCode, message string, cause error) *DomainError {
	return &DomainError{Code: code, Message: message, Cause: cause}
}

func wrapDomainError(code ErrorCode, format string, err error) *DomainError {
	return domainError(code, fmt.Sprintf(format, err), err)
}

func AsDomainError(err error) (*DomainError, bool) {
	var domainErr *DomainError
	if errors.As(err, &domainErr) {
		return domainErr, true
	}
	return nil, false
}
