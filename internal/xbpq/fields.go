// Package xbpq 字段字典：XBPQ 规则的完整字段清单（机器可读版）。
//
// 它是三件事的共同底座：
//  1. 写源界面「完整字段表格」的数据源（字段名/说明/所属层/验证方式）；
//  2. 逐字段验证的字段路由依据（FieldSpec.Layer / Kind 决定该字段该拿哪份
//     页面样本实测、用什么方式判定通过）；
//  3. 「统一按字段生成源 JSON」的字段顺序来源——GenerateJSON 按本文件顺序输出，
//     同一份规则每次生成的 JSON 结构与字段次序稳定，便于 diff 与版本管理。
//
// 字段清单对齐《xbpq 写源自学笔记》的「以下为完整字段」段落，并补上本引擎
// 实际接受的别名（列表图片 / 列表标题 / 搜索Url 等）。文档见 docs/字段参考.md。
package xbpq

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// FieldKind 描述一个字段值的形态，决定它该被怎样验证。
type FieldKind string

const (
	FieldPattern FieldKind = "pattern" // 截取串：start&&end / || 备选 / [修饰符] / p: / j:
	FieldURL     FieldKind = "url"     // URL 模板：含 {cateId} {catePg} {wd} {id} 等占位符
	FieldList    FieldKind = "list"    // 分隔列表：分类串、请求头集合等
	FieldLiteral FieldKind = "literal" // 字面量/开关/分隔符
)

// FieldLayer 是字段所属的采集层，决定它该在哪类页面样本上实测。
type FieldLayer string

const (
	LayerSite   FieldLayer = "site"   // 站点与请求
	LayerFilter FieldLayer = "filter" // 分类与筛选
	LayerList   FieldLayer = "list"   // 列表层（分类页条目）
	LayerRoute  FieldLayer = "route"  // 线路层（多线路容器）
	LayerDetail FieldLayer = "detail" // 详情层
	LayerPlay   FieldLayer = "play"   // 播放层（详情页分集）
	LayerStream FieldLayer = "stream" // 直链层（播放页取 m3u8/mp4）
	LayerSearch FieldLayer = "search" // 搜索层
	LayerSwitch FieldLayer = "switch" // 行为开关
)

// FieldSpec 是一个字段的完整说明。
type FieldSpec struct {
	Name    string     `json:"name"`
	Aliases []string   `json:"aliases,omitempty"`
	Layer   FieldLayer `json:"layer"`
	Kind    FieldKind  `json:"kind"`
	Group   string     `json:"group"`
	Desc    string     `json:"desc"`
	Verify  string     `json:"verify"`
	Omit    bool       `json:"omit,omitempty"`   // 简写版：命中内置模板时可省略（模板兜底不落到最终 JSON）
	Switch  bool       `json:"switch,omitempty"` // 开关型字段（1/0）
}

// 界面分组（顺序即界面呈现顺序）。
const (
	GroupSite   = "站点与请求"
	GroupFilter = "分类与筛选"
	GroupList   = "列表层（分类页）"
	GroupRoute  = "线路层（多线路）"
	GroupDetail = "详情层"
	GroupPlay   = "播放层（详情页分集）"
	GroupStream = "直链层（播放页）"
	GroupSearch = "搜索层"
	GroupSwitch = "行为开关"
)

// GroupOrder 是界面分组的固定顺序。
var GroupOrder = []string{
	GroupSite, GroupFilter, GroupList,
	GroupRoute, GroupDetail, GroupPlay,
	GroupStream, GroupSearch, GroupSwitch,
}

