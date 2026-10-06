package xbpq

import (
	"encoding/base64"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// ---- HTML 基础工具 ----

// HTMLAttr 取节点属性值。
func HTMLAttr(node *html.Node, name string) string {
	if node != nil {
		for _, attribute := range node.Attr {
			if attribute.Key == name {
				return attribute.Val
			}
		}
	}
	return ""
}

// HTMLClass 判断节点是否带某个 class。
func HTMLClass(node *html.Node, name string) bool {
	for _, value := range strings.Fields(HTMLAttr(node, "class")) {
		if value == name {
			return true
		}
	}
	return false
}

// HTMLNodes 深度优先遍历，返回所有匹配节点。
func HTMLNodes(root *html.Node, match func(*html.Node) bool) []*html.Node {
	var found []*html.Node
	if root == nil {
		return found
	}
	pending := []*html.Node{root}
	for len(pending) > 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if match(node) {
			found = append(found, node)
		}
		for child := node.LastChild; child != nil; child = child.PrevSibling {
			pending = append(pending, child)
		}
	}
	return found
}

// HTMLFirstClass 按候选 class 顺序找第一个命中节点。
func HTMLFirstClass(root *html.Node, names ...string) *html.Node {
	for _, name := range names {
		matches := HTMLNodes(root, func(node *html.Node) bool { return HTMLClass(node, name) })
		if len(matches) > 0 {
			return matches[0]
		}
	}
	return nil
}

// HTMLText 提取子树纯文本（跳过 script/style）。
func HTMLText(root *html.Node) string {
	var text strings.Builder
	for _, node := range HTMLNodes(root, func(node *html.Node) bool {
		return node.Type == html.TextNode
	}) {
		if node.Parent != nil && (node.Parent.Data == "script" || node.Parent.Data == "style") {
			continue
		}
		text.WriteString(node.Data)
		text.WriteByte(' ')
	}
	return strings.Join(strings.Fields(text.String()), " ")
}

var tagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

// CleanText 去除标签实体噪音。
func CleanText(value string) string {
	value = tagPattern.ReplaceAllString(value, "")
	value = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&#13;", "", "\t", " ").Replace(value)
	value = strings.Join(strings.Fields(value), " ")
	return strings.TrimSpace(value)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// ---- MacCMS 播放地址解析 ----

var playerDataPattern = regexp.MustCompile(`(?s)player_aaaa\s*=\s*(\{.*?\})`)

var playerDataFallback = regexp.MustCompile(`(?s)player_data\s*=\s*(\{.*?\})`)

var playerFieldPattern = regexp.MustCompile(`"?(\w+)"?\s*:\s*"((?:[^"\\]|\\.)*)"`)

// PlayerFields 解析播放页中的 player_aaaa / player_data 对象。
func PlayerFields(body string) map[string]string {
	fields := map[string]string{}
	matches := playerDataPattern.FindStringSubmatch(body)
	if len(matches) < 2 {
		matches = playerDataFallback.FindStringSubmatch(body)
	}
	if len(matches) < 2 {
		return fields
	}
	blob := strings.ReplaceAll(matches[1], `\/`, "/")
	for _, pair := range playerFieldPattern.FindAllStringSubmatch(blob, -1) {
		if len(pair) < 3 {
			continue
		}
		if _, exists := fields[pair[1]]; !exists {
			fields[pair[1]] = pair[2]
		}
	}
	return fields
}

var jsUnicodeEscape = regexp.MustCompile(`(?i)%u([0-9a-f]{4})`)
var jsHexEscape = regexp.MustCompile(`%([0-9a-fA-F]{2})`)

// UnescapeJS 还原 JS escape() 产出的 %uXXXX / %XX。
func UnescapeJS(value string) string {
	if !strings.Contains(value, "%") {
		return value
	}
	if strings.Contains(strings.ToLower(value), "%u") {
		value = jsUnicodeEscape.ReplaceAllStringFunc(value, func(match string) string {
			number, err := strconv.ParseInt(match[len(match)-4:], 16, 32)
			if err != nil {
				return match
			}
			return string(rune(number))
		})
	}
	if decoded, err := url.QueryUnescape(value); err == nil {
		return decoded
	}
	_ = jsHexEscape
	return value
}

// DecryptPlayerURL 按 encrypt 标记解密播放地址。
func DecryptPlayerURL(raw, encrypt string) string {
	switch encrypt {
	case "1":
		return UnescapeJS(raw)
	case "2":
		if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
			return UnescapeJS(string(decoded))
		}
		return raw
	default:
		return raw
	}
}

// PlayerURL 播放页取直链：player_aaaa → 常见 JS 变量 → 页面内 m3u8/mp4 直链。
func PlayerURL(body string) string {
	fields := PlayerFields(body)
	if urlValue := fields["url"]; urlValue != "" {
		urlValue = DecryptPlayerURL(urlValue, fields["encrypt"])
		if strings.HasPrefix(strings.ToLower(urlValue), "http") {
			return urlValue
		}
	}
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)\$\.url\s*=\s*"([^"]+)"`),
		regexp.MustCompile(`(?i)"url"\s*:\s*"([^"]+\.(?:m3u8|mp4)[^"]*)"`),
		regexp.MustCompile(`(?i)(https?://[^\s"'<>]+\.(?:m3u8|mp4)[^\s"'<>]*)`),
	} {
		if matches := pattern.FindStringSubmatch(body); len(matches) > 1 {
			return strings.ReplaceAll(matches[1], `\/`, `/`)
		}
	}
	if urlValue := fields["url"]; urlValue != "" {
		return DecryptPlayerURL(urlValue, fields["encrypt"])
	}
	return ""
}

