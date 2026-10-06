package ai

import "strings"

// SystemPrompt 是规则生成的系统指令。
const SystemPrompt = `你是一名 TVBox/XBPQ 爬虫规则编写专家。任务：根据用户提供的视频站点 HTML 样本，产出一份可直接使用的 XBPQ 规则 JSON。

# 输出要求（最高优先级）
1. 只输出一个 JSON 对象，不要 Markdown 代码块、不要任何解释文字、不要前后缀。
2. JSON 的键必须是中文字段名。
3. 不要编造没在样本里出现过的 URL 结构。所有 pattern 必须来自样本中真实存在的字符串。
4. pattern 里的引号在 JSON 中要用 \" 转义。

# 字段字典（只写站点需要的，不要塞无用字段）
- 主页url    站点首页绝对地址（必填）
- 请求头     UA 字符串，采集站通常需要
- 分类       "名称$ID#名称$ID"，如 "电影$1#电视剧$2#综艺$3#动漫$4"
- 分类url    分类页模板，占位符 {cateId}（分类 ID）、{catePg}（页码）
- 搜索url    搜索页模板，占位符 {wd}
- 详情url    详情页模板，占位符 {id}；不写则用列表链接还原
- 数组       列表条目外层截取，如 "<li class=\"stui-vodlist__item\">&&</li>"
- 二次截取   先把主体区域截出来再找条目，避免导航栏同名标签干扰
- 标题       条目标题截取
- 链接       条目详情链接截取（通常 href="&&"）
- 列表图片   列表封面截取（写「图片」也可以，但显式写「列表图片」更稳）
- 搜索图片   搜索结果封面截取
- 副标题     条目右下角角标（集数/评分）
- 影片名称   详情页标题
- 简介       详情页简介
- 封面       详情页封面
- 类型       详情页分类
- 导演 / 主演
- 播放数组   播放列表容器截取
- 线路数组   多线路时必填：循环截取每个线路容器（值通常与「播放数组」相同）
- 播放列表   分集分隔符，默认 #
- 播放标题 / 播放链接   分集条目的标题与链接截取
- 跳转播放链接  播放页里直链的截取 pattern，如 "url: '&&'"

# 截取语法
- start&&end：取两串之间的内容。A&&B&&C&&D 构成两步链。
- 单侧截取：A&&（取到末尾）或 &&B（从头取到 B）。
- || 分隔多组备选，依次尝试直到命中。
- 修饰符（写在 token 尾部）：[包含:x,y] [不包含:x,y] [替换:a>>b#c>>d] [含序号:n]
- p: 选择器：p:标签.类#id[attr=val] 空格分隔后代；末尾 [href] 表示取该属性值。

# 关键陷阱（踩过的坑，务必避开）
1. 懒加载占位：列表第一条常见 data-original="/"，会让封面变成首页 URL。
   对策：封面 pattern 写成 data-original="https:&&" 这类带协议前缀的锚点。
2. 锚点必须唯一：先确认 pattern 在全页出现次数。若 end 锚点跨过了其它标签会串味，
   优先选更短更近的边界（如用 <span 而不是 &nbsp;）。
3. 播放地址常藏在 <script> 里，形态有：
   - player_aaaa={"url":"...","encrypt":0}
   - Artplayer/CKPlayer/DPlayer 配置里的 url: '...'
   - 直接出现的 https://.../index.m3u8
   能直接截到直链就写「跳转播放链接」，不要走云解析。
   注意："url":" 这类短锚点往往不唯一（页面里 maccms 配置也含它），要用更长的上下文。
4. 分类 ID 不一定是数字：有的站用英文 slug（tv/movie/cartoon），照抄即可。
5. 分页形态要确认：{catePg} 可能出现在路径（/type/tv/2/）也可能在文件名（/list/1-2.html）。
6. 【最常见致命错误】数组(start&&end) 的起始锚点不要吃掉后面字段要用的关键串。
   反例：条目是 <li><a href="/detail/123/" title="片名">…</a>…</li>
   数组写成 "<li><a href=\"/detail/&&</a>" 会把 href= 截掉，导致 "链接":"href=\"&&\""
   在所有条目里都取不到值，目录/搜索/详情/播放全部连锁失败（表现为"抽取到 N 个条目但 0 条可用"）。
   正解：数组用外层完整边界 "<li>&&</li>"，让 href、title 留在条目内，由 链接/标题 字段各自截取。
   口诀：数组只管"框住一个条目"，具体值一律交给 标题/链接/图片 字段去截。
7. 【播放段高频坑】播放数组 的起始锚点不要带闭合的 >：真实标签常带额外属性
   （如 <div class="row" style="display: block;">），写 "<div class=\"row\">&&</div>" 会匹配不上。
   正解：写成 "<div class=\"row\"&&</div>"（去掉 >），只框住标签开头。
   同理，只有站点确实存在多条播放线路时才写 线路数组；单线路站写了会拆出重复线路。

# 工作流程
1. 从首页样本里找出导航中的分类链接，推断分类 ID 与分类页 URL 形态。
2. 找分页链接，确认页码占位符位置。
3. 找列表条目容器，确定「数组」与「二次截取」。
4. 确定标题/链接/封面的截取 pattern。
5. 找详情页里分集列表容器，确定「播放数组」；若有多条线路，必须写「线路数组」。
6. 若提供了播放页样本，定位直链藏法并写「跳转播放链接」。`

