// Package signin lets a streamer link their AudD account from the Twitch
// extension's configuration page with "Sign in with AudD".
//
// It runs OAuth 2.0 authorization code with PKCE against AudD's authorization
// server on behalf of the page:
//
//  1. The page makes a random secret and calls Start with the broadcaster's
//     Twitch extension JWT and session = hex(SHA-256(secret)). The sign-in
//     now belongs to that channel.
//  2. The page links to Open, which redirects the new tab to AudD's approval
//     page.
//  3. AudD sends the tab back to Callback. It names the Twitch channel and
//     asks the person to confirm; on confirmation it exchanges the code,
//     reads the account's API token from the AudD MCP server, revokes the
//     sign-in and shows a "go back to Twitch" page.
//  4. Meanwhile the page polls Result with the secret and the JWT, and
//     receives the API token once. It then links the account with the same
//     twitch.php?save_user request a pasted token uses.
//
// Sessions live in memory for sessionTTL (20 minutes) and are single-use. A sign-in link
// can be forwarded, so two things keep someone from collecting another
// person's API token with it: only the channel that started a sign-in can
// read its result, and the person approving is shown that channel's name and
// must confirm it before anything is read.
package signin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Codes in the {"error": ...} field of Result responses.
const (
	ErrBadRequest         = "bad_request"                 // a field is missing or malformed
	ErrTooManySignIns     = "too_many_sign_ins"           // too many sign-ins in progress
	ErrWrongJWT           = "wrong_jwt"                   // the Twitch JWT is invalid or expired
	ErrNotBroadcaster     = "not_broadcaster"             // the JWT is not the broadcaster's
	ErrSignInExpired      = "sign_in_expired"             // AudD did not accept the sign-in
	ErrPermissionMissing  = "token_permission_missing"    // the sign-in did not grant token:read
	ErrServiceUnavailable = "account_service_unavailable" // AudD's sign-in or account service failed
)

// Values of the "status" field of Result responses.
const (
	StatusUnknown  = "unknown"  // no such session for this channel (used or expired)
	StatusPending  = "pending"  // waiting for the person to approve
	StatusDone     = "done"     // api_token is in the response
	StatusDeclined = "declined" // the person refused on AudD's page
	StatusFailed   = "failed"   // see the error field
)

// AudD's endpoints.
const (
	DefaultAuthorizeURL = "https://dashboard.audd.io/api/oauth2/authorize"
	DefaultTokenURL     = "https://dashboard-api.audd.io/oauth2/token"
	DefaultRevokeURL    = "https://dashboard-api.audd.io/oauth2/revoke"
	DefaultMCPURL       = "https://mcp.audd.io"
)

const (
	scope = "token:read"
	// sessionTTL covers the page replacing an unused link after 9 minutes plus
	// the 10 minutes it waits after the click.
	sessionTTL  = 20 * time.Minute
	maxSessions = 5000
	// maxChannelSessions is how many sign-ins one channel can have in
	// progress; starting another drops its oldest.
	maxChannelSessions = 5
)

// Config configures a Service.
type Config struct {
	// ClientID is the OAuth client registered with AudD for the extension,
	// and RedirectURI the public address of Callback registered for it.
	ClientID    string
	RedirectURI string
	// CheckBroadcaster verifies a Twitch extension JWT. It returns the channel
	// ID and "" when the JWT is valid and belongs to the channel's
	// broadcaster, or the code ErrWrongJWT or ErrNotBroadcaster.
	CheckBroadcaster func(twitchJWT string) (channelID string, errCode string)
	// ChannelName returns the name of a Twitch channel to show to the person
	// signing in, or "" when it can't be found (optional).
	ChannelName func(channelID string) string
	// Report receives failures worth logging (optional).
	Report func(error)
	// HTTPClient calls AudD (a client with a 20 s timeout when nil).
	HTTPClient *http.Client
	// ClientVersion is reported to the MCP server as the client version.
	ClientVersion string
	// The AudD endpoints; the defaults above when empty.
	AuthorizeURL, TokenURL, RevokeURL, MCPURL string
}

type session struct {
	created  time.Time
	channel  string // the Twitch channel ID that started the sign-in
	verifier string
	status   string // StatusPending, StatusDone, StatusDeclined or StatusFailed
	working  bool   // Callback is exchanging the code
	apiToken string
	errCode  string
}

// Service holds the sign-ins in progress.
type Service struct {
	cfg      Config
	mu       sync.Mutex
	sessions map[string]*session
	now      func() time.Time
}

