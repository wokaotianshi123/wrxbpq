package xbpq

import (
	"regexp"
	"strings"
)

// ---- 内置模板（对齐 XBPQ jar 的「简写」能力）----
//
// 实测统计（XBPQ.json 334 份 csp_XBPQ 源）：203 份（61%）不写 主页url，
// 185 份（55%）只写 分类url+分类 就正常工作——这些字段全部由 jar 内置模板补齐。
// 本文件按 分类url 的形态识别模板家族，为缺失字段补默认截取 pattern（|| 多组备选），
// 使同一份简写规则在本引擎里同样可用。

// templateFamily 是一种 URL 形态对应的默认字段集。
type templateFamily struct {
	match  *regexp.Regexp
	fields map[string]string
}

var families = []templateFamily{
	{
		// 苹果CMS/MacCMS 家族：/index.php/vod/show|type/…、/vodshow/…、/vodtype/…
		// 页面多为 stui 模板；详情/播放/搜索均有高置信默认。
		// || 备选链按 XBPQ.json 379 份真实规则聚类的高频形态排序（stui→hl→module→通用）。
		match: regexp.MustCompile(`(?i)/index\.php/vod/(show|type)/|/vod(show|type)[/_]|vod-list-id-`),
		fields: map[string]string{
			"数组":     `class="stui-vodlist__thumb&&</a>||stui-vodlist__box">&&</div></div>||hl-item-thumb hl-lazy"&&</a>||module-poster-item&&</a>||<li class="col-md-6&&</li>||<li&&</li>`,
			"标题":     `title="&&"||alt="&&"`,
			"图片":     `data-original="&&"||data-src="&&"||src="&&"`,
			"副标题":    `pic-text text-right">&&</span>||class="remark">&&<||module-item-text">&&</div>`,
			"链接":     `href="&&"`,
			"简介":     `detail-content" style=*>&&</span>||class="stui-content__detail&&</div>||简介：</em>&&`,
			"播放数组":   `<ul class="stui-content__playlist&&</ul>||id="hl-plays-list"&&</ul>||mod play-list&&</ul||class="play_box&&</div>||<ul class="item clearfix&&</ul>`,
			"播放列表":   `<a&&</a>`,
			"播放标题":   `>&&</a>||>&&<`,
			"播放链接":   `href="&&"`,
			"线路标题":   `<h3 class="title">&&</h3>||play_tit&&</`,
			// 聚类最高频跳转形态（15 处，含尾引号——单侧无尾锚会吃到页尾）：
			// var player_ 前缀锚点不会误命中同页 var maccms 的 "url":"。
			"跳转播放链接": `var player_*"url":"&&"`,
			"搜索url":  `{host}/index.php/vod/search.html?wd={wd}`,
			"搜索数组":   `class="stui-vodlist__thumb&&</a>||<li&&</li>`,
		},
	},
	{
		// 短剧/路径式 CMS：/show/{id}-{area}-…-{pg}---{year}.html、/vs/、/vshow/ 等
		// 页面同为 stui 或 module 模板。
		match: regexp.MustCompile(`(?i)/(show|vs|vshow|screen|list)/?\{?cateId\}?`),
		fields: map[string]string{
			"数组":   `class="stui-vodlist__thumb&&</a>||module-item&&</a>||<li&&</li>`,
			"标题":   `title="&&"||alt="&&"`,
			"图片":   `data-original="&&"||data-src="&&"||src="&&"`,
			"副标题":  `pic-text text-right">&&</span>||module-item-text">&&</div>`,
			"链接":   `href="&&"`,
			"简介":   `detail-content" style=*>&&</span>||class="video-info&&</div>`,
			"播放数组": `<ul class="stui-content__playlist&&</ul>||class="module-play-list&&</div>`,
			"播放列表": `<a&&</a>||<li&&</li>`,
			"播放标题": `>&&</a>||>&&<`,
			"播放链接": `href="&&"`,
			"线路标题": `<h3 class="title">&&</h3>`,
			"跳转播放链接": `var player_*"url":"&&"`,
		},
	},
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
	if category == "" {
		return
	}
	for _, fam := range families {
		if !fam.match.MatchString(category) {
			continue
		}
		origin := hostOrigin(category)
		for key, value := range fam.fields {
			key = normalizeKey(key)
			if strings.Contains(value, "{host}") {
				if origin == "" {
					continue
				}
				value = strings.ReplaceAll(value, "{host}", origin)
			}
			if strings.TrimSpace(r.fields[key]) == "" {
				r.fields[key] = value
				r.order = append(r.order, key)
			}
		}
		break
	}
	r.fields["模板已套用"] = "1"
}

// TemplateApplied 报告规则是否处于简写模板补齐状态（有 分类url 但缺核心字段）。
func (r *Rule) TemplateApplied() bool {
	category := r.Field("分类url", "分类Url")
	return category != "" && r.Field("数组") != "" && !r.declaresField("数组")
}

func (r *Rule) declaresField(name string) bool {
	value, found := r.fields[normalizeKey(name)]
	return found && strings.TrimSpace(value) != ""
}

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
