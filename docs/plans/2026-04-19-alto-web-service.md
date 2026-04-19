# ALTO - Audio Library Transcode Organizer

## Overview
- Web service (Go 1.26.2) for managing audio transcoding on a home server
- Provides a directory-tree web UI for browsing mounted music libraries
- Supports transcoding to FLAC (lossless) and Opus (lossy) via ffmpeg with configurable presets
- Indexes audio files with ffprobe, stores metadata in SQLite
- Real-time transcoding progress via SSE, three output placement modes
- Packaged as a Docker container with mounted library directories configured via env vars
- UI: server-side Go templates + HTMX, design matches the teal/green gradient logo

## Context (from discovery)
- Files/components involved: new Go project from scratch, existing `.ralphex/` config, `static/logo.svg`, `.editorconfig`
- Logo color palette: teal (#3AD4C4), blue (#0FAFFF), green (#7AE072) gradients - UI should harmonize
- External tools: `ffmpeg` (transcode), `ffprobe` (metadata extraction)
- SQLite via `modernc.org/sqlite`, HTTP via `net/http` stdlib (Go 1.22+ routing)
- Dependencies identified: HTMX (CDN or vendored), SQLite driver, slog for logging

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility

## Testing Strategy
- **Unit tests**: required for every task (see Development Approach above)
- Use `testing` stdlib + table-driven tests
- Mock ffmpeg/ffprobe with interfaces for unit tests
- Integration tests for SQLite operations using in-memory DB
- HTTP handler tests via `httptest`

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with + prefix
- Document issues/blockers with ! prefix
- Update plan if implementation deviates from original scope

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): tasks achievable within this codebase
- **Post-Completion** (no checkboxes): Docker deployment, manual testing, external configs
- **Checkbox placement**: only in Task sections

## Implementation Steps

### Task 1: Project scaffolding and tooling
- [x] initialize `go.mod` with module path `github.com/semsemyonoff/ALTO` (Go 1.26.2)
- [x] create directory structure: `cmd/alto/`, `internal/library/`, `internal/transcode/`, `internal/server/`, `internal/db/`, `web/templates/`, `web/static/`
- [x] create `cmd/alto/main.go` with minimal HTTP server startup, env var parsing for all runtime config: `ALTO_LIBRARIES` (comma-separated `name:path`), `ALTO_PORT` (default 8080), `ALTO_OUTPUT_DIR` (default `/out`), `ALTO_DB_PATH` (default `./alto.db`), `ALTO_CACHE_DIR` (default `./cache`)
- [x] create `Makefile` with targets: `build`, `test`, `lint`, `run`, `docker-build`
- [x] configure `.golangci.yml` with modernize enabled, plus govet, errcheck, staticcheck, unused, gosimple, ineffassign
- [x] create `Dockerfile` (multi-stage: Go builder + Alpine runtime with ffmpeg/ffprobe)
- [x] create `docker-compose.yml` example with library volume mounts and /out mount
- [x] write test for env var parsing (success + missing vars + duplicate library names + invalid library name characters); startup logs a warning if `ALTO_OUTPUT_DIR` resolves inside a library root (valid but scanner will exclude it)
- [x] run `go test ./...` and `golangci-lint run` - must pass before next task

### Task 2: SQLite database and schema
- [x] add `modernc.org/sqlite` dependency
- [x] create `internal/db/db.go` with DB struct, `Open(path)` / `Close()`, migration on startup
- [x] define schema with relational constraints:
  - `libraries` (id INTEGER PK, name TEXT UNIQUE NOT NULL, path TEXT UNIQUE NOT NULL) — names validated on startup: must be non-empty, unique, and contain only `[a-zA-Z0-9_-]` (used as filesystem slug in shared /out layout)
  - `directories` (id INTEGER PK, library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE, path TEXT NOT NULL, has_cover BOOLEAN, cover_path TEXT, codec_summary TEXT, UNIQUE(library_id, path))
  - `tracks` (id INTEGER PK, directory_id INTEGER NOT NULL REFERENCES directories(id) ON DELETE CASCADE, filename TEXT NOT NULL, codec TEXT, bitrate INTEGER, duration REAL, sample_rate INTEGER, channels INTEGER, size INTEGER, UNIQUE(directory_id, filename))
  - enable `PRAGMA foreign_keys = ON` on every connection
