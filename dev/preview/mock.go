package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// devSecret signs the stand-in Twitch JWTs. It is a development value only.
var devSecret = []byte("audd-twitch-extension-preview-secret")

type song struct {
	Artist, Title, Album, Slug string
	Link                       string // overrides the generated lis.tn link
	NoLink                     bool
}

// Made-up songs for the mock history.
var catalog = []song{
	{"Kaia Moreno", "Paper Planes at Midnight", "Night Shift", "", "", false},
	{"Neon Harbor", "Tidal (Extended Mix)", "Tidal", "", "", false},
	{"The Lanterns", "Orbit", "Small Hours", "", "", false},
	{"Juno Vale", "Slow Static", "Slow Static", "", "", false},
	{"Mira & The Echo", "Afterglow", "Afterglow", "", "", false},
	{"Ostrava", "Concrete Gardens", "Brutal Bloom", "", "", false},
	{"DJ Saltwater", "Low Tide Dub", "Coastal", "", "", false},
	{"Ruby Ferro", "Telephone Wire", "Telephone Wire", "", "", false},
	{"Hollow Pines", "Woodsmoke", "Northern", "", "", false},
	{"Ama Kwarteng", "Golden Hour Highlife", "Accra Nights", "", "", false},
	{"Static Bloom", "Pixel Heart", "8 Bit Summer", "", "", false},
	{"Lena Sorensen", "Glass Harbour", "Fjord", "", "", false},
	{"Moth Club", "Neon Moth", "Moth Club", "", "", false},
	{"Ivo Petrov", "Sofia Rain", "Sofia Rain", "", "", false},
	{"Velvet Arcade", "Insert Coin", "High Score", "", "", false},
	{"Sun Ra Lee", "Copper Moon", "Copper Moon", "", "", false},
}

// Strings that stress the layout and the escaping.
var longSongs = []song{
	{"Berliner Philharmoniker, Herbert von Karajan, Wiener Singverein, Gundula Janowitz, Hilde Rössel-Majdan",
		"Symphony No. 9 in D Minor, Op. 125 \"Choral\": IV. Presto – Allegro assai (Live at the Royal Albert Hall, 2019 Remastered Version)", "", "", "", false},
	{"高橋洋子", "残酷な天使のテーゼ (Director's Edit Version)", "", "", "", false},
	{"<img src=x onerror=alert(1)>", "<script>alert('title')</script> & \"quotes\"", "", "", "", false},
	{"Aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Supercalifragilisticexpialidociousandthensomemorewithoutanyspaces", "", "", "", false},
	{"Zé", "Ok", "", "", "", false},
}

var noCoverSongs = []song{
	{"Ruby Ferro", "Song with a broken cover", "", "broken", "", false},
	{"Hollow Pines", "Song AudD has no page for", "", "", "https://lis.tn/VhpgG", false},
	{"Ostrava", "Song without a link", "", "", "", true},
}

type item map[string]interface{}

type channelState struct {
	items  []item // oldest first
	pushed int
}

type account struct {
	State     string // none, active, setupfailed, expiring, expired, unpaid, error-<code>, down
	ChannelID string
	UserID    string
	Broken    bool // twitch.php answers internal_error whatever is asked (account=stuck)
}

type harness struct {
	mu       sync.Mutex
	channels map[string]*channelState
	accounts map[string]*account       // by channel
	sessions map[string]*signInSession // Sign in with AudD, by session
	flaky    map[string]int
}

func newHarness() *harness {
	h := &harness{
		channels: map[string]*channelState{},
		accounts: map[string]*account{},
		sessions: map[string]*signInSession{},
		flaky:    map[string]int{},
	}
	return h
}

