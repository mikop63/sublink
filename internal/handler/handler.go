package handler

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"vless-aggregator/internal/aggregator"
	"vless-aggregator/internal/config"
)

type SubHandler struct {
	cfgMgr   *config.Manager
	agg      *aggregator.Aggregator
	logger   *slog.Logger
	pageTmpl *template.Template
}

func NewSubHandler(cfgMgr *config.Manager, agg *aggregator.Aggregator, logger *slog.Logger) *SubHandler {
	funcs := template.FuncMap{
		"inc": func(i int) int { return i + 1 },
	}
	return &SubHandler{
		cfgMgr:   cfgMgr,
		agg:      agg,
		logger:   logger,
		pageTmpl: template.Must(template.New("sub").Funcs(funcs).Parse(subscriptionPageHTML)),
	}
}

func (h *SubHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	subPath := r.URL.RequestURI()

	result, err := h.agg.Fetch(r.Context(), subPath)
	if err != nil {
		h.logger.Error("aggregation failed", "path", subPath, "error", err)
		http.Error(w, "all upstream hosts failed", http.StatusBadGateway)
		return
	}

	if len(result.SourceErrors) > 0 {
		h.logger.Warn("partial upstream failure",
			"path", subPath,
			"failed", len(result.SourceErrors),
			"total", len(h.cfgMgr.Get().Upstream.Hosts),
		)
	}

	// Browser with non-empty result -> HTML page with QR code
	if wantHTML(r) && len(result.Lines) > 0 {
		h.serveHTML(w, r, result)
		return
	}

	h.serveSubscription(w, r, result)
}

// wantHTML returns true when the client explicitly accepts text/html (browser).
func wantHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// serveSubscription returns base64-encoded subscription for VPN clients.
func (h *SubHandler) serveSubscription(w http.ResponseWriter, r *http.Request, result *aggregator.Result) {
	cfg := h.cfgMgr.Get()
	body := strings.Join(result.Lines, "\n") + "\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(body))

	// Profile-Title: use configured title or fall back to host
	title := cfg.Server.ProfileTitle
	if title == "" {
		title = r.Host
	}
	w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(title)))
	w.Header().Set("Profile-Update-Interval", fmt.Sprintf("%d", cfg.Upstream.UpdateInterval))
	w.Header().Set("Subscription-Userinfo", formatUserinfo(result.Userinfo))
	w.Header().Set("Routing-Enable", "true")

	// Announce header (optional description shown in some VPN clients)
	if cfg.Server.Announce != "" {
		w.Header().Set("Announce", "base64:"+base64.StdEncoding.EncodeToString([]byte(cfg.Server.Announce)))
	}

	// Profile-Web-Page-Url: full public URL of this subscription page
	if cfg.Server.PublicURL != "" {
		w.Header().Set("Profile-Web-Page-Url", cfg.Server.PublicURL+r.URL.RequestURI())
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, encoded)
}

// serveHTML renders the subscription info page with QR code.
func (h *SubHandler) serveHTML(w http.ResponseWriter, r *http.Request, result *aggregator.Result) {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	subURL := fmt.Sprintf("%s://%s%s", scheme, r.Host, r.URL.RequestURI())

	type pageData struct {
		SubURL   string
		Lines    []string
		Upload   string
		Download string
		Total    string
		Expire   string
	}

	data := pageData{
		SubURL:   subURL,
		Lines:    result.Lines,
		Upload:   formatBytes(result.Userinfo.Upload),
		Download: formatBytes(result.Userinfo.Download),
		Total:    formatBytes(result.Userinfo.Total),
	}
	if result.Userinfo.Total == 0 {
		data.Total = "\u221e"
	}
	if result.Userinfo.Expire > 0 {
		data.Expire = time.Unix(result.Userinfo.Expire, 0).Format("2006-01-02")
	} else {
		data.Expire = "No expiry"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	h.pageTmpl.Execute(w, data)
}

func formatUserinfo(ui aggregator.Userinfo) string {
	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d",
		ui.Upload, ui.Download, ui.Total, ui.Expire)
}

