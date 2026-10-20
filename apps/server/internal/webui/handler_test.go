package webui

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
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

func TestCompressedAssetsPreserveContentAndHTTPMetadata(t *testing.T) {
	modified := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{"assets/app.js", "assets/app.css"} {
		t.Run(name, func(t *testing.T) {
			original := bytes.Repeat([]byte("/* desktop asset */\n"), 256)
			h := fromFS(fstest.MapFS{name: {Data: original, ModTime: modified}})
			serve := func(method string, headers map[string]string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, "/"+name, nil)
				for key, value := range headers {
					r.Header.Set(key, value)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			identity := serve("GET", nil)
			encoded := serve("GET", map[string]string{"Accept-Encoding": "gzip"})
			if identity.Code != http.StatusOK || encoded.Code != http.StatusOK || encoded.Header().Get("Content-Encoding") != "gzip" || encoded.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatalf("unexpected compressed response: HTTP %d, %v", encoded.Code, encoded.Header())
			}
			if encoded.Header().Get("Content-Type") != identity.Header().Get("Content-Type") || encoded.Header().Get("Last-Modified") != modified.Format(http.TimeFormat) || encoded.Header().Get("Content-Length") != strconv.Itoa(encoded.Body.Len()) || encoded.Body.Len() >= len(original) {
				t.Fatalf("compressed representation metadata changed: %v", encoded.Header())
			}
			reader, err := gzip.NewReader(encoded.Body)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := io.ReadAll(reader)
			if err != nil || reader.Close() != nil || !bytes.Equal(decoded, original) {
				t.Fatalf("compressed content differs from the asset: %v", err)
			}
			head := serve("HEAD", map[string]string{"Accept-Encoding": "gzip"})
			if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Encoding") != "gzip" || head.Header().Get("Content-Length") != encoded.Header().Get("Content-Length") || head.Header().Get("Content-Type") != encoded.Header().Get("Content-Type") {
				t.Fatalf("HEAD does not describe the compressed GET: HTTP %d, %v", head.Code, head.Header())
			}
			partial := serve("GET", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=2-9"})
			if partial.Code != http.StatusPartialContent || partial.Header().Get("Content-Encoding") != "" || partial.Header().Get("Vary") != "Accept-Encoding" || !bytes.Equal(partial.Body.Bytes(), original[2:10]) || partial.Header().Get("Content-Range") != "bytes 2-9/5120" {
				t.Fatalf("range request did not retain the original representation: HTTP %d, %v", partial.Code, partial.Header())
			}
			unchanged := serve("GET", map[string]string{"Accept-Encoding": "gzip", "If-Modified-Since": modified.Format(http.TimeFormat)})
			if unchanged.Code != http.StatusNotModified || unchanged.Body.Len() != 0 || unchanged.Header().Get("Content-Encoding") != "" || unchanged.Header().Get("Content-Length") != "" || unchanged.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatalf("conditional request changed: HTTP %d, %v", unchanged.Code, unchanged.Header())
			}
			precondition := serve("GET", map[string]string{"Accept-Encoding": "gzip", "If-Unmodified-Since": modified.Add(-time.Hour).Format(http.TimeFormat)})
			if precondition.Code != http.StatusPreconditionFailed || precondition.Header().Get("Content-Encoding") != "" {
				t.Fatalf("failed precondition retained compression metadata: HTTP %d, %v", precondition.Code, precondition.Header())
			}
		})
	}
}

