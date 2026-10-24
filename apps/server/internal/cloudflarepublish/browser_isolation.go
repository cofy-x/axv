package cloudflarepublish

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
	"golang.org/x/net/publicsuffix"
)

const (
	isolationPath            = "/__axv/isolation"
	isolationConfirmPath     = isolationPath + "/confirm"
	isolationCookie          = "__Host-AXV-Isolation"
	isolationChallengeCookie = "__Host-AXV-Isolation-Challenge"
	isolationProofLifetime   = 15 * time.Minute
	isolationChallengeLife   = time.Minute
	maxUsedChallenges        = 4096
)

var nativeIsolationClient = regexp.MustCompile(`^(Go-http-client|curl|axv)/[0-9]+(?:\.[0-9]+){0,3}(?:-[A-Za-z0-9][A-Za-z0-9.-]*)?$`)

type isolationToken struct {
	Host       string `json:"host"`
	Generation string `json:"generation"`
	Nonce      string `json:"nonce"`
	Issued     int64  `json:"iat"`
	Expires    int64  `json:"exp"`
}

// browserIsolation is a compatibility gate for a fresh, immutable application
// hostname, before guest JavaScript or service workers can run there. Its cookie
// is not identity, provider-policy freshness, or permission to change targets.
// Native clients retain the independently enforced Host/Origin/JWT boundaries.
func (m *Manager) browserIsolation(w http.ResponseWriter, r *http.Request, entry *runtimeEntry, now time.Time) bool {
	if len(r.Header.Values("Service-Worker")) != 0 || strings.EqualFold(r.Header.Get("Sec-Fetch-Dest"), "serviceworker") {
		controlError(w, http.StatusForbidden, "publication_service_worker_unsupported", "Service workers and offline applications are not supported on published application origins.")
		return false
	}
	reserved := r.URL.Path == isolationPath || strings.HasPrefix(r.URL.Path, isolationPath+"/")
	browser := hasFetchMetadata(r)
	if !browser {
		if reserved || !nativeIsolationRequest(r) {
			controlError(w, http.StatusForbidden, "publication_browser_unsupported", "This client must support Fetch Metadata and the browser isolation check.")
			return false
		}
		return true
	}
	if !validFetchMetadata(r) {
		controlError(w, http.StatusForbidden, "publication_browser_unsupported", "Use a browser that supplies complete Fetch Metadata for this application.")
		return false
	}
	if reserved {
		if r.URL.Path == isolationConfirmPath {
			m.confirmBrowserIsolation(w, r, entry, now)
		} else {
			controlError(w, http.StatusNotFound, "publication_isolation_not_found", "Unknown AXV browser isolation endpoint.")
		}
		return false
	}
	if proof, ok := singleIsolationCookie(r, isolationCookie); ok {
		if _, valid := m.readIsolationToken(proof, "proof", entry, now); valid {
			return true
		}
	}
	if r.Method == http.MethodGet && r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document" {
		m.writeIsolationBootstrap(w, entry, now)
		return false
	}
	controlError(w, http.StatusForbidden, "publication_isolation_required", "Open this application's HTTPS address in a browser tab to complete its isolation check first.")
	return false
}

func hasFetchMetadata(r *http.Request) bool {
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "sec-fetch-") {
			return true
		}
	}
	return false
}

func validFetchMetadata(r *http.Request) bool {
	for _, name := range []string{"Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest"} {
		if len(r.Header.Values(name)) != 1 {
			return false
		}
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "same-site", "cross-site", "none":
	default:
		return false
	}
	switch r.Header.Get("Sec-Fetch-Mode") {
	case "navigate", "same-origin", "cors", "no-cors", "websocket":
		return true
	}
	return false
}

func nativeIsolationRequest(r *http.Request) bool {
	values := r.Header.Values("User-Agent")
	if len(values) == 1 && nativeIsolationClient.MatchString(values[0]) {
		return true
	}
	return len(values) == 0 && len(r.Header.Values("Origin")) == 0 &&
		(r.Method == http.MethodGet || r.Method == http.MethodHead) && len(r.Header.Values("Upgrade")) == 0
}

