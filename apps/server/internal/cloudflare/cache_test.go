package cloudflare

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSensitiveResponsesCannotReplaceNoStorePolicy(t *testing.T) {
	for _, mode := range []string{"header", "write", "flush", "empty"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w, finish := PreventCaching(w)
				defer finish()
				for _, name := range []string{"Cache-Control", "CDN-Cache-Control", "Cloudflare-CDN-Cache-Control", "Surrogate-Control"} {
					w.Header().Set(name, "public, max-age=86400")
					w.Header()[strings.ToLower(name)] = []string{"public, max-age=86400"}
				}
				w.Header().Set("Expires", "Fri, 01 Jan 2100 00:00:00 GMT")
				w.Header().Set("Pragma", "cache")
				switch mode {
				case "header":
					w.WriteHeader(http.StatusNoContent)
				case "write":
					_, _ = io.WriteString(w, "private response")
				case "flush":
					// SSE handlers commonly require the legacy Flusher interface.
					w.(http.Flusher).Flush()
				}
			}))
			defer server.Close()
			response, err := server.Client().Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			_, _ = io.ReadAll(response.Body)
			for _, name := range []string{"Cache-Control", "CDN-Cache-Control", "Cloudflare-CDN-Cache-Control", "Surrogate-Control"} {
				values := response.Header.Values(name)
				if len(values) != 1 || values[0] != "no-store" {
					t.Fatalf("%s could be overridden: %q", name, values)
				}
			}
			if response.Header.Get("Pragma") != "no-cache" || response.Header.Get("Expires") != "Thu, 01 Jan 1970 00:00:00 GMT" {
				t.Fatal("legacy cache policy could be overridden")
			}
		})
	}
}
