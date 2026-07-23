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
	"errors"
	"strings"
	"testing"

	"github.com/siyuan-note/siyuan/kernel/model/symemo"
)

func TestCreateHTMLTopicFailureEnvelopeRedactsCausesAndCarriesAcceptance(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        *symemo.DomainError
		wantCode   symemo.ErrorCode
		wantFields []string
	}{
		{
			name: "partial",
			err:  &symemo.DomainError{Code: symemo.ErrElementWritePartial, ElementID: "20260723150000-partial", EventID: "20260723150001-partevt", Cause: errors.New(`H:\secret\memo.db <p>raw</p> SQL`)},
			wantFields: []string{
				`"errorCode":"element-write-partial"`,
				`"elementId":"20260723150000-partial"`,
				`"eventId":"20260723150001-partevt"`,
				`"createAccepted":false`,
				`"reviewAccepted":false`,
				`"retryable":false`,
			},
		},
		{
			name: "accepted projection",
			err:  &symemo.DomainError{Code: symemo.ErrProjectionRefreshFailed, ElementID: "20260723150002-acceptd", EventID: "20260723150003-accevt", AcceptedEventID: "20260723150003-accevt", CreateAccepted: true, ReviewAccepted: true, Cause: errors.New(`SQL constraint failed for <script>bad</script>`)},
			wantFields: []string{
				`"errorCode":"projection-refresh-failed"`,
				`"elementId":"20260723150002-acceptd"`,
				`"eventId":"20260723150003-accevt"`,
				`"acceptedEventId":"20260723150003-accevt"`,
				`"createAccepted":true`,
				`"reviewAccepted":true`,
				`"retryable":false`,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			previousBooted := symemoIsBooted
			previousCreate := symemoCreateElement
			symemoIsBooted = func() bool { return true }
			symemoCreateElement = func(context.Context, symemo.CreateElementCommand) (symemo.CreateElementResult, error) {
				return symemo.CreateElementResult{}, test.err
			}
			t.Cleanup(func() {
				symemoIsBooted = previousBooted
				symemoCreateElement = previousCreate
			})

			response := invokeSymemoHandler(t, createHTMLTopic, `{"title":"Topic","html":"<p>Body</p>"}`)
			body := response.Body.String()
			for _, want := range test.wantFields {
				if !strings.Contains(body, want) {
					t.Fatalf("failure body missing %s: %s", want, body)
				}
			}
			for _, forbidden := range []string{`H:\secret`, "memo.db", "<p>raw</p>", "<script>", "SQL"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("failure body leaked %q: %s", forbidden, body)
				}
			}
		})
	}
}
