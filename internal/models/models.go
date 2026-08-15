// Package models defines the shared data types passed between Gamarr
// subsystems: search results, requests, notifications, and related structs.
package models

// SearchResult represents a search result from any source.
type SearchResult struct {
	Title            string   `json:"title"`
	Size             int64    `json:"size"`
	SizeHuman        string   `json:"size_human"`
	Seeders          int      `json:"seeders"`
	Leechers         int      `json:"leechers"`
	Indexer          string   `json:"indexer"`
	DownloadURL      string   `json:"download_url"`
	MagnetURL        string   `json:"magnet_url"`
	InfoHash         string   `json:"info_hash"`
	GUID             string   `json:"guid"`
	Platform         string   `json:"platform"`
	PlatformSlug     string   `json:"platform_slug"`
	IsPC             bool     `json:"is_pc"`
	Age              int      `json:"age"`
	SourceType       string   `json:"source_type"`                 // acquisition path: "indexer", "ddl", "scan", "import", or "csv"
	DownloadProtocol string   `json:"download_protocol,omitempty"` // "torrent" or "nzb"
	SafetyScore      int      `json:"safety_score"`
	SafetyWarnings   []string `json:"safety_warnings"`
	VimmID           string   `json:"vimm_id,omitempty"`

	// Per-file content hashes, populated by native drivers that expose them
	// (archive.org). These carry verifiable file identity downstream to the
	// normalize/convert hand-off (F5) so it can validate before converting.
	MD5  string `json:"md5,omitempty"`
	SHA1 string `json:"sha1,omitempty"`

	// Library duplicate detection
	InLibrary bool `json:"in_library"`

	// Scoring fields (populated by scorer).
	Score          int             `json:"score"`
	ScoreBreakdown *ScoreBreakdown `json:"score_breakdown,omitempty"`

	// Attrs carries the release-name attributes parsed from Title
	// (No-Intro/Redump naming: region, disc, revision, proto/BIOS flags).
	// Populated by the selection package; nil when unparsed.
	Attrs *ReleaseAttrs `json:"attrs,omitempty"`
}

// ReleaseAttrs are the structured attributes a No-Intro/Redump-style release
// name encodes in its parenthetical and bracket tags. The selection engine
// filters and ranks on these instead of re-deriving them from raw titles.
type ReleaseAttrs struct {
	CleanTitle   string   `json:"clean_title,omitempty"` // tags + extension stripped; game identity
	SetKey       string   `json:"set_key,omitempty"`     // title minus disc token + extension, lowered — disc-set identity
	Regions      []string `json:"regions,omitempty"`     // lowered: "usa", "world", "europe", ...
	Languages    []string `json:"languages,omitempty"`   // from (En,Fr,De)
	Revision     int      `json:"revision,omitempty"`    // (Rev N); 0 = base release
	DiscIndex    int      `json:"disc_index,omitempty"`  // (Disc N); 0 = not a disc member
	DiscTotal    int      `json:"disc_total,omitempty"`  // (Disc N of M); 0 = unknown
	IsProto      bool     `json:"is_proto,omitempty"`    // (Proto)/(Beta)/(Sample)
	IsDemo       bool     `json:"is_demo,omitempty"`
	IsBIOS       bool     `json:"is_bios,omitempty"`       // [BIOS] tag or prefix
	IsUnlicensed bool     `json:"is_unl,omitempty"`        // (Unl)
	BadDump      bool     `json:"bad_dump,omitempty"`      // [b]
	VerifiedDump bool     `json:"verified_dump,omitempty"` // [!]
	FormatHint   string   `json:"format_hint,omitempty"`   // "chd","cue","iso","gdi","zip","7z","raw"
}

// ScoreBreakdown provides a detailed breakdown of a search result's confidence score.
type ScoreBreakdown struct {
	TitleMatch    int    `json:"title_match"`    // 0-40
	PlatformMatch int    `json:"platform_match"` // 0-15
	SeederScore   int    `json:"seeder_score"`   // 0-15
	SizeScore     int    `json:"size_score"`     // 0-15
	SafetyScore   int    `json:"safety_score"`   // 0-15
	ProfileAdjust int    `json:"profile_adjust"` // release-profile preferred-word adjustment
	Total         int    `json:"total"`          // 0-100 (clamped; always == SearchResult.Score)
	Confidence    string `json:"confidence"`     // "high", "medium", "low"
}

