package xbpq

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// DefaultUserAgent 未指定 UA 时的默认值。
const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"

// MobileUserAgent 是手机 UA 兜底值。大量国内采集站对桌面 UA 返回风控页/空壳，
// 换手机 UA 常常能拿到真实 HTML（笔记与实测站（如 hanjuds 类 mobile 站）均如此），
// 所以重试耗尽后会用它再试一次。
const MobileUserAgent = "Mozilla/5.0 (Linux; Android 12; Pixel 6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Mobile Safari/537.36"

// retryableStatus 判断该状态码是否值得重试：408 请求超时、429 限流、5xx 服务端错误。
func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

// maxBodyBytes 单页抓取上限，避免 OOM。
const maxBodyBytes = 8 << 20

// Fetcher 负责按规则抓取页面。
type Fetcher struct {
	Client  *http.Client
	Agent   string
	Timeout int // 秒，<=0 用默认 20
}

// NewFetcher 构造一个抓取器。
func NewFetcher(agent string) *Fetcher {
	if strings.TrimSpace(agent) == "" {
		agent = DefaultUserAgent
	}
	return &Fetcher{
		Client: &http.Client{Timeout: 0}, // 超时由 context 控制
		Agent:  agent,
	}
}

func (f *Fetcher) timeout() int {
	if f.Timeout > 0 {
		return f.Timeout
	}
	return 20
}

func (f *Fetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return http.DefaultClient
}

// Get 抓取 URL 并返回解码后的文本。spec 支持 `url;post;body` 形态。
// 带重试：408/429/5xx 与网络瞬时错误会退避重试，仍失败再换手机 UA 兜底一次。
func (f *Fetcher) Get(ctx context.Context, spec, referer string) (string, error) {
	address, method, body := splitSpec(spec)
	if address == "" {
		return "", fmt.Errorf("地址为空")
	}
	return f.getWithRetry(ctx, address, method, body, referer, f.Agent)
}

// getWithRetry 按 UA 维度做一轮取数：先用 f.Agent，遇到可重试错误退避重试，
// 全部失败后若当前 UA 不是手机 UA，则换手机 UA 再来一轮（应对站点的桌面风控）。
// 单次尝试的超时与整体 ctx 预算共同生效，重试不会把整体耗时拖到平台掐断。
func (f *Fetcher) getWithRetry(ctx context.Context, address, method, body, referer, agent string) (string, error) {
	const attempts = 3
	backoff := []time.Duration{400 * time.Millisecond, 1200 * time.Millisecond}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return "", lastErr
			}
			return "", err
		}
		payload, status, err := f.doOnce(ctx, address, method, body, referer, agent)
		if err == nil && retryableStatus(status) {
			err = fmt.Errorf("HTTP %d", status)
		}
		if err == nil {
			return payload, nil
		}
		lastErr = err
		if status > 0 && !retryableStatus(status) {
			// 4xx（如 404/403）重试没有意义，直接失败。
			break
		}
		if attempt < len(backoff) {
			select {
			case <-ctx.Done():
				return "", lastErr
			case <-time.After(backoff[attempt]):
			}
		}
	}
	// 最后一招：换手机 UA 再试一轮。
	if agent != MobileUserAgent && !strings.Contains(agent, "Mobile") {
		return f.getWithRetry(ctx, address, method, body, referer, MobileUserAgent)
	}
	return "", lastErr
}

// doOnce 发起一次真实请求。agent 为空时用抓取器默认 UA。
// 返回 (解码后的正文, HTTP 状态码, 错误)：状态码一并回传，由 getWithRetry 判断是否值得重试——
// 单次尝试的独立超时按 timeout/2 收紧，保证「一次尝试 + 退避 + 换 UA 再一轮」
// 总耗时仍能落在调用方的 ctx 预算内（Vercel 函数 300 秒硬掐）。
func (f *Fetcher) doOnce(ctx context.Context, address, method, body, referer, agent string) (string, int, error) {
	if strings.TrimSpace(agent) == "" {
		agent = f.Agent
	}
	perTry := f.timeout() / 2
	if perTry < 6 {
		perTry = 6
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(perTry)*time.Second)
	defer cancel()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, pickMethod(method), address, reader)
	if err != nil {
		return "", 0, err
	}
	request.Header.Set("User-Agent", agent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	// 手机 UA 场景补上移动端常见头，部分站点据此下发移动版 HTML。
	if strings.Contains(agent, "Mobile") {
		request.Header.Set("Sec-Fetch-Dest", "document")
		request.Header.Set("Sec-Fetch-Mode", "navigate")
		request.Header.Set("Sec-Fetch-Site", "none")
		request.Header.Set("Upgrade-Insecure-Requests", "1")
	}
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	if method != "" {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := f.client().Do(request)
	if err != nil {
		return "", 0, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		// 只吞掉少量正文便于排错，其余丢弃，避免风控大页占内存。
		_, _ = io.CopyN(io.Discard, response.Body, 1024)
		return "", response.StatusCode, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return "", response.StatusCode, err
	}
	return DecodeBody(string(payload)), response.StatusCode, nil
}

func pickMethod(method string) string {
	if strings.EqualFold(method, http.MethodPost) {
		return http.MethodPost
	}
	return http.MethodGet
}

// splitSpec 解析 `url;post;body` / `url;get` 形态。
func splitSpec(raw string) (address, method, body string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", ""
	}
	parts := strings.Split(raw, ";")
	address = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		switch strings.ToLower(strings.TrimSpace(parts[1])) {
		case "post":
			method = http.MethodPost
			if len(parts) > 2 {
				body = strings.Join(parts[2:], ";")
			}
		}
	}
	return address, method, body
}

// ComposeSpec 把 (地址, 方法, body) 拼回 spec 串。
func ComposeSpec(address, method, body string) string {
	if method == "" {
		return address
	}
	return address + ";" + method + ";" + body
}

var charsetPattern = regexp.MustCompile(`(?i)charset\s*=\s*["' ]?(gbk|gb2312|gb18030)`)

// DecodeBody GBK/GB2312 页面转 UTF-8。
func DecodeBody(body string) string {
	if body == "" {
		return body
	}
	head := body
	if len(head) > 4096 {
		head = head[:4096]
	}
	if !charsetPattern.MatchString(head) {
		return body
	}
	decoded, _, err := transform.String(simplifiedchinese.GBK.NewDecoder(), body)
	if err != nil || strings.TrimSpace(decoded) == "" {
		return body
	}
	return decoded
}
