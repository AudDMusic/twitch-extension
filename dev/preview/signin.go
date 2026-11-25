package main

import (
	"crypto/sha256"
	"encoding/hex"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A mock of the backend's Sign in with AudD endpoints:
//
//	POST signIn/start/   jwt, session   {"status":"pending"}, or 400, 401, 403, 429
//	GET  signIn/open/?session=...       the tab the configuration page opens
//	POST signIn/result/  jwt, secret    {"status":"unknown"|"pending"|"done"|"declined"|"failed"}
//
// The real backend sends the tab to AudD's approval page, then shows its own
// page asking to link the account to the channel. Here a stand-in page does
// AudD's part (pick the account and how the sign-in ends) and a second one
// the backend's confirmation.

const signInTTL = 10 * time.Minute

type signInSession struct {
	channel  string
	started  time.Time
	state    string // started, opened, approved, done, declined, failed
	apiToken string
	errCode  string
}

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// The errors the stand-in AudD page can end a sign-in with.
var signInErrors = []string{"sign_in_expired", "token_permission_missing", "account_service_unavailable"}

func (h *harness) signInRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/mock/lastSong/signIn/start/", h.signInStart)
	mux.HandleFunc("/mock/lastSong/signIn/open/", h.signInOpen)
	mux.HandleFunc("/mock/lastSong/signIn/result/", h.signInResult)
	// signin=nobackend: /mock/lastSong-old/ has no sign-in endpoints (404).
	// signin=down: every sign-in endpoint answers a gateway error.
	mux.HandleFunc("/mock/lastSong-down/signIn/", badGateway)
	// signin=busy: the backend refuses to start more sign-ins.
	mux.HandleFunc("/mock/lastSong-busy/signIn/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too_many_sign_ins"})
	})
	// signin=noresult: the sign-in starts and the AudD tab works, but the
	// result can't be fetched.
	mux.HandleFunc("/mock/lastSong-noresult/signIn/start/", h.signInStart)
	mux.HandleFunc("/mock/lastSong-noresult/signIn/open/", h.signInOpen)
	mux.HandleFunc("/mock/lastSong-noresult/signIn/result/", badGateway)
	// signin=linked: the backend links the account itself when the sign-in
	// finishes and sends the account with the result, so the page doesn't ask
	// twitch.php (which may be failing: account=stuck).
	mux.HandleFunc("/mock/lastSong-linked/signIn/start/", h.signInStart)
	mux.HandleFunc("/mock/lastSong-linked/signIn/open/", h.signInOpen)
	mux.HandleFunc("/mock/lastSong-linked/signIn/result/", h.signInResult)
}

// linkHere is the backend linking the account that owns the token to the
// channel, and the account's state as twitch.php would answer. The caller
// holds h.mu.
func (h *harness) linkHere(channel, apiToken string) interface{} {
	a := h.accounts[channel]
	if a == nil {
		a = &account{ChannelID: channel, UserID: strconv.Itoa(1000 + len(h.accounts))}
		h.accounts[channel] = a
	}
	switch apiToken {
	case "expired-token":
		a.State = "expired"
	case "unpaid-token":
		a.State = "unpaid"
	default:
		a.State = "active"
	}
	if a.State == "active" {
		return []interface{}{true, a.fields(channel)}
	}
	return []interface{}{false, "zero_limit", a.fields(channel)}
}

func badGateway(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusBadGateway)
	w.Write([]byte("<html><body>502 Bad Gateway</body></html>"))
}

// signInCaller checks the form's jwt (the channel's broadcaster) and the
// 64-hex-digit value of the named field, and answers the error itself.
func signInCaller(w http.ResponseWriter, r *http.Request, field string) (channel string, ok bool) {
	token := r.FormValue("jwt")
	if r.Method != http.MethodPost || token == "" || !hex64.MatchString(r.FormValue(field)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return "", false
	}
	claims := claimsOf(token)
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong_jwt"})
		return "", false
	}
	if role, _ := claims["role"].(string); role != "broadcaster" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not_broadcaster"})
		return "", false
	}
	channel, _ = claims["channel_id"].(string)
	return channel, true
}

func (h *harness) signInStart(w http.ResponseWriter, r *http.Request) {
	channel, ok := signInCaller(w, r, "session")
	if !ok {
		return
	}
	h.mu.Lock()
	h.sessions[r.FormValue("session")] = &signInSession{channel: channel, started: time.Now(), state: "started"}
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "pending"})
}

