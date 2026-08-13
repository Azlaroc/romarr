// Package download orchestrates torrent, DDL, and NZB downloads across
// clients and watches for completed transfers to import into the library.
package download

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/normalize"
	"gamarr/internal/nzbget"
	"gamarr/internal/platform"
	"gamarr/internal/qbit"
	"gamarr/internal/safety"
	"gamarr/internal/search"
)

// NotifyCallback is called when a download completes or fails.
// Parameters: userID, notifType, title, message.
type NotifyCallback func(userID, notifType, title, message string)

// Manager handles download orchestration.
type Manager struct {
	cfg          *config.Config
	jobs         *db.JobStore
	qb           *qbit.Client
	transmission *TransmissionClient
	deluge       *DelugeClient
	nzbget       *nzbget.Client
	norm         *normalize.Normalizer
	NotifyFunc   NotifyCallback

	// importing single-flights torrent imports per job ID: the watcher tick,
	// a restart-resumed tick, and the manual organize button may all try to
	// launch the same import.
	importing sync.Map
}

// New creates a new download Manager.
func New(cfg *config.Config, jobs *db.JobStore, qb *qbit.Client) *Manager {
	mgr := &Manager{cfg: cfg, jobs: jobs, qb: qb, norm: normalize.New(cfg)}

	// Initialize optional download clients.
	if cfg.HasTransmission() {
		mgr.transmission = NewTransmissionClient(cfg)
		slog.Info("Transmission client initialized", "url", cfg.TransmissionURL)
	}
	if cfg.HasDeluge() {
		mgr.deluge = NewDelugeClient(cfg)
		slog.Info("Deluge client initialized", "url", cfg.DelugeURL)
	}
	if cfg.HasNZBGet() {
		mgr.nzbget = nzbget.New(cfg.NZBGetURL, cfg.NZBGetUser, cfg.NZBGetPass)
		slog.Info("NZBGet client initialized", "url", cfg.NZBGetURL)
	}

	return mgr
}

// Jobs returns the job store.
func (m *Manager) Jobs() *db.JobStore { return m.jobs }

// QB returns the qBittorrent client.
func (m *Manager) QB() *qbit.Client { return m.qb }

// Transmission returns the Transmission client (may be nil).
func (m *Manager) Transmission() *TransmissionClient { return m.transmission }

// Deluge returns the Deluge client (may be nil).
func (m *Manager) Deluge() *DelugeClient { return m.deluge }

// NZBGet returns the NZBGet client (may be nil).
func (m *Manager) NZBGet() *nzbget.Client { return m.nzbget }

// newJobID generates an 8-char job ID.
func newJobID() string {
	b := make([]byte, 4)
	_, _ = io.ReadFull(cryptoReader(), b)
	return fmt.Sprintf("%x", b)
}

// TorrentSpec describes a torrent download request.
type TorrentSpec struct {
	URL          string
	InfoHash     string
	Title        string
	Platform     string
	PlatformSlug string
	IsPC         bool
	// TargetFile names one file inside a pack torrent to download and import
	// (selective download, #256). Empty = whole content. Wired in F3 PR-3.
	TargetFile string
}

// DownloadTorrent submits a torrent to qBittorrent and registers a job that
// the completion watcher drives through import. Job↔torrent association is by
// infohash — from the spec or parsed out of a magnet link — with a per-job
// qBittorrent tag as the fallback for .torrent URLs whose hash is unknown
// until qBittorrent resolves them (the watcher learns it from the tag).
// Torrents that only Transmission/Deluge could take are rejected outright:
// nothing ever watched those clients, so an add there was a silent black hole.
func (m *Manager) DownloadTorrent(spec TorrentSpec) (string, error) {
	if spec.URL == "" {
		return "", fmt.Errorf("no download URL")
	}
	if !m.cfg.HasQBittorrent() {
		return "", fmt.Errorf("qBittorrent not configured")
	}

	hash := normalizeInfoHash(spec.InfoHash)
	if hash == "" {
		hash = parseBTIH(spec.URL)
	}

	jobID := newJobID()
	tag := "gamarr-" + jobID
	jobData := map[string]interface{}{
		"status":        "downloading",
		"title":         spec.Title,
		"platform":      spec.Platform,
		"platform_slug": spec.PlatformSlug,
		"is_pc":         spec.IsPC,
		"error":         nil,
		"detail":        "Sending to qBittorrent...",
		"source_type":   "torrent",
		"source_client": "qbittorrent",
		"torrent_hash":  hash,
		"qb_tag":        tag,
		"started_at":    time.Now().Unix(),
	}
	if spec.TargetFile != "" {
		jobData["target_file"] = spec.TargetFile
	}
	m.jobs.Set(jobID, jobData)

	// A duplicate add silently no-ops in qBittorrent — the tag would never
	// appear and association would time out — so adopt an existing torrent.
	if hash != "" {
		if existing := m.qb.GetTorrentsFiltered("", "", hash); len(existing) > 0 {
			m.jobs.Update(jobID, "detail", "Torrent already in qBittorrent; watching...")
			slog.Info("torrent already present, adopting", "title", spec.Title, "hash", hash)
			return jobID, nil
		}
	}

	// Selective download (#256): a .torrent-URL add goes in stopped so no
	// unwanted pack bytes download before the watcher prio-0s the rest.
	// Magnets always add running — a stopped magnet never fetches metadata via
	// DHT — and accept the brief head start before selection.
	stopped := spec.TargetFile != "" && !strings.HasPrefix(strings.ToLower(spec.URL), "magnet:")
	if !m.qb.AddTorrentOpts(spec.URL, qbit.AddOptions{
		SavePath: m.cfg.QBSavePath,
		Category: m.cfg.QBCategory,
		Tags:     tag,
		Stopped:  stopped,
	}) {
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error",
			"error":  "Failed to add torrent to qBittorrent",
		})
		return jobID, nil
	}

	m.jobs.Update(jobID, "detail", "Downloading via qBittorrent...")
	slog.Info("torrent added", "title", spec.Title, "hash", hash, "tag", tag)
	return jobID, nil
}

