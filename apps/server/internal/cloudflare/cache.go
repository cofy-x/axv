package cloudflare

import (
	"bufio"
	"net"
	"net/http"
	"strings"
)

// PreventCaching applies no-store to sensitive origin responses. The
// returned finalizer must run before the handler returns, including when an
// application only changes headers and lets net/http write an empty response.
// Applications cannot replace this policy in WriteHeader, Write or Flush.
// Cloudflare cache rules must also respect these origin cache-control headers.
func PreventCaching(w http.ResponseWriter) (http.ResponseWriter, func()) {
	protected := &noStoreWriter{ResponseWriter: w}
	protected.protect()
	return protected, protected.protect
}

type noStoreWriter struct{ http.ResponseWriter }

func (w *noStoreWriter) protect() {
	header := w.ResponseWriter.Header()
	for name := range header {
		switch strings.ToLower(name) {
		case "cache-control", "cdn-cache-control", "cloudflare-cdn-cache-control", "surrogate-control", "pragma", "expires":
			delete(header, name)
		}
	}
	header.Set("Cache-Control", "no-store")
	header.Set("CDN-Cache-Control", "no-store")
	header.Set("Cloudflare-CDN-Cache-Control", "no-store")
	header.Set("Surrogate-Control", "no-store")
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "Thu, 01 Jan 1970 00:00:00 GMT")
}

func (w *noStoreWriter) Header() http.Header {
	// ReverseProxy copies upgrade headers after Hijack and then obtains Header
	// again to write its raw 101 response. Protect that access as well as the
	// ordinary WriteHeader/Write/Flush commit paths.
	w.protect()
	return w.ResponseWriter.Header()
}

func (w *noStoreWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *noStoreWriter) WriteHeader(status int) {
	w.protect()
	w.ResponseWriter.WriteHeader(status)
}

func (w *noStoreWriter) Write(data []byte) (int, error) {
	w.protect()
	return w.ResponseWriter.Write(data)
}

func (w *noStoreWriter) Flush() { _ = w.FlushError() }

func (w *noStoreWriter) FlushError() error {
	w.protect()
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *noStoreWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.protect()
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
