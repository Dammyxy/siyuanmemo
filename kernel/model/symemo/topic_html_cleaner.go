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
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/88250/lute/ast"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const topicHTMLCleaningPolicyVersion = "siyuanmemo-topic-html-v1"

var newTopicHTMLNodeID = ast.NewNodeID

var safeCSSScalarPattern = regexp.MustCompile(`^[#a-zA-Z0-9\s.,()%+-]+$`)

func cleanTopicHTMLFragment(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", errors.New("HTML is empty")
	}
	context := &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Body, Data: "body"}
	nodes, err := xhtml.ParseFragment(strings.NewReader(input), context)
	if err != nil {
		return "", err
	}
	root := &xhtml.Node{Type: xhtml.DocumentNode}
	for _, node := range cleanTopicHTMLChildren(nodes) {
		appendHTMLChild(root, node)
	}
	if err = assignTopicHTMLNodeIDs(root); err != nil {
		return "", err
	}
	var builder strings.Builder
	renderable := false
	for node := root.FirstChild; node != nil; node = node.NextSibling {
		if topicHTMLNodeRenderable(node) {
			renderable = true
		}
		renderTopicHTMLNode(&builder, node)
	}
	if !renderable || strings.TrimSpace(builder.String()) == "" {
		return "", errors.New("HTML has no renderable Topic material")
	}
	return builder.String(), nil
}

func cleanTopicHTMLChildren(nodes []*xhtml.Node) []*xhtml.Node {
	var out []*xhtml.Node
	for _, node := range nodes {
		out = append(out, cleanTopicHTMLNode(node)...)
	}
	return out
}

func cleanTopicHTMLNode(node *xhtml.Node) []*xhtml.Node {
	switch node.Type {
	case xhtml.TextNode:
		return []*xhtml.Node{{Type: xhtml.TextNode, Data: node.Data}}
	case xhtml.ElementNode:
		name := strings.ToLower(node.Data)
		if topicHTMLDropsSubtree(name) {
			return nil
		}
		children := cleanTopicHTMLChildren(htmlNodeChildren(node))
		if !topicHTMLKeepsElement(name, node) {
			return children
		}
		cleaned := &xhtml.Node{Type: xhtml.ElementNode, Data: name, Attr: sanitizeTopicHTMLAttrs(name, node.Attr)}
		for _, child := range children {
			appendHTMLChild(cleaned, child)
		}
		return []*xhtml.Node{cleaned}
	default:
		return nil
	}
}

func htmlNodeChildren(node *xhtml.Node) []*xhtml.Node {
	var children []*xhtml.Node
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		children = append(children, child)
	}
	return children
}

func appendHTMLChild(parent, child *xhtml.Node) {
	child.Parent = parent
	child.PrevSibling = parent.LastChild
	child.NextSibling = nil
	if parent.LastChild != nil {
		parent.LastChild.NextSibling = child
	} else {
		parent.FirstChild = child
	}
	parent.LastChild = child
}

func topicHTMLDropsSubtree(name string) bool {
	switch name {
	case "head", "title", "script", "style", "link", "meta", "base", "iframe", "object", "embed", "form", "input", "button", "textarea", "select", "option", "video", "audio", "source", "track", "canvas", "svg", "math":
		return true
	default:
		return false
	}
}

func topicHTMLKeepsElement(name string, node *xhtml.Node) bool {
	if name == "html" || name == "body" {
		return false
	}
	if name == "span" {
		return hasTopicHTMLAttr(node, "data-type", "inline-math") && hasTopicHTMLAttr(node, "data-subtype", "math")
	}
	if name == "div" {
		return hasTopicHTMLAttr(node, "data-type", "NodeMathBlock") && hasTopicHTMLAttr(node, "data-subtype", "math")
	}
	switch name {
	case "h1", "h2", "h3", "h4", "h5", "h6", "p", "br", "ul", "ol", "li", "blockquote", "pre", "code", "table", "thead", "tbody", "tfoot", "tr", "th", "td", "figure", "figcaption", "hr", "strong", "b", "em", "i", "u", "s", "del", "ins", "mark", "sub", "sup", "a", "img":
		return true
	default:
		return false
	}
}

func hasTopicHTMLAttr(node *xhtml.Node, key, value string) bool {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) && attr.Val == value {
			return true
		}
	}
	return false
}