// DownloadDDL starts a direct download. md5/sha1 are the expected content
// hashes from the chosen SearchResult (archive.org exposes them); they are
// stored on the job and threaded to organize-time so the convert stage (#261)
// can verify before a destructive convert. Empty when the source has no hash.
func (m *Manager) DownloadDDL(url, vimmID, title, platf, platSlug string, isPC bool, md5, sha1 string) string {
	jobID := newJobID()
	m.jobs.Set(jobID, map[string]interface{}{
		"status":        "downloading",
		"title":         title,
		"platform":      platf,
		"platform_slug": platSlug,
		"is_pc":         isPC,
		"error":         nil,
		"detail":        "Starting direct download...",
		"md5":           md5,
		"sha1":          sha1,
	})
	go m.ddlDownloadWorker(jobID, url, vimmID, title, platf, platSlug, isPC, md5, sha1)
	return jobID
}

// OrganizeTorrent manually triggers organize for a completed torrent.
func (m *Manager) OrganizeTorrent(hash, platf, platSlug string, isPC bool) (string, error) {
	torrents := m.qb.GetTorrents(m.cfg.QBCategory)
	var torrent *qbit.Torrent
	for i := range torrents {
		if torrents[i].Hash == hash {
			torrent = &torrents[i]
			break
		}
	}
	if torrent == nil {
		return "", fmt.Errorf("torrent not found")
	}
	if torrent.Progress < 1.0 {
		return "", fmt.Errorf("torrent not yet complete")
	}

	jobID := newJobID()
	m.jobs.Set(jobID, map[string]interface{}{
		"status":        "scanning",
		"title":         torrent.Name,
		"platform":      platf,
		"platform_slug": platSlug,
		"is_pc":         isPC,
		"error":         nil,
		"detail":        "Scanning and importing...",
		"source_type":   "torrent",
		"source_client": "qbittorrent",
		"torrent_hash":  strings.ToLower(torrent.Hash),
		"started_at":    time.Now().Unix(),
	})

	go m.importTorrentJob(jobID, torrent)
	return jobID, nil
}

