package webui

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// fromFS also supports deep links without turning missing assets into HTML.
func fromFS(files fs.FS) http.Handler {
	static := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(files, name); err != nil {
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			copy := r.Clone(r.Context())
			copy.URL.Path = "/"
			static.ServeHTTP(w, copy)
			return
		}
		static.ServeHTTP(w, r)
	})
}
