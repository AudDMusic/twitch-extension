// Package signin serves the endpoint the extension's configuration page calls
// after a streamer signs in with their AudD account.
//
// The page runs the OAuth device flow against AudD's authorization server
// itself (that server answers the extension's origin), then posts the access
// token here together with the Twitch extension JWT. This endpoint checks that
// the JWT belongs to the channel's broadcaster and reads the account's API
// token from the AudD MCP server, which does not answer browsers on the
// extension's origin. The page then links the account with the same
// twitch.php?save_user request it uses for a pasted token.
//
// Request:  POST, form fields jwt and access_token.
// Response: 200 {"api_token": "..."}, or an error status with
// {"error": "<code>"}; the codes are the Err* constants below.
package signin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt"
)

// Error codes in the {"error": ...} response.
const (
	ErrBadRequest         = "bad_request"                 // jwt or access_token missing
	ErrWrongJWT           = "wrong_jwt"                   // the Twitch JWT is invalid or expired
	ErrNotBroadcaster     = "not_broadcaster"             // the JWT is not the broadcaster's
	ErrSignInExpired      = "sign_in_expired"             // AudD did not accept the access token
	ErrPermissionMissing  = "token_permission_missing"    // the sign-in did not grant token:read
	ErrServiceUnavailable = "account_service_unavailable" // the MCP server failed or was unreachable
)

// DefaultMCPURL is the AudD MCP server.
const DefaultMCPURL = "https://mcp.audd.io"

// mcpProtocolVersion is the MCP revision this client speaks.
const mcpProtocolVersion = "2025-06-18"

// Config configures Handler.
type Config struct {
	// ExtensionSecret is the decoded Twitch extension secret that signs the
	// extension JWTs.
	ExtensionSecret []byte
	// MCPURL is the AudD MCP server (DefaultMCPURL when empty).
	MCPURL string
	// HTTPClient calls the MCP server (a client with a 20 s timeout when nil).
	HTTPClient *http.Client
	// ClientVersion is reported to the MCP server as the client version.
	ClientVersion string
}

// Handler returns the endpoint's handler.
func Handler(cfg Config) http.HandlerFunc {
	if cfg.MCPURL == "" {
		cfg.MCPURL = DefaultMCPURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		h.Set("Cache-Control", "no-store")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			h.Set("Allow", "POST, OPTIONS")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": ErrBadRequest})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if err := r.ParseForm(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": ErrBadRequest})
			return
		}
		twitchJWT := strings.TrimSpace(r.PostForm.Get("jwt"))
		accessToken := strings.TrimSpace(r.PostForm.Get("access_token"))
		if twitchJWT == "" || accessToken == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": ErrBadRequest})
			return
		}
		if code := checkBroadcaster(cfg.ExtensionSecret, twitchJWT); code != "" {
			status := http.StatusUnauthorized
			if code == ErrNotBroadcaster {
				status = http.StatusForbidden
			}
			writeJSON(w, status, map[string]string{"error": code})
			return
		}
		client := &mcpClient{url: cfg.MCPURL, http: cfg.HTTPClient, token: accessToken, version: cfg.ClientVersion}
		apiToken, err := client.apiToken(r.Context())
		if err != nil {
			var ce *codeError
			code := ErrServiceUnavailable
			if errors.As(err, &ce) {
				code = ce.code
			}
			status := http.StatusBadGateway
			switch code {
			case ErrSignInExpired:
				status = http.StatusUnauthorized
			case ErrPermissionMissing:
				status = http.StatusForbidden
			}
			writeJSON(w, status, map[string]string{"error": code})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"api_token": apiToken})
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// checkBroadcaster verifies a Twitch extension JWT and returns "" when it is
// valid and its role is broadcaster, or an error code.
func checkBroadcaster(secret []byte, token string) string {
	if len(secret) == 0 {
		return ErrWrongJWT
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return ErrWrongJWT
	}
	role, _ := claims["role"].(string)
	channel, _ := claims["channel_id"].(string)
	if role != "broadcaster" || channel == "" {
		return ErrNotBroadcaster
	}
	return ""
}

// codeError is an MCP failure mapped to a response code.
type codeError struct {
	code   string
	detail string
}

func (e *codeError) Error() string { return e.code + ": " + e.detail }

func fail(code, format string, args ...interface{}) error {
	return &codeError{code: code, detail: fmt.Sprintf(format, args...)}
}

