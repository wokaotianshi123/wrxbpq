package ai

import "strings"

// 规则生成的三种版本：
//   - RuleVersionSimple（简写版，默认）：命中内置模板家族且皮肤吻合的字段直接省略，交给模板兜底，只写必须字段。
//   - RuleVersionFull（完整版）：不依赖模板兜底，把该站采集链路用到的字段全部按样本真实 HTML 显式写全，
//     产出的规则脱离内置模板也能独立运行。
//   - RuleVersionExpert（专家写源版）：在完整版「字段全部显式写全」的基础上，产出后由服务端把每个
//     pattern 拿去真实样本上逐字段实测（截取、取值、切集数），失败字段的实测证据回喂再修，
//     逐个调试验证通过后才输出——让 guoapp3 的 xbpq 模块能直接识别播放。
const (
	RuleVersionSimple = "simple"
	RuleVersionFull   = "full"
	RuleVersionExpert = "expert"
)

// NormalizeRuleVersion 把外部（前端/CLI）传来的版本串归一到 RuleVersionSimple / RuleVersionFull / RuleVersionExpert。
// 空值、无法识别的值一律回落到简写版（与历史默认行为一致）。
func NormalizeRuleVersion(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "full", "完整版", "完整", "完整规则", "quan", "quanbu":
		return RuleVersionFull
	case "expert", "专家", "专家写源", "专家版", "zj", "zhuojia":
		return RuleVersionExpert
	default:
		return RuleVersionSimple
	}
}

// ruleVersionLabel 返回给人类看的版本名。
func ruleVersionLabel(version string) string {
	switch NormalizeRuleVersion(version) {
	case RuleVersionFull:
		return "完整版"
	case RuleVersionExpert:
		return "专家版"
	default:
		return "简写版"
	}
}

// ruleVersionGenerateDirective 生成阶段注入的版本指令（完整版/专家版都要求字段显式写全，简写版靠模板兜底）。
func ruleVersionGenerateDirective(version string) string {
	switch NormalizeRuleVersion(version) {
	case RuleVersionFull, RuleVersionExpert:
		if NormalizeRuleVersion(version) == RuleVersionExpert {
			return "【本次输出：专家写源版——字段全部显式写全，且每个 pattern 必须能通过服务端逐字段实测】\n" +
				"1. 与完整版相同：禁止因为「模板能兜底」就省略字段，指纹「可省略字段清单」里的字段也逐个按样本真实 HTML 显式写出（「写了必坏」例外除外）；主页url 必写。\n" +
				"2. 比完整版更严：写完后服务端会把 数组/标题/链接/列表图片/副标题/播放数组/线路数组/播放列表/播放标题/播放链接 等每个字段\n" +
				"   拿去真实页面样本上逐一执行截取——截不出值、取值是裸 ID、切集数对不上页面条目数的字段会被打回，附上实测证据要求重写。\n" +
				"   所以每条 pattern 都必须来自样本/指纹里逐字真实存在的锚点，先在心里用样本验一遍再写；不确定的宁可在 reason 里说明，不要赌模板默认值。\n" +
				"3. 三条实测铁律（{catePg}、{cateId}、分类 ID）与「pattern 来自真实 HTML」的要求只高不低，一律照上文执行；写了 详情url 就逐字照抄「详情url 模板」实测模板。\n" +
				"4. 【例外】「写了必坏」的字段别写：player_aaaa 站不写 跳转播放链接（见陷阱10）、单线路站不写 线路数组（见陷阱7）、站点没有的形态不为凑字数硬造。\n"
		}
		return "【本次输出：完整版——不靠模板兜底，字段全部显式写全】\n" +
			"1. 与简写版相反：禁止因为「模板能兜底」就省略字段。指纹「可省略字段清单」里列出的那些字段（数组/标题/链接/图片/副标题/简介/播放数组/播放标题/播放链接/跳转播放链接/搜索url 等），" +
			"本版本要【逐个按样本或指纹里的真实 HTML 显式写出来】（第 6 条列出的「写了必坏」例外除外），让规则即使换到没有内置模板的 jar 上也能独立工作。\n" +
			"2. 完整版只是「不省略」，不是「可以照抄模板默认值」：每条 pattern 仍必须是样本 HTML / 指纹里逐字真实存在的字符串，" +
			"模板默认与该站皮肤不符时，按该站真实 HTML 改写，绝不硬套模板默认。\n" +
			"3. 主页url 在完整版里【必须写】（简写版可省，完整版把站点根地址明确写进去）。\n" +
			"4. 三条实测铁律（{catePg}、{cateId}、分类 ID）与「pattern 来自真实 HTML」的要求，完整版只高不低，一律照上文执行；" +
			"详情url 不是硬性必写，但完整版按「字段全部显式写全」本就应把它写出来（形态照样本「详情url 模板」的实测模板抄）。\n" +
			"5. 皮肤不符 / 未命中模板的字段本就是简写版也要写的，完整版同样写；不要为了省事把某字段留空或省略。\n" +
			"6. 【例外】「写了必坏」的字段两版都别写：player_aaaa 站不写 跳转播放链接（见陷阱10）、单线路站不写 线路数组（见陷阱7）、" +
			"站点根本没有的形态（无筛选就不写 {year}/{area} 之类）不为凑字数硬造——完整版写的是【该站真实存在】的字段，不是全字典。\n"
	default:
		return "【本次输出：简写版——命中模板的字段可省略，交给引擎兜底】\n" +
			"按上文「简写机制」：命中内置模板家族且皮肤吻合的字段直接省略，只写必须字段（分类url、分类、" +
			"搜索url（模板没给时），以及与皮肤不符 / 模板截不到的字段）。\n" +
			"⚠ 简写版也必须写 播放列表：真机 jar 没它默认按 # 切分集，stui/MacCMS 的 <li> 连排分集段内无 # → " +
			"真机只剩 1 集（见陷阱 12），模板默认只对本地引擎有效，别省。\n"
	}
}

