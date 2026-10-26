package publish

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/internal/origin"
)

func isolationFixture(t *testing.T) (*Manager, *runtimeEntry, time.Time) {
	t.Helper()
	m := &Manager{}
	if _, err := rand.Read(m.isolationKey[:]); err != nil {
		t.Fatal(err)
	}
	return m, &runtimeEntry{hostname: "app-0123456789abcdef0123456789abcdef.example.net", generation: "0123456789abcdef0123456789abcdef"}, time.Now().Truncate(time.Second)
}

func browserRequest(method, address, mode, dest string) *http.Request {
	r := httptest.NewRequest(method, address, nil)
	r.Header.Set("User-Agent", "Mozilla/5.0")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Sec-Fetch-Mode", mode)
	r.Header.Set("Sec-Fetch-Dest", dest)
	return r
}

func runIsolation(m *Manager, entry *runtimeEntry, now time.Time, request *http.Request) (*httptest.ResponseRecorder, bool) {
	response := httptest.NewRecorder()
	w, finish := origin.PreventCaching(response)
	allowed := m.browserIsolation(w, request, entry, now)
	finish()
	return response, allowed
}

func isolationProof(m *Manager, entry *runtimeEntry, now time.Time, kind string) string {
	lifetime := isolationProofLifetime
	if kind == "challenge" {
		lifetime = isolationChallengeLife
	}
	return m.signIsolationToken(isolationToken{Host: entry.hostname, Generation: entry.generation, Nonce: "fedcba9876543210fedcba9876543210", Issued: now.Unix(), Expires: now.Add(lifetime).Unix()}, kind)
}

func TestBrowserIsolationClientClassificationCannotUseUAToBypassMetadata(t *testing.T) {
	m, entry, now := isolationFixture(t)
	for _, test := range []struct {
		name, ua, origin, mode, dest string
		proof, websocket, allow      bool
	}{
		{name: "raw read", allow: true},
		{name: "empty UA origin is not raw", origin: "https://" + entry.hostname},
		{name: "Go native", ua: "Go-http-client/1.1", origin: "https://" + entry.hostname, allow: true},
		{name: "curl native", ua: "curl/8.5.0", allow: true},
		{name: "AXV native", ua: "axv/0.0.0-dev", allow: true},
		{name: "unknown client", ua: "Custom client"},
		{name: "legacy browser", ua: "Mozilla/5.0 Safari/605.1.15"},
		{name: "invalid native version", ua: "curl/not-a-version"},
		{name: "native prefix only", ua: "curl/8.5.0 Mozilla/5.0"},
		{name: "fetch without proof", ua: "Mozilla/5.0", mode: "cors"},
		{name: "native UA cannot hide browser fetch", ua: "curl/8.5.0", mode: "cors"},
		{name: "browser WebSocket without proof", ua: "Mozilla/5.0", mode: "websocket", websocket: true},
		{name: "native WebSocket", ua: "Go-http-client/1.1", origin: "https://" + entry.hostname, websocket: true, allow: true},
		{name: "browser with checked cookie", ua: "Mozilla/5.0", mode: "cors", proof: true, allow: true},
		{name: "checked WebWorker", ua: "Mozilla/5.0", mode: "same-origin", dest: "worker", proof: true, allow: true},
		{name: "checked SharedWorker", ua: "Mozilla/5.0", mode: "same-origin", dest: "sharedworker", proof: true, allow: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://"+entry.hostname+"/app", nil)
			if test.ua != "" {
				r.Header.Set("User-Agent", test.ua)
			}
			if test.origin != "" {
				r.Header.Set("Origin", test.origin)
			}
			if test.mode != "" {
				r.Header.Set("Sec-Fetch-Site", "same-origin")
				r.Header.Set("Sec-Fetch-Mode", test.mode)
				r.Header.Set("Sec-Fetch-Dest", test.dest)
			}
			if test.proof {
				r.AddCookie(&http.Cookie{Name: isolationCookie, Value: isolationProof(m, entry, now, "proof")})
			}
			if test.websocket {
				r.Header.Set("Upgrade", "websocket")
			}
			response, allowed := runIsolation(m, entry, now, r)
			if allowed != test.allow || !allowed && response.Code != http.StatusForbidden {
				t.Fatalf("client classification bypassed its browser gate: allowed=%t, response=%d %s", allowed, response.Code, response.Body.String())
			}
		})
	}
	r := httptest.NewRequest(http.MethodGet, "http://"+entry.hostname+"/app", nil)
	r.Header.Set("User-Agent", "curl/8.5.0")
	r.Header.Set("Sec-Fetch-Dest", "document")
	if response, allowed := runIsolation(m, entry, now, r); allowed || response.Code != http.StatusForbidden {
		t.Fatal("partial browser metadata took the native bypass")
	}
}

