package xbpq

import (
	"regexp"
	"sort"
	"strings"
)

// ---- 内置模板（对齐 XBPQ jar 的「简写」能力）----
//
// 实测统计（XBPQ.json 334 份 csp_XBPQ 源）：203 份（61%）不写 主页url，
// 185 份（55%）只写 分类url+分类 就正常工作——这些字段全部由 jar 内置模板补齐。
// 本文件按 分类url 的形态识别模板家族，为缺失字段补默认截取 pattern（|| 多组备选），
// 使同一份简写规则在本引擎里同样可用。
//
// 家族按「专用 → 通用」排序，命中的家族**依次合并**（先命中的字段优先，
// 后面的家族只补还没被覆盖的字段），使 JSON 接口规则也能拿到详情/播放层默认。
// 与 docs/站点结构模板库.md 一一对应。

// templateFamily 是一种 URL 形态对应的默认字段集。
type templateFamily struct {
	name   string
	match  *regexp.Regexp
	fields map[string]string
}

var families = []templateFamily{
	{
		// 苹果CMS新版 mxone 皮肤（/template/mxone/，如 hanjuds.com、80s成全影视）：
		// 分类形态是「目录 + index-{cateId}-{catePg}.html」（列表目录名与域名无关，故 match 只认 index- 段）；
		// 详情 /{dir}/{数字}.html、播放 /{dir}/play-{id}-{线}-{集}.html、
		// 列表条目 div class="module-item"、分集容器 div class="module-blocklist"（<a> 连排，无 <li>）、
		// 多线路 div class="module-list module-player-list tab-list sort-list"（每条线路一个）。
		// 关键差异（与 §1/§2 myui/stui 老皮肤全部不同）：
		//   ① 线路容器与分集容器同锚点——线路数组必须锚 tab-list（截出的每段=该线路分集框），
		//      锚 module-tab-item 按钮会让每条线路 0 集；
		//   ② 分集是 <a> 连排、段内无 #，播放列表分隔符写 </a>（不是 <li>/不是 #）；
		//   ③ 取流是 iframe+base64 二级页（src="…/url?url=<base64 m3u8>"），引擎 PlayerURL 内置解码，
		//      跳转播放链接【省略不写】；
		//   ④ 搜索 GET：{host}/…list------…-.html?wd={wd}。
		// 放在 MacCMS 之前：专用 → 通用，先命中字段优先。
		name:  "MacCMS新版mxone",
		match: regexp.MustCompile(`(?i)/index-(?:\{cateId\}|[0-9])[^/"?]*\.html`),
		fields: map[string]string{
			"数组":   `class="module-item"><div class="module-item-cover"&&</div></div>||<div class="module-item"&&</div></div>`,
			"标题":   `title="&&"`,
			"副标题":  `class="module-item-text">&&</div>`,
			"图片":   `data-src="&&"||data-original="&&"`,
			"链接":   `href="&&"`,
			"播放数组": `class="module-list module-player-list tab-list sort-list&&</div>`,
			"线路数组": `class="module-list module-player-list tab-list sort-list&&</div>`,
			"播放列表": `</a>`,
			"播放标题": `<span>&&</span>`,
			"播放链接": `href="&&"`,
			"搜索数组":   `<div class="module-search-item"&&<div class="video-info-main">`,
			"搜索标题":   `<h3>&&</h3>`,
			"搜索链接":   `video-serial" href="&&"`,
			"搜索图片":   `data-src="&&"`,
		},
	},
	{
		// MacCMS 自带 API（/api.php/provide/vod/，?ac=videolist|list）：列表层是 JSON。
		// 模板库 §五：接口站列表用 j: 模式，详情/播放仍走 HTML 详情页（下方默认链已带）。
		name:  "MacCMS接口(JSON)",
		match: regexp.MustCompile(`(?i)api\.php/provide/vod|/provide/vod/?|ac=videolist`),
		fields: map[string]string{
			"数组":     `j:list`,
			"标题":     `j:vod_name`,
			"图片":     `j:vod_pic`,
			"副标题":    `j:vod_remarks`,
			"链接":     `/index.php/vod/detail/id/+j:vod_id+.html`,
			"详情url":  `/index.php/vod/detail/id/{id}.html`,
			"搜索url":  `{host}/index.php/vod/search.html?wd={wd}`,
			"播放数组":   `<ul class="stui-content__playlist&&</ul>||<ul class="myui-content__list&&</ul>`,
			"播放列表":   `<a`,
			"播放标题":   `>&&</a>||>&&<`,
			"播放链接":   `href="&&"`,
			"跳转播放链接": `var player_*"url":"&&"`,
		},
	},
	{
		// 苹果CMS/MacCMS 家族：/index.php/vod/show|type/…、/vodshow/…、/vodtype/…、/list/2-1.html 伪静态
		// 页面多为 stui / myui 模板；详情/播放/搜索均有高置信默认。
		// || 备选链按 XBPQ.json 379 份真实规则聚类的高频形态排序（myui→stui→hl→module→通用）。
		name: "MacCMS",
		match: regexp.MustCompile(
			`(?i)/index\.php/(vod|art)/|/vod(show|type|play|detail)/|/list/(?:\{cateId\}|[0-9])[^/"]*\.html|vod-(?:list|play)-id-`),
		fields: map[string]string{
			"数组":   `<a class="myui-vodlist__thumb&&</a>||class="myui-vodlist__box&&</a>||class="stui-vodlist__thumb&&</a>||stui-vodlist__box">&&</div></div>||hl-item-thumb hl-lazy"&&</a>||module-poster-item&&</a>||<li class="col-md-6&&</li>||<li&&</li>`,
			"标题":   `title="&&"||alt="&&"`,
			"图片":   `data-original="&&"||data-src="&&"||src="&&"`,
			"副标题":  `pic-text text-right">&&</span>||class="remark">&&<||module-item-text">&&</div>`,
			"链接":   `href="&&"`,
			"简介":   `detail-content" style=*>&&</span>||class="stui-content__detail&&</div>||简介：</em>&&`,
			"播放数组": `<ul class="stui-content__playlist&&</ul>||<ul class="myui-content__list&&</ul>||id="hl-plays-list"&&</ul>||mod play-list&&</ul||class="play_box&&</div>||class="module-play-list&&</div>`,
			"播放列表": `<a`,
			"播放标题": `>&&</a>||>&&<`,
			"播放链接": `href="&&"`,
			"线路标题": `<h3 class="title">&&</h3>||<span class="title">&&</span>||play_tit&&</`,
			// 聚类最高频跳转形态（15 处，含尾引号——单侧无尾锚会吃到页尾）：
			// var player_ 前缀锚点不会误命中同页 var maccms 的 "url":"。
			"跳转播放链接": `var player_*"url":"&&"`,
			"搜索url":  `{host}/index.php/vod/search.html?wd={wd}`,
			"搜索数组":   `class="stui-vodlist__thumb&&</a>||<li&&</li>`,
		},
	},
	{
		// 短剧/路径式 CMS（模板库 §3/§4）：/show/{id}-{area}-…-{pg}---{year}.html、/vs/、
		// /type/{slug}/{pg}/ 等。conch/module/stui 皮肤混杂，备选链兜底。
		name:  "路径式泛型",
		match: regexp.MustCompile(`(?i)/(show|vs|vshow|screen|list|type|category|fenlei)/(?:\{cateId\}|[0-9a-z])`),
		fields: map[string]string{
			"数组":     `class="stui-vodlist__thumb&&</a>||module-item&&</a>||<li&&</li>`,
			"标题":     `title="&&"||alt="&&"`,
			"图片":     `data-original="&&"||data-src="&&"||src="&&"`,
			"副标题":    `pic-text text-right">&&</span>||module-item-text">&&</div>`,
			"链接":     `href="&&"`,
			"简介":     `detail-content" style=*>&&</span>||class="video-info&&</div>`,
			"播放数组":   `<ul class="stui-content__playlist&&</ul>||class="module-play-list&&</div>||<ul class="myui-content__list&&</ul>||<ul class="content-list&&</ul>`,
			"播放列表":   `<li`,
			"播放标题":   `>&&</a>||>&&<`,
			"播放链接":   `href="&&"`,
			"线路标题":   `<h3 class="title">&&</h3>`,
			"跳转播放链接": `var player_*"url":"&&"`,
		},
	},
	{
		// 通用高频字段（模板库 §〇全局 Top-1）：任何有 分类url 的形态都能兜底。
		// 只补全局最稳的字段，绝不在这里猜 数组/播放数组 这类站专属结构。
		name:  "通用高频",
		match: regexp.MustCompile(`.`),
		fields: map[string]string{
			"标题":   `title="&&"||alt="&&"`,
			"链接":   `href="&&"`,
			"图片":   `data-original="&&"||data-src="&&"||src="&&"`,
			"播放标题": `>&&</a>||>&&<`,
			"播放链接": `href="&&"`,
		},
	},
}