func formatBytes(b int64) string {
	if b == 0 {
		return "0 B"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// LoggingMiddleware logs every request.
func LoggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// ── Embedded HTML template ────────────────────────────────────────────────────

const subscriptionPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<title>Subscription Info</title>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
body{min-height:100vh;background:#0f172a;font-family:system-ui,-apple-system,sans-serif;
     color:#e2e8f0;padding:1.5rem 1rem}
.page{max-width:680px;margin:0 auto}

.card{background:#1e293b;border-radius:14px;overflow:hidden;
      box-shadow:0 8px 32px rgba(0,0,0,.4);margin-bottom:1.25rem}
.card-header{display:flex;align-items:center;justify-content:space-between;
             padding:1.1rem 1.5rem;border-bottom:1px solid #334155}
.card-title{font-size:1rem;font-weight:700;color:#f1f5f9}
.badge{background:#4c1d95;color:#c4b5fd;font-size:.75rem;font-weight:600;
       padding:.25rem .65rem;border-radius:6px}
.card-body{padding:1.5rem}

.qr-wrap{display:flex;flex-direction:column;align-items:center;gap:.85rem;margin-bottom:1.75rem}
.qr-label{background:#4c1d95;color:#c4b5fd;font-size:.73rem;font-weight:700;
          padding:.28rem .75rem;border-radius:6px;letter-spacing:.05em;text-transform:uppercase}
.qr-box{background:#fff;border-radius:12px;padding:12px;cursor:pointer;
        box-shadow:0 4px 20px rgba(0,0,0,.35);transition:transform .15s,box-shadow .15s}
.qr-box:hover{transform:scale(1.04);box-shadow:0 6px 28px rgba(99,102,241,.35)}
.qr-hint{font-size:.78rem;color:#64748b;text-align:center;line-height:1.6}

.stats{width:100%;border-collapse:collapse;font-size:.875rem}
.stats tr{border-bottom:1px solid #0f172a}
.stats tr:last-child{border-bottom:none}
.stats td{padding:.65rem .4rem}
.stats td:first-child{color:#94a3b8;font-weight:600;width:38%;
                      text-transform:uppercase;font-size:.73rem;letter-spacing:.06em}
.stats td:last-child{color:#f1f5f9;font-weight:500}
.tag{display:inline-block;padding:.22rem .6rem;border-radius:5px;font-size:.75rem;font-weight:700}
.tag-active{background:#14532d;color:#86efac}
.tag-unlimited{background:#4c1d95;color:#c4b5fd}

.link-item{margin-bottom:1rem}
.link-label{background:#4c1d95;color:#c4b5fd;font-size:.72rem;font-weight:700;
            padding:.25rem .65rem;border-radius:6px 6px 0 0;display:inline-block}
.link-box{background:#0f172a;border:1px solid #334155;border-radius:0 8px 8px 8px;
          padding:1rem 3rem 1rem 1rem;font-family:'SFMono-Regular',Consolas,monospace;
          font-size:.78rem;line-height:1.6;color:#a5b4fc;word-break:break-all;
          cursor:pointer;transition:background .2s,border-color .2s;position:relative}
.link-box:hover{background:#1e1b4b;border-color:#6366f1}
.copy-icon{position:absolute;top:.75rem;right:.75rem;color:#475569;font-size:.9rem;
           pointer-events:none;transition:color .2s}
.link-box:hover .copy-icon{color:#818cf8}

.toast{position:fixed;bottom:1.75rem;left:50%;transform:translateX(-50%);
       background:#1e3a8a;color:#bfdbfe;border:1px solid #3b82f6;
       padding:.6rem 1.5rem;border-radius:8px;font-size:.875rem;font-weight:600;
       opacity:0;transition:opacity .25s;pointer-events:none;white-space:nowrap;
       box-shadow:0 4px 16px rgba(0,0,0,.4)}
.toast.show{opacity:1}

@media(max-width:480px){.card-body{padding:1rem}.link-box{font-size:.72rem}}
</style>
</head>
<body>
<div class="page">

  <div class="card">
    <div class="card-header">
      <span class="card-title">Subscription Info</span>
      <span class="badge">vless-aggregator</span>
    </div>
    <div class="card-body">
      <div class="qr-wrap">
        <span class="qr-label">Subscription</span>
        <div class="qr-box" title="Click to copy subscription URL" onclick="copyText('{{.SubURL}}')">
          <canvas id="qr-canvas"></canvas>
        </div>
        <span class="qr-hint">
          Scan with your VPN app to add subscription<br>
          or click QR to copy the URL
        </span>
      </div>
      <table class="stats">
        <tr>
          <td>Status</td>
          <td>
            {{if eq .Total "\u221e"}}
              <span class="tag tag-unlimited">Unlimited</span>
            {{else}}
              <span class="tag tag-active">Active</span>
            {{end}}
          </td>
        </tr>
        <tr><td>Servers</td><td>{{len .Lines}} configs</td></tr>
        <tr><td>Uploaded</td><td>{{.Upload}}</td></tr>
        <tr><td>Downloaded</td><td>{{.Download}}</td></tr>
        <tr><td>Total quota</td><td>{{.Total}}</td></tr>
        <tr><td>Expiry</td><td>{{.Expire}}</td></tr>
      </table>
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <span class="card-title">Configurations</span>
      <span class="badge">{{len .Lines}} servers</span>
    </div>
    <div class="card-body">
      {{range $i, $line := .Lines}}
      <div class="link-item">
        <div class="link-label">Config #{{inc $i}}</div>
        <div class="link-box" onclick="copyText(this.dataset.v)" data-v="{{$line}}">
          <span class="copy-icon">⎘</span>{{$line}}
        </div>
      </div>
      {{end}}
    </div>
  </div>

</div>

<div class="toast" id="toast">Copied ✓</div>

<script src="https://cdnjs.cloudflare.com/ajax/libs/qrious/4.0.2/qrious.min.js"></script>
<script>
new QRious({
  element: document.getElementById('qr-canvas'),
  value: '{{.SubURL}}',
  size: 200,
  background: '#ffffff',
  foreground: '#0f172a',
  padding: 4,
  level: 'M'
});

function copyText(text) {
  navigator.clipboard.writeText(text).then(function() {
    var t = document.getElementById('toast');
    t.classList.add('show');
    setTimeout(function() { t.classList.remove('show'); }, 2000);
  });
}
</script>
</body>
</html>`