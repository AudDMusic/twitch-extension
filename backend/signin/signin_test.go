package signin

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAudD stands in for AudD's token and revocation endpoints and the MCP
// server.
type fakeAudD struct {
	t          *testing.T
	mu         sync.Mutex
	challenge  string // the code_challenge of the authorization request
	tokenError string
	mcpStatus  int
	revoked    []string
	srv        *httptest.Server
}

func newFakeAudD(t *testing.T) *fakeAudD {
	f := &fakeAudD{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		f.mu.Lock()
		defer f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		ok := r.PostForm.Get("grant_type") == "authorization_code" && r.PostForm.Get("code") == "good-code" &&
			r.PostForm.Get("client_id") == "client-1" && r.PostForm.Get("redirect_uri") == "https://ebs.example/callback/" &&
			base64.RawURLEncoding.EncodeToString(sum[:]) == f.challenge
		if f.tokenError != "" || !ok {
			e := f.tokenError
			if e == "" {
				e = "invalid_grant"
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": e})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "access-1", "refresh_token": "refresh-1"})
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		f.mu.Unlock()
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		status := f.mcpStatus
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer access-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if r.Method == http.MethodDelete {
			return
		}
		var msg struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&msg)
		w.Header().Set("Mcp-Session-Id", "s1")
		if msg.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result interface{}
		switch msg.Method {
		case "initialize":
			result = map[string]interface{}{"protocolVersion": mcpProtocolVersion}
		case "tools/call":
			result = map[string]interface{}{"content": []map[string]string{{"type": "text", "text": `{"api_token":"api-token-1"}`}}}
		}
		// Answer tools/call as an event stream and the rest as plain JSON.
		b, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": *msg.ID, "result": result})
		if msg.Method == "tools/call" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message\ndata: " + string(b) + "\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAudD) service() *Service {
	return New(Config{
		ClientID:    "client-1",
		RedirectURI: "https://ebs.example/callback/",
		CheckBroadcaster: func(jwt string) (string, string) {
			switch jwt {
			case "broadcaster":
				return "1001", ""
			case "other-broadcaster":
				return "2002", ""
			case "viewer":
				return "", ErrNotBroadcaster
			}
			return "", ErrWrongJWT
		},
		ChannelName: func(id string) string {
			if id == "1001" {
				return "some<b>streamer"
			}
			return ""
		},
		AuthorizeURL: "https://audd.example/authorize",
		TokenURL:     f.srv.URL + "/token",
		RevokeURL:    f.srv.URL + "/revoke",
		MCPURL:       f.srv.URL + "/mcp",
	})
}

const testSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func sessionOf(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func post(h http.HandlerFunc, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func start(t *testing.T, s *Service, jwt, session string) (int, map[string]string) {
	t.Helper()
	w := post(s.Start, url.Values{"jwt": {jwt}, "session": {session}})
	out := map[string]string{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("start: %v in %q", err, w.Body.String())
	}
	return w.Code, out
}

// open starts a sign-in as "broadcaster", follows the page's link and returns
// the authorization request.
func (f *fakeAudD) open(t *testing.T, s *Service, session string) url.Values {
	t.Helper()
	if code, out := start(t, s, "broadcaster", session); code != http.StatusOK || out["status"] != StatusPending {
		t.Fatalf("start: %d %v", code, out)
	}
	w := httptest.NewRecorder()
	s.Open(w, httptest.NewRequest("GET", "/lastSong/signIn/open/?session="+session, nil))
	if w.Code != http.StatusFound {
		t.Fatalf("open: status %d, body %s", w.Code, w.Body.String())
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil || !strings.HasPrefix(u.String(), "https://audd.example/authorize?") {
		t.Fatalf("open: redirected to %q", w.Header().Get("Location"))
	}
	q := u.Query()
	f.mu.Lock()
	f.challenge = q.Get("code_challenge")
	f.mu.Unlock()
	return q
}

// arrive is AudD sending the person back to the callback address.
func arrive(s *Service, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.Callback(w, httptest.NewRequest("GET", "/lastSong/signIn/callback/?"+query, nil))
	return w
}

// callback is the person arriving from AudD and confirming the channel.
func callback(s *Service, query string) *httptest.ResponseRecorder {
	w := arrive(s, query)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `name="decision" value="link"`) {
		return w
	}
	form, _ := url.ParseQuery(query)
	form.Set("decision", "link")
	return post(s.Callback, form)
}

func result(t *testing.T, s *Service, jwt, secret string) (int, map[string]string) {
	t.Helper()
	w := post(s.Result, url.Values{"jwt": {jwt}, "secret": {secret}})
	out := map[string]string{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("result: %v in %q", err, w.Body.String())
	}
	return w.Code, out
}

func TestSignIn(t *testing.T) {
	f := newFakeAudD(t)
	s := f.service()
	session := sessionOf(testSecret)

	if _, out := result(t, s, "broadcaster", testSecret); out["status"] != StatusUnknown {
		t.Fatalf("before open: %v", out)
	}
	q := f.open(t, s, session)
	for k, want := range map[string]string{"response_type": "code", "client_id": "client-1", "scope": "token:read",
		"redirect_uri": "https://ebs.example/callback/", "state": session, "code_challenge_method": "S256"} {
		if q.Get(k) != want {
			t.Errorf("authorization request: %s = %q, want %q", k, q.Get(k), want)
		}
	}
	if _, out := result(t, s, "broadcaster", testSecret); out["status"] != StatusPending {
		t.Fatalf("after open: %v", out)
	}
	// Opening the link again keeps the same sign-in.
	if again := f.open(t, s, session); again.Get("code_challenge") != q.Get("code_challenge") {
		t.Error("reopening the link changed the code challenge")
	}

	// Arriving from AudD only asks to confirm the channel, by name; nothing is
	// read yet.
	w := arrive(s, "code=good-code&state="+session)
	if !strings.Contains(w.Body.String(), "the Twitch channel some&lt;b&gt;streamer?") {
		t.Fatalf("confirmation page: %s", w.Body.String())
	}
	if _, out := result(t, s, "broadcaster", testSecret); out["status"] != StatusPending {
		t.Fatalf("before confirming: %v", out)
	}
	w = callback(s, "code=good-code&state="+session)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "account is linked") {
		t.Fatalf("callback: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("callback page has no Referrer-Policy: no-referrer")
	}
	f.mu.Lock()
	revoked := strings.Join(f.revoked, ",")
	f.mu.Unlock()
	if revoked != "refresh-1,access-1" {
		t.Errorf("revoked %q", revoked)
	}

	// Only the broadcaster who started the sign-in and holds the secret gets
	// the token, once.
	if _, out := result(t, s, "other-broadcaster", testSecret); out["status"] != StatusUnknown || out["api_token"] != "" {
		t.Errorf("another channel: %v", out)
	}
	if code, out := result(t, s, "viewer", testSecret); code != http.StatusForbidden || out["error"] != ErrNotBroadcaster {
		t.Errorf("viewer: %d %v", code, out)
	}
	if code, out := result(t, s, "garbage", testSecret); code != http.StatusUnauthorized || out["error"] != ErrWrongJWT {
		t.Errorf("bad jwt: %d %v", code, out)
	}
	if _, out := result(t, s, "broadcaster", strings.Repeat("f", 64)); out["status"] != StatusUnknown || out["api_token"] != "" {
		t.Errorf("other secret: %v", out)
	}
	if _, out := result(t, s, "broadcaster", testSecret); out["status"] != StatusDone || out["api_token"] != "api-token-1" {
		t.Fatalf("done: %v", out)
	}
	if _, out := result(t, s, "broadcaster", testSecret); out["status"] != StatusUnknown || out["api_token"] != "" {
		t.Errorf("second read: %v", out)
	}
	// The code can't be replayed and the link is used up.
	if w := callback(s, "code=good-code&state="+session); w.Code != http.StatusGone {
		t.Errorf("replayed callback: %d", w.Code)
	}
}

func TestDeclined(t *testing.T) {
	session := sessionOf(testSecret)
	t.Run("on AudD's page", func(t *testing.T) {
		f := newFakeAudD(t)
		s := f.service()
		f.open(t, s, session)
		if w := callback(s, "error=access_denied&state="+session); !strings.Contains(w.Body.String(), "cancelled") {
			t.Fatalf("callback: %s", w.Body.String())
		}
		if _, out := result(t, s, "broadcaster", testSecret); out["status"] != StatusDeclined {
			t.Fatalf("result: %v", out)
		}
	})
	t.Run("on the channel confirmation", func(t *testing.T) {
		f := newFakeAudD(t)
		s := f.service()
		f.open(t, s, session)
		w := post(s.Callback, url.Values{"code": {"good-code"}, "state": {session}, "decision": {"cancel"}})
		if !strings.Contains(w.Body.String(), "cancelled") {
			t.Fatalf("callback: %s", w.Body.String())
		}
		if _, out := result(t, s, "broadcaster", testSecret); out["status"] != StatusDeclined {
			t.Fatalf("result: %v", out)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.revoked) != 0 {
			t.Error("the code was exchanged after a cancel")
		}
	})
}

func TestStart(t *testing.T) {
	f := newFakeAudD(t)
	s := f.service()
	session := sessionOf(testSecret)
	// The link doesn't work before the sign-in is started.
	w := httptest.NewRecorder()
	s.Open(w, httptest.NewRequest("GET", "/lastSong/signIn/open/?session="+session, nil))
	if w.Code != http.StatusGone {
		t.Errorf("open before start: %d", w.Code)
	}
	if code, out := start(t, s, "viewer", session); code != http.StatusForbidden || out["error"] != ErrNotBroadcaster {
		t.Errorf("viewer: %d %v", code, out)
	}
	if code, out := start(t, s, "garbage", session); code != http.StatusUnauthorized || out["error"] != ErrWrongJWT {
		t.Errorf("bad jwt: %d %v", code, out)
	}
	if code, _ := start(t, s, "broadcaster", "not-a-session"); code != http.StatusBadRequest {
		t.Errorf("bad session: %d", code)
	}
	if code, _ := start(t, s, "broadcaster", session); code != http.StatusOK {
		t.Fatalf("start: %d", code)
	}
	if code, _ := start(t, s, "broadcaster", session); code != http.StatusOK {
		t.Errorf("starting again: %d", code)
	}
	if code, _ := start(t, s, "other-broadcaster", session); code != http.StatusBadRequest {
		t.Errorf("another channel taking the session: %d", code)
	}
	// A channel keeps a limited number of sign-ins; the oldest is dropped.
	now := time.Now()
	s.now = func() time.Time { now = now.Add(time.Second); return now }
	for i := 0; i < maxChannelSessions; i++ {
		if code, _ := start(t, s, "broadcaster", sessionOf(strings.Repeat(string(rune('a'+i)), 64))); code != http.StatusOK {
			t.Fatalf("start %d: %d", i, code)
		}
	}
	if len(s.sessions) != maxChannelSessions {
		t.Errorf("%d sessions kept, want %d", len(s.sessions), maxChannelSessions)
	}
	if _, kept := s.sessions[session]; kept {
		t.Error("the oldest session was not dropped")
	}
}

func TestFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		tokenError string
		mcpStatus  int
		code       string
		want       string
	}{
		"wrong code":         {code: "bad-code", want: ErrSignInExpired},
		"token server error": {code: "good-code", tokenError: "server_error", want: ErrServiceUnavailable},
		"mcp down":           {code: "good-code", mcpStatus: 502, want: ErrServiceUnavailable},
		"scope not granted":  {code: "good-code", mcpStatus: 403, want: ErrPermissionMissing},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeAudD(t)
			f.tokenError, f.mcpStatus = tc.tokenError, tc.mcpStatus
			s := f.service()
			session := sessionOf(testSecret)
			f.open(t, s, session)
			if w := callback(s, "code="+tc.code+"&state="+session); !strings.Contains(w.Body.String(), "didn&#39;t work") {
				t.Fatalf("callback: %s", w.Body.String())
			}
			if _, out := result(t, s, "broadcaster", testSecret); out["status"] != StatusFailed || out["error"] != tc.want {
				t.Fatalf("result: %v, want error %s", out, tc.want)
			}
		})
	}
}

func TestExpiryAndBadInput(t *testing.T) {
	f := newFakeAudD(t)
	s := f.service()
	now := time.Now()
	s.now = func() time.Time { return now }
	session := sessionOf(testSecret)
	f.open(t, s, session)
	now = now.Add(sessionTTL + time.Second)
	if w := callback(s, "code=good-code&state="+session); w.Code != http.StatusGone {
		t.Errorf("expired callback: %d", w.Code)
	}
	if _, out := result(t, s, "broadcaster", testSecret); out["status"] != StatusUnknown {
		t.Errorf("expired result: %v", out)
	}
	for _, bad := range []string{"", "abc", strings.Repeat("G", 64), strings.Repeat("a", 65)} {
		w := httptest.NewRecorder()
		s.Open(w, httptest.NewRequest("GET", "/lastSong/signIn/open/?session="+bad, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("open with session %q: %d", bad, w.Code)
		}
	}
	if code, out := result(t, s, "broadcaster", "short"); code != http.StatusBadRequest || out["error"] != ErrBadRequest {
		t.Errorf("short secret: %d %v", code, out)
	}
	if w := callback(s, "code=x&state=unknown"); w.Code != http.StatusGone {
		t.Errorf("unknown state: %d", w.Code)
	}
}
