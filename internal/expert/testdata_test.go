package expert

import "github.com/wrxbpq/wrxbpq/internal/ai"

// stuiCatalog 一份精简 stui 目录页样本：4 个条目（含 1 个导航区干扰 li）、图片、角标副标题。
const stuiCatalog = `<html><body>
<ul class="nav"><li><a href="/type/1.html">电影</a></li><li><a href="/type/2.html">电视剧</a></li></ul>
<div class="stui-pannel_bd">
<ul class="stui-vodlist clearfix">
<li><div class="stui-vodlist__box"><a class="stui-vodlist__thumb lazyload" href="/detail/100.html" title="测试影片一" data-original="https://img.a.com/1.jpg"><span class="play hidden-xs"></span></a><div class="stui-vodlist__detail"><h4 class="title text-ellipsis"><a href="/detail/100.html">测试影片一</a></h4><p class="text text-ellipsis text-extend"><span>3.2分</span></p></div></div></li>
<li><div class="stui-vodlist__box"><a class="stui-vodlist__thumb lazyload" href="/detail/101.html" title="测试影片二" data-original="https://img.a.com/2.jpg"><span class="play hidden-xs"></span></a><div class="stui-vodlist__detail"><h4 class="title text-ellipsis"><a href="/detail/101.html">测试影片二</a></h4><p class="text text-ellipsis text-extend"><span>更新到12集</span></p></div></div></li>
<li><div class="stui-vodlist__box"><a class="stui-vodlist__thumb lazyload" href="/detail/102.html" title="测试影片三" data-original="https://img.a.com/3.jpg"><span class="play hidden-xs"></span></a><div class="stui-vodlist__detail"><h4 class="title text-ellipsis"><a href="/detail/102.html">测试影片三</a></h4><p class="text text-ellipsis text-extend"><span>HD</span></p></div></div></li>
<li><div class="stui-vodlist__box"><a class="stui-vodlist__thumb lazyload" href="/detail/103.html" title="测试影片四" data-original="https://img.a.com/4.jpg"><span class="play hidden-xs"></span></a><div class="stui-vodlist__detail"><h4 class="title text-ellipsis"><a href="/detail/103.html">测试影片四</a></h4><p class="text text-ellipsis text-extend"><span>已完结</span></p></div></div></li>
</ul>
</div>
</body></html>`

// stuiDetail 一份精简 stui 详情页样本：两个并列播放线路容器（各 3 集，<li> 连排段内无 #）。
// 另有 meta description（简介陷阱源）与正文简介区（col-pd）。
const stuiDetail = `<!DOCTYPE html><html><head>
<meta name="description" content="测试影片一剧情:一个关于逐字段验证的故事。">
<title>测试影片一</title>
</head><body>
<div class="stui-content__detail"><p class="col-pd">简介：本站样本仅用于专家写源离线单测，正文里没有 meta 里的词。</p></div>
<div class="stui-pannel clearfix"><div class="stui-pannel__head"><h3 class="title">播放源电视剧</h3></div>
<ul class="stui-content__playlist clearfix">
<li><a href="/play/100-1-1.html">第1集</a></li><li><a href="/play/100-1-2.html">第2集</a></li><li><a href="/play/100-1-3.html">第3集</a></li></ul></div>
<div class="stui-pannel clearfix"><div class="stui-pannel__head"><h3 class="title">播放源快车</h3></div>
<ul class="stui-content__playlist clearfix">
<li><a href="/play/100-2-1.html">第1集</a></li><li><a href="/play/100-2-2.html">第2集</a></li><li><a href="/play/100-2-3.html">第3集</a></li></ul></div>
<div class="stui-pannel clearfix"><div class="stui-pannel__head"><h3 class="title">猜你喜欢</h3></div>
<ul class="stui-vodlist clearfix"><li><a href="/detail/200.html">别片</a></li></ul></div>
</body></html>`

func goodSamples() []ai.Sample {
	return []ai.Sample{
		{Label: "首页 https://demo.com", Content: stuiCatalog},
		{Label: "分类页 https://demo.com/type/1.html", Content: stuiCatalog},
		{Label: "详情页 https://demo.com/detail/100.html", Content: stuiDetail},
	}
}