// importTorrentJob imports a completed torrent's payload into the library by
// COPYING it out — the original payload keeps seeding untouched. The pipeline
// renames, rewrites, and deletes inside whatever it is handed (DAT rename,
// .m3u writes, CHD convert consuming sources, sidecar), so it must never see
// the live payload. The job completes at import; the torrent's seed lifecycle
// is independent (qBittorrent share limits, or the opt-in seed janitor).
func (m *Manager) importTorrentJob(jobID string, torrent *qbit.Torrent) {
	if _, busy := m.importing.LoadOrStore(jobID, struct{}{}); busy {
		return
	}
	defer m.importing.Delete(jobID)

	job, ok := m.jobs.Get(jobID)
	if !ok {
		return
	}
	platf, _ := job["platform"].(string)
	platSlug, _ := job["platform_slug"].(string)
	isPC, _ := job["is_pc"].(bool)
	title, _ := job["title"].(string)
	if title == "" {
		title = torrent.Name
	}

	contentPath := torrent.ContentPath
	if contentPath == "" || !pathExists(contentPath) {
		savePath := torrent.SavePath
		if savePath == "" {
			savePath = m.cfg.QBSavePath
		}
		contentPath = filepath.Join(savePath, torrent.Name)
	}
	if !pathExists(contentPath) {
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error",
			"error":  fmt.Sprintf("Cannot find downloaded files at %s", contentPath),
		})
		slog.Error("content path not found", "path", contentPath)
		return
	}

	// Selective download (#256): the import narrows to the plucked file — the
	// pack's prio-0 neighbors (partial .!qB spillover included) never get
	// scanned, copied, or tracked.
	if resolved, _ := job["target_file_resolved"].(string); resolved != "" {
		savePath := torrent.SavePath
		if savePath == "" {
			savePath = m.cfg.QBSavePath
		}
		target := filepath.Join(savePath, filepath.FromSlash(resolved))
		if !pathExists(target) {
			m.jobs.UpdateMulti(jobID, map[string]interface{}{
				"status": "error",
				"error":  fmt.Sprintf("Plucked file not found at %s", target),
			})
			slog.Error("target file not found", "path", target)
			return
		}
		contentPath = target
	}

	// Layer 2: ClamAV — a read-only scan, safe on the seeding payload. An
	// infected torrent is never worth seeding: delete it, files included.
	m.jobs.UpdateMulti(jobID, map[string]interface{}{
		"status": "scanning", "detail": "Running virus scan...",
	})
	isClean, infected := safety.ScanWithClamAV(contentPath, m.cfg.ClamAVContainer, m.cfg.ClamAVSocket, m.cfg.DockerSocket)
	if !isClean {
		slog.Warn("ClamAV found infections", "title", title, "infected", infected)
		detail := infected
		if len(detail) > 3 {
			detail = detail[:3]
		}
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error",
			"error":  fmt.Sprintf("Virus detected: %s", strings.Join(detail, "; ")),
			"detail": "Infected files found - download quarantined",
		})
		m.qb.DeleteTorrent(torrent.Hash, true)
		return
	}

	// Platform detection (read-only) when the request didn't pin one.
	if platSlug == "" && !isPC {
		if info, ok := platform.DetectPlatformFromMetadata(contentPath); ok {
			platf, platSlug, isPC = info.Name, info.Slug, info.IsPC
			m.jobs.UpdateMulti(jobID, map[string]interface{}{
				"platform": platf, "platform_slug": platSlug, "is_pc": isPC,
			})
			slog.Info("detected platform from metadata", "platform", platf)
		}
	}
	if platSlug == "" && !isPC {
		if info, ok := platform.DetectPlatformFromFiles(contentPath, torrent.Name); ok {
			platf, platSlug, isPC = info.Name, info.Slug, info.IsPC
			m.jobs.UpdateMulti(jobID, map[string]interface{}{
				"platform": platf, "platform_slug": platSlug, "is_pc": isPC,
			})
			slog.Info("detected platform from files/title", "platform", platf)
		}
	}
	if !isPC && platSlug == "" {
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "completed", "detail": "Downloaded (unknown platform, left in staging)",
		})
		slog.Warn("no platform slug, left in downloads", "name", torrent.Name)
		return
	}

	base := sanitizeFilename(filepath.Base(contentPath))
	var destRoot, dest string
	if isPC {
		destRoot = m.cfg.GamesVaultPath
		dest = filepath.Join(destRoot, base)
	} else {
		destRoot = m.cfg.GamesRomsPath
		dest = filepath.Join(m.romDestDir(platSlug), base)
	}
	// Never clobber content another source already put at the destination.
	if pathExists(dest) {
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error",
			"error":  fmt.Sprintf("Destination already exists: %s", dest),
		})
		return
	}

	// Copy into a dot-dir staging area on the destination filesystem (invisible
	// to the library scanner and RomM), then hand the copy to the pipeline —
	// its own move is a same-fs rename. A crashed prior attempt's leftovers are
	// wiped first, so re-entry after a restart is safe.
	m.jobs.UpdateMulti(jobID, map[string]interface{}{
		"status": "importing", "detail": "Copying out of seeding payload...",
	})
	tmpDir := filepath.Join(destRoot, ".gamarr-tmp", jobID)
	os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error", "error": fmt.Sprintf("Import staging failed: %v", err),
		})
		return
	}
	tmp := filepath.Join(tmpDir, base)
	if err := copyContent(contentPath, tmp); err != nil {
		os.RemoveAll(tmpDir)
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error", "error": fmt.Sprintf("Import copy failed: %v", err),
		})
		return
	}

	if isPC {
		if err := moveContent(tmp, dest); err != nil {
			os.RemoveAll(tmpDir)
			m.jobs.UpdateMulti(jobID, map[string]interface{}{
				"status": "error", "error": fmt.Sprintf("Organize failed: %v", err),
			})
			return
		}
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "completed", "detail": "Moved to GameVault",
		})
		writeMetadataSidecar(dest, title, platf, platSlug, isPC, "torrent")
		m.TrackInLibrary(title, platf, platSlug, isPC, dest, 0, "torrent", "prowlarr", "torrent:"+torrent.Hash)
		m.jobs.LogActivity("download_completed", title, "Organized to GameVault", jobID, nil)
		slog.Info("PC game imported", "name", sanitizeLog(title), "dest", sanitizeLog(dest))
	} else {
		if _, err := m.fulfillLocalROM(tmp, fulfillMeta{
			JobID:        jobID,
			Title:        title,
			Platform:     platf,
			PlatformSlug: platSlug,
			Source:       "torrent",
			SourceClient: "prowlarr",
			SourceID:     "torrent:" + torrent.Hash,
		}); err != nil {
			os.RemoveAll(tmpDir)
			return
		}
	}
	os.RemoveAll(tmpDir)
	m.jobs.Update(jobID, "imported_at", time.Now().Unix())

	if m.cfg.RemoveAfterImport {
		m.qb.DeleteTorrent(torrent.Hash, true)
		slog.Info("removed torrent after import", "name", torrent.Name)
	}
	if m.NotifyFunc != nil {
		m.NotifyFunc("", "download_complete", title,
			"Downloaded and imported: "+title+" ("+platf+")")
	}
}

