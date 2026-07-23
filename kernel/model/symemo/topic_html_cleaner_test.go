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
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/88250/lute/ast"
)

var feature004NodeIDPattern = regexp.MustCompile(`data-symemo-node-id="[^"]+"`)

func TestTopicHTMLCleanerPolicyMatrix(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantHTML string
	}{
		{
			name:     "fragment parsing omits wrappers and replaces caller IDs",
			input:    `<html><head><title>ignored</title></head><body><p data-symemo-node-id="caller">Hi <strong>there</strong></p></body></html>`,
			wantHTML: `<p data-symemo-node-id="ID">Hi <strong>there</strong></p>`,
		},
		{
			name:     "unknown containers unwrap and dangerous subtrees disappear",
			input:    `<section><script><p>bad</p></script><article><p onclick="evil()" style="position: fixed; color: red; font-weight: bold; background-image: url(x)">ok</p></article></section>`,
			wantHTML: `<p data-symemo-node-id="ID" style="color: red; font-weight: bold">ok</p>`,
		},
		{
			name:     "urls and attributes are restricted",
			input:    `<p><a href="javascript:bad()" title="drop">bad</a><a href="https://example.com/a?b=1" rel="nofollow">good</a><img src="data:image/png;base64,AAA" alt="bad"><img src="#figure" alt="ok"></p>`,
			wantHTML: `<p data-symemo-node-id="ID"><a title="drop">bad</a><a href="https://example.com/a?b=1">good</a><img alt="bad"><img alt="ok" src="#figure"></p>`,
		},
		{
			name:     "pre and code whitespace is preserved",
			input:    "<pre><code>line 1\n  line 2\t&amp;</code></pre>",
			wantHTML: "<pre data-symemo-node-id=\"ID\"><code>line 1\n  line 2\t&amp;</code></pre>",
		},
		{
			name:     "math and table nodes remain renderable",
			input:    `<table><tr><td data-symemo-node-id="caller"><span data-type="inline-math" data-subtype="math" data-content="x^2"></span></td></tr></table><div data-type="NodeMathBlock" data-subtype="math" data-content="\int"></div>`,
			wantHTML: `<table><tbody><tr><td data-symemo-node-id="ID"><span data-content="x^2" data-subtype="math" data-type="inline-math"></span></td></tr></tbody></table><div data-content="\int" data-subtype="math" data-symemo-node-id="ID" data-type="NodeMathBlock"></div>`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cleaned, err := cleanTopicHTMLFragment(test.input)
			if err != nil {
				t.Fatalf("cleaning failed: %v", err)
			}
			normalized := feature004NodeIDPattern.ReplaceAllString(cleaned, `data-symemo-node-id="ID"`)
			if normalized != test.wantHTML {
				t.Fatalf("cleaned HTML\nactual: %s\nwant:   %s", normalized, test.wantHTML)
			}
			if strings.Contains(cleaned, "caller") || strings.Contains(cleaned, "<script") || strings.Contains(cleaned, "onclick") || strings.Contains(cleaned, "javascript:") || strings.Contains(cleaned, "data:image") || strings.Contains(cleaned, "position:") || strings.Contains(cleaned, "background-image") {
				t.Fatalf("forbidden content survived: %s", cleaned)
			}
		})
	}
}

func TestTopicHTMLCleanerRejectsNonRenderableFragments(t *testing.T) {
	for _, input := range []string{"", "   ", "<!-- only comment -->", "<script>bad()</script>", `<img src="file:///C:/secret.png">`} {
		if cleaned, err := cleanTopicHTMLFragment(input); err == nil {
			t.Fatalf("non-renderable input %q cleaned to %q", input, cleaned)
		}
	}
}

func TestTopicHTMLCleanerGeneratedMaterialFixtureSet(t *testing.T) {
	for i := 0; i < 100; i++ {
		input := fmt.Sprintf(`<section><p data-symemo-node-id="caller-%03d" style="color: #%06d; position: fixed">Body %03d</p><script>bad()</script><img src="https://example.com/%03d.png" alt="image %03d"><a href="#anchor-%03d">jump</a></section>`, i, i, i, i, i, i)
		cleaned, err := cleanTopicHTMLFragment(input)
		if err != nil {
			t.Fatalf("fixture %d cleaning failed: %v", i, err)
		}
		if !strings.Contains(cleaned, "Body ") || !strings.Contains(cleaned, `src="https://example.com/`) || !strings.Contains(cleaned, `data-symemo-node-id="`) {
			t.Fatalf("fixture %d lost renderable content: %s", i, cleaned)
		}
		for _, forbidden := range []string{"caller-", "<script", "position:", "onclick", "javascript:", "data:image"} {
			if strings.Contains(cleaned, forbidden) {
				t.Fatalf("fixture %d retained forbidden content %q: %s", i, forbidden, cleaned)
			}
		}
	}
}

func TestTopicHTMLCleanerRetriesInvalidAndDuplicateGeneratedNodeIDs(t *testing.T) {
	previous := newTopicHTMLNodeID
	generated := []string{"invalid", "20260723152000-nodeaaa", "20260723152000-nodeaaa", "20260723152001-nodebbb"}
	newTopicHTMLNodeID = func() string {
		id := generated[0]
		generated = generated[1:]
		return id
	}
	t.Cleanup(func() { newTopicHTMLNodeID = previous })

	cleaned, err := cleanTopicHTMLFragment("<p>First</p><p>Second</p>")
	if err != nil {
		t.Fatal(err)
	}
	matches := feature004NodeIDPattern.FindAllString(cleaned, -1)
	if len(matches) != 2 || matches[0] == matches[1] {
		t.Fatalf("generated node identities = %#v in %s", matches, cleaned)
	}
	for _, match := range matches {
		id := strings.TrimSuffix(strings.TrimPrefix(match, `data-symemo-node-id="`), `"`)
		if !ast.IsNodeIDPattern(id) {
			t.Fatalf("generated node identity %q is invalid", id)
		}
	}
}

func TestTopicHTMLCleanerFailsAfterBoundedInvalidNodeIDGeneration(t *testing.T) {
	previous := newTopicHTMLNodeID
	newTopicHTMLNodeID = func() string { return "invalid" }
	t.Cleanup(func() { newTopicHTMLNodeID = previous })

	if cleaned, err := cleanTopicHTMLFragment("<p>Body</p>"); err == nil {
		t.Fatalf("invalid generated node identities cleaned to %q", cleaned)
	}
}