// ruleVersionFixRequirement 修复阶段针对版本的差异化要求（返回一条编号要求文本，不含编号）。
func ruleVersionFixRequirement(version string) string {
	switch NormalizeRuleVersion(version) {
	case RuleVersionFull:
		return "（本轮是完整版）保持【所有字段都显式写全】：修好的字段继续逐字写出，模板能兜底的字段也不要删回省略——完整版不允许退化成简写；" +
			"缺哪个字段（含主页url）就按样本真实 HTML 补哪个，pattern 一律来自样本 / 指纹，禁止用模板默认值充数。\n"
	case RuleVersionExpert:
		return "（本轮是专家版）保持【所有字段都显式写全】且每个 pattern 必须能在真实样本上实测截出值：" +
			"修好的字段继续逐字写出，不允许退化成简写；pattern 一律来自样本 / 指纹里逐字真实存在的锚点，改完后自问一遍「拿这个锚点在样本原文里跑 start&&end 能不能截出想要的值」，不能就别交。\n"
	default:
		return "（本轮是简写版）简写过度导致某字段截不到（模板默认与样本皮肤不符）：只把坏的那个字段按样本 HTML 逐字补写，" +
			"其它可省字段继续省——不要退化成全字段堆砌。\n"
	}
}

// SystemPrompt 是规则生成的系统指令。
const SystemPrompt = `你是一名 TVBox/XBPQ 爬虫规则编写专家。任务：根据用户提供的视频站点 HTML 样本，产出一份可直接使用的 XBPQ 规则 JSON。

# 输出要求（最高优先级）
1. 只输出一个 JSON 对象，不要 Markdown 代码块、不要任何解释文字、不要前后缀。
2. JSON 的键必须是中文字段名。
3. 不要编造没在样本里出现过的 URL 结构。所有 pattern 必须来自样本中真实存在的字符串。
4. pattern 里的引号在 JSON 中要用 \" 转义。

# 简写机制（XBPQ 最核心的优势，必须会用）
XBPQ jar 与本引擎都内置模板：按「分类url」形态识别站点家族，自动补齐 数组/标题/链接/图片/副标题/
简介/播放数组/播放标题/播放链接/跳转播放链接/搜索url 等字段的默认截取链（|| 多皮肤备选）。
实测 61% 的真实规则不写 主页url，55% 只写「分类url + 分类」两个字段就正常工作。
因此写规则的正确姿势是【按网页实际源框架决定写与不写】，而不是全字段堆砌：
1. 指纹块给出「命中内置模板家族」与「可省略字段清单」：清单里且样本 HTML 与皮肤吻合的字段——省略不写，交给模板兜底。
2. 必须写的字段：分类url、分类、搜索url（模板没给或站形态不同时）；
   以及样本 HTML 与模板默认链对不上的字段——那些必须按样本逐字实测写。
3. 【简写三条铁律，违反必翻车】：
   a) 省略 主页url 时，分类url 必须写含域名的绝对地址（https://站点域名/…）——相对路径 jar 定位不到站点，直接识别失败。
   b) 分类url 必须同时含分类占位 {cateId} 和分页占位 {catePg}——缺 {catePg} 翻页永远停在第 1 页，
      paging 检测必挂；缺 {cateId} 切分类永远打开同一个页面，catalog 串档必挂（规则声明了 ≥2 个分类时）。
      {cateId} 放哪一段以指纹「分类检测」实测位置为准，{catePg} 的【形态】（/{catePg}/、-{catePg}.html、?pg={catePg}…）
      必须以指纹「分页实测」结论为准：
      实测通过 → 照抄实测模板（页码数字替换处写 {catePg}），禁止改成别的形态；
      没有实测结论 → 按样本分页链接形态写最可信的一种，并在验证 paging 失败时逐个换形态重试，
      形态对错只有"拼出来真实抓取比对两页内容"才能判定，不能凭长相推断。
   c) 分类字段（名称$ID#名称$ID）里的 ID 必须以指纹「分类检测」实测结论为准：
      有「已实测分类串」→ 分类字段逐字取它（这些 ID 是服务端真实抓取过、页面能出 ≥3 个条目的）；
      有「实测失败分类」→ 那些名称的 ID 不要写进 分类 字段（抓回页面空/失败，写了点进去没内容）；
      「{cateId} 位置（实测）」标了分类 ID 在 URL 哪一段 → 分类url 里 {cateId} 就放在那一段，禁止挪到别处；
      没有实测结论（样本不足/导航扒不齐）→ 按首页导航逐字抄，并以验证 catalog 能否出条目把关，
      分类 ID 对不对只有"拼进分类url 真抓一次、看页面有没有条目"才能判定，不能从导航扒个数字就叫它照抄。
   （详情url 不作硬性必写【10-10 取消旧规定】：简写版可省略，交给模板/列表链接还原兜底；
   完整版按「字段全部显式写全」应写它。无论哪版，一旦写了 详情url，形态逐字照抄样本
   「详情url 模板」给出的实测模板（服务端由真实详情页地址反推、回填比对逐字一致）；
   没有该样本才从条目链接里找数字 ID 段换成 {id}，并以 detail 步骤实测能打开把关。）
4. 皮肤不符（指纹提示"未识别到标准皮肤"或"⚠ 皮肤与模板链不吻合"）：不要简写，核心字段全部按样本写。
5. 多线路站的 线路数组、player_aaaa 站的 跳转播放链接 省略策略见下方陷阱清单，它们优先于简写规则。
简写不对时的递进修法（排错步骤）：①只留 分类url+分类 起步；②无数据→补 分类/数组/标题/链接/图片；
③点不开详情→补 链接；④无播放列表→补 播放数组 相关；⑤无法播放→补 播放链接/跳转播放链接。

# 字段字典（只写站点需要的，不要塞无用字段）
- 主页url    站点首页绝对地址（模板可推导，简写时可省）
- 请求头     UA 字符串，采集站通常需要
- 分类       "名称$ID#名称$ID"，如 "电影$1#电视剧$2#综艺$3#动漫$4"
- 分类url    分类页模板，占位符 {cateId}（分类 ID，多分类站【必写】，缺了切分类全开同一页）、{catePg}（页码，【必写】，缺了 paging 必挂）；
             两个占位必须同时出现在同一个 分类url 里；
             省掉 主页url 时必须写含域名的绝对地址 https://站点域名/…；
             筛选占位符 {area} {class} {year} {by} 写进模板即自动开启筛选；
             {lang} {letter} 支持差，【不要写】（写了筛选会坏）
- 搜索url    搜索页模板，占位符 {wd}；jar/模板多数能自动获取，形态标准时可省；POST 形态：网址;post;键1=值1&键2=值2
- 详情url    详情页模板，占位符 {id}；简写版可省（模板/列表链接还原兜底），完整版应写；写了就照「详情url 模板」样本的实测模板逐字抄
- 数组       列表条目外层截取，如 "<li class=\"stui-vodlist__item\">&&</li>"
- 二次截取   先把主体区域截出来再找条目，避免导航栏同名标签干扰
- 标题       条目标题截取
- 链接       条目详情链接截取（通常 href="&&"）
- 列表图片   列表封面截取（写「图片」也可以，但显式写「列表图片」更稳）
- 搜索图片   搜索结果封面截取
- 副标题     条目右下角角标（集数/评分）
- 影片名称   详情页标题（详情层语义，不要用 标题）
- 简介 / 封面 / 类型 / 导演 / 主演
- 播放二次截取 / 播放数组   分集容器截取（播放数组 先按 播放二次截取 缩小再截）
- 线路二次截取 / 线路数组 / 线路标题  多线路时必填：循环截取每个线路容器；
  线路数组 可加 [排序:自建蓝光>腾腾>优优] 按线路名优先级排序
- 多线二次截取 / 多线数组 / 多线链接   播放列表不在详情页、要靠再抓一次才拿到时用（少见）
- 播放列表   分集【分隔符】（引擎按它 strings.Split 切开每条分集），默认 #
  ⚠ 它不是截取串，【禁止写成 前缀&&后缀】——页面上找不到该字面量就切不开，
  整个容器会被当成 1 集（实测 5 线路×4 集的站会只剩 1 集）。
  ⚠ 也【不要省略不写】：本引擎虽有模板默认，但真机 jar 默认按 # 切；stui/MacCMS 分集 <li> 连排、
  段内没有 # 时 jar 把整段当 1 集——真机上详情页只显示一集（55ys9 实测教训）。简写版也要显式写它。
  正确写法：分集是 <li> 结构 → 写 </li>（用闭合标签；开标签常带属性或空格，匹配不稳）；
  分集之间本就有 # / $ 之类符号 → 直接写那个符号。
- 播放标题 / 播放链接   分集条目的标题与链接截取
- 跳转播放链接  播放页里直链的截取 pattern，如 url: '&&'

# 截取语法
- start&&end：取两串之间的内容。A&&B&&C&&D 构成两步链。
- 单侧截取：A&&（取到末尾）或 &&B（从头取到 B）。
- || 分隔多组备选，依次尝试直到命中。
- 修饰符（写在 token 尾部）：[包含:x,y] [不包含:x,y] [替换:a>>b#c>>d] [含序号:n] [排序:a>b>c]
- p: 选择器：p:标签.类#id[attr=val] 空格分隔后代；末尾 [href] 表示取该属性值。
- 通配符 *：起始锚点里可用一个 *（如 <h*>&&</h），匹配任意内容；一个字段只用一个。
- 转义：连接符 $ # & * [ ] 要表本义时用 \ 转义（如 href="?cat\&&&"）。
- + 拼接：字面段与截取段混合，如 /play/+href="/vod/&&.html+-1-1.html；URL+j:取值 也靠它。
- j: json 模式：接口返回 JSON 时不用截取，字段值写 j:路径，如 "数组":"j:data.list"、
  "标题":"j:name"、"跳转播放链接":"j:data.urls[0].cdnUrl"。下标从 0 开始，[]取全部，[n,]跳过前 n 个。
  二次截取填 "Base64" 表示整段只解码；Base64(a&&b) 对截取结果解码。
- 不含 && 也不含 j: 的字段值是「指定字符串」字面量（如 "线路标题":"SVIP短剧"、固定图片 URL）。
- 指定截取：字段值可按分类分组 "默认--a&&b||连续剧--c&&d||搜索--e&&f"，各分类走不同截取（直播 txt 分块常用）。

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
4. 分类 ID 不一定是数字：有的站用英文 slug（tv/movie/cartoon），照抄即可。ID 真假以「分类检测」实测为准——
   导航里看着像 ID 的段（比如筛选串 2----------- 的 2）未必真能出内容，实测通过才可信。
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
   线路数组 只在站点确实存在多条播放线路时才写；单线路站写了会拆出重复线路。
8. 【stui/MacCMS 模板坑】分集列表往往在 <ul class="stui-content__playlist…"> 里；播放数组 必须
   用这个内层 ul（"<ul class=\"stui-content__playlist clearfix\"&&</ul>"），不要用外层的
   <div class="stui-pannel_bd…"> ——div&&</div> 在嵌套结构里会把内容截错位，分集拆不出来。
   多线路站线路数组 与 播放数组 用同一个 ul 锚点是正确写法（每个容器就是一条线路）。
9. 【链接吃前缀坑】链接 一律写 "href=\"&&\"" 截出完整路径（如 /vod/123.html）。
   不要写成 href="/vod/&&.html" 这类把路径前缀嵌进锚点的形态——那样只会截出裸数字 ID，
   拼出的详情链接全坏（目录看着有 36 条但点不开、详情/播放连锁失败）。
10.【MacCMS player_aaaa 站】若指纹提示检测到 player_aaaa 配置对象：【不要写 跳转播放链接】，
   引擎内置解析会直接从 player_aaaa 取 url；手写 "url":"&&" 常先命中同页 var maccms 的
   "url":"站点域名"，截出坏值。
11.【多线路写法（对照 XBPQ-main 官方样例）】三种形态按样本选：
   a) stui/myui：每线路一个并列 ul/div 容器（常带 <h3 class="title">播放源1</h3> 标题行）——
      线路数组 与 播放数组 同锚点；线路标题 截标题行（模板有默认，皮肤不符才手写）。
   b) hl(海蓝)：线路是 <a class="hl-tabs-btn hl-slide-swiper" data-value="…"> 一排按钮，
      分集在切换面板里——线路数组="class=\"hl-tabs-btn hl-slide-swiper\"&&</a>"，
      线路标题=">&&</a>" 或 data-value，可加 [替换:线路1>>腾腾#播放>>空][排序:…]。
   c) JSON 接口：线路数组="j:data.seriesInfo" 之类的路径。
   指纹报告 ≥2 条线路却只写了 播放数组：分集数会变成所有线路之和、且无法切换线路——这是错误。
12.【真机分集塌缩坑（55ys9 实测教训）】播放列表 不能省：真机 jar 没写它时默认按 # 切分集，
   而 stui/MacCMS 的分集是 <li> 单行连排、段内根本没有 # ——jar 把整个播放容器当 1 集，
   真机详情页只显示第一条（本引擎有模板默认会掩盖这个问题，"我们测着对"≠真机对）。
   stui/myui 分集 <li> 结构 → 显式写 "播放列表":"</li>"。
13.【简介抄 meta 坑（55ys9 实测教训）】<meta name="description" content="…剧情:…"> 里的词
   （"剧情:" 等）不代表正文里有它。从 meta 抄锚点配 宽结尾（&&</p>）会从 meta 一路吞到正文，
   简介里混进标题、甚至 var maccms 脚本整段。简介 锚点必须取详情页【正文】里真实出现的边界
   （如 "简介："、<p class="col-pd"> 一类），并在 detail 实测里确认简介文本干净。

# 工作流程
0. 若消息里给出【站点指纹】：它是服务端从真实页面解析并核实过出现次数的锚点，
   pattern 一律逐字符照抄指纹（含空格引号），只有指纹没覆盖的字段才回样本里逐字复制。
0a. 用户消息开头会指定【本次输出：简写版 / 完整版 / 专家写源版】：简写版按上面「简写机制」省略靠模板兜底的字段；
   完整版不省略——指纹「可省略清单」里的字段也逐个显式写出（pattern 仍必须来自真实 HTML，
   且"写了必坏"的例外照旧：player_aaaa 站不写 跳转播放链接、单线路站不写 线路数组）；
   专家写源版在完整版要求之上，每个 pattern 还会被服务端拿去真实样本逐字段实测截值，截不出值会被打回重写，
   所以必须先在样本里人工核对一遍锚点边界再写。
1. 先看「模板与简写」块：命中家族且皮肤吻合 → 只写必须字段，其余按可省略清单省略（完整版除外）；
   未命中/皮肤不符 → 全字段按样本写。
2. 分类字段与 {cateId} 位置以指纹「分类检测」实测结论为准（简写铁律 c）：已实测分类串逐字取用，
   实测失败分类不写入；没有实测结论才从首页导航逐字抄 ID，并以验证 catalog 出条目把关。
3. 找分页链接，确认页码占位符位置。
4. 找列表条目容器，确定「数组」与「二次截取」（简写命中时可交给模板）。
5. 确定标题/链接/封面的截取 pattern（模板皮肤吻合时可省）。
6. 找详情页里分集列表容器，确定「播放数组」；指纹报告多线路时【必须】按陷阱 11 写「线路数组」。
7. 若提供了播放页样本，定位直链藏法并写「跳转播放链接」（player_aaaa 站省略）。`

