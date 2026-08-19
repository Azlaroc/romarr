package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gamarr/internal/platform"
)

// IGDB is the metadata authority for games, the way TMDB is for films.
//
// It authenticates through Twitch (IGDB is Twitch-owned): client credentials
// buy a bearer token good for about two months, and every request carries the
// client id alongside it. The token is cached and refreshed under a lock, so a
// burst of searches costs one token, not one per search.
//
// Queries are IGDB's own APIcalypse dialect — a POST body, not query params.
type IGDB struct {
	clientID     string
	clientSecret string
	apiBase      string
	authBase     string
	http         *http.Client

	mu        sync.Mutex
	token     string
	tokenTill time.Time
}

const (
	igdbAPIBase   = "https://api.igdb.com/v4"
	igdbAuthBase  = "https://id.twitch.tv"
	igdbTimeout   = 20 * time.Second
	igdbMaxBody   = 4 << 20
	igdbSkewEarly = 5 * time.Minute
)

// IGDBOption configures the provider (tests point it at a stub).
type IGDBOption func(*IGDB)

// WithIGDBBase overrides the API and auth hosts. An empty value leaves that
// host at its default, so a caller can redirect one without the other.
func WithIGDBBase(api, auth string) IGDBOption {
	return func(p *IGDB) {
		if api != "" {
			p.apiBase = strings.TrimRight(api, "/")
		}
		if auth != "" {
			p.authBase = strings.TrimRight(auth, "/")
		}
	}
}

// WithIGDBHTTPClient injects a client (timeouts, transport).
func WithIGDBHTTPClient(c *http.Client) IGDBOption { return func(p *IGDB) { p.http = c } }

// NewIGDB builds the provider. Empty credentials are allowed: the provider
// reports itself unconfigured rather than failing at construction, so the
// settings screen can say so plainly.
func NewIGDB(clientID, clientSecret string, opts ...IGDBOption) *IGDB {
	p := &IGDB{
		clientID:     strings.TrimSpace(clientID),
		clientSecret: strings.TrimSpace(clientSecret),
		apiBase:      igdbAPIBase,
		authBase:     igdbAuthBase,
		http:         &http.Client{Timeout: igdbTimeout},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Name implements Provider.
func (p *IGDB) Name() string { return "igdb" }

// Configured implements Provider.
func (p *IGDB) Configured() bool { return p != nil && p.clientID != "" && p.clientSecret != "" }

// Search implements Provider.
func (p *IGDB) Search(ctx context.Context, query string, limit int) ([]Game, error) {
	if !p.Configured() {
		return nil, fmt.Errorf("igdb: not configured")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// APIcalypse: `search` ranks by relevance; the field list is explicit
	// because IGDB returns nothing you did not ask for.
	body := fmt.Sprintf(
		`search %s; fields name,slug,summary,first_release_date,cover.url,platforms.name,platforms.slug,platforms.id; limit %d;`,
		quoteAPIcalypse(query), limit)

	raw, err := p.post(ctx, "/games", body)
	if err != nil {
		return nil, err
	}
	var results []igdbGame
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, fmt.Errorf("igdb: decode games: %w", err)
	}
	out := make([]Game, 0, len(results))
	for _, g := range results {
		out = append(out, g.toGame())
	}
	return out, nil
}

// quoteAPIcalypse renders a search term as an APIcalypse string literal.
func quoteAPIcalypse(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, ``), `"`, ``) + `"`
}

type igdbGame struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Summary   string `json:"summary"`
	FirstDate int64  `json:"first_release_date"`
	Cover     struct {
		URL string `json:"url"`
	} `json:"cover"`
	Platforms []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"platforms"`
}

func (g igdbGame) toGame() Game {
	out := Game{
		ProviderID: g.ID,
		Name:       g.Name,
		Slug:       g.Slug,
		Summary:    g.Summary,
		CoverURL:   coverURL(g.Cover.URL),
	}
	if g.FirstDate > 0 {
		out.ReleaseYear = time.Unix(g.FirstDate, 0).UTC().Year()
	}
	for _, pl := range g.Platforms {
		// The registry owns the vocabulary: IGDB's identity is a column on
		// our row, so this is a lookup, not a translation table living here.
		if slug, ok := platform.SlugForIGDB(pl.Slug, pl.ID); ok {
			out.Platforms = append(out.Platforms, slug)
			continue
		}
		if pl.Name != "" {
			out.UnmappedPlatforms = append(out.UnmappedPlatforms, pl.Name)
		}
	}
	return out
}

// coverURL turns IGDB's protocol-relative thumbnail URL into an https URL at
// a size worth looking at. IGDB serves "//images.igdb.com/.../t_thumb/x.jpg";
// the size segment is swappable and t_thumb is 90px wide, which is a blurry
// square in any grid.
func coverURL(raw string) string {
	if raw == "" {
		return ""
	}
	u := strings.Replace(raw, "t_thumb", "t_cover_big", 1)
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

// post issues one APIcalypse request, refreshing the token when needed.
func (p *IGDB) post(ctx context.Context, path, body string) ([]byte, error) {
	token, err := p.bearer(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiBase+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Client-ID", p.clientID)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("igdb: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		// The cached token was rejected (revoked, or the app's secret was
		// rotated). Drop it so the next call re-authenticates instead of
		// failing forever.
		p.mu.Lock()
		p.token, p.tokenTill = "", time.Time{}
		p.mu.Unlock()
		return nil, fmt.Errorf("igdb: credentials rejected")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("igdb: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, igdbMaxBody))
}

// bearer returns a valid token, fetching one when the cache is empty or close
// to expiry.
func (p *IGDB) bearer(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token != "" && time.Now().Before(p.tokenTill.Add(-igdbSkewEarly)) {
		return p.token, nil
	}
	form := url.Values{
		"client_id":     {p.clientID},
		"client_secret": {p.clientSecret},
		"grant_type":    {"client_credentials"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.authBase+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("igdb auth: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("igdb auth: HTTP %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, igdbMaxBody)).Decode(&tok); err != nil {
		return "", fmt.Errorf("igdb auth: decode: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("igdb auth: empty token")
	}
	p.token = tok.AccessToken
	p.tokenTill = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return p.token, nil
}
