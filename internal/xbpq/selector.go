package xbpq

import (
	"strings"

	"golang.org/x/net/html"
)

// selectorPart 是选择器里的一个层级条件。
type selectorPart struct {
	tag         string
	classes     []string
	id          string
	attrs       [][2]string
	attr        string // 末尾 [href] 形式：取值属性
	contains    []string
	notContains []string
}

type selector struct {
	ancestors []selectorPart
	target    selectorPart
	extract   string // text / html；空 = text
}

func parseSelector(pattern string) (selector, bool) {
	body := pattern
	switch {
	case strings.HasPrefix(body, "p:"):
		body = body[2:]
	case strings.HasPrefix(body, "jsoup:"):
		body = body[6:]
	default:
		return selector{}, false
	}
	var contains, notContains []string
	for strings.HasSuffix(body, "]") {
		open := matchBracket(body)
		if open < 0 {
			break
		}
		inner := body[open+1 : len(body)-1]
		word, payload, _ := strings.Cut(inner, ":")
		switch strings.TrimSpace(word) {
		case "包含":
			contains = append(contains, splitList(payload)...)
			body = strings.TrimSpace(body[:open])
			continue
		case "不包含":
			notContains = append(notContains, splitList(payload)...)
			body = strings.TrimSpace(body[:open])
			continue
		}
		break
	}
	parts := strings.FieldsFunc(body, func(r rune) bool { return r == ' ' || r == '>' })
	var parsed []selectorPart
	for _, token := range parts {
		part, ok := parseSelectorPart(token)
		if !ok {
			return selector{}, false
		}
		parsed = append(parsed, part)
	}
	if len(parsed) == 0 {
		return selector{}, false
	}
	out := selector{target: parsed[len(parsed)-1], extract: "text"}
	if len(parsed) > 1 {
		out.ancestors = parsed[:len(parsed)-1]
	}
	out.target.contains = append(out.target.contains, contains...)
	out.target.notContains = append(out.target.notContains, notContains...)
	return out, true
}

func parseSelectorPart(token string) (selectorPart, bool) {
	part := selectorPart{}
	var brackets []string
	var base strings.Builder
	for index := 0; index < len(token); {
		if token[index] == '[' {
			closing := strings.IndexByte(token[index:], ']')
			if closing < 0 {
				return part, false
			}
			brackets = append(brackets, token[index+1:index+closing])
			index += closing + 2
			continue
		}
		base.WriteByte(token[index])
		index++
	}
	name := base.String()
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		for _, class := range strings.Split(name[dot+1:], ".") {
			if class != "" {
				part.classes = append(part.classes, class)
			}
		}
		name = name[:dot]
	}
	if hash := strings.IndexByte(name, '#'); hash >= 0 {
		if len(name) > hash+1 {
			part.id = name[hash+1:]
		}
		name = name[:hash]
	}
	if name != "" {
		part.tag = strings.ToLower(name)
	}
	for _, bracket := range brackets {
		if bracket == "" {
			continue
		}
		if strings.ContainsAny(bracket, "=`") {
			key, value, _ := strings.Cut(strings.Trim(bracket, "`"), "=")
			value = strings.Trim(value, "'\"")
			part.attrs = append(part.attrs, [2]string{strings.ToLower(strings.TrimSpace(key)), value})
			continue
		}
		part.attr = bracket
	}
	return part, true
}

func partMatches(node *html.Node, part selectorPart) bool {
	if node == nil || node.Type != html.ElementNode {
		return false
	}
	if part.tag != "" && strings.ToLower(node.Data) != part.tag {
		return false
	}
	if part.id != "" && HTMLAttr(node, "id") != part.id {
		return false
	}
	for _, class := range part.classes {
		if !HTMLClass(node, class) {
			return false
		}
	}
	for _, pair := range part.attrs {
		value, found := "", false
		for _, attribute := range node.Attr {
			if strings.EqualFold(attribute.Key, pair[0]) {
				value, found = attribute.Val, true
				break
			}
		}
		if !found || (pair[1] != "" && value != pair[1]) {
			return false
		}
	}
	for _, word := range part.contains {
		if !strings.Contains(HTMLText(node), word) {
			return false
		}
	}
	for _, word := range part.notContains {
		if strings.Contains(HTMLText(node), word) {
			return false
		}
	}
	return true
}

func nodeHasAncestorChain(node *html.Node, ancestors []selectorPart) bool {
	if len(ancestors) == 0 {
		return true
	}
	need := len(ancestors) - 1
	current := node.Parent
	for current != nil && need >= 0 {
		if current.Type == html.ElementNode && partMatches(current, ancestors[need]) {
			need--
		}
		current = current.Parent
	}
	return need < 0
}

// selectorNodes 在 document 上执行选择器，返回目标节点列表。
func selectorNodes(document *html.Node, sel selector) []*html.Node {
	var found []*html.Node
	for _, node := range HTMLNodes(document, func(n *html.Node) bool { return n.Type == html.ElementNode }) {
		if !partMatches(node, sel.target) {
			continue
		}
		if !nodeHasAncestorChain(node, sel.ancestors) {
			continue
		}
		found = append(found, node)
	}
	return found
}

// SelectorFirstString 从 html 字符串重新解析后执行选择器取第一项。
func SelectorFirstString(sourceHTML, pattern string) string {
	values := SelectorStrings(sourceHTML, pattern)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// SelectorStrings 执行选择器并返回全部命中文本/属性值。
func SelectorStrings(sourceHTML, pattern string) []string {
	sel, ok := parseSelector(pattern)
	if !ok {
		return nil
	}
	document, err := html.Parse(strings.NewReader(sourceHTML))
	if err != nil {
		return nil
	}
	var values []string
	for _, node := range selectorNodes(document, sel) {
		if sel.target.attr != "" {
			values = append(values, strings.TrimSpace(HTMLAttr(node, sel.target.attr)))
			continue
		}
		if sel.extract == "html" {
			values = append(values, RenderHTML(node))
			continue
		}
		values = append(values, HTMLText(node))
	}
	return values
}

// PartWithin 判断 node 是否在 scope 子树内。
func PartWithin(node, scope *html.Node) bool {
	if scope == nil {
		return true
	}
	for current := node; current != nil; current = current.Parent {
		if current == scope {
			return true
		}
	}
	return false
}

// RenderHTML 把节点渲染回 HTML 字符串。
func RenderHTML(node *html.Node) string {
	var builder strings.Builder
	if node != nil {
		_ = html.Render(&builder, node)
	}
	return builder.String()
}
