package server

import (
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
)

// templateEngine loads and caches HTML templates from a directory.
// Templates are loaded lazily on first render.
type templateEngine struct {
	mu   sync.RWMutex
	dir  string
	tmpl *template.Template
}

// render executes the named template with data, writing to w.
// If templates have not been loaded yet, it attempts to load them first.
func (te *templateEngine) render(w http.ResponseWriter, name string, data any) {
	te.mu.RLock()
	tmpl := te.tmpl
	te.mu.RUnlock()

	if tmpl == nil {
		te.mu.Lock()
		// Double-checked load.
		if te.tmpl == nil {
			pattern := filepath.Join(te.dir, "*.html")
			t, err := template.ParseGlob(pattern)
			if err != nil {
				te.mu.Unlock()
				slog.Warn("template load failed", "dir", te.dir, "err", err)
				http.Error(w, "templates not available", http.StatusInternalServerError)
				return
			}
			te.tmpl = t
		}
		tmpl = te.tmpl
		te.mu.Unlock()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("template execute", "name", name, "err", err)
	}
}