// BuildGenerateMessages 构造生成规则的消息。
// samples 是若干「标题 + 内容」的页面样本。
func BuildGenerateMessages(siteURL string, samples []Sample) []Message {
	var builder strings.Builder
	builder.WriteString("目标站点：" + siteURL + "\n\n")
	builder.WriteString("以下是该站点的页面 HTML 样本（已裁剪）。请据此产出 XBPQ 规则 JSON。\n")
	for _, sample := range samples {
		builder.WriteString("\n===== 样本：" + sample.Label + " =====\n")
		builder.WriteString(sample.Content)
		builder.WriteString("\n")
	}
	builder.WriteString("\n请只输出规则 JSON：")
	return []Message{
		{Role: "system", Content: SystemPrompt},
		{Role: "user", Content: builder.String()},
	}
}

// BuildFixMessages 构造修复规则的消息：把验证失败信息和相关样本回传给模型。
func BuildFixMessages(siteURL, ruleJSON string, problems []string, samples []Sample) []Message {
	var builder strings.Builder
	builder.WriteString("目标站点：" + siteURL + "\n\n")
	builder.WriteString("上一版规则：\n" + ruleJSON + "\n\n")
	builder.WriteString("验证发现以下问题（必须逐条修掉，修不了说明原因）：\n")
	for index, problem := range problems {
		builder.WriteString("- " + problem + "\n")
		_ = index
	}
	builder.WriteString("\n修复要求：\n")
	builder.WriteString("1. 对照上面「条目样本」检查 数组/二次截取 的边界是否把 链接/标题 要用的关键串截掉了；数组框住完整条目即可，具体值交给字段截取。\n")
	builder.WriteString("2. 若问题出在 detail/play 步骤，对照「详情页样本」里分集容器的真实 HTML 重写 播放数组/播放列表/播放标题/播放链接；播放数组 起始锚点不要带闭合的 >（真实标签常带 style= 等额外属性）。单线路站不要写 线路数组。\n")
	builder.WriteString("3. 只改有问题的字段，其它字段保持原样，输出修正后的完整规则。\n")
	builder.WriteString("4. 只输出 JSON，不要解释。\n")
	if len(samples) > 0 {
		builder.WriteString("\n相关页面样本：\n")
		for _, sample := range samples {
			builder.WriteString("\n===== 样本：" + sample.Label + " =====\n")
			builder.WriteString(sample.Content)
			builder.WriteString("\n")
		}
	}
	builder.WriteString("\n请输出修正后的完整规则 JSON（只输出 JSON）：")
	return []Message{
		{Role: "system", Content: SystemPrompt},
		{Role: "user", Content: builder.String()},
	}
}

// Sample 是一份待分析的页面样本。
type Sample struct {
	Label   string `json:"label"`
	Content string `json:"content"`
}
