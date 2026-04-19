package transcode

// Codec identifies the target audio codec.
type Codec string

const (
	CodecFLAC Codec = "flac"
	CodecOpus Codec = "opus"
)

// OutputMode determines where transcoded files are placed.
type OutputMode string

const (
	// OutputShared mirrors the library path structure under ALTO_OUTPUT_DIR.
	OutputShared OutputMode = "shared"
	// OutputLocal creates a .alto-out/ subdirectory inside the source directory.
	OutputLocal OutputMode = "local"
	// OutputReplace performs atomic in-place file replacement with rollback.
	OutputReplace OutputMode = "replace"
)

// Preset defines codec parameters for a transcoding operation.
type Preset struct {
	Name             string
	Codec            Codec
	CompressionLevel int    // FLAC: 0–8; Opus: always 10
	Bitrate          string // Opus only, e.g. "128k"
	CopyMetadata     bool
	CopyCover        bool
}

// FLAC presets — all have verify semantics and copy metadata/cover by default.
var (
	FLACFast     = Preset{Name: "Fast", Codec: CodecFLAC, CompressionLevel: 0, CopyMetadata: true, CopyCover: true}
	FLACBalanced = Preset{Name: "Balanced", Codec: CodecFLAC, CompressionLevel: 5, CopyMetadata: true, CopyCover: true}
	FLACMax      = Preset{Name: "Max Compression", Codec: CodecFLAC, CompressionLevel: 8, CopyMetadata: true, CopyCover: true}
)

// Opus presets — all use vbr, application audio, compression_level 10.
var (
	OpusMusicBalanced = Preset{Name: "Music Balanced", Codec: CodecOpus, CompressionLevel: 10, Bitrate: "128k", CopyMetadata: true, CopyCover: true}
	OpusMusicHigh     = Preset{Name: "Music High", Codec: CodecOpus, CompressionLevel: 10, Bitrate: "160k", CopyMetadata: true, CopyCover: true}
	OpusArchiveLossy  = Preset{Name: "Archive Lossy", Codec: CodecOpus, CompressionLevel: 10, Bitrate: "192k", CopyMetadata: true, CopyCover: true}
)

// DefaultPresets returns all built-in presets in display order.
func DefaultPresets() []Preset {
	return []Preset{
		FLACFast, FLACBalanced, FLACMax,
		OpusMusicBalanced, OpusMusicHigh, OpusArchiveLossy,
	}
}