func TestBrowserIsolationBootstrapChecksEverySuffixWithoutReplacingApplicationCookies(t *testing.T) {
	for _, test := range []struct{ host, suffixes string }{
		{"pub.example.net", `["net"]`},
		{"pub.foo.github.io", `["github.io","io"]`},
		{"pub.foo.pages.dev", `["pages.dev","dev"]`},
		{"pub.example.co.uk", `["co.uk","uk"]`},
	} {
		t.Run(test.host, func(t *testing.T) {
			m, entry, now := isolationFixture(t)
			entry.hostname = test.host
			r := browserRequest(http.MethodGet, "http://"+entry.hostname+"//app%2Fpath?q=one%2Ftwo", "navigate", "document")
			r.AddCookie(&http.Cookie{Name: "application", Value: "retain-session"})
			originalCookie := r.Header.Get("Cookie")
			response, allowed := runIsolation(m, entry, now, r)
			if allowed || response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("navigation did not receive the owned bootstrap: %d", response.Code)
			}
			body := response.Body.String()
			for _, text := range []string{"const suffixes = " + test.suffixes, "__Host-AXV-Probe-", "AXV_Isolation_Probe_", "Domain=", "if (read(probe)) throw", "cleanup();", "location.reload();", isolationConfirmPath} {
				if !strings.Contains(body, text) {
					t.Fatalf("bootstrap omitted its compatibility or exact URL boundary: %q", text)
				}
			}
			if r.URL.RequestURI() != "//app%2Fpath?q=one%2Ftwo" || strings.Contains(body, "app%2Fpath") {
				t.Fatal("bootstrap changed or unnecessarily reflected the original application URL")
			}
			cookies := response.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Name != isolationChallengeCookie || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Domain != "" || cookies[0].Path != "/" {
				t.Fatalf("bootstrap changed app cookies or weakened its challenge cookie: %+v", cookies)
			}
			if r.Header.Get("Cookie") != originalCookie || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Header().Get("Content-Security-Policy"), "default-src 'none'") || !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Fatal("bootstrap replaced application state, cache policy or its owned response policy")
			}
		})
	}
}

func confirmation(entry *runtimeEntry, challenge string) *http.Request {
	data, _ := json.Marshal(map[string]string{"challenge": challenge})
	r := browserRequest(http.MethodPost, "http://"+entry.hostname+isolationConfirmPath, "same-origin", "")
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.Header.Set("Origin", "https://"+entry.hostname)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: isolationChallengeCookie, Value: challenge})
	return r
}

