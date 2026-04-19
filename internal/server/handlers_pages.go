package server

import (
	"bytes"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/semsemyonoff/ALTO/internal/db"
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

// --- Tree node rendering ---

// TreeNodeData holds pre-computed data for rendering a single directory tree node.
type TreeNodeData struct {
	LibraryID    int64
	Path         string // relative path within library (e.g. "Jazz/Miles Davis")
	PathEncoded  string // URL-encoded path for use in query params
	AbsPath      string // absolute filesystem path
	AbsEncoded   string // URL-encoded absolute path
	Basename     string // last path segment for display (e.g. "Miles Davis")
	HasCover     bool
	CodecSummary string
	CodecClass   string // CSS class for the codec badge
}

// codecClass maps a codec summary string to its CSS modifier class.
func codecClass(summary string) string {
	switch strings.ToUpper(summary) {
	case "FLAC":
		return "codec-flac"
	case "OPUS":
		return "codec-opus"
	case "MP3":
		return "codec-mp3"
	case "MIXED":
		return "codec-mixed"
	default:
		if summary == "" {
			return ""
		}
		return "codec-other"
	}
}

// buildTreeNodeData converts a db.Directory to a TreeNodeData for template rendering.
func buildTreeNodeData(lib LibraryConfig, dir db.Directory) TreeNodeData {
	absPath := filepath.Join(lib.Path, filepath.FromSlash(dir.Path))
	return TreeNodeData{
		LibraryID:    dir.LibraryID,
		Path:         dir.Path,
		PathEncoded:  url.QueryEscape(dir.Path),
		AbsPath:      absPath,
		AbsEncoded:   url.QueryEscape(absPath),
		Basename:     filepath.Base(dir.Path),
		HasCover:     dir.HasCover,
		CodecSummary: dir.CodecSummary,
		CodecClass:   codecClass(dir.CodecSummary),
	}
}

// treeNodeTmpl is the inline template for rendering a single tree node.
// Defined once at package init so tests and the API handler share the same template
// without requiring template files on disk.
var treeNodeTmpl = template.Must(template.New("tree_node").Parse(`<div class="tree-node" data-path="{{.Path}}">
  <div class="tree-node-row"
       hx-get="/api/tree/{{.LibraryID}}/children?parent={{.PathEncoded}}"
       hx-target="next .tree-children"
       hx-swap="innerHTML"
       hx-trigger="click"
       onclick="this.classList.toggle('expanded')"
       title="{{.Path}}">
    <span class="tree-toggle">▶</span>
    <span class="tree-icon">{{if .HasCover}}🎵{{else}}📁{{end}}</span>
    <span class="tree-label">{{.Basename}}</span>
    {{if .CodecSummary}}<span class="codec-badge {{.CodecClass}}">{{.CodecSummary}}</span>{{end}}
    <span class="tree-actions">
      <a class="tree-open-link"
         href="/dir?path={{.AbsEncoded}}"
         target="_blank"
         rel="noopener"
         onclick="event.stopPropagation()"
         title="Open in new tab">↗</a>
    </span>
  </div>
  <div class="tree-children"></div>
</div>
`))

// renderTreeNodes renders a slice of directories as HTML tree node fragments.
// The result is safe to embed as template.HTML.
func renderTreeNodes(nodes []TreeNodeData) (template.HTML, error) {
	var buf bytes.Buffer
	for _, nd := range nodes {
		if err := treeNodeTmpl.Execute(&buf, nd); err != nil {
			return "", err
		}
	}
	return template.HTML(buf.String()), nil
}

// findLibConfigByID returns the LibraryConfig matching id, or zero value + false.
func findLibConfigByID(cfg Config, id int64) (LibraryConfig, bool) {
	for _, l := range cfg.Libraries {
		if l.ID == id {
			return l, true
		}
	}
	return LibraryConfig{}, false
}

// --- Index page ---

// indexPageData is the template data for the index page.
type indexPageData struct {
	Libraries   []db.Library
	SelectedID  int64
	TopDirsHTML template.HTML // pre-rendered tree node HTML
}

// handleIndex renders the main application page.
// GET /{$}
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	libs, err := s.db.GetLibraries()
	if err != nil {
		slog.Error("handleIndex: GetLibraries", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := indexPageData{
		Libraries: libs,
	}

	if len(libs) > 0 {
		selected := libs[0]
		data.SelectedID = selected.ID

		libCfg, ok := findLibConfigByID(s.cfg, selected.ID)
		if ok {
			dirs, err := s.db.GetDirectoryChildren(selected.ID, "")
			if err != nil {
				slog.Error("handleIndex: GetDirectoryChildren", "err", err)
			} else {
				nodes := make([]TreeNodeData, len(dirs))
				for i, d := range dirs {
					nodes[i] = buildTreeNodeData(libCfg, d)
				}
				rendered, err := renderTreeNodes(nodes)
				if err != nil {
					slog.Error("handleIndex: renderTreeNodes", "err", err)
				} else {
					data.TopDirsHTML = rendered
				}
			}
		}
	}

	s.tmpl.render(w, "index.html", data)
}