// fieldSpecs 是完整字段字典，顺序即「统一按字段生成源 JSON」的字段顺序。
var fieldSpecs = []FieldSpec{
	// ---- 站点与请求 ----
	{Name: "主页url", Aliases: []string{"首页url", "请求"}, Layer: LayerSite, Kind: FieldURL,
		Group: GroupSite, Desc: "站点首页绝对地址。省略时 分类url 必须自带域名。",
		Verify: "拼出地址真实抓取；能返回站点首页即通过。"},
	{Name: "编码", Layer: LayerSite, Kind: FieldLiteral, Group: GroupSite, Omit: true,
		Desc: "页面编码，默认 UTF-8；老站写 gb2312（看 <meta charset>）。",
		Verify: "对照样本 <meta charset> 判定。"},
	{Name: "请求头", Aliases: []string{"User-Agent", "UA"}, Layer: LayerSite, Kind: FieldList,
		Group: GroupSite, Desc: "UA 字符串或完整请求头；采集站通常需要，特殊站写「手机」。",
		Verify: "带该 UA 真实抓首页；站点返回风控页/空壳即不通过。"},
	{Name: "头部集合", Layer: LayerSite, Kind: FieldList, Group: GroupSite, Omit: true,
		Desc: "支持的请求头数据集，用 $ 和 # 分隔。", Verify: "语法检查 + 抓首页比对。"},
	{Name: "播放请求头", Layer: LayerSite, Kind: FieldList, Group: GroupSite, Omit: true,
		Desc: "播放请求单独的头部数据集（CDN 校验 Referer 时需要）。", Verify: "抓直链时带上比对。"},
	{Name: "起始页", Layer: LayerSite, Kind: FieldLiteral, Group: GroupSwitch, Omit: true,
		Desc: "默认 1；站点页码从 0 或 2 起时改。", Verify: "第 1 页应能出条目。", Switch: true},

	// ---- 分类与筛选 ----
	{Name: "分类url", Aliases: []string{"分类Url"}, Layer: LayerFilter, Kind: FieldURL, Group: GroupFilter,
		Desc: "分类页模板。多分类站必须同时含 {cateId} 与 {catePg}；省略 主页url 时须含域名。",
		Verify: "真实抓取：第 1 页应出条目、第 2 页条目应不同（分页生效）。"},
	{Name: "分类", Layer: LayerFilter, Kind: FieldList, Group: GroupFilter,
		Desc: "「名称$ID#名称$ID」。ID 以真实抓取能出条目为准。",
		Verify: "逐个 ID 真实抓取，能提出条目才算有效。"},
	{Name: "分类值", Layer: LayerFilter, Kind: FieldList, Group: GroupFilter, Omit: true,
		Desc: "海阔模式时用，以 & 分隔。", Verify: "语法检查。"},
	{Name: "分类二次截取", Layer: LayerFilter, Kind: FieldPattern, Group: GroupFilter, Omit: true,
		Desc: "先把分类区域截出来再找分类条目，避开导航干扰。",
		Verify: "在首页样本执行 List，应截出分类项。"},
	{Name: "分类数组", Layer: LayerFilter, Kind: FieldPattern, Group: GroupFilter, Omit: true,
		Desc: "分类项外层截取。", Verify: "首页样本 List 应截出 ≥2 个分类项。"},
	{Name: "分类标题", Layer: LayerFilter, Kind: FieldPattern, Group: GroupFilter, Omit: true,
		Desc: "分类项名称截取。", Verify: "分类数组每段内应截出非空名称。"},
	{Name: "分类ID", Layer: LayerFilter, Kind: FieldPattern, Group: GroupFilter, Omit: true,
		Desc: "分类 ID 截取。", Verify: "截出的 ID 拼进 分类url 应能提出条目。"},
	{Name: "筛选", Layer: LayerFilter, Kind: FieldLiteral, Group: GroupFilter, Omit: true,
		Desc: "留空按 分类url 占位符自动生成；也可填外部 json 地址或直接贴 {…}。",
		Verify: "语法检查。"},
	{Name: "类型", Aliases: []string{"类型值"}, Layer: LayerFilter, Kind: FieldList, Group: GroupFilter, Omit: true,
		Desc: "对应 {cateId} 维度的类型筛选，用 $ # 分隔。", Verify: "抓取带该筛选的分类页比对。"},
	{Name: "剧情", Layer: LayerFilter, Kind: FieldList, Group: GroupFilter, Omit: true,
		Desc: "对应 {class}，内置通用模板，可用 [替换:惊悚>>空] 修改。",
		Verify: "抓取带该筛选的分类页比对。"},
	{Name: "地区", Layer: LayerFilter, Kind: FieldList, Group: GroupFilter, Omit: true,
		Desc: "对应 {area}，内置通用模板。", Verify: "抓取带该筛选的分类页比对。"},
	{Name: "年份", Layer: LayerFilter, Kind: FieldList, Group: GroupFilter, Omit: true,
		Desc: "对应 {year}，默认近 15 年；格式「起始年-终止年」。",
		Verify: "抓取带该筛选的分类页比对。"},
	{Name: "排序", Layer: LayerFilter, Kind: FieldList, Group: GroupFilter, Omit: true,
		Desc: "对应 {by}，默认「时间$time#热门$hits」。",
		Verify: "抓取带排序参数的分类页比对条目顺序。"},

	// ---- 列表层 ----
	{Name: "二次截取", Layer: LayerList, Kind: FieldPattern,
		Group: GroupList, Omit: true,
		Desc: "先把主体区域截出来再找条目，避免导航栏同名标签干扰。",
		Verify: "起始锚点须在分类页样本真实存在。"},
	{Name: "数组", Layer: LayerList, Kind: FieldPattern, Group: GroupList, Omit: true,
		Desc: "列表条目外层截取。只框住一个完整条目，别吃掉 href/title。",
		Verify: "在分类页样本执行 List：须截出多段且多数段含 href。"},
	{Name: "标题", Aliases: []string{"列表标题"}, Layer: LayerList, Kind: FieldPattern,
		Group: GroupList, Omit: true, Desc: "条目标题截取。",
		Verify: "在 数组 截出的每段内应都能截出非空标题。"},
	{Name: "链接", Aliases: []string{"列表链接"}, Layer: LayerList, Kind: FieldPattern,
		Group: GroupList, Omit: true, Desc: "条目详情链接截取，通常写 href=\"&&\"。",
		Verify: "须截出完整路径或地址；截出裸数字 ID 判失败。"},
	{Name: "列表图片", Aliases: []string{"图片", "主图", "封面图"}, Layer: LayerList, Kind: FieldPattern,
		Group: GroupList, Omit: true, Desc: "列表封面截取。懒加载站写 data-original/data-src。",
		Verify: "须截出图片地址；占位值（/ 或空）判失败。"},
	{Name: "副标题", Aliases: []string{"备注"}, Layer: LayerList, Kind: FieldPattern,
		Group: GroupList, Omit: true, Desc: "条目角标（集数/评分）。",
		Verify: "部分条目没有属正常，能截出非空即通过。"},

	// ---- 线路层 ----
	{Name: "线路二次截取", Layer: LayerRoute, Kind: FieldPattern, Group: GroupRoute, Omit: true,
		Desc: "先把多线路区域截出来再找每条线路容器。",
		Verify: "起始锚点须在详情页样本真实存在。"},
	{Name: "线路数组", Layer: LayerRoute, Kind: FieldPattern, Group: GroupRoute, Omit: true,
		Desc: "多线路必填：每段=该线路分集容器，值通常与 播放数组 相同。",
		Verify: "详情页样本 List：每段都须含分集链接（锚按钮会让每条 0 集）。"},
	{Name: "线路标题", Layer: LayerRoute, Kind: FieldPattern, Group: GroupRoute, Omit: true,
		Desc: "线路名称截取；不写 && 即固定字面量。",
		Verify: "在各线路容器内应截出非空名称。"},

	// ---- 详情层 ----
	{Name: "详情url", Aliases: []string{"详情页url"}, Layer: LayerDetail, Kind: FieldURL,
		Group: GroupDetail, Omit: true, Desc: "详情页模板，占位符 {id}。省略时用列表链接还原。",
		Verify: "把 {id} 回填真实 ID，抓取应能打开。"},
	{Name: "影片名称", Aliases: []string{"name"}, Layer: LayerDetail, Kind: FieldPattern,
		Group: GroupDetail, Omit: true, Desc: "详情页标题。详情层不要用「标题」。",
		Verify: "详情页样本应截出非空标题。"},
	{Name: "简介", Layer: LayerDetail, Kind: FieldPattern, Group: GroupDetail, Omit: true,
		Desc: "剧情简介。锚点须取正文真实边界，别从 <meta> 抄词。",
		Verify: "截出文本须干净，不含脚本片段。"},
	{Name: "封面", Aliases: []string{"详情封面"}, Layer: LayerDetail, Kind: FieldPattern,
		Group: GroupDetail, Omit: true, Desc: "详情页封面。", Verify: "须截出图片地址。"},
	{Name: "状态", Aliases: []string{"影片状态"}, Layer: LayerDetail, Kind: FieldPattern,
		Group: GroupDetail, Omit: true, Desc: "更新状态（如 更新至12集）。",
		Verify: "详情页样本应截出非空状态。"},
	{Name: "导演", Layer: LayerDetail, Kind: FieldPattern, Group: GroupDetail, Omit: true,
		Desc: "导演。", Verify: "详情页样本应截出非空内容。"},
	{Name: "主演", Layer: LayerDetail, Kind: FieldPattern, Group: GroupDetail, Omit: true,
		Desc: "主演。", Verify: "详情页样本应截出非空内容。"},
	{Name: "影片类型", Layer: LayerDetail, Kind: FieldLiteral, Group: GroupDetail, Omit: true,
		Desc: "影片类型（部分壳使用）。", Verify: "按站点实际需要填。"},
	{Name: "影片年代", Layer: LayerDetail, Kind: FieldLiteral, Group: GroupDetail, Omit: true,
		Desc: "影片年代（部分壳使用）。", Verify: "按站点实际需要填。"},
	{Name: "影片地区", Layer: LayerDetail, Kind: FieldLiteral, Group: GroupDetail, Omit: true,
		Desc: "影片地区（部分壳使用）。", Verify: "按站点实际需要填。"},

	// ---- 播放层 ----
	{Name: "播放二次截取", Layer: LayerPlay, Kind: FieldPattern, Group: GroupPlay, Omit: true,
		Desc: "先把播放区截出来再找分集容器。", Verify: "起始锚点须在详情页样本真实存在。"},
	{Name: "播放数组", Layer: LayerPlay, Kind: FieldPattern, Group: GroupPlay, Omit: true,
		Desc: "分集容器截取。起始锚点不要带闭合的 >（真实标签常带 style 等属性）。",
		Verify: "详情页样本 List：须截出容器段且段内含分集链接。"},
	{Name: "播放列表", Layer: LayerPlay, Kind: FieldLiteral, Group: GroupPlay,
		Desc: "分集【分隔符】，不是截取串。<li> 连排写 </li>，<a> 连排写 </a>，本就有 # 就写 #。",
		Verify: "在详情页样本必须真实出现；按它切分集，段数须与页面条目数一致。"},
	{Name: "播放标题", Layer: LayerPlay, Kind: FieldPattern, Group: GroupPlay, Omit: true,
		Desc: "分集条目标题截取。", Verify: "每个分集段内应都能截出非空标题。"},
	{Name: "播放链接", Layer: LayerPlay, Kind: FieldPattern, Group: GroupPlay, Omit: true,
		Desc: "分集条目播放页链接截取。", Verify: "须截出完整路径或地址，不能是裸 ID。"},
	{Name: "播放链接前缀", Layer: LayerPlay, Kind: FieldLiteral, Group: GroupPlay, Omit: true,
		Desc: "播放链接前缀。", Verify: "拼出的播放页地址应可打开。"},
	{Name: "播放链接后缀", Layer: LayerPlay, Kind: FieldLiteral, Group: GroupPlay, Omit: true,
		Desc: "播放链接后缀。", Verify: "拼出的播放页地址应可打开。"},

	// ---- 直链层 ----
	{Name: "跳转播放链接", Layer: LayerStream, Kind: FieldPattern, Group: GroupStream, Omit: true,
		Desc: "播放页里直链的截取，如 url: '&&'。player_aaaa/iframe+base64 站应省略交给引擎。",
		Verify: "播放页样本截出的值须是 http/m3u8/mp4 地址。"},
	{Name: "免嗅", Layer: LayerStream, Kind: FieldLiteral, Group: GroupSwitch, Omit: true,
		Desc: "默认 0；播放地址是明文时置 1 免嗅探。", Verify: "配合直链实测。", Switch: true},
	{Name: "嗅探词", Layer: LayerStream, Kind: FieldLiteral, Group: GroupSwitch, Omit: true,
		Desc: "默认使用壳的规则。", Verify: "保留项。", Switch: true},
	{Name: "过滤词", Layer: LayerStream, Kind: FieldLiteral, Group: GroupSwitch, Omit: true,
		Desc: "默认使用壳的规则。", Verify: "保留项。", Switch: true},
	{Name: "强制解析", Layer: LayerStream, Kind: FieldLiteral, Group: GroupSwitch, Omit: true,
		Desc: "默认 0；置 1 时全走解析，失败再嗅探。", Verify: "解析实测。", Switch: true},
	{Name: "直接播放", Layer: LayerStream, Kind: FieldLiteral, Group: GroupSwitch, Omit: true,
		Desc: "默认 0；为 1 时所有分类直接播放，也可指定分类（# 分隔）。",
		Verify: "解析出的地址须为可播直链。", Switch: true},
	{Name: "短视频", Layer: LayerStream, Kind: FieldLiteral, Group: GroupSwitch, Omit: true,
		Desc: "默认 0；1 时拉整页短视频到播放列表。", Verify: "直接播放类才需要。", Switch: true},

	// ---- 搜索层 ----
	{Name: "搜索url", Aliases: []string{"搜索Url"}, Layer: LayerSearch, Kind: FieldURL,
		Group: GroupSearch, Omit: true,
		Desc: "搜索页模板，占位符 {wd}。POST 形态：网址;post;键1=值1&键2=值2。",
		Verify: "拼关键词真实抓取，应截出搜索结果条目。"},
	{Name: "搜索模式", Layer: LayerSearch, Kind: FieldLiteral, Group: GroupSearch, Omit: true,
		Desc: "默认 json 与截取混合使用。", Verify: "语法检查。"},
	{Name: "搜索二次截取", Layer: LayerSearch, Kind: FieldPattern, Group: GroupSearch, Omit: true,
		Desc: "先截出搜索结果主体区域。", Verify: "起始锚点须在搜索页样本真实存在。"},
	{Name: "搜索数组", Layer: LayerSearch, Kind: FieldPattern, Group: GroupSearch, Omit: true,
		Desc: "搜索结果条目外层截取。", Verify: "搜索页样本 List：须截出多段含 href。"},
	{Name: "搜索标题", Layer: LayerSearch, Kind: FieldPattern, Group: GroupSearch, Omit: true,
		Desc: "搜索结果标题截取。", Verify: "每段内应截出非空标题。"},
	{Name: "搜索链接", Layer: LayerSearch, Kind: FieldPattern, Group: GroupSearch, Omit: true,
		Desc: "搜索结果详情链接截取。", Verify: "须截出完整路径，不能是裸 ID。"},
	{Name: "搜索图片", Layer: LayerSearch, Kind: FieldPattern, Group: GroupSearch, Omit: true,
		Desc: "搜索结果封面截取。", Verify: "须截出图片地址。"},
	{Name: "搜索链接前缀", Layer: LayerSearch, Kind: FieldLiteral, Group: GroupSearch, Omit: true,
		Desc: "搜索链接前缀。", Verify: "拼出的详情地址应可打开。"},
	{Name: "搜索链接后缀", Layer: LayerSearch, Kind: FieldLiteral, Group: GroupSearch, Omit: true,
		Desc: "搜索链接后缀。", Verify: "拼出的详情地址应可打开。"},

	// ---- 行为开关 ----
	{Name: "首页", Layer: LayerSwitch, Kind: FieldLiteral, Group: GroupSwitch, Omit: true,
		Desc: "可指定首页展示分类与数量，如「电影$20」。",
		Verify: "抓首页应能按该分类取到条目。", Switch: true},
	{Name: "倒序", Layer: LayerSwitch, Kind: FieldLiteral, Group: GroupSwitch, Omit: true,
		Desc: "一般自动倒序；需强制时写 1。", Verify: "条目顺序应倒过来。", Switch: true},
	{Name: "图片代理", Layer: LayerSwitch, Kind: FieldLiteral, Group: GroupSwitch, Omit: true,
		Desc: "默认 0；封面不显示时试 1。", Verify: "封面应能正常加载。", Switch: true},
	{Name: "调试", Layer: LayerSwitch, Kind: FieldLiteral, Group: GroupSwitch, Omit: true,
		Desc: "写 1 让壳弹出截到的链接，仅真机 jar 有效。", Verify: "无（仅真机）。", Switch: true},
}

