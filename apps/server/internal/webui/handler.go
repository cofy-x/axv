package webui

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Vite's default output names contain an eight-character base64url content hash.
var fingerprintedAsset = regexp.MustCompile(`^assets/[^/]+-[A-Za-z0-9_-]{8}\.(js|css)$`)

// fromFS also supports deep links without turning missing assets into HTML.
// Production assets are immutable, so compressed copies can share their lifetime.
func fromFS(files fs.FS) http.Handler {
	static := http.FileServer(http.FS(files))
	compressed := gzipAssets{files: files, entries: make(map[string][]byte)}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := &assetResponseWriter{ResponseWriter: w, cacheControl: "no-cache"}
		if r.Method != "GET" && r.Method != "HEAD" {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		info, err := fs.Stat(files, name)
		if err != nil {
			if path.Ext(name) != "" {
				http.NotFound(response, r)
				return
			}
			copy := r.Clone(r.Context())
			copy.URL.Path = "/"
			static.ServeHTTP(response, copy)
			return
		}
		if info.Mode().IsRegular() && path.Ext(name) == ".webmanifest" {
			// Do not depend on the deployment host's MIME database.
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		if info.Mode().IsRegular() && r.URL.Path == "/"+name && fingerprintedAsset.MatchString(name) {
			response.cacheControl = "private, max-age=31536000, immutable"
		}
		if info.Mode().IsRegular() && info.Size() >= 1024 && (path.Ext(name) == ".js" || path.Ext(name) == ".css") {
			w.Header().Add("Vary", "Accept-Encoding")
			// Keep FileServer's redirects and byte-range representation unchanged.
			if r.URL.Path == "/"+name && r.Header.Get("Range") == "" && acceptsGzip(r.Header.Values("Accept-Encoding")) {
				if data := compressed.get(name); len(data) != 0 {
					response.gzip = true
					http.ServeContent(response, r, path.Base(name), info.ModTime(), bytes.NewReader(data))
					return
				}
			}
		}
		static.ServeHTTP(response, r)
	})
}

// Commit representation headers only after FileServer/ServeContent succeeds.
// ServeContent calculates compressed lengths before the encoding header is set.
type assetResponseWriter struct {
	http.ResponseWriter
	cacheControl string
	gzip         bool
	wroteHeader  bool
}

func (w *assetResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if status == http.StatusOK || status == http.StatusPartialContent || status == http.StatusNotModified {
		w.Header().Set("Cache-Control", w.cacheControl)
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	if w.gzip && status == http.StatusOK {
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *assetResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

type gzipAssets struct {
	files   fs.FS
	mu      sync.Mutex
	entries map[string][]byte
}

func (assets *gzipAssets) get(name string) []byte {
	assets.mu.Lock()
	defer assets.mu.Unlock()
	if data, found := assets.entries[name]; found {
		return data
	}
	data, err := fs.ReadFile(assets.files, name)
	if err != nil {
		return nil
	}
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	_, _ = writer.Write(data)
	_ = writer.Close()
	var compressed []byte
	if output.Len() < len(data) {
		compressed = output.Bytes()
	}
	// Only existing eligible asset names reach this cache, including assets for
	// which compression has no benefit. Missing paths never create entries.
	assets.entries[name] = compressed
	return compressed
}

func acceptsGzip(values []string) bool {
	gzipQuality, wildcardQuality := -1.0, 0.0
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			parts := strings.Split(item, ";")
			encoding := strings.TrimSpace(parts[0])
			if !strings.EqualFold(encoding, "gzip") && encoding != "*" {
				continue
			}
			quality := 1.0
			for _, parameter := range parts[1:] {
				key, value, found := strings.Cut(parameter, "=")
				if strings.EqualFold(strings.TrimSpace(key), "q") {
					parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
					if !found || err != nil || !(parsed >= 0 && parsed <= 1) {
						quality = 0
					} else {
						quality = parsed
					}
				}
			}
			if encoding == "*" {
				wildcardQuality = quality
			} else {
				gzipQuality = quality
			}
		}
	}
	if gzipQuality >= 0 {
		return gzipQuality > 0
	}
	return wildcardQuality > 0
}
