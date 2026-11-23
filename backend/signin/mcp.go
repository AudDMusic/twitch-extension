package signin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"strings"
	"time"
)

// mcpProtocolVersion is the MCP revision this client speaks.
const mcpProtocolVersion = "2025-06-18"

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
		_, _ = io.Copy(ioutil.Discard, io.LimitReader(resp.Body, 4096))
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
		_, _ = io.Copy(ioutil.Discard, io.LimitReader(resp.Body, 4096))
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
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if m := flush(); m != nil {
		return m, nil
	}
	return nil, fail(ErrServiceUnavailable, "%s: the event stream ended without a response", method)
}
