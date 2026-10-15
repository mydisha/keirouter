package gateway

import (
	"bytes"
	"compress/gzip"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// staticGzipMinSize skips compressing tiny files where the gzip framing would
// outweigh the savings.
const staticGzipMinSize = 1024

// staticHandler serves the dashboard SPA build. Vite emits content-hashed
// files under /assets, so those are cached as immutable; index.html is always
// revalidated so a new deploy is picked up on the next load. Text assets are
// gzipped once and kept in memory (the build is a few MB at most), so repeat
// requests cost a map lookup instead of a compression pass.
type staticHandler struct {
	dir string
	gz  sync.Map // clean URL path -> *staticGzEntry
}

type staticGzEntry struct {
	mod  time.Time
	size int64
	data []byte
}

func newStaticHandler(dir string) *staticHandler {
	return &staticHandler{dir: dir}
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upath := path.Clean("/" + r.URL.Path)
	if upath == "/" {
		upath = "/index.html"
	}
	full := filepath.Join(h.dir, filepath.FromSlash(upath))
	fi, err := os.Stat(full)
	if err != nil || fi.IsDir() {
		// A missing hashed asset means a stale page is asking for a chunk from
		// an older build; answering with index.html would only surface as a
		// MIME error, so 404 it. Everything else is a client-side route.
		if strings.HasPrefix(upath, "/assets/") {
			http.NotFound(w, r)
			return
		}
		upath = "/index.html"
		full = filepath.Join(h.dir, "index.html")
		if fi, err = os.Stat(full); err != nil {
			http.NotFound(w, r)
			return
		}
	}

	hdr := w.Header()
	switch {
	case strings.HasPrefix(upath, "/assets/"):
		hdr.Set("Cache-Control", "public, max-age=31536000, immutable")
	case upath == "/index.html":
		hdr.Set("Cache-Control", "no-cache")
	default:
		hdr.Set("Cache-Control", "public, max-age=3600")
	}

	ext := path.Ext(upath)
	if ct := mime.TypeByExtension(ext); ct != "" {
		hdr.Set("Content-Type", ct)
	}

	if staticCompressible(ext) && fi.Size() >= staticGzipMinSize {
		hdr.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r.Header.Get("Accept-Encoding")) {
			if data, ok := h.gzipped(upath, full, fi); ok {
				hdr.Set("Content-Encoding", "gzip")
				http.ServeContent(w, r, upath, fi.ModTime(), bytes.NewReader(data))
				return
			}
		}
	}

	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	http.ServeContent(w, r, upath, fi.ModTime(), f)
}

// gzipped returns the cached gzip body for upath, compressing it on first use
// or when the file on disk changed since it was cached.
func (h *staticHandler) gzipped(upath, full string, fi os.FileInfo) ([]byte, bool) {
	if v, ok := h.gz.Load(upath); ok {
		e := v.(*staticGzEntry)
		if e.mod.Equal(fi.ModTime()) && e.size == fi.Size() {
			return e.data, true
		}
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, false
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, false
	}
	if _, err := zw.Write(raw); err != nil {
		return nil, false
	}
	if err := zw.Close(); err != nil {
		return nil, false
	}
	data := buf.Bytes()
	h.gz.Store(upath, &staticGzEntry{mod: fi.ModTime(), size: fi.Size(), data: data})
	return data, true
}

func staticCompressible(ext string) bool {
	switch strings.ToLower(ext) {
	case ".js", ".mjs", ".css", ".html", ".svg", ".json", ".map", ".txt", ".webmanifest", ".xml":
		return true
	}
	return false
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip (or *),
// honouring an explicit q=0 opt-out.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		coding = strings.ToLower(strings.TrimSpace(coding))
		if coding != "gzip" && coding != "*" {
			continue
		}
		q := strings.ReplaceAll(strings.ToLower(params), " ", "")
		if q == "q=0" || q == "q=0.0" || q == "q=0.00" || q == "q=0.000" {
			return false
		}
		return true
	}
	return false
}