func (m *Manager) ddlDownloadWorker(jobID, dlURL, vimmID, title, platf, platSlug string, isPC bool, md5, sha1 string) {
	staging := m.cfg.QBSavePath
	if err := os.MkdirAll(staging, 0755); err != nil {
		slog.Error("cannot create staging dir", "path", staging, "error", err)
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error",
			"error":  fmt.Sprintf("cannot create staging dir %s: %v", staging, err),
		})
		return
	}

	var filepath_ string
	var dlErr error

	if vimmID != "" {
		filepath_ = m.downloadVimmGame(vimmID, staging, jobID)
	} else if dlURL != "" {
		filepath_, dlErr = m.downloadDDL(dlURL, staging, jobID)
	}

	if filepath_ == "" || !pathExists(filepath_) {
		job, ok := m.jobs.Get(jobID)
		if ok {
			status, _ := job["status"].(string)
			if status != "error" {
				errMsg := "Download failed"
				if dlErr != nil {
					errMsg = fmt.Sprintf("Download failed: %v", dlErr)
				}
				m.jobs.UpdateMulti(jobID, map[string]interface{}{
					"status": "error", "error": errMsg,
				})
			}
		}
		return
	}

	// ClamAV scan
	m.jobs.UpdateMulti(jobID, map[string]interface{}{
		"status": "scanning", "detail": "Running virus scan...",
	})
	isClean, infected := safety.ScanWithClamAV(filepath_, m.cfg.ClamAVContainer, m.cfg.ClamAVSocket, m.cfg.DockerSocket)
	if !isClean {
		slog.Warn("ClamAV found infections in DDL", "title", title, "infected", infected)
		detail := infected
		if len(detail) > 3 {
			detail = detail[:3]
		}
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error",
			"error":  fmt.Sprintf("Virus detected: %s", strings.Join(detail, "; ")),
		})
		os.Remove(filepath_)
		return
	}

	m.jobs.UpdateMulti(jobID, map[string]interface{}{
		"status": "organizing", "detail": "Moving to library...",
	})
	m.organizeDDLFile(jobID, filepath_, title, platf, platSlug, isPC, md5, sha1)
}

