package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/keiretsu-labs/tempvm/internal/gateway"
)

func healthHandler(sources ...gateway.StatusProvider) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, request *http.Request) {
		landingHandler(w, request, sources...)
	})
	for _, path := range []string{"/healthz", "/readyz"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			status := currentStatus(sources...)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(struct {
				Status         string `json:"status"`
				ActiveSessions int    `json:"active_sessions"`
				MaxSessions    int    `json:"max_sessions"`
				MaxSessionTTL  string `json:"max_session_ttl"`
			}{
				Status:         "ok",
				ActiveSessions: status.ActiveSessions,
				MaxSessions:    status.MaxSessions,
				MaxSessionTTL:  statusTTL(status),
			})
		})
	}
	return mux
}

func currentStatus(sources ...gateway.StatusProvider) gateway.Status {
	if len(sources) == 0 || sources[0] == nil {
		return gateway.Status{}
	}
	return sources[0].Status()
}

func statusTTL(status gateway.Status) string {
	if status.MaxSessionTTL <= 0 {
		return "disabled"
	}
	return status.MaxSessionTTL.String()
}

func statusMaxSessions(status gateway.Status) string {
	if status.MaxSessions <= 0 {
		return "unlimited"
	}
	return stringValue(status.MaxSessions)
}

func stringValue(value int) string {
	if value < 0 {
		return "unknown"
	}
	return strconv.Itoa(value)
}

func landingHandler(w http.ResponseWriter, request *http.Request, sources ...gateway.StatusProvider) {
	if request.URL.Path != "/" {
		http.NotFound(w, request)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		status := currentStatus(sources...)
		page := strings.NewReplacer(
			"__ACTIVE_SESSIONS__", stringValue(status.ActiveSessions),
			"__MAX_SESSIONS__", statusMaxSessions(status),
			"__MAX_SESSION_TTL__", statusTTL(status),
		).Replace(landingPage)
		_, _ = w.Write([]byte(page))
	}
}

const landingPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="dark">
  <title>tempvm</title>
  <style>
    :root {
      color-scheme: dark;
      --bg: #090b10;
      --panel: #11151d;
      --line: #252c38;
      --text: #f4f6f8;
      --muted: #9ba6b5;
      --green: #66e3a4;
      --blue: #8cb4ff;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      display: grid;
      place-items: center;
      padding: 32px 20px;
      background:
        radial-gradient(circle at 20% 10%, rgba(52, 89, 164, .18), transparent 34rem),
        var(--bg);
      color: var(--text);
      font: 16px/1.55 ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    }
    main {
      width: min(760px, 100%);
      border: 1px solid var(--line);
      border-radius: 18px;
      background: color-mix(in srgb, var(--panel) 94%, transparent);
      box-shadow: 0 24px 80px rgba(0, 0, 0, .45);
      overflow: hidden;
    }
    header, section { padding: 28px 32px; }
    header { border-bottom: 1px solid var(--line); }
    .eyebrow {
      display: flex;
      align-items: center;
      gap: 10px;
      color: var(--green);
      font-size: 13px;
      letter-spacing: .08em;
      text-transform: uppercase;
    }
    .dot {
      width: 9px;
      height: 9px;
      border-radius: 50%;
      background: var(--green);
      box-shadow: 0 0 18px var(--green);
    }
    h1 {
      margin: 12px 0 6px;
      font-size: clamp(34px, 8vw, 58px);
      line-height: 1;
      letter-spacing: -.05em;
    }
    p { margin: 0; color: var(--muted); }
    .command {
      margin: 22px 0;
      padding: 18px 20px;
      overflow-x: auto;
      border: 1px solid var(--line);
      border-radius: 12px;
      background: #080a0f;
      color: var(--text);
      white-space: nowrap;
    }
    .prompt { color: var(--blue); user-select: none; }
    dl {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 18px;
      margin: 28px 0 0;
    }
    dl div {
      padding-top: 14px;
      border-top: 1px solid var(--line);
    }
    dt {
      color: var(--muted);
      font-size: 12px;
      letter-spacing: .08em;
      text-transform: uppercase;
    }
    dd { margin: 5px 0 0; }
    footer {
      padding: 18px 32px;
      border-top: 1px solid var(--line);
      color: var(--muted);
      font-size: 12px;
    }
    @media (max-width: 560px) {
      header, section { padding: 24px; }
      dl { grid-template-columns: 1fr; }
      footer { padding: 16px 24px; }
    }
  </style>
</head>
<body>
  <main>
    <header>
      <div class="eyebrow"><span class="dot"></span> Ottawa gateway online</div>
      <h1>tempvm</h1>
      <p>A fresh Linux microVM for one SSH session.</p>
    </header>
    <section>
      <p>Start a machine:</p>
      <div class="command"><span class="prompt">$</span> ssh linux@tempvm</div>
      <p>The VM is created when you connect and deleted when your SSH session ends.</p>
      <dl>
        <div><dt>Guest</dt><dd>Ubuntu 24.04 · x86_64</dd></div>
        <div><dt>Runtime</dt><dd>Kata · Cloud Hypervisor</dd></div>
        <div><dt>Node</dt><dd>Shiro · Ottawa</dd></div>
        <div><dt>Active sessions</dt><dd>__ACTIVE_SESSIONS__ / __MAX_SESSIONS__</dd></div>
        <div><dt>Max TTL</dt><dd>__MAX_SESSION_TTL__</dd></div>
      </dl>
    </section>
    <footer>Tailnet access only · ephemeral credentials · bounded lifetime</footer>
  </main>
</body>
</html>
`
