package signin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt"
)

var testSecret = []byte("test-extension-secret")

func twitchJWT(t *testing.T, secret []byte, role string, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"exp":        exp.Unix(),
		"channel_id": "57165160",
		"user_id":    "57165160",
		"role":       role,
	})
	s, err := tok.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fakeMCP is a minimal AudD MCP server: one access token, get_api_token
// listed only when the token has token:read.
type fakeMCP struct {
	mu          sync.Mutex
	token       string
	tokenRead   bool
	sse         bool
	status      int    // non-zero: answer every request with this status
	toolIsError string // non-empty: get_api_token returns isError with this text
	textOnly    bool   // put the token only in the text content
	calls       []string
	deleted     bool
}

func (f *fakeMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodDelete {
		f.deleted = true
		w.WriteHeader(http.StatusOK)
		return
	}
	var msg struct {
		ID     *int                   `json:"id"`
		Method string                 `json:"method"`
		Params map[string]interface{} `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&msg)
	f.calls = append(f.calls, msg.Method)
	if msg.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != "session-1" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if msg.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result interface{}
	var rpcErr interface{}
	switch msg.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "session-1")
		result = map[string]interface{}{"protocolVersion": "2025-06-18", "capabilities": map[string]interface{}{"tools": map[string]interface{}{}}}
	case "tools/call":
		name, _ := msg.Params["name"].(string)
		switch {
		case name != "get_api_token" || !f.tokenRead:
			rpcErr = map[string]interface{}{"code": -32602, "message": "Unknown tool: " + name}
		case f.toolIsError != "":
			result = map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "text", "text": f.toolIsError}}, "isError": true}
		case f.textOnly:
			result = map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "text", "text": `{"api_token":"apitoken123"}`}}}
		default:
			result = map[string]interface{}{
				"content":           []interface{}{map[string]interface{}{"type": "text", "text": `{"api_token":"apitoken123"}`}},
				"structuredContent": map[string]interface{}{"api_token": "apitoken123"},
			}
		}
	}
	resp := map[string]interface{}{"jsonrpc": "2.0", "id": *msg.ID}
	if rpcErr != nil {
		resp["error"] = rpcErr
	} else {
		resp["result"] = result
	}
	b, _ := json.Marshal(resp)
	if f.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func post(t *testing.T, h http.Handler, form url.Values) (int, map[string]string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/lastSong/auddApiToken/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("missing CORS header")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("responses must not be cached")
	}
	return rec.Code, body
}

func TestReadsTheAPIToken(t *testing.T) {
	for _, sse := range []bool{false, true} {
		for _, textOnly := range []bool{false, true} {
			f := &fakeMCP{token: "access-1", tokenRead: true, sse: sse, textOnly: textOnly}
			srv := httptest.NewServer(f)
			h := Handler(Config{ExtensionSecret: testSecret, MCPURL: srv.URL})
			code, body := post(t, h, url.Values{"jwt": {twitchJWT(t, testSecret, "broadcaster", time.Now().Add(time.Hour))}, "access_token": {"access-1"}})
			srv.Close()
			if code != http.StatusOK || body["api_token"] != "apitoken123" {
				t.Fatalf("sse=%v textOnly=%v: got %d %v", sse, textOnly, code, body)
			}
			want := "initialize,notifications/initialized,tools/call"
			if got := strings.Join(f.calls, ","); got != want {
				t.Errorf("calls = %s, want %s", got, want)
			}
			if !f.deleted {
				t.Errorf("the MCP session was not closed")
			}
		}
	}
}

func TestErrors(t *testing.T) {
	good := func(t *testing.T) string { return twitchJWT(t, testSecret, "broadcaster", time.Now().Add(time.Hour)) }
	cases := []struct {
		name   string
		mcp    *fakeMCP
		form   func(t *testing.T) url.Values
		status int
		code   string
	}{
		{"missing access token", &fakeMCP{}, func(t *testing.T) url.Values { return url.Values{"jwt": {good(t)}} }, 400, ErrBadRequest},
		{"missing jwt", &fakeMCP{}, func(t *testing.T) url.Values { return url.Values{"access_token": {"a"}} }, 400, ErrBadRequest},
		{"jwt signed with another secret", &fakeMCP{}, func(t *testing.T) url.Values {
			return url.Values{"jwt": {twitchJWT(t, []byte("other"), "broadcaster", time.Now().Add(time.Hour))}, "access_token": {"a"}}
		}, 401, ErrWrongJWT},
		{"expired jwt", &fakeMCP{}, func(t *testing.T) url.Values {
			return url.Values{"jwt": {twitchJWT(t, testSecret, "broadcaster", time.Now().Add(-time.Minute))}, "access_token": {"a"}}
		}, 401, ErrWrongJWT},
		{"viewer jwt", &fakeMCP{}, func(t *testing.T) url.Values {
			return url.Values{"jwt": {twitchJWT(t, testSecret, "viewer", time.Now().Add(time.Hour))}, "access_token": {"a"}}
		}, 403, ErrNotBroadcaster},
		{"access token not accepted", &fakeMCP{token: "other", tokenRead: true}, func(t *testing.T) url.Values {
			return url.Values{"jwt": {good(t)}, "access_token": {"access-1"}}
		}, 401, ErrSignInExpired},
		{"token:read not granted", &fakeMCP{token: "access-1"}, func(t *testing.T) url.Values {
			return url.Values{"jwt": {good(t)}, "access_token": {"access-1"}}
		}, 403, ErrPermissionMissing},
		{"tool error mentioning the scope", &fakeMCP{token: "access-1", tokenRead: true, toolIsError: "requires the token:read scope"}, func(t *testing.T) url.Values {
			return url.Values{"jwt": {good(t)}, "access_token": {"access-1"}}
		}, 403, ErrPermissionMissing},
		{"tool error", &fakeMCP{token: "access-1", tokenRead: true, toolIsError: "database is down"}, func(t *testing.T) url.Values {
			return url.Values{"jwt": {good(t)}, "access_token": {"access-1"}}
		}, 502, ErrServiceUnavailable},
		{"server error", &fakeMCP{status: 503}, func(t *testing.T) url.Values {
			return url.Values{"jwt": {good(t)}, "access_token": {"access-1"}}
		}, 502, ErrServiceUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(c.mcp)
			defer srv.Close()
			h := Handler(Config{ExtensionSecret: testSecret, MCPURL: srv.URL})
			code, body := post(t, h, c.form(t))
			if code != c.status || body["error"] != c.code {
				t.Fatalf("got %d %v, want %d %s", code, body, c.status, c.code)
			}
			if body["api_token"] != "" {
				t.Fatalf("an error response carried a token")
			}
		})
	}
}

func TestUnreachableMCP(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close() // nothing listens there now
	h := Handler(Config{ExtensionSecret: testSecret, MCPURL: u, HTTPClient: &http.Client{Timeout: 2 * time.Second}})
	code, body := post(t, h, url.Values{"jwt": {twitchJWT(t, testSecret, "broadcaster", time.Now().Add(time.Hour))}, "access_token": {"a"}})
	if code != http.StatusBadGateway || body["error"] != ErrServiceUnavailable {
		t.Fatalf("got %d %v", code, body)
	}
}

func TestMethods(t *testing.T) {
	h := Handler(Config{ExtensionSecret: testSecret})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/", nil))
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("OPTIONS: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
}

func TestEmptySecretRefusesEveryJWT(t *testing.T) {
	if checkBroadcaster(nil, twitchJWT(t, []byte(""), "broadcaster", time.Now().Add(time.Hour))) != ErrWrongJWT {
		t.Fatal("a JWT was accepted without an extension secret")
	}
}
