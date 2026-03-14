package aggregator

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"vless-aggregator/internal/config"
)

type Result struct {
	Lines        []string
	Userinfo     Userinfo
	SourceErrors map[string]error
}

type Userinfo struct {
	Upload   int64
	Download int64
	Total    int64
	Expire   int64
}

type Aggregator struct {
	cfgMgr *config.Manager
	logger *slog.Logger
	mu     sync.Mutex
	client *http.Client
	lastTO int
}

func New(cfgMgr *config.Manager, logger *slog.Logger) *Aggregator {
	to := cfgMgr.Get().Upstream.TimeoutSec
	return &Aggregator{
		cfgMgr: cfgMgr,
		logger: logger,
		client: buildClient(to),
		lastTO: to,
	}
}

// Fetch sends subPath to every configured upstream host concurrently
// and returns a merged result.
func (a *Aggregator) Fetch(ctx context.Context, subPath string) (*Result, error) {
	cfg := a.cfgMgr.Get()
	client := a.getClient(cfg.Upstream.TimeoutSec)

	type partial struct {
		host     string
		lines    []string
		userinfo Userinfo
		err      error
	}

	ch := make(chan partial, len(cfg.Upstream.Hosts))
	var wg sync.WaitGroup

	for _, host := range cfg.Upstream.Hosts {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			url := strings.TrimRight(h, "/") + subPath
			lines, ui, err := fetchOne(ctx, client, url)
			ch <- partial{host: h, lines: lines, userinfo: ui, err: err}
		}(host)
	}

	go func() { wg.Wait(); close(ch) }()

	result := &Result{SourceErrors: make(map[string]error)}
	for p := range ch {
		if p.err != nil {
			a.logger.Warn("upstream error", "host", p.host, "path", subPath, "error", p.err)
			result.SourceErrors[p.host] = p.err
			continue
		}
		result.Lines = append(result.Lines, p.lines...)
		result.Userinfo.Upload += p.userinfo.Upload
		result.Userinfo.Download += p.userinfo.Download
		if p.userinfo.Total > 0 {
			result.Userinfo.Total += p.userinfo.Total
		}
		if p.userinfo.Expire > 0 {
			if result.Userinfo.Expire == 0 || p.userinfo.Expire < result.Userinfo.Expire {
				result.Userinfo.Expire = p.userinfo.Expire
			}
		}
	}

	if len(result.Lines) == 0 && len(result.SourceErrors) == len(cfg.Upstream.Hosts) {
		return nil, fmt.Errorf("all %d upstream hosts failed", len(cfg.Upstream.Hosts))
	}

	return result, nil
}

func fetchOne(ctx context.Context, client *http.Client, url string) ([]string, Userinfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, Userinfo{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/xhtml+xml")
	req.Header.Set("User-Agent", "vless-aggregator/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, Userinfo{}, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, Userinfo{}, fmt.Errorf("upstream status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, Userinfo{}, fmt.Errorf("read body: %w", err)
	}

	ui := parseUserinfo(resp.Header.Get("Subscription-Userinfo"))
	lines := decodeLines(body)
	return lines, ui, nil
}

func decodeLines(body []byte) []string {
	s := strings.TrimSpace(string(body))
	if dec, err := base64.StdEncoding.DecodeString(s); err == nil {
		s = strings.TrimSpace(string(dec))
	} else if dec, err := base64.URLEncoding.DecodeString(s); err == nil {
		s = strings.TrimSpace(string(dec))
	}
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func parseUserinfo(h string) Userinfo {
	var ui Userinfo
	for _, part := range strings.Split(h, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		var v int64
		fmt.Sscanf(strings.TrimSpace(kv[1]), "%d", &v)
		switch strings.TrimSpace(kv[0]) {
		case "upload":
			ui.Upload = v
		case "download":
			ui.Download = v
		case "total":
			ui.Total = v
		case "expire":
			ui.Expire = v
		}
	}
	return ui
}

func (a *Aggregator) getClient(timeoutSec int) *http.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if timeoutSec != a.lastTO {
		a.client = buildClient(timeoutSec)
		a.lastTO = timeoutSec
	}
	return a.client
}

func buildClient(timeoutSec int) *http.Client {
	if timeoutSec <= 0 {
		timeoutSec = 15
	}
	return &http.Client{
		Timeout: time.Duration(timeoutSec) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // upstream panels use self-signed certs
			MaxIdleConns:        50,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}