// New returns a Service.
func New(cfg Config) *Service {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	if cfg.AuthorizeURL == "" {
		cfg.AuthorizeURL = DefaultAuthorizeURL
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = DefaultTokenURL
	}
	if cfg.RevokeURL == "" {
		cfg.RevokeURL = DefaultRevokeURL
	}
	if cfg.MCPURL == "" {
		cfg.MCPURL = DefaultMCPURL
	}
	return &Service{cfg: cfg, sessions: map[string]*session{}, now: time.Now}
}

func (s *Service) report(err error) {
	if err != nil && s.cfg.Report != nil {
		s.cfg.Report(err)
	}
}

// sweep drops expired sessions. The caller holds s.mu.
func (s *Service) sweep() {
	limit := s.now().Add(-sessionTTL)
	for id, e := range s.sessions {
		if e.created.Before(limit) {
			delete(s.sessions, id)
		}
	}
}

func validSessionID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// apiHeaders sets the headers of the endpoints the configuration page calls
// and reports whether the request should be handled further.
func apiHeaders(w http.ResponseWriter, r *http.Request) bool {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Cache-Control", "no-store")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	if r.Method != http.MethodPost {
		h.Set("Allow", "POST, OPTIONS")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": ErrBadRequest})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ErrBadRequest})
		return false
	}
	return true
}

// broadcaster checks the request's jwt field and returns the channel ID, or
// writes the error response and returns "".
func (s *Service) broadcaster(w http.ResponseWriter, r *http.Request) string {
	twitchJWT := strings.TrimSpace(r.PostForm.Get("jwt"))
	if twitchJWT == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ErrBadRequest})
		return ""
	}
	channel, code := s.cfg.CheckBroadcaster(twitchJWT)
	if code == "" && channel == "" {
		code = ErrNotBroadcaster
	}
	if code != "" {
		status := http.StatusUnauthorized
		if code == ErrNotBroadcaster {
			status = http.StatusForbidden
		}
		writeJSON(w, status, map[string]string{"error": code})
		return ""
	}
	return channel
}

// Start is called by the configuration page before it shows the sign-in
// link: POST, form fields jwt (the broadcaster's Twitch extension JWT) and
// session (64 hex characters, the SHA-256 of the page's secret). It answers
// 200 {"status":"pending"}.
func (s *Service) Start(w http.ResponseWriter, r *http.Request) {
	if !apiHeaders(w, r) {
		return
	}
	id := r.PostForm.Get("session")
	if !validSessionID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ErrBadRequest})
		return
	}
	channel := s.broadcaster(w, r)
	if channel == "" {
		return
	}
	verifier, err := randomString(48)
	if err != nil {
		s.report(err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": ErrServiceUnavailable})
		return
	}
	s.mu.Lock()
	s.sweep()
	if e, exists := s.sessions[id]; exists {
		// Starting again is fine for the channel that started it; nobody else
		// learns whether the session exists.
		ok := e.channel == channel && e.status == StatusPending
		s.mu.Unlock()
		if ok {
			writeJSON(w, http.StatusOK, map[string]string{"status": StatusPending})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": ErrBadRequest})
		}
		return
	}
	// A channel keeps at most maxChannelSessions sign-ins; the oldest goes.
	var oldestID string
	var oldest time.Time
	count := 0
	for sid, e := range s.sessions {
		if e.channel != channel {
			continue
		}
		count++
		if oldestID == "" || e.created.Before(oldest) {
			oldestID, oldest = sid, e.created
		}
	}
	if count >= maxChannelSessions {
		delete(s.sessions, oldestID)
	}
	if len(s.sessions) >= maxSessions {
		s.mu.Unlock()
		s.report(errors.New("signin: too many sign-ins in progress"))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": ErrTooManySignIns})
		return
	}
	s.sessions[id] = &session{created: s.now(), channel: channel, verifier: verifier, status: StatusPending}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": StatusPending})
}

// Open is the sign-in link: GET ?session=<64 hex> redirects to AudD's
// approval page. It only works for a sign-in that was started and is still
// waiting.
func (s *Service) Open(w http.ResponseWriter, r *http.Request) {
	pageHeaders(w)
	id := r.URL.Query().Get("session")
	if r.Method != http.MethodGet || !validSessionID(id) {
		writePage(w, http.StatusBadRequest, pageExpired)
		return
	}
	s.mu.Lock()
	s.sweep()
	e, exists := s.sessions[id]
	if !exists || e.status != StatusPending || e.working {
		s.mu.Unlock()
		writePage(w, http.StatusGone, pageExpired)
		return
	}
	verifier := e.verifier
	s.mu.Unlock()

	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {s.cfg.ClientID},
		"redirect_uri":          {s.cfg.RedirectURI},
		"scope":                 {scope},
		"state":                 {id},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		"resource":              {s.cfg.MCPURL},
	}
	sep := "?"
	if strings.Contains(s.cfg.AuthorizeURL, "?") {
		sep = "&"
	}
	http.Redirect(w, r, s.cfg.AuthorizeURL+sep+q.Encode(), http.StatusFound)
}