func (m *Manager) downloadDDL(dlURL, destPath, jobID string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, _ := http.NewRequest("GET", dlURL, nil)
	req.Header.Set("User-Agent", "Gamarr/1.0")

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("DDL download failed", "url", sanitizeLog(dlURL), "error", err)
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		slog.Error("DDL download failed", "url", sanitizeLog(dlURL), "status", resp.StatusCode)
		return "", fmt.Errorf("HTTP %d from server", resp.StatusCode)
	}

	total := resp.ContentLength
	cd := resp.Header.Get("Content-Disposition")
	fnRe := regexp.MustCompile(`filename="?([^";\n]+)"?`)
	var filename string
	if m := fnRe.FindStringSubmatch(cd); m != nil {
		filename = strings.TrimSpace(m[1])
	} else {
		parts := strings.Split(strings.Split(dlURL, "?")[0], "/")
		filename = parts[len(parts)-1]
		// URL path segments are percent-encoded (archive.org, Myrient, …).
		// Decode so the on-disk name is the real title ("Game (USA).zip") and
		// not "Game%20%28USA%29.zip", which would otherwise leak into the
		// library file_path and the extracted-folder name.
		if decoded, err := url.PathUnescape(filename); err == nil {
			filename = decoded
		}
	}
	// The filename comes from the remote server (Content-Disposition or URL);
	// never let it name a path outside the staging dir.
	filename = sanitizeFilename(filename)

	fp, err := safeChild(destPath, filename)
	if err != nil {
		slog.Error("DDL rejected unsafe filename", "filename", sanitizeLog(filename))
		return "", err
	}
	f, err := os.Create(fp)
	if err != nil {
		slog.Error("DDL cannot create file", "path", sanitizeLog(fp), "error", err)
		return "", fmt.Errorf("cannot create file %s: %v", fp, err)
	}
	defer f.Close()

	downloaded := int64(0)
	lastUpdate := time.Now()
	buf := make([]byte, 256*1024)
	var writeErr error

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr = f.Write(buf[:n]); writeErr != nil {
				break
			}
			downloaded += int64(n)
			if time.Since(lastUpdate) > 2*time.Second && total > 0 {
				pct := float64(downloaded) / float64(total) * 100
				m.jobs.Update(jobID, "detail",
					fmt.Sprintf("Downloading... %.1f%% (%s/%s)", pct, search.HumanSize(downloaded), search.HumanSize(total)))
				lastUpdate = time.Now()
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				writeErr = readErr
			}
			break
		}
	}
	// Don't report a truncated file as a finished download — a dropped
	// connection or full disk would otherwise pass a partial archive to the
	// scan/organize pipeline as if it were complete.
	if writeErr != nil {
		os.Remove(fp)
		return "", fmt.Errorf("download interrupted after %s: %w", search.HumanSize(downloaded), writeErr)
	}
	if total > 0 && downloaded != total {
		os.Remove(fp)
		return "", fmt.Errorf("incomplete download: got %s of %s", search.HumanSize(downloaded), search.HumanSize(total))
	}
	m.jobs.Update(jobID, "detail", fmt.Sprintf("Downloaded %s", search.HumanSize(downloaded)))
	return fp, nil
}

var vimmFormRe = regexp.MustCompile(`<form[^>]*id=["']dl_form["'][^>]*>`)
var vimmActionRe = regexp.MustCompile(`action="([^"]+)"`)
var vimmMediaRe = regexp.MustCompile(`name="mediaId"\s+value="(\d+)"`)
var vimmJSMediaRe = regexp.MustCompile(`"ID":(\d+)`)
var vimmDLRe = regexp.MustCompile(`(//dl\d*\.vimm\.net/[^"']*)`)
var vimmDLNumRe = regexp.MustCompile(`dl(\d+)`)