func sanitizeTopicHTMLAttrs(name string, attrs []xhtml.Attribute) []xhtml.Attribute {
	values := map[string]string{}
	for _, attr := range attrs {
		key := strings.ToLower(attr.Key)
		if strings.HasPrefix(key, "on") || key == "data-symemo-node-id" {
			continue
		}
		switch key {
		case "style":
			if style := sanitizeTopicHTMLStyle(attr.Val); style != "" {
				values[key] = style
			}
		case "href":
			if name == "a" {
				if href := sanitizeTopicHTMLURL(attr.Val); href != "" {
					values[key] = href
				}
			}
		case "src":
			if name == "img" {
				if src := sanitizeTopicHTMLURL(attr.Val); src != "" {
					values[key] = src
				}
			}
		case "alt", "title":
			if name == "img" || name == "a" {
				values[key] = attr.Val
			}
		case "id":
			if isSafeTopicHTMLID(attr.Val) {
				values[key] = attr.Val
			}
		case "colspan", "rowspan":
			if (name == "td" || name == "th") && isPositiveSmallInteger(attr.Val) {
				values[key] = strings.TrimSpace(attr.Val)
			}
		case "scope":
			if name == "th" && isSafeTopicHTMLToken(attr.Val) {
				values[key] = strings.TrimSpace(attr.Val)
			}
		case "start":
			if name == "ol" && isPositiveSmallInteger(attr.Val) {
				values[key] = strings.TrimSpace(attr.Val)
			}
		case "reversed":
			if name == "ol" {
				values[key] = "reversed"
			}
		case "data-type":
			if (name == "span" && attr.Val == "inline-math") || (name == "div" && attr.Val == "NodeMathBlock") {
				values[key] = attr.Val
			}
		case "data-subtype":
			if (name == "span" || name == "div") && attr.Val == "math" {
				values[key] = attr.Val
			}
		case "data-content":
			if (name == "span" || name == "div") && attr.Val != "" {
				values[key] = attr.Val
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]xhtml.Attribute, 0, len(keys))
	for _, key := range keys {
		out = append(out, xhtml.Attribute{Key: key, Val: values[key]})
	}
	return out
}

func sanitizeTopicHTMLURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "#") {
		if len(trimmed) > 1 && !strings.ContainsAny(trimmed, " \t\r\n") {
			return trimmed
		}
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return trimmed
}

func sanitizeTopicHTMLStyle(raw string) string {
	allowed := map[string]bool{
		"font-weight":      true,
		"font-style":       true,
		"text-decoration":  true,
		"text-align":       true,
		"color":            true,
		"background-color": true,
	}
	order := []string{"background-color", "color", "font-style", "font-weight", "text-align", "text-decoration"}
	values := map[string]string{}
	for _, declaration := range strings.Split(raw, ";") {
		parts := strings.SplitN(declaration, ":", 2)
		if len(parts) != 2 {
			continue
		}
		property := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.Join(strings.Fields(strings.TrimSpace(parts[1])), " ")
		lowerValue := strings.ToLower(value)
		if !allowed[property] || value == "" || !safeCSSScalarPattern.MatchString(value) || strings.Contains(lowerValue, "url(") || strings.Contains(lowerValue, "var(") || strings.Contains(lowerValue, "calc(") || strings.Contains(lowerValue, "expression") || strings.ContainsAny(value, `@<>{}\`) {
			continue
		}
		values[property] = value
	}
	var kept []string
	for _, property := range order {
		if value, ok := values[property]; ok {
			kept = append(kept, property+": "+value)
		}
	}
	return strings.Join(kept, "; ")
}

func isSafeTopicHTMLID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n<>\"'`") {
		return false
	}
	return true
}

func isSafeTopicHTMLToken(value string) bool {
	return isSafeTopicHTMLID(value)
}

func isPositiveSmallInteger(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 4 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != "0"
}

func assignTopicHTMLNodeIDs(root *xhtml.Node) error {
	generated := map[string]bool{}
	var assignmentErr error
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if assignmentErr != nil {
			return
		}
		if node.Type == xhtml.ElementNode && topicHTMLNeedsNodeID(node.Data) {
			id := ""
			for attempt := 0; attempt < 32; attempt++ {
				candidate := newTopicHTMLNodeID()
				if !ast.IsNodeIDPattern(candidate) || generated[candidate] {
					continue
				}
				id = candidate
				break
			}
			if id == "" {
				assignmentErr = errors.New("generated Topic material node identity is unavailable")
				return
			}
			generated[id] = true
			node.Attr = append(node.Attr, xhtml.Attribute{Key: "data-symemo-node-id", Val: id})
			sort.Slice(node.Attr, func(i, j int) bool { return node.Attr[i].Key < node.Attr[j].Key })
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return assignmentErr
}

func topicHTMLNeedsNodeID(name string) bool {
	switch name {
	case "h1", "h2", "h3", "h4", "h5", "h6", "p", "li", "blockquote", "pre", "td", "th", "figure", "hr":
		return true
	case "div":
		return true
	default:
		return false
	}
}

func topicHTMLNodeRenderable(node *xhtml.Node) bool {
	switch node.Type {
	case xhtml.TextNode:
		return strings.TrimSpace(node.Data) != ""
	case xhtml.ElementNode:
		if node.Data == "img" && topicHTMLAttrValue(node, "src") != "" {
			return true
		}
		if node.Data == "hr" {
			return true
		}
		if (node.Data == "span" || node.Data == "div") && topicHTMLAttrValue(node, "data-content") != "" {
			return true
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if topicHTMLNodeRenderable(child) {
				return true
			}
		}
	}
	return false
}

func topicHTMLAttrValue(node *xhtml.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func renderTopicHTMLNode(builder *strings.Builder, node *xhtml.Node) {
	switch node.Type {
	case xhtml.TextNode:
		builder.WriteString(html.EscapeString(node.Data))
	case xhtml.ElementNode:
		builder.WriteByte('<')
		builder.WriteString(node.Data)
		for _, attr := range node.Attr {
			builder.WriteByte(' ')
			builder.WriteString(attr.Key)
			builder.WriteString(`="`)
			builder.WriteString(html.EscapeString(attr.Val))
			builder.WriteByte('"')
		}
		builder.WriteByte('>')
		if topicHTMLVoidElement(node.Data) {
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			renderTopicHTMLNode(builder, child)
		}
		builder.WriteString("</")
		builder.WriteString(node.Data)
		builder.WriteByte('>')
	}
}

func topicHTMLVoidElement(name string) bool {
	return name == "br" || name == "hr" || name == "img"
}