// Callback is the OAuth redirect address. AudD sends the person here with GET
// after they approve; the page names the Twitch channel that started the
// sign-in and asks them to confirm. The confirmation comes back with POST,
// and only then is the code exchanged and the API token read.
func (s *Service) Callback(w http.ResponseWriter, r *http.Request) {
	pageHeaders(w)
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writePage(w, http.StatusMethodNotAllowed, pageExpired)
		return
	}
	q := r.URL.Query()
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if err := r.ParseForm(); err != nil {
			writePage(w, http.StatusBadRequest, pageExpired)
			return
		}
		q = r.PostForm
	}
	id, code := q.Get("state"), q.Get("code")
	s.mu.Lock()
	s.sweep()
	e, exists := s.sessions[id]
	if !exists || e.status != StatusPending || e.working {
		s.mu.Unlock()
		writePage(w, http.StatusGone, pageExpired)
		return
	}
	if oauthErr := q.Get("error"); oauthErr != "" || code == "" {
		if oauthErr == "access_denied" {
			e.status = StatusDeclined
			s.mu.Unlock()
			writePage(w, http.StatusOK, pageDeclined)
			return
		}
		e.status, e.errCode = StatusFailed, ErrServiceUnavailable
		s.mu.Unlock()
		s.report(errors.New("signin: authorization failed: " + oauthErr + " " + q.Get("error_description")))
		writePage(w, http.StatusOK, pageFailed)
		return
	}
	channel := e.channel
	if r.Method == http.MethodGet {
		s.mu.Unlock()
		name := ""
		if s.cfg.ChannelName != nil {
			name = s.cfg.ChannelName(channel)
		}
		writeConfirmPage(w, confirmData{Channel: name, ChannelID: channel, State: id, Code: code})
		return
	}
	if q.Get("decision") != "link" {
		e.status = StatusDeclined
		s.mu.Unlock()
		writePage(w, http.StatusOK, pageDeclined)
		return
	}
	e.working = true
	verifier := e.verifier
	s.mu.Unlock()

	apiToken, err := s.finish(r.Context(), code, verifier)

	s.mu.Lock()
	e.working = false
	if err != nil {
		e.status, e.errCode = StatusFailed, ErrServiceUnavailable
		var ce *codeError
		if errors.As(err, &ce) {
			e.errCode = ce.code
		}
	} else {
		e.status, e.apiToken = StatusDone, apiToken
	}
	s.mu.Unlock()
	if err != nil {
		s.report(err)
		writePage(w, http.StatusOK, pageFailed)
		return
	}
	writePage(w, http.StatusOK, pageDone)
}

// finish exchanges the code, reads the API token and revokes the sign-in.
func (s *Service) finish(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {s.cfg.RedirectURI},
		"client_id":     {s.cfg.ClientID},
		"code_verifier": {verifier},
		"resource":      {s.cfg.MCPURL},
	}
	status, body, err := s.postForm(ctx, s.cfg.TokenURL, form)
	if err != nil {
		return "", fail(ErrServiceUnavailable, "token request: %v", err)
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
	}
	_ = json.Unmarshal(body, &tok)
	if status != http.StatusOK || tok.AccessToken == "" {
		if tok.Error == "invalid_grant" {
			return "", fail(ErrSignInExpired, "token request: invalid_grant")
		}
		return "", fail(ErrServiceUnavailable, "token request: HTTP %d %s", status, tok.Error)
	}
	// The sign-in is only needed for this one read, so it is revoked whether
	// or not the read works.
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, t := range []string{tok.RefreshToken, tok.AccessToken} {
			if t != "" {
				_, _, _ = s.postForm(rctx, s.cfg.RevokeURL, url.Values{"token": {t}, "client_id": {s.cfg.ClientID}})
			}
		}
	}()
	client := &mcpClient{url: s.cfg.MCPURL, http: s.cfg.HTTPClient, token: tok.AccessToken, version: s.cfg.ClientVersion}
	return client.apiToken(ctx)
}

func (s *Service) postForm(ctx context.Context, address string, form url.Values) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "audd-twitch-extension/"+s.cfg.ClientVersion)
	resp, err := s.cfg.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, err
}