func TestBrowserIsolationChallengeProofAndReplayBoundaries(t *testing.T) {
	m, entry, now := isolationFixture(t)
	challenge := isolationProof(m, entry, now, "challenge")
	for _, test := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"wrong Origin", func(r *http.Request) { r.Header.Set("Origin", "https://other.example.net") }},
		{"missing challenge cookie", func(r *http.Request) { r.Header.Del("Cookie") }},
		{"duplicate cookie", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: isolationChallengeCookie, Value: challenge}) }},
		{"wrong challenge cookie", func(r *http.Request) { r.Header.Set("Cookie", isolationChallengeCookie+"=other") }},
		{"another host cookie", func(r *http.Request) {
			foreign := &runtimeEntry{hostname: "other.example.net", generation: entry.generation}
			r.Header.Set("Cookie", isolationChallengeCookie+"="+isolationProof(m, foreign, now, "challenge"))
		}},
		{"another generation cookie", func(r *http.Request) {
			foreign := &runtimeEntry{hostname: entry.hostname, generation: "other-generation"}
			r.Header.Set("Cookie", isolationChallengeCookie+"="+isolationProof(m, foreign, now, "challenge"))
		}},
		{"expired cookie", func(r *http.Request) {
			r.Header.Set("Cookie", isolationChallengeCookie+"="+isolationProof(m, entry, now.Add(-2*time.Minute), "challenge"))
		}},
		{"cross-site confirmation", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
		{"navigation confirmation", func(r *http.Request) { r.Header.Set("Sec-Fetch-Mode", "navigate") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := confirmation(entry, challenge)
			test.change(r)
			if response, allowed := runIsolation(m, entry, now, r); allowed || response.Code != http.StatusForbidden || len(response.Result().Cookies()) != 0 {
				t.Fatal("untrusted confirmation issued browser isolation proof")
			}
		})
	}
	for _, test := range []struct {
		name string
		edit func(*isolationToken)
	}{
		{"challenge host changed", func(token *isolationToken) { token.Host = "other.example.net" }},
		{"challenge generation changed", func(token *isolationToken) { token.Generation = "other-generation" }},
		{"challenge expired", func(token *isolationToken) { token.Issued = now.Add(-time.Minute).Unix(); token.Expires = now.Unix() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			token := isolationToken{Host: entry.hostname, Generation: entry.generation, Nonce: "0123456789abcdef0123456789abcdef", Issued: now.Unix(), Expires: now.Add(isolationChallengeLife).Unix()}
			test.edit(&token)
			response, _ := runIsolation(m, entry, now, confirmation(entry, m.signIsolationToken(token, "challenge")))
			if response.Code != http.StatusForbidden || len(response.Result().Cookies()) != 0 {
				t.Fatal("a changed or expired challenge issued browser proof")
			}
		})
	}
	badSignature := "A" + challenge[1:]
	if challenge[0] == 'A' {
		badSignature = "B" + challenge[1:]
	}
	if response, _ := runIsolation(m, entry, now, confirmation(entry, badSignature)); response.Code != http.StatusForbidden {
		t.Fatal("a tampered challenge issued browser proof")
	}
	response, allowed := runIsolation(m, entry, now, confirmation(entry, challenge))
	if allowed || response.Code != http.StatusNoContent {
		t.Fatalf("valid exact-origin confirmation failed: %d %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != isolationCookie || cookies[0].MaxAge != 900 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Domain != "" || cookies[0].Path != "/" {
		t.Fatalf("confirmation weakened proof or replaced the shared short challenge cookie: %+v", cookies)
	}
	proof := cookies[0].Value
	if _, ok := m.readIsolationToken(proof, "proof", entry, now.Add(14*time.Minute)); !ok {
		t.Fatal("the original finite proof was not usable before expiry")
	}
	if _, ok := m.readIsolationToken(proof, "proof", entry, now.Add(isolationProofLifetime)); ok {
		t.Fatal("a reconnect renewed the fixed browser proof lifetime")
	}
	if response, _ = runIsolation(m, entry, now, confirmation(entry, challenge)); response.Code != http.StatusForbidden {
		t.Fatal("a consumed challenge was replayed")
	}
	for _, test := range []struct {
		name string
		kind string
		edit func(*isolationToken)
	}{
		{"different host", "proof", func(token *isolationToken) { token.Host = "other.example.net" }},
		{"different generation", "proof", func(token *isolationToken) { token.Generation = "different-generation" }},
		{"expired", "proof", func(token *isolationToken) { token.Expires = now.Unix() }},
		{"future issuance", "proof", func(token *isolationToken) { token.Issued = now.Add(time.Second).Unix() }},
		{"overlong proof", "proof", func(token *isolationToken) { token.Expires = now.Add(isolationProofLifetime + time.Second).Unix() }},
		{"challenge cannot become proof", "challenge", func(*isolationToken) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			token := isolationToken{Host: entry.hostname, Generation: entry.generation, Nonce: "0123456789abcdef0123456789abcdef", Issued: now.Unix(), Expires: now.Add(isolationProofLifetime).Unix()}
			test.edit(&token)
			if _, ok := m.readIsolationToken(m.signIsolationToken(token, test.kind), "proof", entry, now); ok {
				t.Fatal("proof lost its exact host/generation/purpose/time boundary")
			}
		})
	}
	tampered := "A" + proof[1:]
	if proof[0] == 'A' {
		tampered = "B" + proof[1:]
	}
	if _, ok := m.readIsolationToken(tampered, "proof", entry, now); ok {
		t.Fatal("tampered proof was accepted")
	}
	restarted, _, _ := isolationFixture(t)
	if _, ok := restarted.readIsolationToken(proof, "proof", entry, now); ok {
		t.Fatal("a prior manager's proof survived restart")
	}
}

func TestBrowserIsolationConcurrentTabsKeepIndependentSingleUseChallenges(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%t", reverse), func(t *testing.T) {
			m, entry, now := isolationFixture(t)
			var challenges []string
			for range 2 {
				r := browserRequest(http.MethodGet, "http://"+entry.hostname+"/app", "navigate", "document")
				response, _ := runIsolation(m, entry, now, r)
				cookies := response.Result().Cookies()
				if len(cookies) != 1 || cookies[0].Name != isolationChallengeCookie {
					t.Fatal("first navigation did not issue one short challenge cookie")
				}
				challenges = append(challenges, cookies[0].Value)
			}
			// Both tabs now share the last navigation's cookie, but their body
			// challenges remain distinct. Confirm in either completion order.
			lastCookie := challenges[1]
			if reverse {
				challenges[0], challenges[1] = challenges[1], challenges[0]
			}
			for _, challenge := range challenges {
				r := confirmation(entry, challenge)
				r.Header.Set("Cookie", isolationChallengeCookie+"="+lastCookie)
				response, _ := runIsolation(m, entry, now, r)
				if response.Code != http.StatusNoContent {
					t.Fatalf("one tab invalidated another's outstanding check: %d %s", response.Code, response.Body.String())
				}
				for _, cookie := range response.Result().Cookies() {
					if cookie.Name == isolationChallengeCookie {
						t.Fatal("one tab cleared the shared challenge cookie")
					}
				}
			}
			for _, challenge := range challenges {
				r := confirmation(entry, challenge)
				r.Header.Set("Cookie", isolationChallengeCookie+"="+lastCookie)
				if response, _ := runIsolation(m, entry, now, r); response.Code != http.StatusForbidden {
					t.Fatal("a tab reused a consumed body challenge")
				}
			}
		})
	}
}

