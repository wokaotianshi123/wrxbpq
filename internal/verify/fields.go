// fields.go 逐字段实测验证：把规则里【显式声明】的每个字段拿到对应页面样本或真实抓取上单独验证，
// 输出每字段 pass/fail/warn/skip 与证据。
//
// 这是写源界面「逐字段填空 → 即时验证 → 出错标红 → AI 纠正」闭环的后端底座：
//   - 截取串字段（pattern）：锚点须在对应层样本里逐字存在，且真跑一遍引擎截取能提出合格值；
//   - URL 字段：真实拼接抓取（分类url 还比对两页验证分页）；
//   - 模板补齐的字段（规则没显式写）返回 skip——简写版原理，固化模块不实测不落 JSON。
package verify

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/wrxbpq/wrxbpq/internal/ai"
	"github.com/wrxbpq/wrxbpq/internal/xbpq"
)

// FieldFinding 是单个字段的验证结论。
type FieldFinding struct {
	Field    string `json:"field"`
	Status   string `json:"status"`   // pass / fail / warn / skip
	Message  string `json:"message"`  // 一句话结论（失败时即「该字段为何标红」的原因）
	Evidence string `json:"evidence"` // 截出的样例值 / 实测数字等佐证
}

// FieldOptions 是逐字段验证的输入。
type FieldOptions struct {
	Rule    string      `json:"rule"`
	Site    string      `json:"site"`
	Samples []ai.Sample `json:"samples"`
	Keyword string      `json:"keyword"` // 搜索类字段实测词
}

// sampleByLabel 按标签前缀取样本正文（probe 的标签形如「分类页 https://…」「详情页 …」）。
func sampleByLabel(samples []ai.Sample, prefix string) string {
	for _, sample := range samples {
		if strings.HasPrefix(sample.Label, prefix) {
			return sample.Content
		}
	}
	return ""
}

var bareIDPattern = regexp.MustCompile(`^[0-9]{1,12}$`)

// fieldCheckCtx 承载一次逐字段验证所需的全部上下文。
// 把原本散落一长串的 checkOneField 参数收进一个结构体，调用点更干净、易读。
type fieldCheckCtx struct {
	ctx         context.Context
	fetcher     *xbpq.Fetcher
	rule        xbpq.Rule
	base        string // 站点根地址（已去尾斜杠）
	keyword     string // 搜索类字段实测词
	samples     []ai.Sample
	homeBody    string // 首页样本
	catalogBody string // 分类页样本
	detailBody  string // 详情页样本
	playBody    string // 播放页样本
}

// 验证相关的阈值（从散布在逻辑里的魔法数字抽出，集中管理、便于统一调整）。
const (
	homeBodyMinLen    = 200 // 首页/带 UA 抓首页少于此字符，疑似风控页或空壳
	detailBodyMinLen  = 300 // 详情页少于此字符，疑似 404 软页或风控
	anchorMinRunes    = 4   // 起始锚点过短（< 此 rune 数）不做"一字未现"硬性判定
	itemSampleLimit   = 8   // 条目段内截取最多抽测前 N 段
	categoryProbeLimit = 5  // 分类实测最多抽测前 N 个
)