var unicodeEscapePattern = regexp.MustCompile(`\\?u([0-9a-fA-F]{4})`)

func decodeUnicode(value string) string {
	return unicodeEscapePattern.ReplaceAllStringFunc(value, func(match string) string {
		hex := match[len(match)-4:]
		if number, err := strconv.ParseInt(hex, 16, 32); err == nil {
			return string(rune(number))
		}
		return match
	})
}

// NormalizePlaybackURL 归一化播放地址（还原 \/ 转义、修 unicode 路径）。
func NormalizePlaybackURL(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, `\/`, `/`))
	raw = strings.Trim(raw, ",\\。，;；")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "p.") || strings.Contains(raw, "c1.") {
		parts := strings.Split(raw, "/")
		if len(parts) >= 3 {
			base := strings.Join(parts[:len(parts)-2], "/")
			folder := parts[len(parts)-2]
			if decoded := decodeUnicode(folder); decoded != folder {
				return base + "/" + url.PathEscape(decoded) + "/index.m3u8"
			}
		}
	}
	return raw
}

// IsHTTPMediaURL 判断是否为 http(s) 绝对地址。
func IsHTTPMediaURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil
}

// LooksLikeMedia 粗判是否像媒体直链。
func LooksLikeMedia(address string) bool {
	lower := strings.ToLower(address)
	return strings.Contains(lower, ".m3u8") || strings.Contains(lower, ".mp4") || strings.Contains(lower, ".flv")
}

// ---- MacCMS 详情兜底 ----

var titleSuffixes = []string{"在线观看", "免费观看", "高清完整版", "完整版", "全集", "在线播放", "高清", "免费"}

// DetailTitle 详情标题兜底。
func DetailTitle(document *html.Node) string {
	for _, name := range []string{"module-info-heading", "detail-title", "video-info-title", "page-title"} {
		if node := HTMLFirstClass(document, name); node != nil {
			if text := HTMLText(node); text != "" {
				return cleanTitle(text)
			}
		}
	}
	if nodes := HTMLNodes(document, func(node *html.Node) bool { return node.Data == "title" }); len(nodes) > 0 {
		return cleanTitle(HTMLText(nodes[0]))
	}
	return ""
}

func cleanTitle(value string) string {
	value = strings.TrimSpace(value)
	for _, suffix := range titleSuffixes {
		value = strings.TrimSuffix(value, suffix)
	}
	return strings.TrimSpace(value)
}

// DetailIntro 简介兜底。
func DetailIntro(document *html.Node) string {
	for _, name := range []string{"module-info-introduction-content", "detail-content", "video-info-content", "introduction_introEllipsis"} {
		if node := HTMLFirstClass(document, name); node != nil {
			if text := HTMLText(node); text != "" {
				return truncate(text, 2000)
			}
		}
	}
	for _, meta := range HTMLNodes(document, func(node *html.Node) bool { return node.Data == "meta" }) {
		if strings.EqualFold(HTMLAttr(meta, "name"), "description") {
			if content := strings.TrimSpace(HTMLAttr(meta, "content")); content != "" {
				return truncate(content, 2000)
			}
		}
	}
	return ""
}

// CoverAddress 规范化封面地址（过滤懒加载占位）。
func CoverAddress(raw, pageURL string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "/" || strings.HasPrefix(raw, "data:") {
		return ""
	}
	if !strings.Contains(raw, ".") {
		return ""
	}
	return Absolute(pageURL, raw)
}

// DetailCover 详情封面兜底。
func DetailCover(document *html.Node, pageURL string) string {
	for _, name := range []string{"module-item-pic", "detail-pic", "video-info-pic", "pic"} {
		for _, node := range HTMLNodes(document, func(node *html.Node) bool { return HTMLClass(node, name) }) {
			if address := cardCover(node, pageURL); address != "" {
				return address
			}
		}
	}
	for _, image := range HTMLNodes(document, func(node *html.Node) bool { return node.Data == "img" }) {
		for _, attribute := range []string{"data-original", "data-src", "src"} {
			if address := CoverAddress(HTMLAttr(image, attribute), pageURL); address != "" {
				return address
			}
		}
	}
	return ""
}

func cardCover(node *html.Node, pageURL string) string {
	for _, image := range HTMLNodes(node, func(n *html.Node) bool { return n.Data == "img" }) {
		for _, attribute := range []string{"data-original", "data-src", "src"} {
			if address := CoverAddress(HTMLAttr(image, attribute), pageURL); address != "" {
				return address
			}
		}
	}
	if style := HTMLAttr(node, "style"); style != "" {
		if matches := regexp.MustCompile(`url\(([^)]+)\)`).FindStringSubmatch(style); len(matches) > 1 {
			if address := CoverAddress(strings.Trim(matches[1], `'"`), pageURL); address != "" {
				return address
			}
		}
	}
	return ""
}

// DetailCategory 类型兜底。
func DetailCategory(document *html.Node) string {
	for _, name := range []string{"module-info-tag-link", "detail-tag", "video-info-actor"} {
		nodes := HTMLNodes(document, func(node *html.Node) bool { return HTMLClass(node, name) })
		if len(nodes) > 0 {
			var parts []string
			for _, anchor := range HTMLNodes(nodes[0], func(node *html.Node) bool { return node.Data == "a" }) {
				if text := HTMLText(anchor); text != "" {
					parts = append(parts, text)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, ",")
			}
		}
	}
	return ""
}