func (m *Manager) downloadVimmGame(gameID, destPath, jobID string) string {
	client := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	gameURL := fmt.Sprintf("https://vimm.net/vault/%s", gameID)
	m.jobs.Update(jobID, "detail", "Fetching game page...")

	req, _ := http.NewRequest("GET", gameURL, nil)
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("Vimm fetch failed", "error", err)
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	pageText := string(body)

	if strings.Contains(pageText, "unavailable at the request of") {
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error", "error": "Game removed by DMCA takedown",
		})
		return ""
	}

	// Extract form action and mediaId
	formTag := vimmFormRe.FindString(pageText)
	var actionURL, mediaID string

	if formTag != "" {
		if m := vimmActionRe.FindStringSubmatch(formTag); m != nil {
			actionURL = m[1]
		}
	}
	if m := vimmMediaRe.FindStringSubmatch(pageText); m != nil {
		mediaID = m[1]
	}

	// Fallbacks
	if mediaID == "" {
		if m := vimmJSMediaRe.FindStringSubmatch(pageText); m != nil {
			mediaID = m[1]
			slog.Info("Vimm: found mediaId from JS", "id", mediaID)
		}
	}
	if actionURL == "" {
		if m := vimmDLRe.FindStringSubmatch(pageText); m != nil && mediaID != "" {
			actionURL = m[1]
			slog.Info("Vimm: found dl URL from page", "url", actionURL)
		}
	}

	if actionURL == "" || mediaID == "" {
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error", "error": "Could not find download form on Vimm",
		})
		return ""
	}

	if strings.HasPrefix(actionURL, "//") {
		actionURL = "https:" + actionURL
	}
	slog.Info("Vimm download", "action", actionURL, "mediaId", mediaID)

	m.jobs.Update(jobID, "detail", "Starting download from Vimm...")
	time.Sleep(3 * time.Second) // Respectful delay

	// Try action URL and alternates
	dlURLs := []string{actionURL}
	if n := vimmDLNumRe.FindStringSubmatch(actionURL); n != nil {
		dlURLs = append(dlURLs, fmt.Sprintf("https://download%s.vimm.net/download/", n[1]))
	}

	var dlResp *http.Response
	for _, dlURL := range dlURLs {
		form := strings.NewReader(fmt.Sprintf("mediaId=%s", mediaID))
		req, _ := http.NewRequest("POST", dlURL, form)
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Referer", gameURL)
		req.Header.Set("Origin", "https://vimm.net")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		streamClient := &http.Client{
			Timeout: 10 * time.Minute,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
		r, err := streamClient.Do(req)
		if err != nil {
			slog.Warn("Vimm download failed", "url", dlURL, "error", err)
			continue
		}
		if r.StatusCode == 200 {
			slog.Info("Vimm download started", "url", dlURL)
			dlResp = r
			break
		}
		r.Body.Close()
		slog.Warn("Vimm download rejected", "url", dlURL, "status", r.StatusCode)
	}

	if dlResp == nil {
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error",
			"error":  fmt.Sprintf("Vimm download server rejected request (tried %d URLs)", len(dlURLs)),
		})
		return ""
	}
	defer dlResp.Body.Close()

	total := dlResp.ContentLength
	cd := dlResp.Header.Get("Content-Disposition")
	fnRe := regexp.MustCompile(`filename="?([^";\n]+)"?`)
	filename := fmt.Sprintf("%s.7z", gameID)
	if fm := fnRe.FindStringSubmatch(cd); fm != nil {
		filename = strings.TrimSpace(fm[1])
	}
	// The filename comes from the remote server (Content-Disposition); never let
	// it name a path outside the staging dir.
	filename = sanitizeFilename(filename)

	fp, err := safeChild(destPath, filename)
	if err != nil {
		slog.Error("Vimm rejected unsafe filename", "filename", sanitizeLog(filename))
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error", "error": "Vimm returned an unsafe filename",
		})
		return ""
	}
	f, err := os.Create(fp)
	if err != nil {
		return ""
	}
	defer f.Close()

	downloaded := int64(0)
	buf := make([]byte, 256*1024)
	var writeErr error
	for {
		n, readErr := dlResp.Body.Read(buf)
		if n > 0 {
			if _, writeErr = f.Write(buf[:n]); writeErr != nil {
				break
			}
			downloaded += int64(n)
			if total > 0 {
				pct := float64(downloaded) / float64(total) * 100
				m.jobs.Update(jobID, "detail",
					fmt.Sprintf("Downloading... %.1f%% (%s/%s)", pct, search.HumanSize(downloaded), search.HumanSize(total)))
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				writeErr = readErr
			}
			break
		}
	}
	// A dropped connection or full disk must not pass a truncated .7z off as a
	// finished download — it would land in the library as a complete game.
	if writeErr != nil || (total > 0 && downloaded != total) {
		os.Remove(fp)
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error",
			"error":  fmt.Sprintf("Vimm download incomplete (%s of %s)", search.HumanSize(downloaded), search.HumanSize(total)),
		})
		return ""
	}
	m.jobs.Update(jobID, "detail", fmt.Sprintf("Downloaded %s", search.HumanSize(downloaded)))
	return fp
}

func (m *Manager) organizeDDLFile(jobID, fp, title, platf, platSlug string, isPC bool, md5, sha1 string) {
	// md5/sha1 are the expected content hashes from the source. They are carried
	// to organize-time for the #261 verify-before-convert gate; empty when the
	// source exposes no hash (torrent/Vimm), in which case verify is skipped.
	if md5 != "" || sha1 != "" {
		slog.Debug("organize: expected content hashes available", "md5", md5, "sha1", sha1)
	}
	filename := sanitizeFilename(filepath.Base(fp))
	if isPC {
		dest := filepath.Join(m.cfg.GamesVaultPath, filename)
		if err := moveFile(fp, dest); err != nil {
			m.jobs.UpdateMulti(jobID, map[string]interface{}{
				"status": "error", "error": fmt.Sprintf("Organize failed: %v", err),
			})
			return
		}
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "completed", "detail": "Moved to GameVault",
		})
		writeMetadataSidecar(dest, title, platf, platSlug, isPC, "ddl")
		m.TrackInLibrary(title, platf, platSlug, isPC, dest, 0, "ddl", "ddl", "ddl:"+dest)
		m.jobs.LogActivity("download_completed", title, "DDL to GameVault", jobID, nil)
		slog.Info("DDL PC game organized", "file", sanitizeLog(filename), "dest", sanitizeLog(dest))
	} else if platSlug != "" {
		if _, err := m.fulfillLocalROM(fp, fulfillMeta{
			JobID:        jobID,
			Title:        title,
			Platform:     platf,
			PlatformSlug: platSlug,
			Source:       "ddl",
			SourceClient: "ddl",
			MD5:          md5,
			SHA1:         sha1,
		}); err != nil {
			return
		}
	} else {
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "completed", "detail": "Downloaded (unknown platform, left in staging)",
		})
	}
}