// FieldDictionary 返回完整字段字典（按生成 JSON 的规范顺序）。
func FieldDictionary() []FieldSpec { return append([]FieldSpec(nil), fieldSpecs...) }

var (
	specByName    = map[string]FieldSpec{}
	specByAlias   = map[string]FieldSpec{}
	fieldIndexSet = map[string]bool{}
)

func init() {
	for _, spec := range fieldSpecs {
		key := normalizeKey(spec.Name)
		specByName[key] = spec
		fieldIndexSet[key] = true
		for _, alias := range spec.Aliases {
			specByAlias[normalizeKey(alias)] = spec
		}
	}
}

// LookupField 按字段名（或别名）查字段说明。未知字段返回 false。
func LookupField(name string) (FieldSpec, bool) {
	key := normalizeKey(name)
	if spec, ok := specByName[key]; ok {
		return spec, true
	}
	if spec, ok := specByAlias[key]; ok {
		return spec, true
	}
	return FieldSpec{}, false
}

// IsKnownField 报告字段名（或别名）是否在字典里。
func IsKnownField(name string) bool {
	_, ok := LookupField(name)
	return ok
}

// CanonicalFieldName 把别名归一到规范字段名；未知字段原样返回（去首尾空白）。
func CanonicalFieldName(name string) string {
	if spec, ok := LookupField(name); ok {
		return spec.Name
	}
	return strings.TrimSpace(name)
}