// httpLike 判断截出的值像不像 URL/路径。
func fieldHTTPLike(value string) bool {
	return strings.HasPrefix(value, "http") || (strings.HasPrefix(value, "/") && len(value) > 1)
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func clipField(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// CheckFields 验证规则显式声明的字段。fields 为空时验证全部显式字段。
// 返回顺序与字段字典顺序一致，前端可直接映射到表格行。
func CheckFields(ctx context.Context, opts FieldOptions, fields []string) []FieldFinding {
	rule, ok := xbpq.ParseRule(opts.Rule)
	if !ok {
		return []FieldFinding{{Field: "整体", Status: "fail", Message: "规则 JSON 无法识别为 XBPQ 规则"}}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	samples := opts.Samples
	base := strings.TrimRight(strings.TrimSpace(opts.Site), "/")
	if base == "" {
		base = strings.TrimRight(rule.HomeURL(), "/")
	}
	keyword := strings.TrimSpace(opts.Keyword)
	if keyword == "" {
		keyword = "爱"
	}
	fetcher := xbpq.NewFetcher(rule.UserAgent())
	fetcher.Timeout = 25

	// 把请求字段名归一到规范名；未知名保留原样（生成 JSON 时按自定义字段处理）。
	want := map[string]bool{}
	for _, name := range fields {
		if spec, found := xbpq.LookupField(name); found {
			want[spec.Name] = true
		} else {
			want[xbpq.CanonicalFieldName(name)] = true
		}
	}
	checkAll := len(want) == 0

	declared := rule.DeclaredFields()
	declaredCanonical := map[string]string{}
	for key, value := range declared {
		name := xbpq.CanonicalFieldName(key)
		declaredCanonical[name] = value
	}

	catalogBody := sampleByLabel(samples, "分类页")
	detailBody := sampleByLabel(samples, "详情页")
	playBody := sampleByLabel(samples, "播放页")
	homeBody := sampleByLabel(samples, "首页")

	c := fieldCheckCtx{
		ctx:         ctx,
		fetcher:     fetcher,
		rule:        rule,
		base:        base,
		keyword:     keyword,
		samples:     samples,
		homeBody:    homeBody,
		catalogBody: catalogBody,
		detailBody:  detailBody,
		playBody:    playBody,
	}

	var out []FieldFinding
	add := func(name string) {
		value := rule.Field(name)
		if strings.TrimSpace(value) == "" {
			out = append(out, FieldFinding{Field: name, Status: "skip", Message: "未填写，跳过"})
			return
		}
		if _, explicit := declaredCanonical[name]; !explicit {
			out = append(out, FieldFinding{Field: name, Status: "skip",
				Message:  "模板兜底值（规则未显式声明），无需实测",
				Evidence: clipField(value, 80)})
			return
		}
		out = append(out, checkOneField(c, name, value))
	}

	if checkAll {
		for _, name := range xbpq.OrderedFieldNames() {
			if _, explicit := declaredCanonical[name]; explicit {
				add(name)
			}
		}
		// 字典外的自定义显式字段
		for name := range declaredCanonical {
			if !xbpq.IsKnownField(name) {
				add(name)
			}
		}
	} else {
		for name := range want {
			add(name)
		}
	}
	return out
}

// checkOneField 分派到具体验证函数：URL 字段在线抓真实页面，截取串字段交给 checkPatternField 实跑引擎。
// 接收 fieldCheckCtx 而非一长串参数，调用点更清爽。
func checkOneField(c fieldCheckCtx, name, value string) FieldFinding {
	ctx := c.ctx
	fetcher := c.fetcher
	rule := c.rule
	base := c.base
	keyword := c.keyword
	homeBody := c.homeBody
	catalogBody := c.catalogBody
	detailBody := c.detailBody

	fail := func(message string) FieldFinding {
		return FieldFinding{Field: name, Status: "fail", Message: message}
	}
	pass := func(message, evidence string) FieldFinding {
		return FieldFinding{Field: name, Status: "pass", Message: message, Evidence: evidence}
	}

	switch name {
	case "主页url", "首页url", "请求":
		if base == "" {
			return fail("地址为空")
		}
		body, err := fetcher.Get(ctx, base, "")
		if err != nil {
			return fail("抓取首页失败：" + err.Error())
		}
		if len(body) < homeBodyMinLen {
			return FieldFinding{Field: name, Status: "warn", Message: "首页返回内容过短，可能是风控页或空壳", Evidence: clipField(body, 120)}
		}
		return pass("首页抓取成功", fmt.Sprintf("%d 字符", len(body)))

	case "分类url", "分类Url":
		return checkCategoryURL(ctx, fetcher, rule, base)

	case "分类":
		return checkCategories(ctx, fetcher, rule, base)

	case "详情url", "详情页url":
		return checkDetailURL(ctx, fetcher, rule, base, catalogBody)

	case "搜索url", "搜索Url":
		return checkSearchURL(ctx, fetcher, rule, base, keyword)

	case "播放列表":
		return checkPlayDivider(rule, detailBody, fail)

	case "编码":
		metaCharset := regexp.MustCompile(`(?i)charset=["']?([a-zA-Z0-9_-]+)`).FindStringSubmatch(homeBody)
		expect := ""
		if len(metaCharset) == 2 {
			expect = strings.ToLower(metaCharset[1])
		}
		got := strings.ToLower(strings.TrimSpace(value))
		if expect != "" && !strings.Contains(expect, got) && !strings.Contains(got, expect) {
			return FieldFinding{Field: name, Status: "warn", Message: "样本 <meta charset> 显示 " + expect + "，与填写的 " + got + " 不一致", Evidence: expect}
		}
		return pass("与样本 meta charset 一致", expect)

	case "请求头", "User-Agent", "UA":
		if strings.EqualFold(strings.TrimSpace(value), "手机") {
			return pass("手机端 UA，交由抓取层处理", "")
		}
		if base != "" {
			body, err := fetcher.Get(ctx, base, "")
			if err != nil {
				return fail("带请求头抓取首页失败：" + err.Error())
			}
			if len(body) < homeBodyMinLen {
				return FieldFinding{Field: name, Status: "warn", Message: "带该请求头抓首页返回过短，疑似风控页", Evidence: clipField(body, 120)}
			}
			return pass("带该请求头抓首页成功", fmt.Sprintf("%d 字符", len(body)))
		}
		return FieldFinding{Field: name, Status: "warn", Message: "没有站点地址可实测，仅做语法检查", Evidence: ""}
	}

	// 搜索层字段没有离线搜索页样本可核（probe 不带搜索页），交由整表 search 步骤在线验证。
	if strings.HasPrefix(name, "搜索") {
		return FieldFinding{Field: name, Status: "skip", Message: "搜索层字段请点「整表验证」里的 search 步骤在线实测", Evidence: ""}
	}

	// ---- 截取串字段：锚点核实 + 引擎实跑 ----
	spec, known := xbpq.LookupField(name)
	target := patternTarget(c, name, spec, known)
	if strings.TrimSpace(target) == "" {
		return FieldFinding{Field: name, Status: "skip", Message: "缺少对应层页面样本，无法离线实测", Evidence: ""}
	}
	return checkPatternField(c, name, value, spec, known, target)
}

// patternTarget 根据字段所属层，选出该字段应在哪类页面样本上实测。
func patternTarget(c fieldCheckCtx, name string, spec xbpq.FieldSpec, known bool) string {
	switch {
	case known && spec.Layer == xbpq.LayerList:
		return c.catalogBody
	case known && (spec.Layer == xbpq.LayerRoute || spec.Layer == xbpq.LayerPlay || spec.Layer == xbpq.LayerDetail):
		return c.detailBody
	case known && spec.Layer == xbpq.LayerStream:
		return c.playBody
	case known && spec.Layer == xbpq.LayerFilter:
		return firstNonEmptyStr(c.homeBody, c.catalogBody)
	default:
		return firstNonEmptyStr(c.catalogBody, c.detailBody, c.homeBody, c.playBody)
	}
}

// startAnchor 取 pattern 第一段备选的开始锚点（&& 前、|| 前）。
func startAnchor(pattern string) string {
	first := strings.SplitN(strings.SplitN(pattern, "&&", 2)[0], "||", 2)[0]
	return strings.TrimSpace(first)
}

// checkPatternField 用引擎截取函数在目标样本上实跑：先判语法与锚点，再按字段类型分派到具体子函数。
func checkPatternField(c fieldCheckCtx, name, value string, spec xbpq.FieldSpec, known bool, sample string) FieldFinding {
	prefixTag := patternPrefixTag(value)

	if !known {
		return FieldFinding{Field: name, Status: "warn",
			Message:  "字段不在字典里，按自定义字段处理：仅检查非空与 JSON 转义",
			Evidence: clipField(value, 80)}
	}

	// 语法：pattern 类必须含 &&（选择器/json 豁免）；不能含字面 \" 转义。
	if msg := patternSyntaxError(spec, value, prefixTag); msg != "" {
		return FieldFinding{Field: name, Status: "fail", Message: msg}
	}

	// 锚点逐字核实（json/选择器豁免）。
	if prefixTag == "" {
		start := startAnchor(value)
		if len([]rune(start)) >= anchorMinRunes && strings.Count(sample, start) == 0 {
			return FieldFinding{Field: name, Status: "fail",
				Message: "起始锚点 \"" + clipField(start, 48) + "\" 在对应页面样本里一字未现——pattern 必须逐字复制样本原文"}
		}
	}

	switch {
	case isContainerField(name):
		return checkContainerField(c, name, value, sample)
	case isItemField(name):
		return checkItemField(c, name, value, spec, sample)
	case name == "跳转播放链接":
		return checkStreamField(name, value, sample)
	case strings.Contains(name, "二次截取"):
		return checkSecondaryField(name, value, sample)
	default:
		return checkDefaultField(name, value, sample, prefixTag)
	}
}

// patternPrefixTag 识别 pattern 的特殊前缀：j: 表示 JSON 路径，p:/jsoup: 表示选择器。
func patternPrefixTag(value string) string {
	switch {
	case strings.HasPrefix(value, "j:"):
		return "json"
	case strings.HasPrefix(value, "p:") || strings.HasPrefix(value, "jsoup:"):
		return "selector"
	default:
		return ""
	}
}

// patternSyntaxError 返回 pattern 的语法错误（空串表示通过）。
func patternSyntaxError(spec xbpq.FieldSpec, value, prefixTag string) string {
	if spec.Kind == xbpq.FieldPattern && prefixTag == "" && !strings.Contains(value, "&&") {
		return "缺少 && 分隔符，XBPQ 截取语法必须是 start&&end（或单侧 A&& / &&B）；JSON 接口站可用 j: 前缀"
	}
	if strings.Contains(value, `\\"`) {
		return `pattern 含字面 \"（反斜杠引号）——JSON 解码后引号应为 " 本身，检查是否多写了一层转义`
	}
	return ""
}

// isContainerField 报告字段是否为「容器型」（截出多个条目/分集段）。
func isContainerField(name string) bool {
	return name == "数组" || name == "分类数组" || name == "线路数组" ||
		name == "播放数组" || name == "多线数组"
}

// isItemField 报告字段是否为「条目段内字段」（在容器截出的每段里再截取值）。
func isItemField(name string) bool {
	return name == "标题" || name == "链接" || name == "列表图片" || name == "副标题" ||
		name == "影片名称" || name == "简介" || name == "封面" || name == "状态" ||
		name == "导演" || name == "主演" || name == "播放标题" || name == "播放链接" ||
		name == "线路标题" || name == "分类标题" || name == "多线链接"
}

// checkContainerField 验证容器型字段：截出的段数与「是否含分集链接」判定。
func checkContainerField(c fieldCheckCtx, name, value, sample string) FieldFinding {
	segments := xbpq.List(sample, value)
	if len(segments) == 0 {
		return FieldFinding{Field: name, Status: "fail",
			Message: "在页面样本上截不出任何段——结束锚点可能把结构框断了，或锚点组合不对"}
	}
	withLink := 0
	for _, segment := range segments {
		if strings.Contains(segment, `href="`) || strings.Contains(segment, "href='") {
			withLink++
		}
	}
	if name == "线路数组" && withLink == 0 {
		return FieldFinding{Field: name, Status: "fail",
			Message: fmt.Sprintf("截出 %d 段但没有一段含分集链接——线路数组 每段必须=该线路的分集容器，锚成线路切换按钮会让每条线路 0 集", len(segments))}
	}
	if withLink == 0 && (name == "数组" || name == "播放数组") {
		return FieldFinding{Field: name, Status: "warn",
			Message: fmt.Sprintf("截出 %d 段但没有一段含 href——确认这些段确实是条目/分集容器", len(segments))}
	}
	evidence := ""
	if len(segments) > 0 {
		evidence = clipField(segments[0], 100)
	}
	return FieldFinding{Field: name, Status: "pass",
		Message:  fmt.Sprintf("截出 %d 段（%d 段含链接）", len(segments), withLink),
		Evidence: evidence}
}

// checkItemField 验证条目段内字段：在外层容器截出的每段里再截取，看能否提出合格值。
func checkItemField(c fieldCheckCtx, name, value string, spec xbpq.FieldSpec, sample string) FieldFinding {
	segments := entrySegments(c.rule, spec, sample)
	if len(segments) == 0 {
		return FieldFinding{Field: name, Status: "fail",
			Message: "外层数组/容器截不出段，无法验证段内截取——先修 数组/播放数组 再回来验本字段"}
	}
	limit := len(segments)
	if limit > itemSampleLimit {
		limit = itemSampleLimit
	}
	okCount := 0
	firstValue := ""
	for _, segment := range segments[:limit] {
		got := strings.TrimSpace(xbpq.CutOnce(segment, value))
		if got == "" {
			continue
		}
		okCount++
		if firstValue == "" {
			firstValue = got
		}
	}
	if okCount == 0 {
		return FieldFinding{Field: name, Status: "fail",
			Message: fmt.Sprintf("在 %d 个段内都截不出值——锚点在条目段内不存在，回样本原文逐字找", limit)}
	}
	f := FieldFinding{Field: name}
	if okCount*3 < limit {
		f.Status = "warn"
		f.Message = fmt.Sprintf("仅 %d/%d 段截出值，锚点可能过窄或不稳", okCount, limit)
	} else {
		f.Status = "pass"
		f.Message = fmt.Sprintf("%d/%d 段截出值", okCount, limit)
	}
	f.Evidence = clipField(firstValue, 80)
	if (name == "链接" || name == "播放链接") && bareIDPattern.MatchString(firstValue) {
		f.Status = "fail"
		f.Message = "截出的是裸数字 ID，拼不成可跳转链接——把路径前缀留给整段，逐字用 href=\"&&\""
	}
	if (name == "列表图片" || name == "封面") && firstValue != "" && !fieldHTTPLike(firstValue) {
		f.Status = "warn"
		f.Message = "截出的图片值不像地址（" + clipField(firstValue, 40) + "），确认站点是否用相对路径或懒加载属性"
	}
	if name == "简介" && (strings.Contains(firstValue, "var ") || strings.Contains(firstValue, "<script")) {
		f.Status = "fail"
		f.Message = "简介截出了脚本内容——锚点从 <meta> 抄词或结尾锚点太宽，吞进了页面脚本"
	}
	return f
}

// checkStreamField 验证跳转播放链接：播放页样本上能截出可播直链。
func checkStreamField(name, value, sample string) FieldFinding {
	got := strings.TrimSpace(xbpq.CutOnce(sample, value))
	if got == "" {
		return FieldFinding{Field: name, Status: "fail",
			Message: "播放页样本截不出直链——锚点不对，或本站地址在 player_aaaa/iframe+base64 里（此时应【省略】该字段交给引擎）"}
	}
	if !fieldHTTPLike(got) && !strings.Contains(got, "m3u8") && !strings.Contains(got, "mp4") {
		return FieldFinding{Field: name, Status: "warn", Message: "截出的值不像可播地址", Evidence: clipField(got, 100)}
	}
	return FieldFinding{Field: name, Status: "pass", Message: "播放页截出直链", Evidence: clipField(got, 100)}
}

// checkSecondaryField 验证二次截取：在页面样本上能截出目标区域。
func checkSecondaryField(name, value, sample string) FieldFinding {
	got := strings.TrimSpace(xbpq.CutOnce(sample, value))
	if got == "" {
		return FieldFinding{Field: name, Status: "fail",
			Message: "在页面样本上截不出区域——起始或结束锚点不存在/边界框断"}
	}
	return FieldFinding{Field: name, Status: "pass",
		Message:  fmt.Sprintf("截出区域 %d 字符", len([]rune(got))),
		Evidence: clipField(got, 80)}
}

// checkDefaultField 兜底验证：锚点存在即认为语法通过（json/选择器豁免时只看是否截出值）。
func checkDefaultField(name, value, sample, prefixTag string) FieldFinding {
	got := strings.TrimSpace(xbpq.CutOnce(sample, value))
	if got == "" && prefixTag == "" && strings.Contains(value, "&&") {
		return FieldFinding{Field: name, Status: "warn",
			Message: "在样本上截出为空，确认该字段在这类页面生效"}
	}
	return FieldFinding{Field: name, Status: "pass", Message: "锚点存在，语法通过", Evidence: clipField(got, 80)}
}

// entrySegments 找到「条目段」：详情/播放/线路层用 线路数组||播放数组 截详情页；
// 列表层用 数组 截分类页；主截取失败时套一层对应二次截取再试。
func entrySegments(rule xbpq.Rule, spec xbpq.FieldSpec, sample string) []string {
	var container, secondary string
	switch spec.Layer {
	case xbpq.LayerPlay, xbpq.LayerRoute, xbpq.LayerDetail:
		container = firstNonEmptyStr(rule.Field("线路数组"), rule.Field("播放数组"))
		secondary = firstNonEmptyStr(rule.Field("播放二次截取"), rule.Field("线路二次截取"))
	default:
		container = rule.Field("数组")
		secondary = rule.Field("二次截取")
	}
	if container == "" {
		return nil
	}
	segments := xbpq.List(sample, container)
	if len(segments) == 0 && secondary != "" {
		if region := xbpq.CutOnce(sample, secondary); region != "" {
			segments = xbpq.List(region, container)
		}
	}
	return segments
}

// checkPlayDivider 播放列表 是分集分隔符不是截取串：必须在详情页播放容器段内真实出现且切得开。
func checkPlayDivider(rule xbpq.Rule, detailBody string, failFinding func(string) FieldFinding) FieldFinding {
	name := "播放列表"
	value := rule.Field(name)
	if detailBody == "" {
		return FieldFinding{Field: name, Status: "skip", Message: "缺少详情页样本，无法实测分隔符", Evidence: ""}
	}
	if strings.Contains(value, "&&") {
		return failFinding("播放列表 是【分集分隔符】不是截取串，不能写成 前缀&&后缀——引擎按它 Split，页面上找不到该字面量，整个播放容器会被当成 1 集")
	}
	container := firstNonEmptyStr(rule.Field("线路数组"), rule.Field("播放数组"))
	scope := detailBody
	var segments []string
	if container != "" && !strings.HasPrefix(container, "j:") && !strings.HasPrefix(container, "p:") {
		segments = xbpq.List(detailBody, container)
		if len(segments) > 0 {
			scope = strings.Join(segments, "\n")
		}
	}
	if !strings.Contains(scope, value) {
		return failFinding(fmt.Sprintf("分隔符 %q 在播放容器段里一次都没出现——Split 切不开，分集只会剩 1 条。<li> 结构写 </li>（开标签常带空格 <li >，匹配不稳）", value))
	}
	worstReal, worstSplit := 0, 0
	for _, segment := range segments {
		realCount := strings.Count(segment, `href="`) + strings.Count(segment, "href='")
		if realCount == 0 {
			continue
		}
		splitCount := len(strings.Split(segment, value))
		if realCount > worstReal {
			worstReal, worstSplit = realCount, splitCount
		}
	}
	if worstReal >= 2 && worstSplit < worstReal {
		return failFinding(fmt.Sprintf("分隔符出现但切不开：分集链接 %d 条只切出 %d 段——换成分集条目之间真实存在且能分开的串", worstReal, worstSplit))
	}
	return FieldFinding{Field: name, Status: "pass", Message: "分隔符在详情页真实存在且能切开分集",
		Evidence: fmt.Sprintf("%d 条链接切 %d 段", worstReal, worstSplit)}
}

// ---- URL 字段在线实测 ----

// checkCategoryURL 拼第 1/2 页真实抓取：第 1 页应出条目、第 2 页条目应不同（分页生效）。
func checkCategoryURL(ctx context.Context, fetcher *xbpq.Fetcher, rule xbpq.Rule, base string) FieldFinding {
	name := "分类url"
	tpl := rule.Field("分类url", "分类Url")
	if !strings.Contains(tpl, "{catePg}") {
		return FieldFinding{Field: name, Status: "fail", Message: "缺少分页占位 {catePg}：翻页会一直停在第 1 页"}
	}
	categories := rule.Categories()
	catID := ""
	if len(categories) > 0 {
		catID = categories[0].ID
	}
	page1 := rule.CategoryURL(base, catID, 1)
	page2 := rule.CategoryURL(base, catID, 2)
	body1, err1 := fetcher.Get(ctx, page1, base)
	if err1 != nil {
		return FieldFinding{Field: name, Status: "fail", Message: "抓取分类第 1 页失败：" + err1.Error(), Evidence: page1}
	}
	if entries := countCatalogEntries(ctx, fetcher, rule, body1); entries == 0 {
		return FieldFinding{Field: name, Status: "fail", Message: "第 1 页抓到了但按 数组 截不出条目——分类url 拼出的页面结构不对", Evidence: page1}
	}
	if page1 == page2 {
		return FieldFinding{Field: name, Status: "fail", Message: "{catePg} 代入 1 和 2 拼出同一个地址，分页占位没生效", Evidence: page1}
	}
	body2, err2 := fetcher.Get(ctx, page2, base)
	if err2 != nil {
		return FieldFinding{Field: name, Status: "warn", Message: "第 2 页抓取失败（可能是单页站或站点限流）：" + err2.Error(), Evidence: page2}
	}
	if strings.TrimSpace(body1) == strings.TrimSpace(body2) {
		return FieldFinding{Field: name, Status: "fail", Message: "第 1 页与第 2 页内容完全相同——{catePg} 形态不对，翻页无效", Evidence: page2}
	}
	return FieldFinding{Field: name, Status: "pass", Message: "两页抓取成功且内容不同，分页生效", Evidence: page1}
}

// countCatalogEntries 用规则数组截条目数（复用整表验证的数组语义）。
func countCatalogEntries(ctx context.Context, fetcher *xbpq.Fetcher, rule xbpq.Rule, body string) int {
	_ = ctx
	_ = fetcher
	arr := rule.Field("数组")
	if arr == "" {
		return -1 // 无法判断，视为有
	}
	return len(xbpq.List(body, arr))
}

// checkCategories 逐个分类 ID 真实抓取，能提出条目才算有效。
func checkCategories(ctx context.Context, fetcher *xbpq.Fetcher, rule xbpq.Rule, base string) FieldFinding {
	name := "分类"
	categories := rule.Categories()
	if len(categories) == 0 {
		return FieldFinding{Field: name, Status: "fail", Message: "分类串解析不出任何「名称$ID」项"}
	}
	var bad []string
	for index, category := range categories {
		if index >= categoryProbeLimit {
			break // 控制时间：抽前 N 个实测
		}
		address := rule.CategoryURL(base, category.ID, 1)
		body, err := fetcher.Get(ctx, address, base)
		if err != nil {
			bad = append(bad, category.Name+"("+category.ID+"):"+err.Error())
			continue
		}
		if entries := countCatalogEntries(ctx, fetcher, rule, body); entries == 0 {
			bad = append(bad, category.Name+"("+category.ID+") 截不出条目")
		}
	}
	if len(bad) == len(categories) {
		return FieldFinding{Field: name, Status: "fail", Message: "全部分类实测失败", Evidence: strings.Join(bad, "；")}
	}
	if len(bad) > 0 {
		return FieldFinding{Field: name, Status: "warn", Message: fmt.Sprintf("%d 个分类实测失败（ID 无效或需登录）", len(bad)), Evidence: strings.Join(bad, "；")}
	}
	return FieldFinding{Field: name, Status: "pass", Message: fmt.Sprintf("分类实测通过（抽测 ≤%d 个）", len(categories))}
}

// checkDetailURL 从分类页样本取一条链接提取 ID，回填 {id} 抓取验证。
func checkDetailURL(ctx context.Context, fetcher *xbpq.Fetcher, rule xbpq.Rule, base, catalogBody string) FieldFinding {
	name := "详情url"
	tpl := rule.Field("详情url", "详情页url")
	if !strings.Contains(tpl, "{id}") {
		return FieldFinding{Field: name, Status: "fail", Message: "详情url 必须含 {id} 占位"}
	}
	id := ""
	// 优先从分类页样本的 链接 截值取 ID
	if catalogBody != "" && rule.Field("数组") != "" {
		for _, entry := range xbpq.List(catalogBody, rule.Field("数组")) {
			link := strings.TrimSpace(xbpq.CutOnce(entry, rule.Field("链接")))
			if link == "" {
				continue
			}
			full := xbpq.Absolute(base, xbpq.JoinLink(base, link))
			if got := xbpq.SourceIDFromURL(full); got != "" {
				id = got
				break
			}
		}
	}
	if id == "" {
		return FieldFinding{Field: name, Status: "skip", Message: "样本里取不到可回填的详情 ID，交给整表验证 detail 步骤", Evidence: tpl}
	}
	address := strings.ReplaceAll(tpl, "{id}", id)
	if !strings.HasPrefix(address, "http") {
		address = xbpq.Absolute(base, address)
	}
	body, err := fetcher.Get(ctx, address, base)
	if err != nil {
		return FieldFinding{Field: name, Status: "fail", Message: "回填 ID " + id + " 抓取详情页失败：" + err.Error(), Evidence: address}
	}
	if len(body) < detailBodyMinLen {
		return FieldFinding{Field: name, Status: "warn", Message: "详情页返回过短，可能 404 软页或风控", Evidence: address}
	}
	return FieldFinding{Field: name, Status: "pass", Message: "回填真实 ID 打开详情页成功", Evidence: address + "（" + fmt.Sprintf("%d 字符", len(body)) + "）"}
}

// checkSearchURL 拼关键词真实抓取，看能否截出结果。
func checkSearchURL(ctx context.Context, fetcher *xbpq.Fetcher, rule xbpq.Rule, base, keyword string) FieldFinding {
	name := "搜索url"
	tpl := rule.Field("搜索url", "搜索Url")
	if !strings.Contains(tpl, "{wd}") {
		return FieldFinding{Field: name, Status: "fail", Message: "搜索url 必须含 {wd} 占位"}
	}
	address := xbpq.RenderURL(tpl, base, map[string]string{"wd": keyword})
	body, err := fetcher.Get(ctx, address, base)
	if err != nil {
		return FieldFinding{Field: name, Status: "fail", Message: "搜索页抓取失败：" + err.Error(), Evidence: address}
	}
	arr := rule.Field("搜索数组")
	if arr != "" {
		if entries := xbpq.List(body, arr); len(entries) == 0 {
			return FieldFinding{Field: name, Status: "warn", Message: "搜索页抓到了，但 搜索数组 截不出结果——若站点返回 JSON 而非 HTML，检查 搜索模式/搜索数组 写法", Evidence: address}
		}
	}
	return FieldFinding{Field: name, Status: "pass", Message: "拼关键词「" + keyword + "」抓取成功", Evidence: address + "（" + fmt.Sprintf("%d 字符", len(body)) + "）"}
}