// RecoverOrphanedTorrents wakes the qBittorrent session after a restart. Job
// recovery itself no longer happens here: torrent jobs persist their infohash
// (or association tag) and the completion watcher re-drives them straight from
// the database, so minting duplicate jobs at boot is structurally impossible.
// Out-of-band torrents are minted as completed_unorganized by the watcher's
// orphan sweep.
func (m *Manager) RecoverOrphanedTorrents() {
	if !m.cfg.HasQBittorrent() {
		slog.Info("orphan torrent recovery disabled")
		return
	}
	for attempt := 0; attempt < 12; attempt++ {
		if m.qb.Login() {
			return
		}
		slog.Info("orphan recovery: waiting for qBit", "attempt", attempt+1)
		time.Sleep(5 * time.Second)
	}
	slog.Warn("qBit login failed after retries")
}

// MaybeNormalize runs the F5 normalize step (DAT 1G1R rename + multi-disc .m3u)
// over a freshly-organized ROM path when the NormalizeROMs setting is on, and
// returns the path to track — unchanged when the step is disabled, the binary
// is unavailable, or Playmatch has no match. Non-blocking: it never fails an
// import. jobID may be empty (manual import), in which case job detail is not
// touched. Callers must pass the specific artifact path (file or per-game dir),
// never a shared platform root.
func (m *Manager) MaybeNormalize(jobID, path, platSlug string) string {
	if !m.LoadSettings().NormalizeROMs {
		return path
	}
	finalPath, res, _ := m.norm.Normalize(context.Background(), path, platSlug, normalize.Policy{})
	if jobID != "" && (res.Renamed || res.Playlist) {
		if job, ok := m.jobs.Get(jobID); ok {
			detail, _ := job["detail"].(string)
			m.jobs.Update(jobID, "detail", detail+" (normalized)")
		}
	}
	return finalPath
}

// MaybeConvert runs the F5 convert step (disc systems → CHD, with verify before
// the source is deleted) when the ConvertROMs setting is on, and returns the path
// to track — unchanged when disabled, the binary is unavailable, the platform is
// not a disc system, or hashStatus is "mismatch". Non-blocking: any per-disc
// error keeps that source untouched. jobID may be empty (no job detail update).
func (m *Manager) MaybeConvert(jobID, path, platSlug, hashStatus string) string {
	if !m.LoadSettings().ConvertROMs {
		return path
	}
	finalPath, res, _ := m.norm.Convert(context.Background(), path, platSlug, hashStatus)
	if jobID != "" && res.Converted > 0 {
		if job, ok := m.jobs.Get(jobID); ok {
			detail, _ := job["detail"].(string)
			m.jobs.Update(jobID, "detail", fmt.Sprintf("%s (converted %d→CHD)", detail, res.Converted))
		}
	}
	return finalPath
}

// verifyDownloadHash streams fp once and compares its md5/sha1 to the expected
// values carried from the source (#259). Returns "ok" (match), "mismatch"
// (differs), or "skipped" (no expected hash, or fp unreadable). Called only when
// a convert is eligible, to authenticate the download before the destructive
// convert deletes the source.
func verifyDownloadHash(fp, expectedMD5, expectedSHA1 string) string {
	if expectedMD5 == "" && expectedSHA1 == "" {
		return "skipped"
	}
	f, err := os.Open(fp)
	if err != nil {
		return "skipped"
	}
	defer f.Close()
	md5h := md5.New()
	sha1h := sha1.New()
	if _, err := io.Copy(io.MultiWriter(md5h, sha1h), f); err != nil {
		return "skipped"
	}
	if expectedMD5 != "" && !strings.EqualFold(hex.EncodeToString(md5h.Sum(nil)), expectedMD5) {
		return "mismatch"
	}
	if expectedSHA1 != "" && !strings.EqualFold(hex.EncodeToString(sha1h.Sum(nil)), expectedSHA1) {
		return "mismatch"
	}
	return "ok"
}

// isExtractableArchive reports whether path has an archive extension the
// organize step knows how to unpack.
func isExtractableArchive(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".zip", ".7z", ".rar":
		return true
	}
	return false
}

// gameDirForArchive returns the clean game directory an archive extracts into:
// the archive path with its extension stripped and basename sanitized, so
// "…/Game (USA).zip" -> "…/Game (USA)". No ".extracted" suffix — a directory
// holding the disc's files is what RomM and the library scanner expect.
func gameDirForArchive(archive string) string {
	base := filepath.Base(archive)
	name := sanitizeFilename(strings.TrimSuffix(base, filepath.Ext(base)))
	return filepath.Join(filepath.Dir(archive), name)
}

// extractToGameDir unpacks archive into gameDirForArchive(archive) and returns
// that directory. It refuses to overwrite an existing destination and cleans up
// a partial directory on failure, so a failed extraction never leaves a
// half-populated game folder behind.
func extractToGameDir(archive string) (string, error) {
	outDir := gameDirForArchive(archive)
	if pathExists(outDir) {
		return "", fmt.Errorf("destination %q already exists", outDir)
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", err
	}
	var cmd *exec.Cmd
	if strings.ToLower(filepath.Ext(archive)) == ".rar" {
		cmd = exec.Command("unrar", "x", "-o+", "-y", archive, outDir+"/")
	} else {
		cmd = exec.Command("7z", "x", fmt.Sprintf("-o%s", outDir), "-y", archive)
	}
	if err := cmd.Run(); err != nil {
		os.RemoveAll(outDir)
		return "", err
	}
	return outDir, nil
}