// OmissibleFields 返回「简写版命中模板时可省略」的字段名清单。
func OmissibleFields() []string {
	var out []string
	for _, spec := range fieldSpecs {
		if spec.Omit {
			out = append(out, spec.Name)
		}
	}
	sort.Strings(out)
	return out
}

// OrderedFieldNames 按字典顺序返回全部字段名。
func OrderedFieldNames() []string {
	out := make([]string, 0, len(fieldSpecs))
	for _, spec := range fieldSpecs {
		out = append(out, spec.Name)
	}
	return out
}

// GenerateJSON 按字典顺序把字段映射序列化成规则 JSON 文本。
//
// 这是「统一按字段生成源 JSON」的唯一出口：
//   - 值为空的字段一律不写（模板兜底的固化模块不落到最终 JSON 里——
//     这正是简写版的原理）；
//   - 键顺序固定为字典顺序，同一份字段集每次生成结果完全一致，便于 diff；
//   - 用标准库逐键编码，pattern 里的引号/反斜杠正确转义，
//     不会出现笔记里「\" 忘记转义」那类问题。
func GenerateJSON(fields map[string]string) string {
	lookup := make(map[string]string, len(fields))
	for key, value := range fields {
		if strings.TrimSpace(key) == "" {
			continue
		}
		lookup[normalizeKey(key)] = SanitizeValue(value)
	}
	var builder strings.Builder
	builder.WriteString("{\n")
	first := true
	for _, spec := range fieldSpecs {
		value, ok := lookup[normalizeKey(spec.Name)]
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		if !first {
			builder.WriteString(",\n")
		}
		first = false
		builder.WriteString("  " + quoteJSON(spec.Name) + ": " + quoteJSON(value))
	}
	var extras []string
	for key := range lookup {
		if fieldIndexSet[key] {
			continue
		}
		if strings.TrimSpace(lookup[key]) == "" {
			continue
		}
		extras = append(extras, key)
	}
	sort.Strings(extras)
	for _, key := range extras {
		if !first {
			builder.WriteString(",\n")
		}
		first = false
		builder.WriteString("  " + quoteJSON(key) + ": " + quoteJSON(lookup[key]))
	}
	builder.WriteString("\n}")
	return builder.String()
}