func (h *harness) routes(mux *http.ServeMux) {
	mux.HandleFunc("/dev/jwt", h.jwt)
	mux.HandleFunc("/dev/pubsub", h.pubsub)
	mux.HandleFunc("/dev/reset", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.channels = map[string]*channelState{}
		h.accounts = map[string]*account{}
		h.sessions = map[string]*signInSession{}
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	// The backend, and variants of it for the signin= parameter (see signin.go).
	for _, base := range []string{"/mock/lastSong/", "/mock/lastSong-old/", "/mock/lastSong-down/", "/mock/lastSong-busy/", "/mock/lastSong-noresult/", "/mock/lastSong-linked/"} {
		mux.HandleFunc(base+"getChannelById/", h.getChannel)
	}
	mux.HandleFunc("/mock/lastSong/longpoll/", h.longpoll)
	h.signInRoutes(mux)
	mux.HandleFunc("/mock/twitch.php", h.twitchPhp)
	mux.HandleFunc("/mock/payments/", h.payments)
	mux.HandleFunc("/mock/dashboard/", func(w http.ResponseWriter, r *http.Request) {
		page(w, "AudD dashboard (mock)", `<p>Your API token: <code>good-token</code></p><p>Other test tokens: <code>expired-token</code>, <code>unpaid-token</code>. Anything else is unknown.</p>`)
	})
	mux.HandleFunc("/mock/lis.tn/", h.songPage)
	mux.HandleFunc("/mock/ytimg/", h.ytThumb)
}

// ------------------------------------------------------------ songs

func slugOf(s song) string {
	if s.Slug != "" {
		return s.Slug
	}
	f := fnv.New32a()
	f.Write([]byte(s.Artist + s.Title))
	return fmt.Sprintf("s%x", f.Sum32())
}

func makeItem(base string, s song, at time.Time, playLength int, legacy bool) item {
	it := item{
		"artist":       s.Artist,
		"title":        s.Title,
		"score":        100,
		"song_length":  at.UTC().Format("2006-01-02 15:04:05"),
		"release_date": "2024-05-17",
		"label":        "Example Records",
	}
	if s.Album != "" {
		it["album"] = s.Album
	}
	switch {
	case s.NoLink:
	case s.Link != "":
		it["song_link"] = s.Link
	default:
		it["song_link"] = base + "/mock/lis.tn/" + slugOf(s)
	}
	if !legacy {
		it["played_at"] = at.Unix()
		if playLength > 0 {
			it["play_length"] = playLength
		}
	}
	return it
}

// scenario builds the history of a mock channel, oldest first.
func scenario(name, base string, now time.Time) []item {
	var out []item
	add := func(s song, ago time.Duration, playLength int, legacy bool) {
		out = append(out, makeItem(base, s, now.Add(-ago), playLength, legacy))
	}
	pick := func(i int) song { return catalog[i%len(catalog)] }
	switch name {
	case "empty", "empty-prod", "down":
		return nil
	case "one":
		add(pick(0), 1*time.Minute, 0, false)
	case "just":
		for i := 9; i >= 1; i-- {
			add(pick(i), time.Duration(i*4+3)*time.Minute, 200, false)
		}
		add(pick(0), 5*time.Minute, 185, false) // ended about 2 minutes ago
	case "stale":
		for i := 9; i >= 0; i-- {
			add(pick(i), 3*time.Hour+time.Duration(i*4)*time.Minute, 0, false)
		}
	case "recent":
		for i := 9; i >= 0; i-- {
			add(pick(i), 20*time.Minute+time.Duration(i*4)*time.Minute, 0, false)
		}
	case "full":
		for i := 39; i >= 0; i-- {
			add(pick(i), time.Duration(i)*47*time.Minute+30*time.Second, 0, false)
		}
	case "long":
		for i := 5; i >= 1; i-- {
			add(pick(i), time.Duration(i*4+4)*time.Minute, 0, false)
		}
		for i := len(longSongs) - 1; i >= 0; i-- {
			add(longSongs[i], time.Duration(i*3+1)*time.Minute, 0, false)
		}
	case "nocover":
		add(pick(3), 12*time.Minute, 0, false)
		for i := len(noCoverSongs) - 1; i >= 0; i-- {
			add(noCoverSongs[i], time.Duration(i*3+1)*time.Minute, 0, false)
		}
	case "youtube":
		add(pick(2), 9*time.Minute, 0, false)
		add(song{"Velvet Arcade", "Insert Coin (Official Video)", "", "", "https://youtu.be/aqz-KE-bpKQ", false}, 2*time.Minute, 0, false)
	case "legacy":
		for i := 9; i >= 0; i-- {
			add(pick(i), time.Duration(i*4+1)*time.Minute, 0, true)
		}
	default: // typical, slow, flaky
		for i := 11; i >= 0; i-- {
			add(pick(i), time.Duration(i*4)*time.Minute+50*time.Second, 0, false)
		}
	}
	return out
}

func (h *harness) channel(name, base string) *channelState {
	c := h.channels[name]
	if c == nil {
		// Configuration-page channels get songs that match their account:
		// none since an unpaid account, old ones since an expired one.
		kind := name
		switch {
		case strings.Contains(name, "unpaid"):
			kind = "empty"
		case strings.Contains(name, "expired"):
			kind = "stale"
		}
		c = &channelState{items: scenario(kind, base, time.Now())}
		h.channels[name] = c
	}
	return c
}

func baseURL(r *http.Request) string { return "http://" + r.Host }

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// getChannel answers like api.audd.io/lastSong/getChannelById/.
func (h *harness) getChannel(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("ch_id")
	switch name {
	case "down":
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("<html><body>503 Service Unavailable</body></html>"))
		return
	case "slow":
		time.Sleep(8 * time.Second)
	case "flaky":
		h.mu.Lock()
		h.flaky[name]++
		n := h.flaky[name]
		h.mu.Unlock()
		if n%3 != 0 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
	case "empty":
		writeJSON(w, http.StatusNotFound, "not_found")
		return
	}
	h.mu.Lock()
	c := h.channel(name, baseURL(r))
	const capacity = 40
	elements := make([]interface{}, capacity)
	// Fill the ring the way the backend does: element i of n goes to i%cap.
	for i, it := range c.items {
		elements[i%capacity] = it
	}
	oldest := len(c.items) % capacity
	if name == "empty-prod" {
		oldest = capacity // what the production backend answers for a new channel
	}
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"NumericalId":      name,
		"StringId":         name,
		"History":          map[string]interface{}{"OldestElement": oldest, "Elements": elements},
		"ExtensionVersion": "",
	})
}