// contentSize returns the byte size of a file, or the recursive size of a
// directory tree, so library entries carry a real size whether they point at a
// single ROM file or an extracted game folder.
func contentSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if info.IsDir() {
		return dirSize(path)
	}
	return info.Size()
}

func writeMetadataSidecar(destPath, title, platf, platSlug string, isPC bool, sourceType string) {
	meta := map[string]interface{}{
		"title":         title,
		"platform":      platf,
		"platform_slug": platSlug,
		"is_pc":         isPC,
		"source":        sourceType,
		"organized_at":  time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	var sidecar string
	fi, err := os.Stat(destPath)
	if err == nil && fi.IsDir() {
		sidecar = filepath.Join(destPath, ".gamarr.json")
	} else {
		sidecar = destPath + ".gamarr.json"
	}
	if err := os.WriteFile(sidecar, data, 0644); err != nil {
		slog.Warn("failed to write metadata sidecar", "error", err)
	}
}

// moveFile moves a file, falling back to copy+delete for cross-device moves.
func moveFile(src, dest string) error {
	err := os.Rename(src, dest)
	if err == nil {
		return nil
	}
	// Cross-device link — copy and delete
	if err := copyFile(src, dest); err != nil {
		return err
	}
	return os.Remove(src)
}

func moveContent(src, dest string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if err := os.Rename(src, dest); err == nil {
			return nil
		}
		if err := copyDir(src, dest); err != nil {
			return err
		}
		return os.RemoveAll(src)
	}
	return moveFile(src, dest)
}

// copyContent recursively copies src (file or dir) to dest without ever
// touching src — the seeding-safe sibling of moveContent. qBittorrent working
// files (*.!qB, *.parts) are skipped: they are partial-piece scratch, not
// payload.
func copyContent(src, dest string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return copyFile(src, dest)
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("path %q escapes source dir", path)
		}
		target, err := safeChild(dest, rel)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if isQbitWorkFile(info.Name()) {
			return nil
		}
		return copyFile(path, target)
	})
}

// isQbitWorkFile reports whether name is qBittorrent partial-piece scratch.
func isQbitWorkFile(name string) bool {
	l := strings.ToLower(name)
	return strings.HasSuffix(l, ".!qb") || strings.HasSuffix(l, ".parts")
}

func copyDir(src, dest string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("path %q escapes source dir", path)
		}
		target, err := safeChild(dest, rel)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// LoadSettings loads settings from disk.
func (m *Manager) LoadSettings() *Settings {
	settingsFile := filepath.Join(m.cfg.DataDir, "settings.json")
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		return &Settings{ExtractArchives: m.cfg.ExtractArchives, NormalizeROMs: m.cfg.NormalizeROMs, ConvertROMs: m.cfg.ConvertROMs}
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return &Settings{ExtractArchives: m.cfg.ExtractArchives, NormalizeROMs: m.cfg.NormalizeROMs, ConvertROMs: m.cfg.ConvertROMs}
	}
	return &s
}

// SaveSettings saves settings to disk.
func (m *Manager) SaveSettings(s *Settings) {
	os.MkdirAll(m.cfg.DataDir, 0755)
	data, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(filepath.Join(m.cfg.DataDir, "settings.json"), data, 0644)
}

// Settings for download behavior.
type Settings struct {
	ExtractArchives bool `json:"extract_archives"`
	// NormalizeROMs gates the F5 normalize step (DAT 1G1R rename + multi-disc
	// .m3u) on import. Ships off; flip it in the UI to enable.
	NormalizeROMs bool `json:"normalize_roms"`
	// ConvertROMs gates the F5 convert step (disc systems → CHD, verify before
	// replacing the source). Ships off; flip it in the UI to enable.
	ConvertROMs bool `json:"convert_roms"`
}

// DDL source management

func (m *Manager) LoadDDLSources() []map[string]interface{} {
	fp := filepath.Join(m.cfg.DataDir, "ddl_sources.json")
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil
	}
	var sources []map[string]interface{}
	json.Unmarshal(data, &sources)
	return sources
}

func (m *Manager) SaveDDLSources(sources []map[string]interface{}) {
	os.MkdirAll(m.cfg.DataDir, 0755)
	data, _ := json.MarshalIndent(sources, "", "  ")
	os.WriteFile(filepath.Join(m.cfg.DataDir, "ddl_sources.json"), data, 0644)
}