func TestAssetCompressionNegotiation(t *testing.T) {
	original := strings.Repeat("console.log('desktop');\n", 128)
	h := fromFS(fstest.MapFS{"assets/app.js": {Data: []byte(original)}})
	for _, tc := range []struct {
		accept []string
		gzip   bool
	}{
		{nil, false}, {[]string{"br"}, false}, {[]string{"gzip"}, true},
		{[]string{"br, gzip;q=0.5"}, true}, {[]string{"GZip; Q=1.0"}, true},
		{[]string{"gzip;q=0"}, false}, {[]string{"gzip;q=0.000"}, false},
		{[]string{"*;q=1"}, true}, {[]string{"*;q=0"}, false},
		{[]string{"gzip;q=0, *;q=1"}, false}, {[]string{"*;q=0, gzip;q=1"}, true},
		{[]string{"gzip;q=invalid"}, false}, {[]string{"gzip;q=NaN"}, false},
		{[]string{"gzip;q=2"}, false}, {[]string{"br", "gzip;q=0.5"}, true},
	} {
		t.Run(strings.Join(tc.accept, ","), func(t *testing.T) {
			r := httptest.NewRequest("GET", "/assets/app.js", nil)
			for _, value := range tc.accept {
				r.Header.Add("Accept-Encoding", value)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK || (w.Header().Get("Content-Encoding") == "gzip") != tc.gzip || w.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatalf("encoding negotiation failed: HTTP %d, %v", w.Code, w.Header())
			}
			if !tc.gzip && w.Body.String() != original {
				t.Fatal("identity fallback changed the asset")
			}
		})
	}
}

func TestCompressionLeavesOtherResponsesUnchanged(t *testing.T) {
	noise := make([]byte, 2048)
	random := rand.New(rand.NewPCG(1, 2))
	for i := range noise {
		noise[i] = byte(random.Uint32())
	}
	h := fromFS(fstest.MapFS{
		"index.html":         {Data: []byte("<html>AXV desktop</html>")},
		"assets/small.js":    {Data: []byte("console.log('desktop')")},
		"assets/noisy.css":   {Data: noise},
		"assets/favicon.svg": {Data: []byte(strings.Repeat("<svg></svg>", 200))},
		"assets/redirect.js": {Data: []byte(strings.Repeat("console.log('desktop');\n", 128))},
	})
	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{"GET", "/assets/small.js", http.StatusOK},
		{"GET", "/assets/noisy.css", http.StatusOK},
		{"GET", "/assets/favicon.svg", http.StatusOK},
		{"GET", "/assets/missing.js", http.StatusNotFound},
		{"GET", "/notes/example", http.StatusOK},
		{"HEAD", "/notes/example", http.StatusOK},
		{"GET", "/assets/redirect.js/", http.StatusMovedPermanently},
		{"POST", "/assets/redirect.js", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			r.Header.Set("Accept-Encoding", "gzip")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("Content-Encoding") != "" {
				t.Fatalf("non-compressed response changed: HTTP %d, %v", w.Code, w.Header())
			}
			if tc.method == "HEAD" && w.Body.Len() != 0 {
				t.Fatal("HEAD returned a response body")
			}
			if tc.path == "/notes/example" && tc.method == "GET" && !strings.Contains(w.Body.String(), "AXV desktop") {
				t.Fatal("deep link no longer serves the desktop")
			}
			if tc.path == "/assets/noisy.css" && !bytes.Equal(w.Body.Bytes(), noise) {
				t.Fatal("compression without a size benefit changed the asset")
			}
		})
	}
}

type countedAssetFS struct {
	fs.FS
	reads atomic.Int32
}

func (files *countedAssetFS) ReadFile(name string) ([]byte, error) {
	files.reads.Add(1)
	return fs.ReadFile(files.FS, name)
}

func TestCompressedCacheCoalescesReadersWithoutCachingRequestPaths(t *testing.T) {
	files := &countedAssetFS{FS: fstest.MapFS{"assets/app.js": {Data: []byte(strings.Repeat("console.log('desktop');\n", 128))}}}
	h := fromFS(files)
	var readers sync.WaitGroup
	for i := range 16 {
		readers.Go(func() {
			r := httptest.NewRequest("GET", "/assets/app.js?request="+strconv.Itoa(i), nil)
			r.Header.Set("Accept-Encoding", "gzip")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "gzip" {
				t.Errorf("concurrent compression failed: HTTP %d, %v", w.Code, w.Header())
			}
		})
	}
	readers.Wait()
	if files.reads.Load() != 1 {
		t.Fatalf("asset compression was repeated %d times", files.reads.Load())
	}
	for i := range 32 {
		r := httptest.NewRequest("GET", "/assets/missing"+strconv.Itoa(i)+".js", nil)
		r.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("missing asset returned HTTP %d", w.Code)
		}
	}
	if files.reads.Load() != 1 {
		t.Fatal("missing request paths reached the compressed asset cache")
	}
}
