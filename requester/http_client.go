package requester

import (
	"crypto/tls"
	"net/http"
	"net/http/cookiejar"
	"time"
)

// HTTPClient http client
type HTTPClient struct {
	http.Client
	transport *http.Transport
	https     bool
	UserAgent string
}

// NewHTTPClient 返回 HTTPClient 的指针,
// 预设了一些配置
func NewHTTPClient() *HTTPClient {
	h := &HTTPClient{
		Client: http.Client{
			Timeout: 50 * time.Second,
		},
		UserAgent: UserAgent,
	}
	h.Client.Jar, _ = cookiejar.New(nil)
	h.lazyInit()
	return h
}

func (h *HTTPClient) lazyInit() {
	if h.transport == nil {
		h.transport = &http.Transport{
			Proxy:       proxyFunc,
			DialContext: dialContext,
			// DialTLS:     h.dialTLSFunc(),
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			TLSHandshakeTimeout:   20 * time.Second,
			DisableKeepAlives:     false,
			DisableCompression:    false, // gzip
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   60,
			ForceAttemptHTTP2:     true,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 25 * time.Second,
			ExpectContinueTimeout: 10 * time.Second,
		}
	}
	if h.Client.Transport == nil {
		h.Client.Transport = h.transport
	}
}

func (h *HTTPClient) configuredTransport() *http.Transport {
	h.lazyInit()
	if transport, ok := h.Client.Transport.(*http.Transport); ok && transport != nil {
		return transport
	}
	return h.transport
}

// SetUserAgent 设置 UserAgent 浏览器标识
func (h *HTTPClient) SetUserAgent(ua string) {
	h.UserAgent = ua
}

// SetProxy 设置代理
func (h *HTTPClient) SetProxy(proxyAddr string) {
	transport := h.configuredTransport()
	u, err := checkProxyAddr(proxyAddr)
	if err != nil {
		transport.Proxy = http.ProxyFromEnvironment
		return
	}

	transport.Proxy = http.ProxyURL(u)
}

// SetCookiejar 设置 cookie
func (h *HTTPClient) SetCookiejar(jar http.CookieJar) {
	h.Client.Jar = jar
}

// ResetCookiejar 清空 cookie
func (h *HTTPClient) ResetCookiejar() {
	h.Jar, _ = cookiejar.New(nil)
}

// SetHTTPSecure 是否启用 https 安全检查, 强制不检查
func (h *HTTPClient) SetHTTPSecure(b bool) {
	h.https = b
	transport := h.configuredTransport()
	// 	if b {
	// 		h.transport.TLSClientConfig = nil
	// 	} else {
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true,
	}
	// 	}
}

// SetKeepAlive 设置 Keep-Alive
func (h *HTTPClient) SetKeepAlive(b bool) {
	h.configuredTransport().DisableKeepAlives = !b
}

// SetGzip 是否启用Gzip
func (h *HTTPClient) SetGzip(b bool) {
	h.configuredTransport().DisableCompression = !b
}

// SetResponseHeaderTimeout 设置目标服务器响应超时时间
func (h *HTTPClient) SetResponseHeaderTimeout(t time.Duration) {
	h.configuredTransport().ResponseHeaderTimeout = t
}

// SetTLSHandshakeTimeout 设置tls握手超时时间
func (h *HTTPClient) SetTLSHandshakeTimeout(t time.Duration) {
	h.configuredTransport().TLSHandshakeTimeout = t
}

// SetTimeout 设置 http 请求超时时间, 默认30s
func (h *HTTPClient) SetTimeout(t time.Duration) {
	h.Client.Timeout = t
}
