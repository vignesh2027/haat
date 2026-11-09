package web

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// gzipStatic serves text assets (scripts, styles, SVG) gzipped to browsers
// that accept it, compressing each file once. The vendored Three.js build
// drops from about 670 KB to under 200 KB this way. Anything else falls
// through to next.
func gzipStatic(files fs.FS, next http.Handler) http.Handler {
	var cache sync.Map // file name to its gzipped bytes
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		ext := path.Ext(name)
		if ext != ".js" && ext != ".css" && ext != ".svg" {
			next.ServeHTTP(w, r)
			return
		}
		// Caches must keep the plain and the gzipped copy apart.
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		var body []byte
		if v, ok := cache.Load(name); ok {
			body = v.([]byte)
		} else {
			raw, err := fs.ReadFile(files, name)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			var b bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
			_, _ = zw.Write(raw)
			_ = zw.Close()
			body = b.Bytes()
			cache.Store(name, body)
		}
		h := w.Header()
		h.Set("Content-Type", mime.TypeByExtension(ext))
		h.Set("Content-Encoding", "gzip")
		h.Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(enc), "gzip") {
			continue
		}
		q := 1.0
		if k, v, ok := strings.Cut(strings.ReplaceAll(params, " ", ""), "="); ok && strings.EqualFold(k, "q") {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				q = f
			}
		}
		return q > 0
	}
	return false
}