- [x] configure SQLite with WAL mode and busy timeout (5s) on Open to support concurrent reads during scan writes
- [x] implement single-writer strategy: all write operations serialized through a `sync.Mutex` in the DB struct
- [x] implement `UpsertLibrary`, `UpsertDirectory`, `UpsertTrack` methods
- [x] implement `DeleteStaleFiles(directoryID, currentFilenames)` and `DeleteStaleDirectories(libraryID, currentPaths)` — removes DB rows for files/directories no longer present on disk
- [x] implement query methods: `GetLibraries`, `GetDirectoryTree(libraryID)`, `GetDirectoryChildren(parentPath)` (for lazy tree expansion), `GetDirectoryFiles(dirPath)`, `GetDirectoryByPath(path)`
- [x] write tests for all DB operations using in-memory SQLite
- [x] write tests for schema migration (fresh DB + idempotent re-run)
- [x] write tests for stale directory/file deletion (rename, remove scenarios)
- [x] write tests for concurrent read/write access (goroutine safety)
- [x] run tests - must pass before next task

### Task 3: Library scanner with ffprobe integration
- [x] create `internal/library/scanner.go` with `Scanner` struct and `Scan(ctx, libPath)` method
- [x] implement directory walking: identify audio directories (dirs containing audio files by extension: flac, opus, ogg, mp3, wav, aac, m4a, wma, alac, ape, wv); skip app-owned directories during scan — exclude `.alto-out/` (ALTO's local output dir name) and any dir matching `.alto-*` (temp, backup, cache); also exclude the resolved `ALTO_OUTPUT_DIR` path if it falls under any library root (prevents shared output from being re-indexed)
- [x] implement ffprobe wrapper (`internal/library/ffprobe.go`): extract codec, bitrate, duration, sample_rate, channels via `ffprobe -v quiet -print_format json -show_streams -show_format`
- [x] implement cover art detection: check for external files (cover.jpg/png, folder.jpg/png, front.jpg/png) and embedded art via ffprobe stream type `attached_pic`
- [x] implement embedded cover art extraction: when only embedded art exists, extract to app-managed cache dir (`$ALTO_CACHE_DIR/covers/<libraryID>/<dir-hash>.jpg`) via `ffmpeg -i input -an -vcodec mjpeg -frames:v 1 output.jpg`; store cache path in `directories.cover_path`; cache dir defaults to `./cache` and is separate from library mounts (works with read-only library mounts)
- [x] implement codec summary for directory (e.g., "FLAC" if all FLAC, "Mixed" if multiple codecs)
- [x] store scan results in DB via `internal/db` methods; after upserting current files, call `DeleteStaleFiles` and `DeleteStaleDirectories` to reconcile removed/renamed entries
- [x] support parallel scanning of multiple libraries via goroutines (each library scanned in its own goroutine, all DB writes go through the serialized writer)
- [x] log scan progress to stdout (slog)
- [x] write tests for scanner with mock ffprobe (interface-based)
- [x] write tests for ffprobe JSON parsing (various codec outputs)
- [x] write tests for audio file extension detection, external cover art detection, and embedded art extraction
- [x] write tests for stale directory/file reconciliation during rescan
- [x] write tests for scanner exclude rules: `.alto-out/`, `.alto-tmp-*`, `.alto-backup-*` dirs must be skipped; a legitimate user dir named `out/` must still be indexed; `ALTO_OUTPUT_DIR` nested under a library root must be skipped; audio files inside excluded dirs must not appear in index
- [x] run tests - must pass before next task

### Task 4: HTTP server and API endpoints
- [x] create `internal/server/server.go` with `Server` struct, router setup using `net/http` ServeMux
- [x] implement path safety module (`internal/server/safepath.go`) with two policies:
  - **library-only** (for read/source endpoints: `/api/dir`, `/api/cover`, `POST /api/transcode` source): canonicalize with `filepath.Clean` + `filepath.EvalSymlinks`, verify resolved path is within a configured library root only; additionally reject any path containing a `.alto-*` segment (`.alto-out/`, `.alto-tmp-*`, `.alto-backup-*`) — these are app-owned and must not be browsable or usable as transcode source; reject with 403 otherwise
  - **destination** (for transcode output paths): walk up to nearest existing ancestor, resolve, verify within library root or `ALTO_OUTPUT_DIR`; validate remaining segments have no `..` or symlinks
  - output dir is never a valid source/read target — prevents browsing or re-transcoding generated content
- [x] implement API endpoints:
  - `GET /api/libraries` - list all libraries
  - `GET /api/tree/{libraryID}` - full directory tree for a library (initial load)
  - `GET /api/tree/{libraryID}/children?parent=...` - children of a specific directory node (for HTMX lazy expansion); returns HTML partial of child nodes
  - `GET /api/dir?path=...` - directory details (files, metadata, cover) — path validated against library roots
  - `POST /api/scan` - trigger re-indexing (returns immediately, runs async); accepts optional `library_id` query param to rescan a single library (omit for full rescan); uses a single-scan lock — if a scan is already running, returns 409 Conflict with current scan status
  - `GET /api/scan/status` - SSE stream for current scan progress (connected to the one active scan; returns "idle" event if no scan running)
  - `GET /api/cover?path=...` - serve cover art for a library directory; path validated against library roots only (library-only policy); server resolves the actual image internally via `directories.cover_path` from DB — may serve an external file from the library dir or a cached extraction from `ALTO_CACHE_DIR`, but callers cannot target cache paths directly
- [x] implement static file serving from `web/static/` (logo, CSS, JS)
- [x] implement template rendering helpers
- [x] write tests for each API endpoint using httptest
- [x] write tests for read-path validation: traversal attacks (`../`), symlink escapes, paths outside library roots, valid paths within roots, `.alto-*` segments rejected (`.alto-out/`, `.alto-tmp-XXX/`, `.alto-backup-XXX/`)
- [x] write tests for destination-path validation: non-existent target dirs under valid roots (should pass), ancestor resolution, `..` in unresolved tail (should reject)
- [x] write tests for error cases (invalid paths, missing library, duplicate scan rejection with 409, etc.)
- [x] run tests - must pass before next task

### Task 5: Web UI - base layout and directory tree
- [x] create base HTML template (`web/templates/base.html`) with ALTO branding, logo, navigation
- [x] design CSS (`web/static/css/style.css`) matching logo palette: teal (#3AD4C4) primary, blue (#0FAFFF) accents, green (#7AE072) highlights, dark background for contrast
- [x] add HTMX (vendor `web/static/js/htmx.min.js` and `web/static/js/htmx-ext-sse.min.js` — SSE extension required for `hx-ext="sse"` used in progress/scan streaming)
- [x] create library selector component (dropdown/tabs for multiple libraries)
- [x] create directory tree component with HTMX lazy-loading (expand on click)
- [x] show codec icon/badge on audio directories in the tree (e.g., "FLAC", "Opus", "MP3", "Mixed")
- [x] implement "open in new tab" link for audio directories
- [x] create index page template (`web/templates/index.html`) combining tree + content area
- [x] write tests for template rendering (no panics, correct HTML structure)
- [x] run tests - must pass before next task

### Task 6: Web UI - audio directory page
- [x] create audio directory template (`web/templates/directory.html`)
- [x] display cover art if available (served via `/api/cover?path=...`)
- [x] display file list table: filename, codec, bitrate, duration, sample rate, channels, file size
- [x] style the page to match ALTO design (teal/green palette, clean layout)
- [x] implement HTMX partial loading so clicking a directory in the tree loads its page in the content area
- [x] write tests for directory page rendering with various file combinations
- [x] run tests - must pass before next task

### Task 7: Transcoding engine with ffmpeg
- [x] create `internal/transcode/engine.go` with `Engine` struct and `Transcode(ctx, job)` method
- [x] define `Job` struct: source dir, target codec, preset/custom params, output mode, list of files
- [x] define preset configurations:
  - FLAC: Fast (compression 0, verify on), Balanced (compression 5, verify on), Max (compression 8, verify on)
  - Opus: Music Balanced (128k), Music High (160k, default), Archive Lossy (192k) - all with vbr on, application audio, compression_level 10
- [x] implement ffmpeg command builder for FLAC: `ffmpeg -i input -c:a flac -compression_level N [-map_metadata 0] [-c:v copy] output.flac`
- [x] implement ffmpeg command builder for Opus: `ffmpeg -i input -c:a libopus -b:a Nk -vbr on -compression_level 10 -application audio [-map_metadata 0] [-c:v copy] output.opus`
- [x] implement three output modes:
  1. Shared /out: mirror directory structure under a library namespace — `<ALTO_OUTPUT_DIR>/<library-name>/<relative-path>/` — to prevent collisions when multiple libraries contain the same relative path; copy non-audio files after conversion
  2. Local out: create `.alto-out/` subdirectory in source audio directory (prefixed to avoid collision with user content and to match scanner exclusion rules)
  3. Replace: atomic per-file replacement with rollback semantics:
     - temp directory created on the **same filesystem** as originals (e.g., `<dir>/.alto-tmp-<jobID>/`)
     - each file: transcode to temp dir -> verify output (ffprobe) -> `os.Rename` original to `<dir>/.alto-backup-<jobID>/<file>` -> `os.Rename` temp to original name
     - on success of all files: remove backup dir
     - on any failure (mid-job error, disk full, context cancel): stop processing, restore all already-replaced files from backup dir, remove temp dir, log what was restored
     - disk space pre-check: estimate output size from input duration * target bitrate, compare with available space via `syscall.Statfs`, warn if < 2x estimated size
- [x] validate all output paths against allowed roots using a destination-safe variant of safepath: instead of `EvalSymlinks` on the full (possibly non-existent) path, walk up to the nearest existing ancestor, resolve that, and verify the resolved ancestor is within a library root or output dir; then validate the remaining unresolved segments contain no `..` or symlink escapes
- [x] implement progress tracking: parse ffmpeg stderr for `time=` progress, calculate percentage from known duration
- [x] implement `Progress` channel/callback reporting: current file, file index, total files, percentage per file
- [x] log all ffmpeg output to stdout (slog)
- [x] write tests for ffmpeg command building (verify correct arguments for each preset and codec)
- [x] write tests for output path calculation (all three modes)
- [x] write tests for replace mode rollback: simulate mid-job failure, verify originals are restored from backup
- [x] write tests for replace mode disk space pre-check
- [x] write tests for progress parsing from ffmpeg stderr
- [x] write tests for non-audio file copying logic
- [x] run tests - must pass before next task

### Task 8: Transcoding API and real-time progress
- [ ] add API endpoints:
  - `POST /api/transcode` - start transcoding job (codec, preset/custom params, output mode, directory path); source directory path canonicalized and validated against library roots via read-path safepath (reject with 403 if outside roots)
  - `GET /api/transcode/{jobID}/progress` - SSE stream for real-time progress
  - `GET /api/transcode/{jobID}/log` - tail log endpoint (returns last N lines from ring buffer; documented as a rolling tail, not a full history)
- [ ] implement job management: track active jobs, prevent duplicate jobs on same directory
- [ ] implement SSE progress streaming: current file name, file progress %, overall progress %, status
- [ ] implement log storage: in-memory ring buffer (configurable size, default 1000 lines) for the tail API; full log always written to container stdout via slog regardless of buffer eviction
- [ ] write tests for transcode API (start, progress polling, completion)
- [ ] write tests for transcode source path validation (traversal, outside library roots -> 403, `.alto-*` source paths -> 403)
- [ ] write tests for SSE event formatting
- [ ] write tests for job deduplication
- [ ] run tests - must pass before next task

### Task 9: Web UI - transcoding interface
- [ ] add "Transcode" button to audio directory page
- [ ] create transcoding modal/panel with HTMX:
  1. Codec selector (FLAC / Opus)
  2. Preset selector (changes based on codec, default preset pre-selected)
  3. "Custom" option revealing: bitrate/compression level, metadata flag (on), cover flag (on)
  4. Advanced section (collapsed by default) for additional ffmpeg params
  5. Output mode selector (Shared /out default, Local out, Replace with warning)
- [ ] implement "Start" button that POSTs to `/api/transcode` via HTMX
- [ ] create progress display component:
  - progress bar (overall)
  - current file name and per-file progress
  - collapsible full log viewer (default collapsed)
- [ ] connect progress display to SSE endpoint via HTMX `hx-ext="sse"`
- [ ] show replace mode warning dialog before starting
- [ ] write tests for transcoding form rendering
- [ ] run tests - must pass before next task

### Task 10: Re-indexing UI and scan integration
- [ ] add "Re-index" button to the UI (header or library selector area)
- [ ] implement scan trigger via HTMX POST to `/api/scan`
- [ ] show scan progress indicator (spinner/progress) connected to `/api/scan/status` SSE
- [ ] after scan completes, refresh the directory tree via HTMX
- [ ] after transcoding completes to local/shared output, offer to re-index the affected library via `POST /api/scan?library_id=N` (library-scoped rescan)
- [ ] write tests for scan trigger and status endpoint
- [ ] run tests - must pass before next task

### Task 11: Verify acceptance criteria
- [ ] verify all requirements from Overview are implemented
- [ ] verify directory tree displays correctly with multiple libraries
- [ ] verify codec icons appear on directory tree nodes
- [ ] verify cover art displays on directory pages
- [ ] verify all FLAC presets produce correct ffmpeg commands
- [ ] verify all Opus presets produce correct ffmpeg commands
- [ ] verify all three output modes work correctly
- [ ] verify real-time progress updates during transcoding
- [ ] verify log output goes to container stdout
- [ ] run full test suite (`go test ./...`)
- [ ] run linter (`golangci-lint run`) - all issues must be fixed

### Task 12: [Final] Update documentation
- [ ] create README.md with project description, screenshot placeholder, usage instructions
- [ ] document env vars: `ALTO_LIBRARIES`, `ALTO_PORT`, `ALTO_OUTPUT_DIR`, `ALTO_DB_PATH`, `ALTO_CACHE_DIR`
- [ ] document Docker usage with docker-compose example
- [ ] document available presets and custom parameters

## Technical Details

### Directory Structure
```
cmd/alto/main.go          - entrypoint, env parsing, server startup
internal/
  db/db.go                - SQLite operations, schema, migrations
  library/
    scanner.go            - directory walking, file discovery
    ffprobe.go            - ffprobe wrapper, metadata extraction
  transcode/
    engine.go             - ffmpeg wrapper, transcoding logic
    presets.go            - codec presets definitions
    progress.go           - ffmpeg progress parsing
  server/
    server.go             - HTTP server, router, middleware
    safepath.go           - path canonicalization + library root validation
    handlers_api.go       - API endpoint handlers
    handlers_pages.go     - page rendering handlers
web/
  templates/
    base.html             - base layout with nav, logo, HTMX
    index.html            - main page with tree + content
    directory.html        - audio directory detail page
    partials/             - HTMX partial templates (tree node, progress, etc.)
  static/
    css/style.css         - main stylesheet (teal/green theme)
    js/htmx.min.js        - HTMX core (vendored)
    js/htmx-ext-sse.min.js - HTMX SSE extension (vendored)
static/logo.svg           - ALTO logo (existing)
Dockerfile                - multi-stage build
docker-compose.yml        - example compose config
.golangci.yml             - linter config
```

### Environment Variables
| Variable | Default | Description |
|---|---|---|
| `ALTO_LIBRARIES` | (required) | Comma-separated `name:path` pairs, e.g. `Music:/music,Lossless:/lossless` |
| `ALTO_PORT` | `8080` | HTTP server port |
| `ALTO_OUTPUT_DIR` | `/out` | Shared output directory for transcoded files |
| `ALTO_DB_PATH` | `./alto.db` | SQLite database file path |
| `ALTO_CACHE_DIR` | `./cache` | App-managed cache (extracted cover art, etc.) — separate from library mounts |

### Transcoding Presets

**FLAC** (all presets: verify on, copy metadata, copy cover art):
| Preset | Compression Level |
|---|---|
| Fast | 0 |
| Balanced (default) | 5 |
| Max Compression | 8 |

**Opus** (all presets: vbr on, application audio, compression_level 10, source channels):
| Preset | Bitrate |
|---|---|
| Music Balanced | 128k |
| Music High (default) | 160k |
| Archive Lossy | 192k |

### Output Modes
1. **Shared /out** (default): mirrors library path structure under library namespace — `<ALTO_OUTPUT_DIR>/<library-name>/<relative-path>/` — copies non-audio files
2. **Local out**: creates `.alto-out/` subdirectory in the audio directory (app-namespaced to avoid hiding user content)
3. **Replace**: atomic per-file replacement with rollback — temp and backup dirs on same filesystem, auto-restore on failure, disk space pre-check; requires user confirmation warning

### Path Security
Two safepath policies:
1. **Library-only** (read/source endpoints: `/api/dir`, `/api/cover`, `POST /api/transcode` source): `filepath.Clean` + `filepath.EvalSymlinks` + prefix check against library roots only. Output dir is never valid here. Additionally rejects any path with `.alto-*` segments (app-owned dirs). Prevents browsing or re-transcoding generated/temp content. Rejects with 403.
2. **Destination** (transcode output paths): walk up to nearest existing ancestor, resolve, verify within library root or `ALTO_OUTPUT_DIR`, validate remaining segments have no `..` or symlinks. Allows creating new directories under valid roots.

### SQLite Concurrency
WAL mode enabled, busy timeout 5s, all writes serialized via `sync.Mutex`. Supports concurrent HTTP reads during background scan writes.

### Data Flow
1. Startup: parse env vars -> open/migrate SQLite DB -> initial scan of all libraries -> start HTTP server
2. Browse: tree loads lazily via HTMX -> click directory -> load file list from DB -> display with cover
3. Transcode: select codec + preset + output mode -> POST job -> engine runs ffmpeg per file -> SSE progress -> completion
4. Re-index: POST scan -> parallel goroutines walk libraries -> ffprobe each file -> upsert DB -> SSE status -> refresh tree

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Manual verification:**
- Test with real music library mounted via Docker
- Verify ffmpeg transcoding quality with various input formats
- Test with large libraries (1000+ directories) for performance
- Verify progress bar accuracy with long files
- Test replace mode safety (backup recommendation)

**Deployment:**
- Push Docker image to registry
- Configure docker-compose on home server with actual library mount points
- Set up volume for /out directory
- Verify ffmpeg/ffprobe versions in container
