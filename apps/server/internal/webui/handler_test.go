package webui

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAssetHandlerAndSPAFallback(t *testing.T) {
	h := fromFS(fstest.MapFS{"index.html": {Data: []byte("<html>Nextstep</html>")}, "assets/app.js": {Data: []byte("console.log('desktop')")}})
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{{"/", 200, "Nextstep"}, {"/notes/example", 200, "Nextstep"}, {"/assets/app.js", 200, "console.log"}, {"/assets/missing.js", 404, "404"}} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
