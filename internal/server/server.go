package server

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/semsemyonoff/ALTO/internal/db"
	"github.com/semsemyonoff/ALTO/internal/transcode"
)

// LibraryScanner is the interface for scanning libraries into the database.
type LibraryScanner interface {
	ScanAll(ctx context.Context, libraries []db.Library) error
}

// TranscodeEngine is the interface for running transcoding jobs.
type TranscodeEngine interface {
	Transcode(ctx context.Context, job transcode.Job, progress chan<- transcode.ProgressReport) error
}

// LibraryConfig holds runtime configuration for a single library.
type LibraryConfig struct {
	ID   int64
	Name string
	Path string
}

// Config is the server configuration.
type Config struct {
	Libraries   []LibraryConfig
	OutputDir   string
	CacheDir    string
	TemplateDir string // defaults to "web/templates"
}

// ScanEvent represents a scan lifecycle event broadcast over SSE.
type ScanEvent struct {
	Type    string `json:"type"`              // "started", "complete", "error", "idle"
	Message string `json:"message,omitempty"` // error message if Type == "error"
}

// scanState manages scan lifecycle and SSE subscriptions under a single mutex.
type scanState struct {
	mu      sync.Mutex
	running bool
	subs    []chan ScanEvent
}

// start attempts to mark a scan as running. Returns false if already running.
func (ss *scanState) start() bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.running {
		return false
	}
	ss.running = true
	return true
}

// subscribe adds a new subscriber and returns (channel, isRunning).
// If isRunning is false the caller should send an idle event instead of blocking.
func (ss *scanState) subscribe() (chan ScanEvent, bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ch := make(chan ScanEvent, 16)
	if !ss.running {
		return ch, false
	}
	ss.subs = append(ss.subs, ch)
	return ch, true
}

// unsubscribe removes a subscriber (e.g. on client disconnect).
func (ss *scanState) unsubscribe(ch chan ScanEvent) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for i, sub := range ss.subs {
		if sub == ch {
			ss.subs = append(ss.subs[:i], ss.subs[i+1:]...)
			return
		}
	}
}

// reset clears the running flag without broadcasting any event.
// Use this when a scan never actually started (e.g. validation error).
func (ss *scanState) reset() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.running = false
}

// broadcast sends an event to all subscribers.
// On "complete" or "error", closes all subscriber channels and marks the scan done.
func (ss *scanState) broadcast(e ScanEvent) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for _, ch := range ss.subs {
		select {
		case ch <- e:
		default:
		}
	}
	if e.Type == "complete" || e.Type == "error" {
		for _, ch := range ss.subs {
			close(ch)
		}
		ss.subs = nil
		ss.running = false
	}
}

// Server is the ALTO HTTP server.
type Server struct {
	db       *db.DB
	scanner  LibraryScanner
	engine   TranscodeEngine
	cfg      Config
	mux      *http.ServeMux
	scan     scanState
	tmpl     templateEngine
	jobs     *jobManager
}

// New creates a new Server and registers all routes.
func New(database *db.DB, scanner LibraryScanner, cfg Config) *Server {
	return NewWithEngine(database, scanner, nil, cfg)
}

// NewWithEngine creates a new Server with an optional TranscodeEngine.
func NewWithEngine(database *db.DB, scanner LibraryScanner, engine TranscodeEngine, cfg Config) *Server {
	tmplDir := cfg.TemplateDir
	if tmplDir == "" {
		tmplDir = "web/templates"
	}
	s := &Server{
		db:      database,
		scanner: scanner,
		engine:  engine,
		cfg:     cfg,
		mux:     http.NewServeMux(),
		tmpl:    templateEngine{dir: tmplDir},
		jobs:    newJobManager(),
	}
	s.registerRoutes()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /dir", s.handleDirPage)
	s.mux.HandleFunc("GET /api/libraries", s.handleLibraries)
	s.mux.HandleFunc("GET /api/tree/{libraryID}", s.handleTree)
	s.mux.HandleFunc("GET /api/tree/{libraryID}/children", s.handleTreeChildren)
	s.mux.HandleFunc("GET /api/dir", s.handleDir)
	s.mux.HandleFunc("POST /api/scan", s.handleScan)
	s.mux.HandleFunc("GET /api/scan/status", s.handleScanStatus)
	s.mux.HandleFunc("GET /api/cover", s.handleCover)
	s.mux.HandleFunc("POST /api/transcode", s.handleTranscodeStart)
	s.mux.HandleFunc("GET /api/transcode/{jobID}/progress", s.handleTranscodeProgress)
	s.mux.HandleFunc("GET /api/transcode/{jobID}/log", s.handleTranscodeLog)

	// Static file serving (no method restriction to support GET + HEAD).
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
}

// libRoots returns the root paths of all configured libraries.
func (s *Server) libRoots() []string {
	roots := make([]string, len(s.cfg.Libraries))
	for i, l := range s.cfg.Libraries {
		roots[i] = l.Path
	}
	return roots
}

// findLibraryForPath returns the LibraryConfig whose resolved root contains resolvedPath,
// along with the relative path within that library (slash-separated, "" for root).
func (s *Server) findLibraryForPath(resolvedPath string) (LibraryConfig, string, bool) {
	for _, lib := range s.cfg.Libraries {
		resolvedRoot, err := filepath.EvalSymlinks(lib.Path)
		if err != nil {
			resolvedRoot = filepath.Clean(lib.Path)
		}
		if isWithin(resolvedPath, resolvedRoot) {
			rel, err := filepath.Rel(resolvedRoot, resolvedPath)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if rel == "." {
				rel = ""
			}
			return lib, rel, true
		}
	}
	return LibraryConfig{}, "", false
}