// MatchTemplate 按 分类url 形态返回命中的模板家族名与合并后的默认字段集
// （专用家族优先，后命中家族只补缺口；{host} 已用 category 的站点根替换）。
// category 可以是带 {cateId} 占位的模板形态，也可以是真实数字 URL——
// 指纹分析用它判断「哪些字段本站可简写」，规则解析用它补齐缺失字段。
func MatchTemplate(category string) (string, map[string]string) {
	category = strings.TrimSpace(category)
	if category == "" {
		return "", nil
	}
	origin := hostOrigin(category)
	var names []string
	merged := map[string]string{}
	for _, fam := range families {
		if !fam.match.MatchString(category) {
			continue
		}
		names = append(names, fam.name)
		for key, value := range fam.fields {
			key = normalizeKey(key)
			if strings.Contains(value, "{host}") {
				if origin == "" {
					continue
				}
				value = strings.ReplaceAll(value, "{host}", origin)
			}
			if _, exists := merged[key]; !exists {
				merged[key] = value
			}
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	return strings.Join(names, "、"), merged
}

// TemplateFieldNames 返回一份合并模板覆盖的字段名（排序后），供指纹块列举。
func TemplateFieldNames(fields map[string]string) []string {
	names := make([]string, 0, len(fields))
	for key := range fields {
		names = append(names, key)
	}
	sort.Strings(names)
	return names
}

// applyTemplate 按 分类url 形态补齐缺失字段（不覆盖已写字段）。幂等。
func (r *Rule) applyTemplate() {
	if r.fields == nil {
		r.fields = map[string]string{}
	}
	if r.fields["模板已套用"] != "" {
		return
	}
	category := r.Field("分类url", "分类Url")
	if category != "" {
		if _, merged := MatchTemplate(category); merged != nil {
			for key, value := range merged {
				if strings.TrimSpace(r.fields[key]) == "" {
					r.fields[key] = value
					r.order = append(r.order, key)
				}
			}
		}
	}
	r.fields["模板已套用"] = "1"
}

// TemplateApplied 报告规则是否处于简写模板补齐状态（有 分类url 但核心字段靠模板）。
func (r *Rule) TemplateApplied() bool {
	category := r.Field("分类url", "分类Url")
	return category != "" && r.Field("数组") != "" && !r.declaresField("数组")
}

// TemplateHitNames 报告当前规则 分类url 命中的模板家族名（规则构造后可供 API 回显）。
func (r *Rule) TemplateHitNames() string {
	if r.fields["模板家族名"] != "" {
		return r.fields["模板家族名"]
	}
	name, _ := MatchTemplate(r.Field("分类url", "分类Url"))
	if name != "" {
		r.fields["模板家族名"] = name
	}
	return name
}

func (r *Rule) declaresField(name string) bool {
	return r.declared[normalizeKey(name)]
}

// DeclaresField 报告字段是否是规则原文里显式写的（模板补齐的不算）。
// 格式自检用它跳过模板默认值——模板锚点本来就不在样本里，逐字校验会误报。
func (r *Rule) DeclaresField(name string) bool { return r.declaresField(name) }

// hostOrigin 从绝对地址提取 scheme://host。
func hostOrigin(address string) string {
	index := strings.Index(address, "://")
	if index < 0 {
		return ""
	}
	rest := address[index+3:]
	if slash := strings.IndexAny(rest, "/?#"); slash >= 0 {
		rest = rest[:slash]
	}
	return address[:index+3] + rest
}
