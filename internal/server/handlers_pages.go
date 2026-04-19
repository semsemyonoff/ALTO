package server

import (
	"bytes"
	"fmt"
	"html/template"
	"log/slog"
	"math"
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
//
// Interaction model:
//   - Clicking the label: loads directory details into #content-area via HTMX hx-select.
//     event.stopPropagation() prevents the row's expand trigger from also firing.
//   - Clicking anywhere else on the row (toggle, icon, codec badge):
//     expands/collapses children and loads child nodes into .tree-children.
var treeNodeTmpl = template.Must(template.New("tree_node").Parse(`<div class="tree-node" data-path="{{.Path}}">
  <div class="tree-node-row"
       hx-get="/api/tree/{{.LibraryID}}/children?parent={{.PathEncoded}}"
       hx-target="next .tree-children"
       hx-swap="innerHTML"
       hx-trigger="click[!event.target.closest('.tree-label,.tree-actions')]"
       onclick="if(!event.target.closest('.tree-label,.tree-actions'))this.classList.toggle('expanded')"
       title="{{.Path}}">
    <span class="tree-toggle">▶</span>
    <span class="tree-icon">{{if .HasCover}}🎵{{else}}📁{{end}}</span>
    <span class="tree-label"
          hx-get="/dir?path={{.AbsEncoded}}"
          hx-target="#content-area"
          hx-select="#dir-content"
          hx-swap="innerHTML"
          hx-push-url="true"
          onclick="event.stopPropagation(); document.querySelectorAll('.tree-node-row').forEach(function(el){el.classList.remove('active')}); this.closest('.tree-node-row').classList.add('active')">{{.Basename}}</span>
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

// --- Directory page ---

// trackRow holds pre-formatted track data for the directory page template.
type trackRow struct {
	Index      int
	Filename   string
	Codec      string
	Bitrate    string
	Duration   string
	SampleRate string
	Channels   int64
	Size       string
}

// dirPageData is the template data for the audio directory detail page.
type dirPageData struct {
	Path         string // absolute resolved path (for cover URL)
	PathEncoded  string // URL-encoded path
	LibraryName  string
	DirName      string
	HasCover     bool
	CodecSummary string
	CodecClass   string
	TrackCount   int
	Tracks       []trackRow
}

// fmtBitrate formats a bitrate in bits/sec to a human-readable string (e.g. "320 kbps").
func fmtBitrate(bps int64) string {
	if bps <= 0 {
		return "–"
	}
	kbps := bps / 1000
	if kbps > 0 {
		return fmt.Sprintf("%d kbps", kbps)
	}
	return fmt.Sprintf("%d bps", bps)
}

// fmtDuration formats a duration in seconds to m:ss or h:mm:ss.
func fmtDuration(secs float64) string {
	if secs <= 0 {
		return "–"
	}
	total := int(math.Round(secs))
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// fmtSampleRate formats a sample rate in Hz to a human-readable string (e.g. "44.1 kHz").
func fmtSampleRate(hz int64) string {
	if hz <= 0 {
		return "–"
	}
	khz := float64(hz) / 1000.0
	if khz == float64(int(khz)) {
		return fmt.Sprintf("%d kHz", int(khz))
	}
	return fmt.Sprintf("%.1f kHz", khz)
}

// fmtSize formats a byte count to a human-readable string (e.g. "25.3 MB").
func fmtSize(bytes int64) string {
	if bytes <= 0 {
		return "–"
	}
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// buildDirPageData constructs dirPageData for the template.
func buildDirPageData(lib LibraryConfig, dir *db.Directory, tracks []db.Track, resolvedPath string) dirPageData {
	rows := make([]trackRow, len(tracks))
	for i, t := range tracks {
		rows[i] = trackRow{
			Index:      i + 1,
			Filename:   t.Filename,
			Codec:      t.Codec,
			Bitrate:    fmtBitrate(t.Bitrate),
			Duration:   fmtDuration(t.Duration),
			SampleRate: fmtSampleRate(t.SampleRate),
			Channels:   t.Channels,
			Size:       fmtSize(t.Size),
		}
	}
	return dirPageData{
		Path:         resolvedPath,
		PathEncoded:  url.QueryEscape(resolvedPath),
		LibraryName:  lib.Name,
		DirName:      filepath.Base(resolvedPath),
		HasCover:     dir.HasCover,
		CodecSummary: dir.CodecSummary,
		CodecClass:   codecClass(dir.CodecSummary),
		TrackCount:   len(tracks),
		Tracks:       rows,
	}
}

// handleDirPage renders the audio directory detail page.
// GET /dir?path=ABSOLUTE_PATH
func (s *Server) handleDirPage(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}

	resolved, err := LibraryOnlyValidate(path, s.libRoots())
	if err != nil {
		WritePathError(w, err)
		return
	}

	lib, rel, ok := s.findLibraryForPath(resolved)
	if !ok {
		http.Error(w, "library not found for path", http.StatusNotFound)
		return
	}

	dir, err := s.db.GetDirectoryByPath(lib.ID, rel)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if dir == nil {
		http.Error(w, "directory not found", http.StatusNotFound)
		return
	}

	tracks, err := s.db.GetDirectoryFiles(dir.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := buildDirPageData(lib, dir, tracks, resolved)
	s.tmpl.render(w, "directory.html", data)
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
