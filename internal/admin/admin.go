package admin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"vless-aggregator/internal/config"
)

type Handler struct {
	cfgMgr *config.Manager
	logger *slog.Logger
}

func NewHandler(cfgMgr *config.Manager, logger *slog.Logger) *Handler {
	return &Handler{cfgMgr: cfgMgr, logger: logger}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/admin/login", h.handleLogin)
	mux.Handle("/admin/", h.authMiddleware(http.HandlerFunc(h.route)))
}

func (h *Handler) route(w http.ResponseWriter, r *http.Request) {
	switch {
	case (r.URL.Path == "/admin/" || r.URL.Path == "/admin") && r.Method == http.MethodGet:
		h.handleDashboard(w, r)
	case r.URL.Path == "/admin/api/config" && r.Method == http.MethodGet:
		h.apiGetConfig(w, r)
	case r.URL.Path == "/admin/api/config" && r.Method == http.MethodPost:
		h.apiSaveConfig(w, r)
	case r.URL.Path == "/admin/logout":
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", MaxAge: -1, Path: "/admin"})
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
	default:
		http.NotFound(w, r)
	}
}

// ── Auth ──────────────────────────────────────────────────────────────────────

const sessionCookie = "admin_session"

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, loginHTML)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	cfg := h.cfgMgr.Get()

	if username != cfg.Admin.Username || password != cfg.Admin.Password {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, strings.ReplaceAll(loginHTML, `id="error" style="display:none"`, `id="error"`))
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    makeToken(username, password),
		Path:     "/admin",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (h *Handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		cfg := h.cfgMgr.Get()
		if cookie.Value != makeToken(cfg.Admin.Username, cfg.Admin.Password) {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// makeToken produces a deterministic session token from credentials.
// Changing the password instantly invalidates all sessions.
func makeToken(username, password string) string {
	raw := username + ":" + password + ":vless-agg"
	sum := 0
	for i, c := range raw {
		sum += int(c) * (i + 7)
	}
	return fmt.Sprintf("%x-%x", sum, len(raw)*31337)
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, dashboardHTML)
}

func (h *Handler) apiGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := h.cfgMgr.Get()
	resp := map[string]any{
		"server_port":     cfg.Server.Port,
		"public_url":      cfg.Server.PublicURL,
		"profile_title":   cfg.Server.ProfileTitle,
		"timeout_sec":     cfg.Upstream.TimeoutSec,
		"update_interval": cfg.Upstream.UpdateInterval,
		"hosts":           cfg.Upstream.Hosts,
		"admin_username":  cfg.Admin.Username,
		"admin_password":  cfg.Admin.Password,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type saveRequest struct {
	ServerPort     int      `json:"server_port"`
	PublicURL      string   `json:"public_url"`
	ProfileTitle   string   `json:"profile_title"`
	TimeoutSec     int      `json:"timeout_sec"`
	UpdateInterval int      `json:"update_interval"`
	Hosts          []string `json:"hosts"`
	AdminUsername  string   `json:"admin_username"`
	AdminPassword  string   `json:"admin_password"`
}

func (h *Handler) apiSaveConfig(w http.ResponseWriter, r *http.Request) {
	var req saveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Filter blank host lines submitted from the form.
	var hosts []string
	for _, host := range req.Hosts {
		if h := strings.TrimSpace(host); h != "" {
			hosts = append(hosts, h)
		}
	}

	// Start from current config and apply only non-zero/non-empty fields.
	cur := *h.cfgMgr.Get()
	if req.ServerPort > 0 {
		cur.Server.Port = req.ServerPort
	}
	cur.Server.PublicURL = strings.TrimRight(strings.TrimSpace(req.PublicURL), "/")
	cur.Server.ProfileTitle = strings.TrimSpace(req.ProfileTitle)
	if req.TimeoutSec > 0 {
		cur.Upstream.TimeoutSec = req.TimeoutSec
	}
	if req.UpdateInterval > 0 {
		cur.Upstream.UpdateInterval = req.UpdateInterval
	}
	if len(hosts) > 0 {
		cur.Upstream.Hosts = hosts
	}
	if req.AdminUsername != "" {
		cur.Admin.Username = req.AdminUsername
	}
	if req.AdminPassword != "" {
		cur.Admin.Password = req.AdminPassword
	}

	if err := h.cfgMgr.Save(&cur); err != nil {
		h.logger.Error("config save failed", "error", err)
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.logger.Info("config updated",
		"hosts", len(cur.Upstream.Hosts),
		"remote", r.RemoteAddr,
		"at", time.Now().Format(time.RFC3339),
	)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"ok":true}`)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

// ── Embedded HTML ─────────────────────────────────────────────────────────────

const loginHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Admin · Login</title>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
body{min-height:100vh;display:flex;align-items:center;justify-content:center;
     background:#0f172a;font-family:system-ui,sans-serif;color:#e2e8f0}
.card{background:#1e293b;border-radius:14px;padding:2.5rem;width:360px;
      box-shadow:0 25px 50px rgba(0,0,0,.5)}
h1{font-size:1.2rem;margin-bottom:1.75rem;text-align:center;color:#f8fafc}
label{display:block;font-size:.75rem;font-weight:700;color:#94a3b8;
      text-transform:uppercase;letter-spacing:.06em;margin-bottom:.35rem}
input{width:100%;padding:.65rem .9rem;border-radius:8px;border:1px solid #334155;
      background:#0f172a;color:#f1f5f9;font-size:.95rem;margin-bottom:1.1rem}
input:focus{outline:2px solid #6366f1;border-color:transparent}
button{width:100%;padding:.75rem;border-radius:8px;border:none;
       background:#6366f1;color:#fff;font-size:1rem;font-weight:700;
       cursor:pointer;transition:background .2s}
button:hover{background:#4f46e5}
.error{background:#450a0a;border:1px solid #dc2626;color:#fca5a5;
       border-radius:8px;padding:.7rem 1rem;font-size:.875rem;margin-bottom:1.1rem}
</style>
</head>
<body>
<div class="card">
  <h1>🔐 Admin Panel</h1>
  <div id="error" class="error" style="display:none">Invalid username or password.</div>
  <form method="POST" action="/admin/login">
    <label>Username</label>
    <input name="username" type="text" autocomplete="username" required>
    <label>Password</label>
    <input name="password" type="password" autocomplete="current-password" required>
    <button type="submit">Sign In</button>
  </form>
</div>
</body></html>`

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>VLESS Aggregator · Admin</title>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
body{min-height:100vh;background:#0f172a;font-family:system-ui,sans-serif;
     color:#e2e8f0;padding:2rem 1rem}
.topbar{display:flex;justify-content:space-between;align-items:flex-start;margin-bottom:2rem}
h1{font-size:1.4rem;color:#f8fafc}
.subtitle{color:#64748b;font-size:.85rem;margin-top:.2rem}
a.logout{color:#64748b;font-size:.85rem;text-decoration:none;padding:.4rem .8rem;
          border:1px solid #334155;border-radius:6px}
a.logout:hover{color:#f1f5f9;border-color:#64748b}
.card{background:#1e293b;border-radius:12px;padding:1.75rem;margin-bottom:1.5rem;
      box-shadow:0 4px 24px rgba(0,0,0,.3)}
.card-title{font-size:.75rem;font-weight:700;color:#a5b4fc;text-transform:uppercase;
             letter-spacing:.07em;margin-bottom:1.25rem}
.hint{font-size:.8rem;color:#64748b;margin-bottom:1rem;line-height:1.5}
code{color:#818cf8;background:#1e1b4b;padding:.1em .35em;border-radius:4px;font-size:.85em}
label{display:block;font-size:.75rem;font-weight:700;color:#94a3b8;
      text-transform:uppercase;letter-spacing:.06em;margin-bottom:.35rem}
input[type=text],input[type=password],input[type=number],input[type=url]{
  width:100%;padding:.6rem .85rem;border-radius:8px;border:1px solid #334155;
  background:#0f172a;color:#f1f5f9;font-size:.95rem;margin-bottom:1rem}
input:focus{outline:2px solid #6366f1;border-color:transparent}
.host-row{display:flex;gap:.5rem;margin-bottom:.5rem}
.host-row input{margin-bottom:0;flex:1}
.btn-rm{background:#450a0a;color:#fca5a5;border:none;border-radius:6px;
        padding:.4rem .75rem;cursor:pointer;font-size:.82rem;white-space:nowrap}
.btn-rm:hover{background:#7f1d1d}
.btn-add{background:#172554;color:#93c5fd;border:1px solid #1e3a8a;border-radius:8px;
         padding:.5rem 1rem;cursor:pointer;font-size:.85rem;margin-top:.25rem}
.btn-add:hover{background:#1e3a8a}
.grid2{display:grid;grid-template-columns:1fr 1fr;gap:1rem}
@media(max-width:540px){.grid2{grid-template-columns:1fr}}
.btn-save{background:#6366f1;color:#fff;border:none;border-radius:8px;
          padding:.75rem 2.5rem;font-size:1rem;font-weight:700;cursor:pointer;
          transition:background .2s}
.btn-save:hover{background:#4f46e5}
.btn-save:disabled{background:#334155;cursor:not-allowed}
/* ── Converter ── */
.convert-row{display:flex;gap:.75rem;align-items:flex-start}
.convert-row input{margin-bottom:0;flex:1}
.btn-convert{background:#0f766e;color:#99f6e4;border:none;border-radius:8px;
             padding:.62rem 1.25rem;cursor:pointer;font-size:.9rem;font-weight:700;
             white-space:nowrap;transition:background .2s;flex-shrink:0}
.btn-convert:hover{background:#0d9488}
.result-box{display:none;margin-top:1.25rem}
.result-url{background:#0f172a;border:1px solid #334155;border-radius:8px;
            padding:.75rem 1rem;font-family:'SFMono-Regular',Consolas,monospace;
            font-size:.85rem;color:#a5b4fc;word-break:break-all;
            cursor:pointer;transition:border-color .2s;position:relative}
.result-url:hover{border-color:#6366f1}
.result-copy-hint{font-size:.72rem;color:#475569;margin-top:.35rem;text-align:right}
.qr-wrap{display:flex;flex-direction:column;align-items:center;gap:.75rem;margin-top:1.25rem}
.qr-frame{background:#fff;border-radius:10px;padding:10px;cursor:pointer;
           box-shadow:0 4px 16px rgba(0,0,0,.35);transition:transform .15s}
.qr-frame:hover{transform:scale(1.03)}
.qr-hint{font-size:.75rem;color:#64748b}
/* ── Toast ── */
.toast{position:fixed;bottom:2rem;right:2rem;padding:.85rem 1.5rem;border-radius:10px;
       font-weight:600;font-size:.95rem;opacity:0;transition:opacity .3s;
       pointer-events:none;z-index:100}
.toast.ok{background:#14532d;color:#86efac;border:1px solid #166534}
.toast.err{background:#450a0a;color:#fca5a5;border:1px solid #dc2626}
.toast.show{opacity:1}
</style>
</head>
<body>
<div class="topbar">
  <div>
    <h1>⚡ VLESS Aggregator</h1>
    <div class="subtitle">Admin Panel — changes take effect immediately</div>
  </div>
  <a class="logout" href="/admin/logout">Sign out</a>
</div>

<!-- ── Upstream Hosts ── -->
<div class="card">
  <div class="card-title">Upstream Hosts</div>
  <p class="hint">
    The incoming request path is appended to each host.<br>
    Client calls <code>https://you.com/api/TOKEN</code> →
    aggregator fetches <code>https://vpn1.example.com/api/TOKEN</code> and
    <code>https://vpn2.example.com/api/TOKEN</code> in parallel.
  </p>
  <div id="hosts"></div>
  <button class="btn-add" onclick="addHost('')">+ Add host</button>
</div>

<!-- ── Upstream Settings ── -->
<div class="card">
  <div class="card-title">Upstream Settings</div>
  <div class="grid2">
    <div>
      <label>Request timeout (seconds)</label>
      <input type="number" id="timeout_sec" min="1" max="120">
    </div>
    <div>
      <label>Profile-Update-Interval (hours)</label>
      <input type="number" id="update_interval" min="1" max="168">
    </div>
  </div>
</div>

<!-- ── Public URL & Profile ── -->
<div class="card">
  <div class="card-title">Subscription Profile</div>
  <p class="hint">
    These values are sent in subscription response headers and shown in VPN apps.
  </p>
  <div class="grid2">
    <div>
      <label>Public URL</label>
      <input type="url" id="public_url" placeholder="https://app.example.com:9999">
      <div style="font-size:.75rem;color:#64748b;margin-top:-.5rem;margin-bottom:.75rem">
        Base URL of this aggregator. Used in <code>Profile-Web-Page-Url</code> header and Link Converter.
      </div>
    </div>
    <div>
      <label>Profile Title</label>
      <input type="text" id="profile_title" placeholder="My VPN Subscription">
      <div style="font-size:.75rem;color:#64748b;margin-top:-.5rem;margin-bottom:.75rem">
        Shown as subscription name in VPN apps (<code>Profile-Title</code> header).
      </div>
    </div>
  </div>
</div>

<!-- ── Admin Credentials ── -->
<div class="card">
  <div class="card-title">Admin Credentials</div>
  <div class="grid2">
    <div>
      <label>Username</label>
      <input type="text" id="admin_username" autocomplete="off">
    </div>
    <div>
      <label>New Password</label>
      <input type="password" id="admin_password" autocomplete="new-password"
             placeholder="leave blank to keep current">
    </div>
  </div>
</div>

<button class="btn-save" id="btn-save" onclick="save()">💾 Save Configuration</button>

<!-- ── Link Converter ── -->
<div class="card" style="margin-top:2rem">
  <div class="card-title">🔗 Link Converter</div>
  <p class="hint">
    Paste a subscription URL from any upstream panel — get a new link
    pointing to this aggregator with the same path.<br>
    <span style="color:#94a3b8">Requires <strong>Public URL</strong> to be set above.</span>
  </p>
  <label>Original URL</label>
  <div class="convert-row">
    <input type="url" id="conv-input" placeholder="https://vpn1.example.com/api/TOKEN">
    <button class="btn-convert" onclick="convert()">Convert →</button>
  </div>

  <div class="result-box" id="result-box">
    <label>Converted URL</label>
    <div class="result-url" id="result-url" onclick="copyResult()" title="Click to copy"></div>
    <div class="result-copy-hint">click to copy</div>

    <div class="qr-wrap">
      <canvas id="conv-qr"></canvas>
      <div class="qr-frame" id="qr-frame" onclick="copyResult()" title="Click to copy URL">
        <canvas id="conv-qr-display"></canvas>
      </div>
      <div class="qr-hint">Click QR to copy URL · Scan with VPN app</div>
    </div>
  </div>
</div>

<div class="toast" id="toast"></div>

<script src="https://cdnjs.cloudflare.com/ajax/libs/qrious/4.0.2/qrious.min.js"></script>
<script>
let savedPassword = '';
let currentPublicURL = '';

async function load() {
  const d = await fetch('/admin/api/config').then(r => r.json());
  savedPassword = d.admin_password;
  currentPublicURL = d.public_url || '';
  document.getElementById('timeout_sec').value = d.timeout_sec;
  document.getElementById('update_interval').value = d.update_interval;
  document.getElementById('admin_username').value = d.admin_username;
  document.getElementById('public_url').value = d.public_url || '';
  document.getElementById('profile_title').value = d.profile_title || '';
  document.getElementById('hosts').innerHTML = '';
  (d.hosts || []).forEach(addHost);
}

function addHost(val) {
  const wrap = document.getElementById('hosts');
  const row = document.createElement('div');
  row.className = 'host-row';
  row.innerHTML =
    '<input type="text" placeholder="https://vpn1.example.com" value="' +
    String(val).replace(/"/g, '&quot;') + '">' +
    '<button class="btn-rm" onclick="this.parentElement.remove()">Remove</button>';
  wrap.appendChild(row);
  if (!val) row.querySelector('input').focus();
}

async function save() {
  const btn = document.getElementById('btn-save');
  btn.disabled = true;
  const hosts = [...document.querySelectorAll('#hosts .host-row input')]
    .map(i => i.value.trim()).filter(Boolean);
  const newPass = document.getElementById('admin_password').value;
  const publicURL = document.getElementById('public_url').value.trim().replace(/\/$/, '');
  const payload = {
    timeout_sec:     parseInt(document.getElementById('timeout_sec').value) || 15,
    update_interval: parseInt(document.getElementById('update_interval').value) || 1,
    public_url:      publicURL,
    profile_title:   document.getElementById('profile_title').value.trim(),
    announce:        document.getElementById('announce').value.trim(),
    profile_title:   document.getElementById('profile_title').value.trim(),
    hosts,
    admin_username: document.getElementById('admin_username').value,
    admin_password: newPass || savedPassword,
  };
  try {
    const res = await fetch('/admin/api/config', {
      method: 'POST',
      headers: {'Content-Type':'application/json'},
      body: JSON.stringify(payload),
    });
    const d = await res.json();
    if (d.ok) {
      if (newPass) savedPassword = newPass;
      currentPublicURL = publicURL;
      document.getElementById('admin_password').value = '';
      toast('Saved ✓', 'ok');
    } else {
      toast('Error: ' + d.error, 'err');
    }
  } catch(e) {
    toast('Network error: ' + e.message, 'err');
  } finally {
    btn.disabled = false;
  }
}

// ── Link Converter ────────────────────────────────────────────────────────────

function convert() {
  const raw = document.getElementById('conv-input').value.trim();
  const base = (document.getElementById('public_url').value.trim() || currentPublicURL).replace(/\/$/, '');

  if (!base) {
    toast('Set Public URL first and save config', 'err');
    return;
  }
  if (!raw) {
    toast('Paste a URL to convert', 'err');
    return;
  }

  let parsed;
  try { parsed = new URL(raw); }
  catch(_) { toast('Invalid URL', 'err'); return; }

  // Keep path + query, replace origin with public URL
  const converted = base + parsed.pathname + parsed.search;

  document.getElementById('result-url').textContent = converted;
  document.getElementById('result-box').style.display = 'block';

  // Draw QR on the visible canvas
  new QRious({
    element: document.getElementById('conv-qr-display'),
    value: converted,
    size: 180,
    background: '#ffffff',
    foreground: '#0f172a',
    padding: 4,
    level: 'M',
  });
}

function copyResult() {
  const text = document.getElementById('result-url').textContent;
  navigator.clipboard.writeText(text).then(function() {
    toast('Copied ✓', 'ok');
  });
}

function toast(msg, type) {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = 'toast ' + type + ' show';
  setTimeout(() => el.className = 'toast ' + type, 3000);
}

load();
</script>
</body></html>`