func (m *Manager) signIsolationToken(token isolationToken, kind string) string {
	payload, _ := json.Marshal(token)
	mac := hmac.New(sha256.New, m.isolationKey[:])
	_, _ = io.WriteString(mac, "AXV publication browser isolation "+kind+"\x00")
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *Manager) readIsolationToken(raw, kind string, entry *runtimeEntry, now time.Time) (isolationToken, bool) {
	var token isolationToken
	if len(raw) > 2048 {
		return token, false
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return token, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return token, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return token, false
	}
	mac := hmac.New(sha256.New, m.isolationKey[:])
	_, _ = io.WriteString(mac, "AXV publication browser isolation "+kind+"\x00")
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) || json.Unmarshal(payload, &token) != nil {
		return isolationToken{}, false
	}
	lifetime := isolationProofLifetime
	if kind == "challenge" {
		lifetime = isolationChallengeLife
	}
	nonce, err := hex.DecodeString(token.Nonce)
	if token.Host != entry.hostname || token.Generation != entry.generation || len(nonce) != 16 || err != nil ||
		token.Issued <= 0 || token.Issued > now.Unix() || token.Expires <= now.Unix() || token.Expires <= token.Issued ||
		token.Expires-token.Issued > int64(lifetime/time.Second) {
		return isolationToken{}, false
	}
	return token, true
}

func singleIsolationCookie(r *http.Request, name string) (string, bool) {
	var value string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == name {
			value = cookie.Value
			count++
		}
	}
	return value, count == 1 && value != ""
}

func (m *Manager) consumeIsolationChallenge(nonce string, expires, now int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	if m.isolationUsed == nil {
		m.isolationUsed = map[string]int64{}
	}
	for used, until := range m.isolationUsed {
		if until <= now {
			delete(m.isolationUsed, used)
		}
	}
	if _, exists := m.isolationUsed[nonce]; exists || len(m.isolationUsed) >= maxUsedChallenges {
		return false
	}
	m.isolationUsed[nonce] = expires
	return true
}