func TestBrowserIsolationNeverForwardsReservedNamespaceOrServiceWorkers(t *testing.T) {
	m, entry, now := isolationFixture(t)
	for _, path := range []string{isolationPath, isolationConfirmPath, isolationPath + "/unknown", isolationPath + "/nested/app"} {
		r := browserRequest(http.MethodGet, "http://"+entry.hostname+path, "navigate", "document")
		r.AddCookie(&http.Cookie{Name: isolationCookie, Value: isolationProof(m, entry, now, "proof")})
		if _, allowed := runIsolation(m, entry, now, r); allowed {
			t.Fatalf("reserved AXV endpoint reached the guest: %s", path)
		}
	}
	for _, metadata := range []bool{false, true} {
		for _, marker := range []string{"Service-Worker", "Sec-Fetch-Dest"} {
			r := httptest.NewRequest(http.MethodGet, "http://"+entry.hostname+"/sw.js", nil)
			r.Header.Set("User-Agent", "Go-http-client/1.1")
			if metadata {
				r = browserRequest(http.MethodGet, "http://"+entry.hostname+"/sw.js", "same-origin", "serviceworker")
			}
			if marker == "Service-Worker" {
				r.Header.Set(marker, "script")
			} else {
				r.Header.Set(marker, "serviceworker")
			}
			r.AddCookie(&http.Cookie{Name: isolationCookie, Value: isolationProof(m, entry, now, "proof")})
			response, allowed := runIsolation(m, entry, now, r)
			if allowed || response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "publication_service_worker_unsupported") {
				t.Fatal("service-worker installation or update escaped the supported application model")
			}
		}
	}
}

func TestBrowserIsolationConfirmationRequiresBoundedStrictUnicodeObject(t *testing.T) {
	m, entry, now := isolationFixture(t)
	challenge := isolationProof(m, entry, now, "challenge")
	for _, data := range [][]byte{
		[]byte(`{"challenge":"\ud800"}`),
		append([]byte(`{"challenge":"`), append([]byte{0xff}, []byte(`"}`)...)...),
		[]byte(`{"challenge":"value"} {}`),
		[]byte(`{"challenge":"value","unknown":true}`),
		[]byte(`null`), []byte(`[]`),
		[]byte(strings.Repeat(" ", 4097)),
	} {
		r := confirmation(entry, challenge)
		r.Body = io.NopCloser(bytes.NewReader(data))
		response, allowed := runIsolation(m, entry, now, r)
		if allowed || response.Code != http.StatusBadRequest || len(response.Result().Cookies()) != 0 {
			t.Fatalf("invalid browser confirmation body was admitted: %q, %d", data, response.Code)
		}
	}
}

func TestBrowserIsolationCredentialsNeverReachApplication(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://app.example.net/", nil)
	r.Header.Add("Cookie", "application=retain; "+isolationCookie+"=private-proof; preferences=dark")
	r.Header.Add("Cookie", isolationChallengeCookie+"=private-challenge; __Host-AXV-Probe-random=probe; AXV_Isolation_Probe_random=probe")
	original := r.Header.Clone()
	clean := withoutIsolationCookies(r)
	if len(clean.Cookies()) != 2 || clean.Header.Get("Cookie") != "application=retain; preferences=dark" || r.Header.Get("Cookie") != original.Get("Cookie") {
		t.Fatalf("isolation credentials escaped or application cookies changed: %#v", clean.Header.Values("Cookie"))
	}
}

func TestBrowserIsolationReplayCacheHasFiniteCapacityAndLifetime(t *testing.T) {
	m, _, now := isolationFixture(t)
	for index := range maxUsedChallenges {
		if !m.consumeIsolationChallenge(fmt.Sprint(index), now.Add(time.Minute).Unix(), now.Unix()) {
			t.Fatal("replay cache reached capacity early")
		}
	}
	if m.consumeIsolationChallenge("overflow", now.Add(time.Minute).Unix(), now.Unix()) {
		t.Fatal("replay cache exceeded its bounded capacity")
	}
	if !m.consumeIsolationChallenge("after-expiry", now.Add(2*time.Minute).Unix(), now.Add(time.Minute).Unix()) || len(m.isolationUsed) != 1 {
		t.Fatal("expired challenge receipts prevented recovery or were retained")
	}
}