// mcpClient is the minimum of an MCP Streamable HTTP client needed to call
// one tool: initialize, notifications/initialized, tools/call.
type mcpClient struct {
	url     string
	http    *http.Client
	token   string
	version string
	session string
	proto   string
	seq     int
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *mcpClient) apiToken(ctx context.Context) (string, error) {
	if err := c.initialize(ctx); err != nil {
		return "", err
	}
	defer c.close()
	msg, err := c.call(ctx, "tools/call", map[string]interface{}{"name": "get_api_token", "arguments": map[string]interface{}{}})
	if err != nil {
		return "", err
	}
	if msg.Error != nil {
		// Tools whose scope wasn't granted are not listed, so calling one
		// fails as an unknown tool.
		return "", fail(classifyToolError(msg.Error.Message), "get_api_token: %s", msg.Error.Message)
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent map[string]interface{} `json:"structuredContent"`
		IsError           bool                   `json:"isError"`
	}
	if err := json.Unmarshal(msg.Result, &res); err != nil {
		return "", fail(ErrServiceUnavailable, "cannot read the get_api_token result: %v", err)
	}
	var text []string
	for _, part := range res.Content {
		if part.Type == "text" {
			text = append(text, part.Text)
		}
	}
	if res.IsError {
		joined := strings.Join(text, " ")
		return "", fail(classifyToolError(joined), "get_api_token: %s", joined)
	}
	if t := tokenFrom(res.StructuredContent); t != "" {
		return t, nil
	}
	for _, s := range text {
		var m map[string]interface{}
		if json.Unmarshal([]byte(s), &m) == nil {
			if t := tokenFrom(m); t != "" {
				return t, nil
			}
		}
	}
	return "", fail(ErrServiceUnavailable, "get_api_token returned no api_token")
}

func tokenFrom(m map[string]interface{}) string {
	if m == nil {
		return ""
	}
	s, _ := m["api_token"].(string)
	return strings.TrimSpace(s)
}

// classifyToolError tells a missing token:read grant (the tool isn't listed
// for this sign-in, or the error names the scope) from other failures.
func classifyToolError(msg string) string {
	l := strings.ToLower(msg)
	for _, w := range []string{"unknown tool", "tool not found", "not allowed", "scope", "token:read"} {
		if strings.Contains(l, w) {
			return ErrPermissionMissing
		}
	}
	return ErrServiceUnavailable
}

func (c *mcpClient) initialize(ctx context.Context) error {
	msg, err := c.call(ctx, "initialize", map[string]interface{}{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "audd-twitch-extension", "version": c.version},
	})
	if err != nil {
		return err
	}
	if msg.Error != nil {
		return fail(ErrServiceUnavailable, "initialize: %s", msg.Error.Message)
	}
	var res struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(msg.Result, &res)
	c.proto = res.ProtocolVersion
	if c.proto == "" {
		c.proto = mcpProtocolVersion
	}
	_, err = c.send(ctx, -1, "notifications/initialized", nil)
	return err
}

// close ends the session; failures don't matter.
func (c *mcpClient) close() {
	if c.session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.url, nil)
	if err != nil {
		return
	}
	c.headers(req)
	if resp, err := c.http.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}
}

func (c *mcpClient) call(ctx context.Context, method string, params interface{}) (*rpcMessage, error) {
	c.seq++
	return c.send(ctx, c.seq, method, params)
}

func (c *mcpClient) headers(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "audd-twitch-extension/"+c.version)
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	if c.proto != "" {
		req.Header.Set("MCP-Protocol-Version", c.proto)
	}
}

// send posts one JSON-RPC message; id < 0 sends a notification.
func (c *mcpClient) send(ctx context.Context, id int, method string, params interface{}) (*rpcMessage, error) {
	body := map[string]interface{}{"jsonrpc": "2.0", "method": method}
	if params != nil {
		body["params"] = params
	}
	if id >= 0 {
		body["id"] = id
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(b))
	if err != nil {
		return nil, fail(ErrServiceUnavailable, "%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.headers(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fail(ErrServiceUnavailable, "%s: %v", method, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fail(ErrSignInExpired, "%s: HTTP 401", method)
	case resp.StatusCode == http.StatusForbidden:
		return nil, fail(ErrPermissionMissing, "%s: HTTP 403", method)
	case resp.StatusCode >= 300:
		return nil, fail(ErrServiceUnavailable, "%s: HTTP %d", method, resp.StatusCode)
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" && c.session == "" {
		c.session = sid
	}
	if id < 0 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, nil
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if ct == "text/event-stream" {
		return readEvent(resp.Body, id, method)
	}
	var msg rpcMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&msg); err != nil {
		return nil, fail(ErrServiceUnavailable, "%s: cannot read the response: %v", method, err)
	}
	return &msg, nil
}

// readEvent reads server-sent events until the response with id arrives.
func readEvent(r io.Reader, id int, method string) (*rpcMessage, error) {
	sc := bufio.NewScanner(io.LimitReader(r, 1<<20))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	want := fmt.Sprint(id)
	var data []string
	flush := func() *rpcMessage {
		defer func() { data = data[:0] }()
		if len(data) == 0 {
			return nil
		}
		var m rpcMessage
		if json.Unmarshal([]byte(strings.Join(data, "\n")), &m) != nil {
			return nil
		}
		if strings.TrimSpace(string(m.ID)) == want && (m.Result != nil || m.Error != nil) {
			return &m
		}
		return nil
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if m := flush(); m != nil {
				return m, nil
			}
			continue
		}
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(v, " "))
		}
	}
	if m := flush(); m != nil {
		return m, nil
	}
	return nil, fail(ErrServiceUnavailable, "%s: the event stream ended without a response", method)
}