// Job represents a download job.
type Job struct {
	Status       string `json:"status"`
	Title        string `json:"title"`
	Platform     string `json:"platform"`
	PlatformSlug string `json:"platform_slug"`
	IsPC         bool   `json:"is_pc"`
	Error        string `json:"error,omitempty"`
	Detail       string `json:"detail,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
}

// DownloadEntry represents an entry in the downloads list (merged job + torrent info).
type DownloadEntry struct {
	Type     string  `json:"type"` // "job" or "torrent"
	Title    string  `json:"title"`
	Platform string  `json:"platform,omitempty"`
	Status   string  `json:"status"`
	JobID    string  `json:"job_id,omitempty"`
	Error    string  `json:"error,omitempty"`
	Detail   string  `json:"detail,omitempty"`
	Progress float64 `json:"progress,omitempty"`
	Size     string  `json:"size,omitempty"`
	Speed    string  `json:"speed,omitempty"`
	ETA      int     `json:"eta,omitempty"`
	Hash     string  `json:"hash,omitempty"`
	// Disc-set membership, mirrored from the job blob so the queue UI can
	// group members. Absent on non-set jobs and pure-torrent entries.
	DiscSetID string `json:"disc_set_id,omitempty"`
	DiscIndex int    `json:"disc_index,omitempty"`
	DiscTotal int    `json:"disc_total,omitempty"`
}

// DownloadRequest is the POST body for /api/download.
type DownloadRequest struct {
	DownloadURL      string `json:"download_url"`
	MagnetURL        string `json:"magnet_url"`
	InfoHash         string `json:"info_hash"`
	Title            string `json:"title"`
	Platform         string `json:"platform"`
	PlatformSlug     string `json:"platform_slug"`
	IsPC             bool   `json:"is_pc"`
	SourceType       string `json:"source_type"`
	VimmID           string `json:"vimm_id"`
	DownloadProtocol string `json:"download_protocol"` // "torrent" or "nzb"

	// Per-file content hashes carried from the chosen SearchResult (archive.org
	// exposes them; see SearchResult.MD5/SHA1) so the normalize/convert stage can
	// verify the downloaded artifact before a destructive convert. Empty for
	// sources that expose no hash (torrent/Vimm).
	MD5  string `json:"md5,omitempty"`
	SHA1 string `json:"sha1,omitempty"`

	// Force bypasses the by-hash duplicate gate (409 duplicate_hash): download
	// even though the release is byte-identical to a library item.
	Force bool `json:"force,omitempty"`

	// TargetFile names one file to pluck out of a multi-file (pack) torrent —
	// an exact in-torrent path or a bare filename (#256). Every other file is
	// set to priority 0 and only the target is imported. Empty = whole content.
	// Torrent protocol only; supplied by API callers now, by the F4 selector
	// later.
	TargetFile string `json:"target_file,omitempty"`

	// Disc-set membership (F4): N downloads sharing a DiscSetID converge into
	// the SetDir game directory and finalize (normalize/convert/.m3u/library
	// row) together when the last one lands. All four fields travel together:
	// DiscIndex is 1-based, DiscTotal is the member count the set completes
	// at, SetDir is the shared single-component directory name (disc token
	// stripped). Supplied by API callers now, stamped by the F4 selector in
	// enforce mode.
	DiscSetID string `json:"disc_set_id,omitempty"`
	DiscIndex int    `json:"disc_index,omitempty"`
	DiscTotal int    `json:"disc_total,omitempty"`
	SetDir    string `json:"set_dir,omitempty"`
}

// DDLSource represents a custom direct-download source.
type DDLSource struct {
	Name      string   `json:"name"`
	URL       string   `json:"url"`
	Type      string   `json:"type"`
	Builtin   bool     `json:"builtin"`
	Platforms []string `json:"platforms,omitempty"`
}

// Settings represents user-configurable settings.
type Settings struct {
	ExtractArchives bool `json:"extract_archives"`
}

// MonitorAction represents a pending/completed monitor action.
type MonitorAction struct {
	ID          string      `json:"id"`
	Action      string      `json:"action"`
	Params      interface{} `json:"params"`
	Description string      `json:"description"`
	Risk        string      `json:"risk"`
	CreatedAt   float64     `json:"created_at,omitempty"`
	Result      string      `json:"result,omitempty"`
	DoneAt      float64     `json:"done_at,omitempty"`
}

// MonitorStatus is the response from /api/monitor/status.
type MonitorStatus struct {
	Enabled        bool             `json:"enabled"`
	Provider       string           `json:"provider"`
	Model          string           `json:"model"`
	AutoFix        bool             `json:"auto_fix"`
	Interval       int              `json:"interval"`
	LastChecked    *float64         `json:"last_checked"`
	Diagnosis      string           `json:"diagnosis"`
	RecentErrors   []ErrorEntry     `json:"recent_errors"`
	PendingActions []*MonitorAction `json:"pending_actions"`
	ActionHistory  []*MonitorAction `json:"action_history"`
}

// ErrorEntry is a captured warning/error log entry.
type ErrorEntry struct {
	Timestamp float64 `json:"ts"`
	TimeStr   string  `json:"ts_str"`
	Level     string  `json:"level"`
	Logger    string  `json:"logger"`
	Message   string  `json:"message"`
}