// Result is polled by the configuration page: POST, form fields jwt (the
// broadcaster's Twitch extension JWT) and secret. It answers 200 with
// {"status": ...}; a finished sign-in is returned once and then forgotten.
// Only the channel that started a sign-in can read it.
func (s *Service) Result(w http.ResponseWriter, r *http.Request) {
	if !apiHeaders(w, r) {
		return
	}
	secret := strings.TrimSpace(r.PostForm.Get("secret"))
	if len(secret) < 32 || len(secret) > 256 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ErrBadRequest})
		return
	}
	channel := s.broadcaster(w, r)
	if channel == "" {
		return
	}
	sum := sha256.Sum256([]byte(secret))
	id := hex.EncodeToString(sum[:])

	s.mu.Lock()
	s.sweep()
	e, exists := s.sessions[id]
	out := map[string]string{"status": StatusUnknown}
	if exists && e.channel == channel {
		out["status"] = e.status
		switch e.status {
		case StatusDone:
			out["api_token"] = e.apiToken
			delete(s.sessions, id)
		case StatusFailed:
			out["error"] = e.errCode
			delete(s.sessions, id)
		case StatusDeclined:
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// The pages shown in the tab the person signed in from.
type page struct{ Title, Text string }

var (
	pageDone = page{"Your AudD account is linked",
		"Go back to the Twitch tab: the extension finishes setting up by itself. You can close this tab."}
	pageDeclined = page{"Sign-in cancelled",
		"Nothing was shared with the extension. To try again, go back to the Twitch tab and choose Sign in with AudD."}
	pageExpired = page{"This sign-in link has expired",
		"Go back to the Twitch tab and choose Sign in with AudD again."}
	pageFailed = page{"Sign-in didn't work",
		"AudD couldn't finish signing you in. Go back to the Twitch tab and try again, or paste your API token there instead."}
)

func pageHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
}

const pageStart = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Title}} · AudD for Twitch</title>
<style>
:root { color-scheme: light dark; --bg: #f7f7f8; --text: #0e0e10; --text-2: #53535f; --line: #adadb8; --accent: #c2093a; --on-accent: #ffffff; }
@media (prefers-color-scheme: dark) { :root { --bg: #0e0e10; --text: #efeff1; --text-2: #adadb8; --line: #53535f; --accent: #e91e4f; } }
html, body { height: 100%; margin: 0; }
body { background: var(--bg); color: var(--text); font: 16px/1.5 Inter, Roobert, "Helvetica Neue", Helvetica, Arial, sans-serif;
  display: flex; align-items: center; justify-content: center; padding: 24px; box-sizing: border-box; }
main { max-width: 440px; }
p.brand { margin: 0 0 20px; font-size: 13px; font-weight: 600; color: var(--text-2); }
h1 { margin: 0 0 8px; font-size: 24px; line-height: 1.25; overflow-wrap: anywhere; }
p { margin: 0; color: var(--text-2); }
form { display: flex; flex-wrap: wrap; gap: 12px; margin-top: 24px; }
button { font: inherit; font-weight: 600; padding: 10px 18px; border-radius: 6px; cursor: pointer; }
button.primary { background: var(--accent); color: var(--on-accent); border: 1px solid transparent; }
button.secondary { background: transparent; color: var(--text); border: 1px solid var(--line); }
button:focus-visible { outline: 3px solid #9147ff; outline-offset: 2px; }
</style>
</head>
<body>
<main>
<p class="brand">AudD for Twitch</p>
`

const pageEnd = `</main>
</body>
</html>
`

var pageTemplate = template.Must(template.New("page").Parse(pageStart + `<h1>{{.Title}}</h1>
<p>{{.Text}}</p>
` + pageEnd))

type confirmData struct {
	Title     string
	Channel   string // the channel's name, "" when it couldn't be found
	ChannelID string
	State     string
	Code      string
}

var confirmTemplate = template.Must(template.New("confirm").Parse(pageStart + `<h1>Link your AudD account to {{if .Channel}}the Twitch channel {{.Channel}}{{else}}Twitch channel ID {{.ChannelID}}{{end}}?</h1>
<p>The extension on this channel will use your AudD account to recognize the music on its streams. If this isn't your channel, choose Cancel.</p>
<form method="post">
<input type="hidden" name="state" value="{{.State}}">
<input type="hidden" name="code" value="{{.Code}}">
<button class="primary" type="submit" name="decision" value="link">Link account</button>
<button class="secondary" type="submit" name="decision" value="cancel">Cancel</button>
</form>
` + pageEnd))

func writePage(w http.ResponseWriter, status int, p page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pageTemplate.Execute(w, p)
}

func writeConfirmPage(w http.ResponseWriter, d confirmData) {
	d.Title = "Link your AudD account"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = confirmTemplate.Execute(w, d)
}
