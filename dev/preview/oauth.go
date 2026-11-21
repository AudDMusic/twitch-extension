package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// A fake of AudD's OAuth authorization server (device flow only) and of the
// MCP server's get_api_token tool, close enough to the real ones for the
// configuration page: the same endpoints, errors and polling rules.

const fakeClientID = "audd-twitch"

type device struct {
	code, userCode string
	scope          string
	expires        time.Time
	state          string // pending, approved, denied, redeemed
	apiToken       string
	grantedScope   string
}

type grant struct {
	scope    string
	apiToken string
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func (h *harness) oauthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/mock/oauth2/device_authorization", h.deviceAuthorization)
	mux.HandleFunc("/mock/oauth2/token", h.token)
	mux.HandleFunc("/mock/oauth2/revoke", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		delete(h.tokens, r.FormValue("token"))
		h.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/mock/device", h.approvePage)
	mux.HandleFunc("/mock/mcp", h.mcp)
}

func (h *harness) deviceAuthorization(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.FormValue("client_id") != fakeClientID {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}
	scope := r.FormValue("scope")
	if !strings.Contains(" "+scope+" ", " token:read ") {
		oauthError(w, http.StatusBadRequest, "invalid_scope", "unknown or unsupported scope")
		return
	}
	d := &device{code: randomHex(16), userCode: userCode(), scope: scope, expires: time.Now().Add(15 * time.Minute), state: "pending"}
	h.mu.Lock()
	h.devices[d.code] = d
	h.codes[d.userCode] = d.code
	h.mu.Unlock()
	base := baseURL(r) + "/mock/device"
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"device_code":               d.code,
		"user_code":                 d.userCode,
		"verification_uri":          base,
		"verification_uri_complete": base + "?user_code=" + d.userCode,
		"expires_in":                900,
		"interval":                  2,
	})
}

func userCode() string {
	const letters = "BCDFGHJKLMNPQRSTVWXZ"
	b := []byte(randomHex(4)) // 8 characters, shown as XXXX-XXXX
	out := make([]byte, 0, 9)
	for i, c := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, letters[int(c)%len(letters)])
	}
	return string(out)
}

func (h *harness) token(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("client_id") != fakeClientID {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}
	if r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.devices[r.FormValue("device_code")]
	switch {
	case d == nil || d.state == "redeemed":
		oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown or used device code")
	case time.Now().After(d.expires):
		oauthError(w, http.StatusBadRequest, "expired_token", "the device code expired")
	case d.state == "denied":
		oauthError(w, http.StatusBadRequest, "access_denied", "the user denied the request")
	case d.state == "pending":
		oauthError(w, http.StatusBadRequest, "authorization_pending", "")
	default:
		d.state = "redeemed"
		access := "at-" + randomHex(12)
		h.tokens[access] = &grant{scope: d.grantedScope, apiToken: d.apiToken}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"access_token":  access,
			"refresh_token": "rt-" + randomHex(12),
			"token_type":    "Bearer",
			"expires_in":    3600,
			"scope":         d.grantedScope,
		})
	}
}

// approvePage stands in for dashboard.audd.io's approval page.
func (h *harness) approvePage(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.FormValue("user_code")))
	h.mu.Lock()
	d := h.devices[h.codes[code]]
	h.mu.Unlock()
	if d == nil {
		page(w, "Sign in to AudD (mock)", `<form><label>Code <input name="user_code" autocomplete="off"></label> <button>Continue</button></form>`)
		return
	}
	if r.Method == http.MethodPost {
		h.mu.Lock()
		if r.FormValue("decision") == "approve" {
			d.state = "approved"
			if r.FormValue("token_read") == "on" {
				d.grantedScope = "token:read"
			}
			d.apiToken = r.FormValue("account")
		} else {
			d.state = "denied"
		}
		h.mu.Unlock()
		page(w, "Done (mock)", `<p>You can close this tab and go back to Twitch.</p>`)
		return
	}
	page(w, "Approve sign-in (mock)", fmt.Sprintf(`<p>Check that this code matches the one on Twitch:</p><p class="mock-code">%s</p>
<form method="post">
<input type="hidden" name="user_code" value="%s">
<p><strong>AudD for Twitch</strong> wants to:</p>
<p><label><input type="checkbox" name="token_read" checked> Read your API token</label></p>
<p>Sign in as:</p>
<p><label><input type="radio" name="account" value="good-token" checked> An account with music recognition on</label><br>
<label><input type="radio" name="account" value="expired-token"> An account whose recognition ended</label><br>
<label><input type="radio" name="account" value="unpaid-token"> An account that never turned it on</label></p>
<p><button name="decision" value="approve">Approve</button> <button name="decision" value="deny">Deny</button></p>
</form>`, html.EscapeString(d.userCode), html.EscapeString(d.userCode)))
}

// mcp is a fake AudD MCP server with one tool, get_api_token, listed only
// for tokens granted token:read. Replies use server-sent events like the
// real server may.
func (h *harness) mcp(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	h.mu.Lock()
	g := h.tokens[token]
	h.mu.Unlock()
	if g == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="AudD", error="invalid_token"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusOK)
		return
	}
	var msg struct {
		ID     *int                   `json:"id"`
		Method string                 `json:"method"`
		Params map[string]interface{} `json:"params"`
	}
	json.NewDecoder(r.Body).Decode(&msg)
	if msg.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	resp := map[string]interface{}{"jsonrpc": "2.0", "id": *msg.ID}
	switch msg.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "preview-"+randomHex(6))
		resp["result"] = map[string]interface{}{"protocolVersion": "2025-06-18", "capabilities": map[string]interface{}{"tools": map[string]interface{}{}}, "serverInfo": map[string]string{"name": "audd-preview", "version": "0"}}
	case "tools/call":
		name, _ := msg.Params["name"].(string)
		if name != "get_api_token" || !strings.Contains(" "+g.scope+" ", " token:read ") {
			resp["error"] = map[string]interface{}{"code": -32602, "message": "Unknown tool: " + name}
		} else {
			resp["result"] = map[string]interface{}{
				"content":           []interface{}{map[string]string{"type": "text", "text": `{"api_token":"` + g.apiToken + `"}`}},
				"structuredContent": map[string]string{"api_token": g.apiToken},
				"isError":           false,
			}
		}
	default:
		resp["error"] = map[string]interface{}{"code": -32601, "message": "Method not found"}
	}
	b, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
}