// BuildGenerateMessages 构造生成规则的消息。
// samples 是若干「标题 + 内容」的页面样本；notes 是用户补充说明（可空）；
// siteFingerprint 是服务端核实过的锚点指纹（可空），有它时以指纹为准。
// version 选择输出「简写版」(RuleVersionSimple) 还是「完整版」(RuleVersionFull)——
// 简写版命中模板的字段可省略靠兜底；完整版所有字段按样本真实 HTML 显式写全。
// 注意：samples 里的「分页实测」「分类检测」样本是服务端真实抓取验证后的结论，
// 优先级最高——分类url 的页码形态与 分类 字段的 ID 都必须照它们写。
func BuildGenerateMessages(siteURL string, samples []Sample, notes, siteFingerprint, version string) []Message {
	var builder strings.Builder
	builder.WriteString("目标站点：" + siteURL + "\n\n")
	builder.WriteString(ruleVersionGenerateDirective(version) + "\n")
	if note := strings.TrimSpace(notes); note != "" {
		builder.WriteString("用户补充说明（请优先采纳，可能包含站点特性、期望字段等）：\n" + note + "\n\n")
	}
	builder.WriteString("样本里的「分页实测」条目是服务端【真实拼接第 2 页 → 抓取 → 与第 1 页比对条目】后的结论：\n")
	builder.WriteString("它给出实测通过的 分类url 模板（页码数字处换成 {catePg}）。分类url 的页码形态一律以它为准照抄，禁止按静态长相另猜形态。\n")
	builder.WriteString("分类url 必须【同时】含 {cateId} 与 {catePg} 两个占位——缺 {catePg} 翻页必挂，缺 {cateId} 多分类切档必挂，自检会逐项拦截。\n")
	builder.WriteString("样本里的「分类检测」条目是服务端【逐个真实抓取导航分类链接 → 看页面能否提出条目 → 比对不同 ID 页面是否相同】后的结论：\n")
	builder.WriteString("「已实测分类串」逐字写进 分类 字段，「实测失败分类」不得写入，{cateId} 按它标的位置放进 分类url——分类 ID 真假以它为准，禁止拿导航扒的数字直接照抄。\n")
	builder.WriteString("样本里的「详情url 模板」条目给出了 详情url 的实测模板（服务端由真实详情页地址反推、回填比对逐字一致）：\n")
	builder.WriteString("详情url 不是硬性必写字段——简写版可省略交给兜底；但一旦写它（完整版应写），形态必须逐字照抄该实测模板（{id} 占位），禁止改形态。\n\n")
	if fp := strings.TrimSpace(siteFingerprint); fp != "" {
		builder.WriteString(fp + "\n\n")
		if NormalizeRuleVersion(version) != RuleVersionSimple {
			builder.WriteString("请优先按上面的站点指纹写 pattern，样本仅作指纹未覆盖字段的补充参考。" +
				"本轮是【" + ruleVersionLabel(version) + "】：指纹里标「可省略」的字段也要按指纹/样本的真实 HTML 显式写出（省略清单仅作「这些字段模板本来会兜底」的说明，本版不靠兜底），" +
				"但逐字形态一律以指纹实测锚点为准，指纹没覆盖的字段回样本复制。\n")
		} else {
			builder.WriteString("请优先按上面的站点指纹写 pattern，样本仅作指纹未覆盖字段的补充参考；指纹标了可省略且与皮肤吻合的字段直接省略，交给引擎模板兜底。\n")
		}
	} else {
		builder.WriteString("以下是该站点的页面 HTML 样本（已裁剪）。请据此产出 XBPQ 规则 JSON。\n")
	}
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
// notes 是用户补充说明（可空），会作为额外修复线索交给模型；
// siteFingerprint 非空时作为锚点权威来源，防止修复时再次抄错 HTML。
// version 沿用生成时的版本选择：简写版修复只补坏字段继续省，完整版修复保持字段全写不退化。
func BuildFixMessages(siteURL, ruleJSON string, problems []string, samples []Sample, notes, siteFingerprint, version string) []Message {
	var builder strings.Builder
	builder.WriteString("目标站点：" + siteURL + "\n\n")
	builder.WriteString("本轮版本：【" + ruleVersionLabel(version) + "】（简写版=可省略字段靠模板兜底；完整版=字段全部显式写全、不依赖模板）\n\n")
	builder.WriteString("上一版规则：\n" + ruleJSON + "\n\n")
	builder.WriteString("验证发现以下问题（必须逐条修掉，修不了说明原因）：\n")
	for index, problem := range problems {
		builder.WriteString("- " + problem + "\n")
		_ = index
	}
	if note := strings.TrimSpace(notes); note != "" {
		builder.WriteString("\n用户补充说明（这是用户对站点/规则的第一手观察，务必优先满足，即使与上面的通用要求冲突）：\n" + note + "\n")
	}
	if fp := strings.TrimSpace(siteFingerprint); fp != "" {
		builder.WriteString("\n" + fp + "\n")
	}
	builder.WriteString("\n修复要求：\n")
	builder.WriteString("1. 对照上面「条目样本」检查 数组/二次截取 的边界是否把 链接/标题 要用的关键串截掉了；数组框住完整条目即可，具体值交给字段截取。\n")
	builder.WriteString("2. 若问题出在 detail/play 步骤，对照「详情页样本」里分集容器的真实 HTML 重写 播放数组/播放列表/播放标题/播放链接；播放数组 起始锚点不要带闭合的 >（真实标签常带 style= 等额外属性）；stui/MacCMS 模板要用内层 ul（…playlist…&&</ul>），别用外层 div&&</div>。单线路站不要写 线路数组；指纹确认多线路时 线路数组 与 播放数组 同锚点即可。若指纹提示 player_aaaa 站，删掉 跳转播放链接 字段交给引擎兜底。\n")
	builder.WriteString("3. " + ruleVersionFixRequirement(version))
	builder.WriteString("4. 若 paging（翻页）失败或第 2 页与第 1 页相同：多半是 分类url 漏写分页占位 {catePg}——补上它；若切分类后各分类目录内容相同（串档）：多半是 分类url 漏写 {cateId}——按指纹「分类检测」标的位置补上它（{cateId} 与 {catePg} 必须同时存在）。若是 catalog/detail 直接失败且规则省了 主页url：把 分类url 改成含域名的绝对地址 https://站点域名/…。\n")
	builder.WriteString("5. 只改有问题的字段，其它字段保持原样，输出修正后的完整规则。\n")
	builder.WriteString("6. 只输出 JSON，不要解释。\n")
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

// ExpertIssue 是专家写源里一条「字段实测未通过」的证据。
// 定义在 ai 包里，避免 prompt 反向依赖上层 expert 包。
type ExpertIssue struct {
	Field   string `json:"field"`
	Problem string `json:"problem"`
}

// BuildExpertAuditMessages 构造专家写源的逐字段实测反馈消息：
// 服务端把上一版规则的每个字段拿去真实样本上逐一执行截取，未通过实测的字段连同
// 实测证据（截了几段、取出的值长什么样）一起回喂，要求只修坏字段、逐字段验证后才输出。
func BuildExpertAuditMessages(siteURL, ruleJSON string, issues []ExpertIssue, samples []Sample, notes, siteFingerprint string) []Message {
	var builder strings.Builder
	builder.WriteString("目标站点：" + siteURL + "\n\n")
	builder.WriteString("本轮是【专家写源】：服务端已经把上一版规则的每个字段拿到真实页面样本上逐一执行截取实测（不是格式检查，是真的跑了一遍引擎）。")
	builder.WriteString("下面列出的字段【没有通过实测】。请逐条对照实测证据重写这些字段的 pattern——先在样本原文里人工模拟一遍 start&&end 截取、确认能截出期望的值，再写进规则；禁止照抄模板默认值或凭印象改写 HTML。\n\n")
	builder.WriteString("上一版规则：\n" + ruleJSON + "\n\n")
	builder.WriteString("实测未通过的字段与证据：\n")
	for _, issue := range issues {
		builder.WriteString("- 字段「" + issue.Field + "」：" + issue.Problem + "\n")
	}
	if note := strings.TrimSpace(notes); note != "" {
		builder.WriteString("\n用户补充说明（优先采纳）：\n" + note + "\n")
	}
	if fp := strings.TrimSpace(siteFingerprint); fp != "" {
		builder.WriteString("\n" + fp + "\n")
	}
	builder.WriteString("\n修复要求：\n")
	builder.WriteString("1. 只改上面实测失败的字段；已通过实测的字段一律原样保留，一个都不能丢、不能顺手改写。\n")
	builder.WriteString("2. 每条新 pattern 的锚点必须是下方「页面样本」里逐字真实存在的字符串（含空格与引号），并按样本里条目的真实结构确定边界：数组只管框住一个完整条目，href/title/src 留给 标题/链接/列表图片 各自截取（陷阱 6/9）。\n")
	builder.WriteString("3. 截出的值若是裸 ID、被吞进相邻标签、或整段为空，说明锚点边界选错——换锚点，不要微调大小写或加通配符赌一把。\n")
	builder.WriteString("4. 播放链失败时对照详情页样本重验：播放数组 框分集容器（锚点不带闭合 >），播放列表 是分集之间真实存在的分隔串（<li> 结构写 </li>），播放标题/播放链接 在每个分集段内都能截出值。\n")
	builder.WriteString("5. 输出修正后的【完整】规则 JSON：只输出 JSON，不要解释、不要代码块。\n")
	if len(samples) > 0 {
		builder.WriteString("\n页面样本（逐字段实测就是拿这些原文跑的，改 pattern 前先在下面找到逐字锚点）：\n")
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

// clipFieldValue 裁剪字段当前值，避免超长 pattern 把纠正 prompt 撑爆。
func clipFieldValue(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= 200 {
		return value
	}
	return string(runes[:200]) + "…"
}

// BuildFieldFixMessages 构造「单个字段 AI 纠正」的消息。
//
// 用途：写源界面逐字段填空时，每个字段内容后都有一个「AI 纠正」按钮。用户点它时，
// 前端把该字段名、当前填入的值、服务端实测出来的问题（截不出值 / 取到裸 ID /
// 起始锚点一字未现 / 多线路没写线路数组 …）连同相关页面样本与站点指纹发来，
// 要求 AI 拿网页源码对该字段核实并纠正。
//
// 关键约束：只处理这一个字段，其它字段原样带回。输出仍然是【完整规则 JSON】
//（前端只取回被纠正的那个字段覆盖），这样模型不必学新输出格式，
// 也不会顺手改坏已经逐字段验证通过的其它字段。
func BuildFieldFixMessages(siteURL, fieldName, currentValue, problem string, samples []Sample, notes, siteFingerprint string) []Message {
	var builder strings.Builder
	builder.WriteString("目标站点：" + siteURL + "\n\n")
	builder.WriteString("本轮是【单字段纠正】：只处理字段「" + fieldName + "」，其它字段一律原样输出、一个字都不许改。\n\n")
	builder.WriteString("该字段当前值：" + clipFieldValue(currentValue) + "\n")
	if strings.TrimSpace(problem) != "" {
		builder.WriteString("服务端在真实页面样本上实测出的问题：" + problem + "\n")
	}
	builder.WriteString("\n纠正步骤（请严格按顺序执行，必须结合下方网页源码，不要凭空编）：\n")
	builder.WriteString("1. 先判断这个字段属于哪一层，再去对应「标签」的样本里找真实 HTML：列表层（数组/二次截取/标题/链接/列表图片/副标题/搜索数组/搜索标题/搜索链接）看「分类页」样本；详情层（影片名称/简介/封面/类型/导演/主演/播放数组/线路数组/播放列表/播放标题/播放链接）看「详情页」样本；直链层（跳转播放链接）看「播放页」样本；URL 字段看服务端真实抓取的结论。\n")
	builder.WriteString("2. 在样本原文里【逐字复制】真实存在的字符串当锚点（含空格与引号，不改属性顺序、不删空格）。绝对不要自己编造或改写锚点——锚点必须能在样本里 Ctrl+F 搜到。\n")
	builder.WriteString("3. 在心里用样本跑一遍 start&&end：确认截出来的正是你要的「标题 / 链接 / 图片 / 分集」，而不是空串、裸数字 ID、脚本碎片、整段 HTML 或被相邻标签吞进去的片段。URL 字段则确认占位符正确（{cateId}/{catePg}/{id}/{wd}）且模板完整可拼接。\n")
	builder.WriteString("4. 这是【失败修正】场景：该字段已经服务端在「真实页面源码」上实测，结论是「不通过」，且原因已在上面给出。所以默认必须给出修正值，绝不能为了「看起来有改动」或图省事而把原值原样写回——原值写回只会让验证再次标红。\n")
	builder.WriteString("5. 只有当你能用该字段的截取语法在下方样本上【真正跑出正确结果】（非空、非裸 ID、非脚本碎片）时，才可以保留原值；否则必须给出新的、经样本验证的写法。\n")
	builder.WriteString("6. 输出要求：只输出一个 JSON 对象，不要 Markdown 代码块、不要解释文字；键是中文 XBPQ 字段名、值是字符串；除「" + fieldName + "」以外的字段全部沿用当前值原样输出（可以只写这一个字段，前端会与现有规则合并）。\n")
	if note := strings.TrimSpace(notes); note != "" {
		builder.WriteString("\n用户补充说明（优先采纳）：\n" + note + "\n")
	}
	if fp := strings.TrimSpace(siteFingerprint); fp != "" {
		builder.WriteString("\n" + fp + "\n")
	}
	if len(samples) > 0 {
		builder.WriteString("\n页面样本（改 pattern 前先在下面找到逐字锚点）：\n")
		for _, sample := range samples {
			builder.WriteString("\n===== 样本：" + sample.Label + " =====\n")
			builder.WriteString(sample.Content)
			builder.WriteString("\n")
		}
	}
	builder.WriteString("\n请输出纠正后的规则 JSON（只输出 JSON）：")
	return []Message{
		{Role: "system", Content: SystemPrompt},
		{Role: "user", Content: builder.String()},
	}
}