func (h *harness) signInOpen(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	s := h.sessions[r.FormValue("session")]
	if s != nil && time.Since(s.started) > signInTTL {
		s = nil
	}
	if s == nil {
		h.mu.Unlock()
		page(w, "This sign-in link has expired (stand-in)", `<p>The backend shows this for a link the configuration page didn't start, or one that ran out. Go back to Twitch and sign in again.</p>`)
		return
	}
	decision := ""
	if r.Method == http.MethodPost {
		decision = r.FormValue("decision")
	}
	switch {
	case decision == "approve" && (s.state == "started" || s.state == "opened"):
		s.state = "approved"
		s.apiToken = r.FormValue("account")
	case decision == "decline" && (s.state == "started" || s.state == "opened"):
		s.state = "declined"
	case decision == "link" && s.state == "approved":
		s.state = "done"
	case decision != "" && (s.state == "started" || s.state == "opened"):
		for _, code := range signInErrors {
			if decision == code {
				s.state = "failed"
				s.errCode = code
			}
		}
	case s.state == "started":
		s.state = "opened"
	}
	state, channel, errCode := s.state, s.channel, s.errCode
	h.mu.Unlock()

	switch state {
	case "opened":
		buttons := ""
		for _, code := range signInErrors {
			buttons += ` <button name="decision" value="` + code + `">` + code + `</button>`
		}
		page(w, "Sign in to AudD (stand-in)", `<p>On the real service, this is AudD's approval page.</p>
<form method="post">
<p>Sign in as:</p>
<p><label><input type="radio" name="account" value="good-token" checked> An account with music recognition on</label><br>
<label><input type="radio" name="account" value="expired-token"> An account whose recognition ended</label><br>
<label><input type="radio" name="account" value="unpaid-token"> An account that never turned it on</label></p>
<p><button name="decision" value="approve">Approve</button> <button name="decision" value="decline">Decline</button></p>
<p>Or end the sign-in with an error:</p>
<p>`+buttons+`</p>
</form>`)
	case "approved":
		page(w, "Link your AudD account (stand-in)", `<p>On the real service, the backend serves this page after AudD's approval.</p>
<p>Link your AudD account to the Twitch channel <strong>`+html.EscapeString(channel)+`</strong>?</p>
<form method="post"><button name="decision" value="link">Link account</button></form>`)
	case "done":
		page(w, "Account linked (stand-in)", `<p>You can go back to Twitch.</p>`)
	case "declined":
		page(w, "Sign-in declined (stand-in)", `<p>You can go back to Twitch.</p>`)
	default:
		page(w, "Sign-in ended (stand-in)", `<p>The sign-in ended with the error <code>`+html.EscapeString(errCode)+`</code>. You can go back to Twitch.</p>`)
	}
}

func (h *harness) signInResult(w http.ResponseWriter, r *http.Request) {
	channel, ok := signInCaller(w, r, "secret")
	if !ok {
		return
	}
	sum := sha256.Sum256([]byte(r.FormValue("secret")))
	id := hex.EncodeToString(sum[:])
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[id]
	switch {
	case s == nil || s.channel != channel || s.state == "started":
		writeJSON(w, http.StatusOK, map[string]string{"status": "unknown"})
	case time.Since(s.started) > signInTTL:
		delete(h.sessions, id)
		writeJSON(w, http.StatusOK, map[string]string{"status": "failed", "error": "sign_in_expired"})
	case s.state == "opened" || s.state == "approved":
		writeJSON(w, http.StatusOK, map[string]string{"status": "pending"})
	case s.state == "done":
		delete(h.sessions, id)
		out := map[string]interface{}{"status": "done", "api_token": s.apiToken}
		if strings.HasPrefix(r.URL.Path, "/mock/lastSong-linked/") {
			out["account"] = h.linkHere(channel, s.apiToken)
		}
		writeJSON(w, http.StatusOK, out)
	case s.state == "declined":
		delete(h.sessions, id)
		writeJSON(w, http.StatusOK, map[string]string{"status": "declined"})
	default:
		delete(h.sessions, id)
		writeJSON(w, http.StatusOK, map[string]string{"status": "failed", "error": s.errCode})
	}
}
