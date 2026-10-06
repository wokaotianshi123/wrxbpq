# wrxbpq

输入一个视频网址 → AI 写出 XBPQ/TVBox 采集规则 JSON → 用真实播放校验规则是否可用。

规则引擎行为对齐 [guoapp3](https://github.com/) 项目 `native/core/provider_xbpq.go`，
写出来的 JSON 可直接作为该项目的自定义源录入。

**核心原则：目录能出不算通过，必须真的播出来才算。** 验证的最后一步会真正请求解析出的直链，
只有返回 `#EXTM3U`（或 MP4 二进制）才判定通过——HTTP 200 但内容是 HTML 错误页一样算失败。

**例外：CDN 地域封锁降级为警告。** 部署在 Vercel（境外机房）时，国内采集站的 CDN 常对服务器 IP 返回
`403 Forbidden / The region has been denied`。这不代表规则有问题——直链本身能被规则正确解出，
浏览器或国内设备实测能播即为证。此类回包会标记为 `△`（警告）而不是 `✗`（失败），
也不会进入"按验证结果修复"的问题列表，避免误导 AI 反复改本来正确的规则。
本地运行（国内网络）则照常拿到 `#EXTM3U` 判定通过。

---

## 它能做什么

| 环节 | 说明 |
|---|---|
| 站点探测 | 链式抓取首页 → 分类页 → 详情页 → 播放页，作为样本交给 AI |
| 规则生成 | 调用任意 OpenAI 兼容接口产出 XBPQ 规则 JSON |
| 分步验证 | 解析 / 目录 / 翻页 / 搜索 / 详情 / 播放，6 步逐项验证 |
| 失败修复 | 把验证错误信息回传给 AI，自动产出修正版规则 |
| 多线路 | 支持 `线路数组` 字段，验证会报告可切换的线路数 |

---

## 快速开始（本地）

需要 Go 1.21+。

```bash
# 编译
go build -o wrxbpq ./cmd/wrxbpq

# 1. 抓站点样本
./wrxbpq probe --site https://www.example.com

# 2. 让 AI 写规则
export AI_BASE_URL=https://api.deepseek.com/v1
export AI_API_KEY=sk-xxx
export AI_MODEL=deepseek-chat
./wrxbpq generate --site https://www.example.com --out rule.json

# 3. 验证（含真实播放校验）
./wrxbpq verify --rule rule.json
```

启动带界面的 Web 服务（两种方式等价）：

```bash
# 方式 A：CLI（从磁盘 public/ 读前端）
./wrxbpq serve --port 8080

# 方式 B：Vercel 入口（前端已 go:embed 进二进制，与云端行为一致）
go run .
# 打开 http://127.0.0.1:8080 （B 方式监听 $PORT，默认 8080）
```

---

## 部署到 Vercel

后端是一个标准 Go HTTP 服务（根目录 `main.go`），前端 `public/` 通过 `go:embed` 打进二进制。
走 Vercel 官方的 **Go Framework Preset**（Serverless 形态），`vercel.json` 已设 `"framework": "go"`。

服务监听 `PORT` 环境变量（Vercel 注入），无需任何构建脚本。

### 方式一：网页导入（最简单）

1. 把本项目推到你的 GitHub
2. 打开 https://vercel.com/new ，导入该仓库
3. Framework Preset 会被 `vercel.json` 自动设为 **Go**，其余保持默认
4. 点 Deploy

### 方式二：命令行

```bash
npx vercel@latest deploy --prod
```

### 环境变量（可选）

在 Vercel 项目设置 → Environment Variables 里配，配了之后网页上就不用每次填 Key：

| 变量 | 说明 |
|---|---|
| `AI_BASE_URL` | OpenAI 兼容接口地址 |
| `AI_API_KEY` | API Key |
| `AI_MODEL` | 模型名 |

> **超时**：Vercel Fluid Compute 下各套餐函数默认/最大时长为 **300 秒（5 分钟）**，
> 本项目的验证与生成单请求都在其内。前端把验证拆成 4 批串行请求（每批 1-2 步），
> 既避免单请求过长，也让结果逐步呈现。生成规则耗时取决于所接模型，若模型本身很慢，
> 可本地 `wrxbpq serve` 生成后仅把规则粘贴到云端验证。

> **网络注意**：Vercel 机房在境外，目标站点若做了地区限制，云端验证会失败。
> 这种情况用本地 `wrxbpq serve` 跑即可。

---

## AI 接口配置

任何实现 `/v1/chat/completions` 的服务都能接。BaseURL 填法：

| 服务商 | BaseURL | 模型示例 |
|---|---|---|
| OpenAI | `https://api.openai.com/v1` | `gpt-4o-mini` |
| DeepSeek | `https://api.deepseek.com/v1` | `deepseek-chat` |
| 月之暗面 | `https://api.moonshot.cn/v1` | `moonshot-v1-32k` |
| 通义千问 | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen-plus` |
| Groq | `https://api.groq.com/openai/v1` | `llama-3.3-70b-versatile` |
| OpenRouter | `https://openrouter.ai/api/v1` | `anthropic/claude-3.5-sonnet` |
| One-API / New-API | `https://你的域名/v1` | 自定义 |
| 本地 Ollama | `http://localhost:11434/v1` | `qwen2.5:14b` |

BaseURL 只写到 `/v1` 即可，程序会自动补 `/chat/completions`；写全了也能识别。

**模型建议**：规则编写需要较强的长上下文与代码能力，推荐 32k 上下文以上的模型。
样本默认 24k 字符 × 4 个页面，加上系统指令大约 12 万字符，小模型容易截断或瞎编。

---

## Web API

| 方法 | 路径 | 请求体 | 说明 |
|---|---|---|---|
| GET | `/api/health` | — | 健康检查 |
| POST | `/api/probe` | `{site, limit?}` | 抓站点样本 |
| POST | `/api/generate` | `{site, baseUrl, model, apiKey?, rule?, problems?, notes?, samples?}` | 生成；带 `rule` 则进入修复模式；`notes` 是给 AI 的补充说明 |
| POST | `/api/verify` | `{rule, site?, keyword?, detailId?, episode?, steps?}` | 分步验证 |

`/api/verify` 返回：

```json
{
  "ok": true,
  "results": [{
    "step": "play",
    "ok": true,
    "message": "第 1 集可播（HTTP 200，返回 #EXTM3U）",
    "details": {"直链": "https://.../index.m3u8", "备选线路": 5},
    "seed": {"detailId": "55388"}
  }]
}
```

`steps` 可选值：`parse` / `catalog` / `paging` / `search` / `detail` / `play`。

---

## 规则字段速查

完整字段字典见 [docs/字段参考.md](docs/字段参考.md)。常用字段：

```
主页url      站点首页（必填）
请求头       UA
分类         "电影$1#电视剧$2"
分类url      .../list/{cateId}-{catePg}.html
搜索url      .../search?wd={wd}
数组         列表条目外层截取
二次截取     先截出主体区域，避免导航栏干扰
标题/链接/列表图片/副标题
影片名称/简介/封面/类型/导演/主演
播放数组     分集容器截取
线路数组     多线路时必填
跳转播放链接  播放页直链截取，如 url: '&&'
```

截取语法：`start&&end` 取中间；`||` 多组备选；`[包含:x]` `[不包含:x]` `[替换:a>>b]` 修饰符；
`p:标签.类#id[href]` 选择器。

---

## 项目结构

```
main.go              Vercel Go 服务入口（go:embed 内嵌前端，监听 $PORT）
internal/
  xbpq/              XBPQ 规则引擎（解析 / 截取 / 选择器 / 抓取 / 采集）
  ai/                OpenAI 兼容客户端 + 生成与修复的 Prompt
  verify/            站点探测 + 分步验证
  httpapi/           共享 HTTP handler + 路由注册（RegisterAPI/NewMux）
cmd/wrxbpq/          本地 CLI（probe / generate / verify / serve）
public/              Web 前端（原生 HTML/CSS/JS，无构建步骤，被 main.go 内嵌）
examples/            两份实测通过的规则样例
docs/字段参考.md      规则字段字典
```

---

## 已知限制

- 站点有 JS 渲染、加密参数或强反爬时，纯 HTTP 抓取拿不到内容，规则无从写起。
- 需要登录才能看的站点不支持。
- 云解析型播放页（地址在 iframe 里二次加密）无法直接截到直链，需人工补规则。
- 封面常见懒加载占位（`data-original="/"`），站点行为导致，规则层面只能跳过占位条目。

---

## 测试

```bash
go test ./...
```

离线单测覆盖规则解析、截取语法、选择器、分类串、多线路分集，不联网。