func (h *harness) newSong(name, base string) item {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.channel(name, base)
	c.pushed++
	s := catalog[(int(time.Now().Unix())/7+c.pushed*5)%len(catalog)]
	it := makeItem(base, s, time.Now(), 0, false)
	c.items = append(c.items, it)
	return it
}

// pubsub streams new songs to the stand-in Twitch helper, the way Twitch
// PubSub delivers the backend's broadcast messages.
func (h *harness) pubsub(w http.ResponseWriter, r *http.Request) {
	every, _ := strconv.Atoi(r.FormValue("every"))
	name := r.FormValue("channel")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	if every <= 0 {
		<-r.Context().Done()
		return
	}
	t := time.NewTicker(time.Duration(every) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			b, _ := json.Marshal(h.newSong(name, baseURL(r)))
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}

// longpoll answers like api.audd.io/lastSong/longpoll/ (golongpoll).
func (h *harness) longpoll(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("category")
	if name == "down" {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	if name == "errorbody" {
		// golongpoll answers its own errors with 200
		writeJSON(w, http.StatusOK, map[string]string{"error": "Invalid or missing 'timeout' arg"})
		return
	}
	timeout, _ := strconv.Atoi(r.FormValue("timeout"))
	if timeout <= 0 || timeout > 120 {
		timeout = 30
	}
	every, _ := strconv.Atoi(r.URL.Query().Get("every"))
	wait := time.Duration(timeout) * time.Second
	if name == "live" || every > 0 {
		wait = 20 * time.Second
	}
	select {
	case <-r.Context().Done():
		return
	case <-time.After(wait):
	}
	if name == "live" {
		it := h.newSong(name, baseURL(r))
		ts := time.Now().UnixMilli()
		writeJSON(w, http.StatusOK, map[string]interface{}{"events": []interface{}{
			map[string]interface{}{"timestamp": ts, "category": name, "data": it},
		}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"timeout": "no events before timeout", "timestamp": time.Now().UnixMilli()})
}

// ------------------------------------------------------- Twitch JWT

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

func (h *harness) jwt(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	channel := q.Get("channel")
	if channel == "" {
		channel = "typical"
	}
	role := q.Get("role")
	if role == "" {
		role = "viewer"
	}
	claims := map[string]interface{}{
		"exp":            time.Now().Add(time.Hour).Unix(),
		"opaque_user_id": "U-preview",
		"channel_id":     channel,
		"role":           role,
		"is_unlinked":    true,
		"pubsub_perms":   map[string]interface{}{"listen": []string{"broadcast", "global"}},
		"dev_account":    q.Get("account"),
	}
	if role == "broadcaster" {
		claims["user_id"] = channel
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	unsigned := b64(header) + "." + b64(payload)
	token := unsigned + "." + b64(hmacSHA256(devSecret, []byte(unsigned)))
	writeJSON(w, http.StatusOK, map[string]string{
		"channelId":  channel,
		"clientId":   "ikcbah7wbue48v7doo4edulmxblt64",
		"token":      token,
		"helixToken": "preview",
		"userId":     "U-preview",
	})
}

func claimsOf(token string) map[string]interface{} {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	if b64(hmacSHA256(devSecret, []byte(parts[0]+"."+parts[1]))) != parts[2] {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var c map[string]interface{}
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	return c
}

// ------------------------------------------------------- twitch.php

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *account) fields(twitchID string) map[string]interface{} {
	f := map[string]interface{}{"user_id": a.UserID, "payments_h": "preview-" + a.UserID, "twitch_id": twitchID}
	day := 24 * time.Hour
	switch a.State {
	case "active", "setupfailed":
		f["streams_till_text"] = time.Now().Add(24 * day).Format("January 2, 2006")
		f["suggest_renewal"] = true // as twitch.php sends it, whenever recognition ends
		if a.State == "setupfailed" { // linked, but the channel couldn't be set up
			f["channel_setup_failed"] = true
		}
	case "expiring":
		f["streams_till_text"] = time.Now().Add(3 * day).Format("January 2, 2006")
		f["suggest_renewal"] = true
	case "expired":
		f["streams_till_text"] = time.Now().Add(-18 * day).Format("January 2, 2006")
	}
	return f
}

func (a *account) answer(twitchID string) interface{} {
	switch {
	case a.State == "none":
		return []interface{}{false, "no_user"}
	case strings.HasPrefix(a.State, "error-"):
		return []interface{}{false, strings.TrimPrefix(a.State, "error-")}
	case a.Broken:
		return []interface{}{false, "internal_error"}
	case a.State == "active" || a.State == "expiring" || a.State == "setupfailed":
		return []interface{}{true, a.fields(twitchID)}
	default: // expired, unpaid
		return []interface{}{false, "zero_limit", a.fields(twitchID)}
	}
}

func (h *harness) twitchPhp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	claims := claimsOf(r.FormValue("jwt"))
	if claims == nil {
		writeJSON(w, 200, []interface{}{false, "wrong_jwt"})
		return
	}
	channel, _ := claims["channel_id"].(string)
	initial, _ := claims["dev_account"].(string)
	if initial == "" {
		initial = "none"
	}
	if initial == "down" {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html><body>502 Bad Gateway</body></html>"))
		return
	}
	if initial == "slow" {
		time.Sleep(6 * time.Second)
		initial = "active"
	}
	action := r.URL.RawQuery
	if initial == "slowlink" { // not linked yet, and linking takes 8 s
		initial = "none"
		if strings.HasPrefix(action, "save_user") {
			time.Sleep(8 * time.Second)
		}
	}
	time.Sleep(300 * time.Millisecond)
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.accounts[channel]
	if a == nil {
		a = &account{State: initial, ChannelID: channel, UserID: strconv.Itoa(1000 + len(h.accounts))}
		if initial == "stuck" { // linked, and twitch.php fails whatever is asked
			a.State, a.Broken = "active", true
		}
		h.accounts[channel] = a
	}
	switch {
	case strings.HasPrefix(action, "get_user"):
		writeJSON(w, 200, a.answer(channel))
	case strings.HasPrefix(action, "signup"):
		if a.State == "none" {
			a.State = "unpaid"
		}
		writeJSON(w, 200, a.answer(channel))
	case strings.HasPrefix(action, "save_user"):
		if a.Broken { // linked already: twitch.php doesn't look at the token
			writeJSON(w, 200, a.answer(channel))
			return
		}
		switch r.FormValue("api_token") {
		case "":
			writeJSON(w, 200, []interface{}{false, "no_token"})
			return
		case "good-token":
			a.State = "active"
		case "expired-token":
			a.State = "expired"
		case "unpaid-token":
			a.State = "unpaid"
		default:
			writeJSON(w, 200, []interface{}{false, "wrong_token"})
			return
		}
		writeJSON(w, 200, a.answer(channel))
	default:
		writeJSON(w, 200, []interface{}{false, "internal_error"})
	}
}

func (h *harness) payments(w http.ResponseWriter, r *http.Request) {
	user := r.FormValue("user_id")
	if r.Method == http.MethodPost {
		h.mu.Lock()
		for _, a := range h.accounts {
			if a.UserID == user {
				a.State = "active"
			}
		}
		h.mu.Unlock()
		page(w, "Payment received (mock)", `<p>Done. Go back to the extension's tab.</p>`)
		return
	}
	page(w, "AudD payment (mock)", `<p>One month of music recognition for one stream: <strong>$45</strong>.</p>
<form method="post"><input type="hidden" name="user_id" value="`+html.EscapeString(user)+`"><button>Pay $45 (mock)</button></form>`)
}

func page(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title>
<link rel="stylesheet" href="/dev/static/mock.css"></head><body><main><p class="mock-tag">Local mock, not the real service</p><h1>%s</h1>%s</main></body></html>`,
		html.EscapeString(title), html.EscapeString(title), body)
}

// ------------------------------------------------- covers and pages

func (h *harness) songPage(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/mock/lis.tn/")
	if _, thumb := r.URL.Query()["thumb"]; thumb {
		switch slug {
		case "broken":
			http.NotFound(w, r)
		case "VhpgG":
			w.Header().Set("Content-Type", "image/jpeg")
		default:
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Header().Set("Cache-Control", "max-age=3600")
			w.Write([]byte(coverSVG(slug)))
		}
		return
	}
	page(w, "Song page (mock)", `<p>On lis.tn this page links the song on streaming services.</p>`)
}

func (h *harness) ytThumb(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 480 360"><rect width="480" height="360" fill="#111"/><rect y="45" width="480" height="270" fill="#2d6cdf"/><circle cx="240" cy="180" r="70" fill="#ffd166"/><path d="M220 150v60l50-30z" fill="#111"/></svg>`))
}

// coverSVG draws a made-up cover from the slug, so every song looks different.
func coverSVG(slug string) string {
	f := fnv.New32a()
	f.Write([]byte(slug))
	n := f.Sum32()
	palettes := [][3]string{
		{"#ff5f6d", "#ffc371", "#2b1055"}, {"#3a7bd5", "#00d2ff", "#0b132b"}, {"#8e2de2", "#f15bb5", "#1b1b3a"},
		{"#f7971e", "#ffd200", "#3d1c02"}, {"#11998e", "#38ef7d", "#062925"}, {"#ee0979", "#ff6a00", "#2a0a18"},
		{"#4568dc", "#b06ab3", "#14112b"}, {"#e0e0e0", "#ffffff", "#9aa0a6"}, {"#0f0f0f", "#3a3a3a", "#000000"},
	}
	p := palettes[n%uint32(len(palettes))]
	shape := ""
	switch (n / 7) % 4 {
	case 0:
		shape = `<circle cx="62" cy="40" r="30" fill="url(#g)"/><rect y="72" width="100" height="28" fill="#000" opacity=".22"/>`
	case 1:
		shape = `<path d="M0 70 Q25 40 50 62 T100 50 V100 H0Z" fill="url(#g)"/><circle cx="30" cy="30" r="10" fill="#fff" opacity=".85"/>`
	case 2:
		shape = `<rect x="18" y="18" width="64" height="64" rx="32" fill="none" stroke="url(#g)" stroke-width="10"/><circle cx="50" cy="50" r="9" fill="url(#g)"/>`
	default:
		shape = `<path d="M0 100 L50 20 L100 100Z" fill="url(#g)"/><path d="M20 100 L60 45 L100 100Z" fill="#000" opacity=".2"/>`
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="%s"/><stop offset="1" stop-color="%s"/></linearGradient></defs><rect width="100" height="100" fill="%s"/>%s</svg>`, p[0], p[1], p[2], shape)
}