func (m *Manager) confirmBrowserIsolation(w http.ResponseWriter, r *http.Request, entry *runtimeEntry, now time.Time) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != "https://"+entry.hostname ||
		r.Header.Get("Sec-Fetch-Site") != "same-origin" || (r.Header.Get("Sec-Fetch-Mode") != "cors" && r.Header.Get("Sec-Fetch-Mode") != "same-origin") {
		controlError(w, http.StatusForbidden, "publication_isolation_forbidden", "Confirm browser isolation from this application's exact HTTPS origin.")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		controlError(w, http.StatusBadRequest, "publication_isolation_invalid", "Provide one bounded browser isolation confirmation.")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	data = bytes.TrimSpace(data)
	var input struct {
		Challenge string `json:"challenge"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err != nil || !jsonunicode.Valid(data) || len(data) == 0 || data[0] != '{' || decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		controlError(w, http.StatusBadRequest, "publication_isolation_invalid", "Provide one bounded browser isolation confirmation.")
		return
	}
	cookie, ok := singleIsolationCookie(r, isolationChallengeCookie)
	_, cookieValid := m.readIsolationToken(cookie, "challenge", entry, now)
	token, valid := m.readIsolationToken(input.Challenge, "challenge", entry, now)
	// Concurrent first navigations replace this one fixed cookie. Either valid
	// cookie belongs to the same origin/generation; only the body's independent
	// nonce is consumed, so another tab's unconsumed challenge remains usable.
	if !ok || !cookieValid || !valid || !m.consumeIsolationChallenge(token.Nonce, token.Expires, now.Unix()) {
		controlError(w, http.StatusForbidden, "publication_isolation_expired", "The browser isolation check expired or changed. Open the application address again.")
		return
	}
	proof := token
	proof.Issued, proof.Expires = now.Unix(), now.Add(isolationProofLifetime).Unix()
	http.SetCookie(w, &http.Cookie{Name: isolationCookie, Value: m.signIsolationToken(proof, "proof"), Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(isolationProofLifetime / time.Second), Expires: time.Unix(proof.Expires, 0)})
	// Let the challenge cookie expire naturally. Clearing it here would reject
	// another tab whose first navigation is still completing its own check.
	w.WriteHeader(http.StatusNoContent)
}

func (m *Manager) writeIsolationBootstrap(w http.ResponseWriter, entry *runtimeEntry, now time.Time) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		controlError(w, http.StatusServiceUnavailable, "publication_isolation_unavailable", "The browser isolation check could not start. Try opening the address again.")
		return
	}
	challenge := isolationToken{Host: entry.hostname, Generation: entry.generation, Nonce: hex.EncodeToString(nonce[:]), Issued: now.Unix(), Expires: now.Add(isolationChallengeLife).Unix()}
	signed := m.signIsolationToken(challenge, "challenge")
	suffix, _ := publicsuffix.PublicSuffix(entry.hostname)
	var suffixes []string
	for suffix != "" {
		suffixes = append(suffixes, suffix)
		_, suffix, _ = strings.Cut(suffix, ".")
	}
	suffixJSON, _ := json.Marshal(suffixes)
	challengeJSON, _ := json.Marshal(signed)
	http.SetCookie(w, &http.Cookie{Name: isolationChallengeCookie, Value: signed, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(isolationChallengeLife / time.Second), Expires: time.Unix(challenge.Expires, 0)})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-same-origin; default-src 'none'; script-src 'nonce-"+challenge.Nonce+"'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = fmt.Fprintf(w, isolationBootstrapHTML, challenge.Nonce, suffixJSON, challengeJSON, challenge.Nonce)
}

func withoutIsolationCookies(r *http.Request) *http.Request {
	clean := r.Clone(r.Context())
	for name, values := range clean.Header {
		if !strings.EqualFold(name, "Cookie") {
			continue
		}
		var keptValues []string
		for _, value := range values {
			var kept []string
			for _, pair := range strings.Split(value, ";") {
				pair = strings.TrimSpace(pair)
				key, _, _ := strings.Cut(pair, "=")
				lower := strings.ToLower(strings.TrimSpace(key))
				if pair != "" && !strings.HasPrefix(lower, "__host-axv-") && !strings.HasPrefix(lower, "axv_isolation_probe_") {
					kept = append(kept, pair)
				}
			}
			if len(kept) != 0 {
				keptValues = append(keptValues, strings.Join(kept, "; "))
			}
		}
		delete(clean.Header, name)
		if len(keptValues) != 0 {
			clean.Header[name] = keptValues
		}
	}
	return clean
}

const isolationBootstrapHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>AXV browser isolation</title></head>
<body><main><h1>Checking browser isolation</h1><p id="isolation-status">Checking cookie boundaries before loading this application.</p><p id="isolation-detail">This compatibility check does not sign you in or change application permissions.</p><noscript>JavaScript and first-party cookies are required to verify this browser's isolation. The application has not loaded.</noscript></main>
<script nonce="%s">
(async () => {
  "use strict";
  const suffixes = %s;
  const challenge = %s;
  const id = "%s";
  const canary = "__Host-AXV-Probe-" + id;
  const probe = "AXV_Isolation_Probe_" + id;
  const read = (name) => document.cookie.split(";").some((pair) => pair.trim() === name + "=" + id);
  const clear = (name, domain) => { try { document.cookie = name + "=; Max-Age=0; Path=/; Secure; SameSite=Strict" + (domain ? "; Domain=" + domain : ""); } catch {} };
  const cleanup = () => { clear(canary); clear(canary, location.hostname); clear(probe); for (const suffix of suffixes) clear(probe, suffix); };
  try {
    document.cookie = canary + "=" + id + "; Path=/; Secure; SameSite=Strict";
    if (!read(canary)) throw new Error("First-party secure cookies are unavailable.");
    clear(canary);
    document.cookie = canary + "=" + id + "; Path=/; Secure; SameSite=Strict; Domain=" + location.hostname;
    if (read(canary)) throw new Error("This browser does not enforce host-only cookie prefixes.");
    for (const suffix of suffixes) {
      document.cookie = probe + "=" + id + "; Path=/; Secure; SameSite=Strict; Domain=" + suffix;
      if (read(probe)) throw new Error("This browser accepts cookies above the application's isolation boundary.");
      clear(probe, suffix);
    }
    cleanup();
    const response = await fetch("/__axv/isolation/confirm", {method: "POST", credentials: "same-origin", mode: "cors", redirect: "error", cache: "no-store", headers: {"Content-Type": "application/json"}, body: JSON.stringify({challenge})});
    if (response.status !== 204) throw new Error("The isolation check could not be confirmed. Open this address again.");
    document.getElementById("isolation-status").textContent = "Cookie boundaries checked. Loading the application.";
    location.reload();
  } catch (error) {
    cleanup();
    document.getElementById("isolation-status").textContent = "Application blocked in this browser.";
    document.getElementById("isolation-detail").textContent = error instanceof Error ? error.message : "The browser isolation check could not complete.";
  }
})();
</script></body></html>`
