// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/netproxy"
)

// probeTimeout bounds each 「测试连接」 request.
const probeTimeout = 10 * time.Second

// NetworkService is Settings → 网络代理: which proxy is in effect (the
// app follows the system proxy; nothing to configure) and a connection
// test through it.
type NetworkService struct {
	Proxy *netproxy.Resolver
	// Probes are the URLs Test fetches (Google's generate_204 and the
	// SiteMoment API's health check in production).
	Probes []string
}

// Probe is one URL fetched by Test.
type Probe struct {
	URL string `json:"url"`
	OK  bool   `json:"ok"`
	// Status is the HTTP status (0 when there was no response).
	Status int   `json:"status"`
	Ms     int64 `json:"ms"`
	// Via is how the request went: "HTTP 127.0.0.1:7890", "SOCKS5 …" or 直连.
	Via   string `json:"via"`
	Error string `json:"error"`
}

// Status is the proxy in effect.
func (n *NetworkService) Status() netproxy.Status { return n.Proxy.Status() }

// Test fetches every probe URL through the proxy in effect, concurrently.
func (n *NetworkService) Test(ctx context.Context) []Probe {
	client := &http.Client{
		Transport:     n.Proxy.Transport(),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	out := make([]Probe, len(n.Probes))
	var wg sync.WaitGroup
	for i, u := range n.Probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = n.probe(ctx, client, u)
		}()
	}
	wg.Wait()
	return out
}

func (n *NetworkService) probe(ctx context.Context, client *http.Client, raw string) Probe {
	p := Probe{URL: raw, Via: "直连"}
	u, err := url.Parse(raw)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	if pu, err := n.Proxy.ProxyFor(u); err == nil && pu != nil {
		p.Via = strings.ToUpper(pu.Scheme) + " " + pu.Host
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	start := time.Now()
	res, err := client.Do(req)
	p.Ms = time.Since(start).Milliseconds()
	if err != nil {
		p.Error = probeError(ctx, err)
		return p
	}
	res.Body.Close()
	p.Status = res.StatusCode
	p.OK = res.StatusCode >= 200 && res.StatusCode < 300
	if !p.OK {
		p.Error = "HTTP " + res.Status
	}
	return p
}

// probeError says in a few words why a probe failed. Errors name only
// hosts and addresses (url.Error's URL is dropped), never credentials.
func probeError(ctx context.Context, err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	msg := err.Error()
	var dnsErr *net.DNSError
	switch {
	case strings.Contains(msg, "proxyconnect") || strings.Contains(msg, "socks connect"):
		return "连不上代理：" + msg
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		return "超过 10 秒没有响应"
	case errors.As(err, &dnsErr):
		return "域名解析失败：" + msg
	case strings.Contains(msg, "tls: ") || strings.Contains(msg, "x509: "):
		return "TLS 握手失败：" + msg
	}
	return msg
}