// MarshalNoEscape 把任意值序列化为 JSON 文本，并关闭 HTML 转义。
//
// 关键：标准库 json.Marshal 默认会把 &&、<、> 转成 \u0026、\u003c、\u003e，
// 而 XBPQ 规则的 && 分隔符和 </ul> 这类锚点本就是常见字符，转义后
// 既难读又容易被引擎误判。统一用本函数，保证写出的源保持原始形态。
func MarshalNoEscape(value any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return ""
	}
	return strings.TrimRight(buf.String(), "\n")
}

// quoteJSON 编码单个 JSON 字符串（含两端引号），关闭 HTML 转义。
func quoteJSON(value string) string {
	out := MarshalNoEscape(value)
	if out == "" {
		return `""`
	}
	return out
}

// ReplacementRune 是 UTF-8 替换符（U+FFFD）。它不会出现在任何合法 XBPQ 锚点里，
// 一旦出现就说明文本在抓取/解码或复制过程中发生过字符丢失。把它写进最终规则
// 会变成"坏锚点"，导致引擎截取出乱码或空值——所以生成源 JSON 前必须剔除。
const ReplacementRune = '\uFFFD'

// SanitizeValue 去掉字段值里的乱码替换符（U+FFFD）以及与之相邻的冗余空白，
// 保证写出的源 JSON 不含损坏字符。它只清除替换符本身，不臆造字符：
// 若锚点本来就因抓取不完整而残缺，验证步骤会拦截并提示回到样本重新核对。
func SanitizeValue(value string) string {
	cleaned := strings.ReplaceAll(value, string(ReplacementRune), "")
	return strings.TrimSpace(cleaned)
}

// FirstNonEmpty 返回第一个非空（去空白后）的字符串，全部为空返回空串。
// 供各包复用，避免在每个调用点重复写「配置回退 / 样本优先」这类逻辑。
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
