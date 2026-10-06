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
func (f *Fetcher) Get(ctx context.Context, spec, referer string) (string, error) {
	address, method, body := splitSpec(spec)
	if address == "" {
		return "", fmt.Errorf("地址为空")
	}
	return f.do(ctx, address, method, body, referer)
}

func (f *Fetcher) do(ctx context.Context, address, method, body, referer string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(f.timeout())*time.Second)
	defer cancel()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, pickMethod(method), address, reader)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", f.Agent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	if method != "" {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := f.client().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		_, _ = io.CopyN(io.Discard, response.Body, 1024)
		return "", fmt.Errorf("HTTP %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return "", err
	}
	return DecodeBody(string(payload)), nil
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
