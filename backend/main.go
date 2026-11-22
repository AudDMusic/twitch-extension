package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/AudDMusic/audd-go"
	"github.com/AudDMusic/twitch-extension/backend/signin"
	tg "github.com/Syfaro/telegram-bot-api"
	"github.com/cloudflare/tableflip"
	"github.com/getsentry/raven-go"
	"github.com/golang-jwt/jwt"
	"github.com/jcuga/golongpoll"
	"github.com/nicklaw5/helix"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/twitch"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

/*
  AudD sends callbacks with results for all the streams to a single callback URL we set, so if we want to process
 the callbacks differently for different streams (e.g. send results to different Twitch extensions depending on what is
 the radio_id in the callbacks), we need to store the information which radio_ids corresponds to which Twitch channels
 somewhere.

  Surely, you can just have a DB or KV-store or whatever instead of doing everything this way, but here we use the
 callback URL stored on the AudD's side to store the radio_ids and corresponding channels. It's done by simply having a
 URL parameter "routes" in the callback URL which contains a JSON map with radio_ids and routes.

  So if this service is available on http://this-service.com/lastSong/, and we want to store the information that the
 stream with radio_id=1 belongs to Twitch user username1 and stream #2 to username2, we simply use something like
  	http://this-service.com/lastSong/callbacks/?secret=[callbacksSecret]&routes={"1":"username1","2":"username2"}
 as the callback URL that we send to AudD. So when AudD sends callbacks to this URL, this service gets the routes variable.

  Then we use this routes variable we stored on AudD's side to understand that we need to send the result to username1
 if we got a callback for stream #1 and to username2 if we got a callback for stream #2.

  And we can also update this variable at any time by sending a new URL to the AudD API using the setCallbackUrl method.

  We have an addition to this service that brings support of Discord and YouTube bots to send results to Discord and
 YouTube chats, let us know if you need this addition, we'll publish it then.

  By the way, routes support comma-separated values, e.g. you can send the results from a stream to
 "username1,username3,youtube:[video id]" instead of just "username1"

  A route is a Twitch login. To name a channel by its numeric Twitch ID instead, prefix it with "id:", e.g.
 {"1":"id:57165160"}. A route without the prefix is always looked up as a login, even if it's all digits.
*/

/*
  By default, the service listens on 127.0.0.1:8334. You can change this in the startServer function.
  Can be used with the following nginx configuration:

	location /lastSong/ {
		proxy_request_buffering off;
		proxy_pass http://127.0.0.1:8334;
	}

  To pass the client's IP to the service, add something like `proxy_set_header X-Real-IP $remote_addr;` inside the location{}
*/

// The package uses the CloudFlare's tableflip to gradually upgrade when get SIGHUP without losing any requests. It
// doesn't support Windows and the build for Windows will fail. If you want to run this service on Windows, remove the
// tableflip stuff in the startServer function.

// How many songs are preserved in the history
const historyCap = 40

// ! Important for security reasons:
// ! 	past some random symbols in the callbacksSecret
// ! 	and pass your callback URL to the AudD API with ?secret=[your secret]
const callbacksSecret = ""

// The current extension version, IDs, secrets.
// !   Please note that at some point in the future when you'll make updates, some of your users will still have
// !   the previous version installed, which means that you'll need to get the version of the extension installed
// !   on a Twitch channel from the frontend, which is currently not implemented
const extensionVersion = "0.1.0"
const extensionOwnerUserId = "57165160" // ID of the user who created/owns the extension
const extensionClientId = "ikcbah7wbue48v7doo4edulmxblt64"
const extensionClientSecret = ""
const extensionSecretBase64 = ""

// Get those three using oauth2 for the owner user with extensionClientId and extensionClientSecret from above
const twitchOauthToken = ""
const twitchOauthRefreshToken = ""
const twitchOauthTokenExpiry = "2020-08-19 00:22:40"

// Remove everything Telegram-related if you don't want to send logs to Telegram
const telegramLogsBotId = ""
const telegramLogsChatId int64 = 0

// If you don't use Sentry, remove everything that starts with raven
const sentryDsn = ""

var mu = sync.RWMutex{}

var lastSong = map[string]audd.RecognitionResult{}

// Returns a new JWT token
func newJwt(chIntId string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"exp":          time.Now().AddDate(99, 0, 0).Unix(),
		"user_id":      extensionOwnerUserId,
		"role":         "external",
		"channel_id":   chIntId,
		"pubsub_perms": map[string]interface{}{"send": []string{"broadcast"}},
	})
	return token.SignedString(extensionSecret)
}

func processResult(result audd.RecognitionResult, chId, timestamp string, playLength int) {
	result.SongLength = GetResultText(result, false, true, 280)
	mu.Lock()
	lastSong[chId] = result
	mu.Unlock()
	knownChannelsMu.RLock()
	chIntId, isSet := channelsStringToInt[chId]
	knownChannelsMu.RUnlock()
	if !isSet {
		user, err := lookUpTwitchUser(chId)
		if capture(err) {
			return
		}
		chIntId = user.ID
		tokenString, err := newJwt(chIntId)
		if capture(err) {
			return
		}
		knownChannelsMu.Lock()
		channelsStringToInt[chId] = chIntId
		// Another callback may have added the channel while we asked Twitch;
		// keep its history instead of replacing it with an empty one.
		if _, exists := knownChannels[chIntId]; !exists {
			knownChannels[chIntId] = &KnownChannel{
				NumericalId: chIntId,
				StringId:    stringID(chId, user.Login),
				History:     NewRotatedQueue(historyCap),
				jwt:         tokenString,
			}
		}
		knownChannelsMu.Unlock()
	}
	// Only the history update holds the lock; sending to Twitch happens after
	// it is released, so slow Twitch requests don't block the read handlers.
	knownChannelsMu.Lock()
	ch := knownChannels[chIntId]
	item, chatText := ch.addResult(result, timestamp, playLength)
	knownChannelsMu.Unlock()
	ch.publish(item, chatText)
}

// twitchIDRoutePrefix marks a route that names a numeric Twitch channel ID
// instead of a login, e.g. {"1":"id:57165160"}. A route without it is always
// a login, even when it is all digits, so a route is never guessed.
const twitchIDRoutePrefix = "id:"

// routeUsersParams returns the Twitch user lookup a route asks for.
func routeUsersParams(route string) (*helix.UsersParams, error) {
	if id, ok := strings.CutPrefix(route, twitchIDRoutePrefix); ok {
		if _, err := strconv.ParseUint(id, 10, 64); err != nil || id == "" {
			return nil, fmt.Errorf("route %q: a Twitch channel ID after %q must be digits", route, twitchIDRoutePrefix)
		}
		return &helix.UsersParams{IDs: []string{id}}, nil
	}
	return &helix.UsersParams{Logins: []string{route}}, nil
}

// stringID is the StringId of a channel: the route, or for an id: route the
// channel's login.
func stringID(route, login string) string {
	if strings.HasPrefix(route, twitchIDRoutePrefix) && login != "" {
		return login
	}
	return route
}

// flexInt reads a JSON number that may also come as a string or with a
// fraction, so an unexpected play_length format doesn't drop the callback.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexInt(v)
	return nil
}

// lookUpTwitchUser finds the Twitch user a route names.
func lookUpTwitchUser(route string) (*helix.User, error) {
	params, err := routeUsersParams(route)
	if err != nil {
		return nil, err
	}
	resp, err := TwitchClient.GetUsers(params)
	if err != nil {
		return nil, err
	}
	if len(resp.Data.Users) == 0 {
		return nil, fmt.Errorf("got empty users from Twitch for %q", route)
	}
	return &resp.Data.Users[0], nil
}

// Retrieves the last song
func apiHandler(w http.ResponseWriter, r *http.Request) {
	raven.SetHttpContext(raven.NewHttp(r))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	mu.RLock()
	result, isSet := lastSong[r.FormValue("ch")]
	mu.RUnlock()
	if !isSet {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	b, err := json.Marshal(result)
	if capture(err) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

// Callbacks structure
type SuccessResult struct {
	Status string `json:"status"`
	Result Songs  `json:"result"`
}
type Songs struct {
	RadioID    int                      `json:"radio_id"`
	Timestamp  string                   `json:"timestamp"`
	PlayLength flexInt                  `json:"play_length,omitempty"`
	Results    []audd.RecognitionResult `json:"results"`
}

// callbackSecretMatches compares the secret from a callback URL in constant
// time. With callbacksSecret empty every callback is refused: anyone could
// post songs to any channel otherwise.
func callbackSecretMatches(got string) bool {
	if callbacksSecret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(callbacksSecret)) == 1
}

// Handles the callbacks
func callbacksHandler(w http.ResponseWriter, r *http.Request) {
	raven.SetHttpContext(raven.NewHttp(r))
	defer captureFunc(r.Body.Close)
	if !callbackSecretMatches(r.URL.Query().Get("secret")) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	b, err := ioutil.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if capture(err) {
		return
	}
	var callback SuccessResult
	err = json.Unmarshal(b, &callback)
	if capture(err) {
		return
	}
	if len(callback.Result.Results) == 0 {
		// Usually, that means that we got a notification instead of results
		fmt.Println(string(b))
		return
	}
	var routesList map[string]string
	err = json.Unmarshal([]byte(r.URL.Query().Get("routes")), &routesList)
	if capture(err) {
		fmt.Println(r.URL.Query().Get("routes"))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	whereToSendCallback := strings.Split(routesList[strconv.Itoa(callback.Result.RadioID)], ",")
	if callback.Result.Results[0].Score < 85 {
		fmt.Println("skipping because of score", string(b))
		return
	}
	for _, route := range whereToSendCallback {
		route = strings.TrimSpace(route)
		if route == "" {
			// No route for this radio_id
			continue
		}
		if strings.Contains(route, "youtube:") {
			route = strings.ReplaceAll(route, "youtube:", "")
			// needs additional code to send results to YouTube chats, let us know if you need this
			continue
		}
		if strings.Contains(route, "discord:") {
			route = strings.ReplaceAll(route, "discord:", "")
			// needs additional code to send results to Discord channels, let us know if you need this
			continue
		}

		// by default, send the results to the Twitch channel specified in the route
		processResult(callback.Result.Results[0], route, callback.Result.Timestamp, int(callback.Result.PlayLength))
		continue
	}
}

// HistoryItem is a song as the extension receives it: the AudD result, with
// SongLength holding the AudD callback timestamp text (read by extension
// versions before 0.1.0), plus the same time as a Unix timestamp and the play
// length.
type HistoryItem struct {
	audd.RecognitionResult
	// PlayedAt is the AudD callback timestamp in Unix seconds (0 if it
	// couldn't be parsed).
	PlayedAt int64 `json:"played_at,omitempty"`
	// PlayLength is how long the song played, in seconds. AudD sends it when
	// callbacks come after a song ends (the default); with callbacks=before it
	// is absent.
	PlayLength int `json:"play_length,omitempty"`
}

// pubsubSong is the part of a song sent through Twitch PubSub.
type pubsubSong struct {
	Artist      string `json:"artist,omitempty"`
	Title       string `json:"title,omitempty"`
	Album       string `json:"album,omitempty"`
	ReleaseDate string `json:"release_date,omitempty"`
	Label       string `json:"label,omitempty"`
	Timecode    string `json:"timecode,omitempty"`
	SongLink    string `json:"song_link,omitempty"`
	Score       int    `json:"score,omitempty"`
	SongLength  string `json:"song_length,omitempty"`
	PlayedAt    int64  `json:"played_at,omitempty"`
	PlayLength  int    `json:"play_length,omitempty"`
}

func pubsubSongOf(v *HistoryItem) pubsubSong {
	return pubsubSong{
		Artist: v.Artist, Title: v.Title, Album: v.Album, ReleaseDate: v.ReleaseDate, Label: v.Label,
		Timecode: v.Timecode, SongLink: v.SongLink, Score: v.Score, SongLength: v.SongLength,
		PlayedAt: v.PlayedAt, PlayLength: v.PlayLength,
	}
}

// The format and time zone of the "timestamp" field of AudD stream callbacks.
const auddTimestampLayout = "2006-01-02 15:04:05"

var auddTimestampZone = time.UTC

func newHistoryItem(result audd.RecognitionResult, timestamp string, playLength int) *HistoryItem {
	item := &HistoryItem{RecognitionResult: result, PlayLength: playLength}
	item.SongLength = timestamp
	if t, err := time.ParseInLocation(auddTimestampLayout, timestamp, auddTimestampZone); err == nil {
		item.PlayedAt = t.Unix()
	}
	return item
}

type RotatedQueue struct {
	OldestElement int
	Elements      []*HistoryItem
}

func NewRotatedQueue(cap int) *RotatedQueue {
	return &RotatedQueue{
		Elements: make([]*HistoryItem, cap, cap),
	}
}

func (q *RotatedQueue) AddElement(v *HistoryItem) {
	q.Elements[q.OldestElement] = v
	q.OldestElement = (q.OldestElement + 1) % cap(q.Elements)
}

var extensionSecret []byte

type KnownChannel struct {
	NumericalId string
	StringId    string
	History     *RotatedQueue
	jwt         string
}

// addResult stores a new result in the history and returns it with the chat
// text. The caller must hold knownChannelsMu for writing.
func (ch *KnownChannel) addResult(result audd.RecognitionResult, timestamp string, playLength int) (*HistoryItem, string) {
	text := result.SongLength // Text for the chat
	item := newHistoryItem(result, timestamp, playLength)
	ch.History.AddElement(item)
	return item, text
}

// publish sends a new result to longpoll clients, to the connected extensions
// and to the Twitch live chat. It must be called without holding
// knownChannelsMu: it waits on Twitch.
func (ch *KnownChannel) publish(v *HistoryItem, text string) {
	if LongPollManager != nil {
		capture(LongPollManager.Publish(ch.NumericalId, v))
	}
	// Twitch PubSub takes messages up to 5 KB, so it gets the fields the
	// extension shows rather than the whole result (which can carry Apple
	// Music, Spotify and other metadata).
	resultEncoded, err := json.Marshal(pubsubSongOf(v))
	if capture(err) {
		return
	}
	// Twitch Helix "Send Extension PubSub Message". The legacy
	// api.twitch.tv/extensions/message/ endpoint no longer exists.
	requestData := map[string]interface{}{
		"target":              []string{"broadcast"},
		"broadcaster_id":      ch.NumericalId,
		"is_global_broadcast": false,
		"message":             string(resultEncoded),
	}
	requestBody, err := json.Marshal(requestData)
	if capture(err) {
		return
	}
	req, err := http.NewRequest("POST", "https://api.twitch.tv/helix/extensions/pubsub", bytes.NewBuffer(requestBody))
	if capture(err) {
		return
	}
	req.Header.Set("User-Agent", "AudD-http-client/1.5.0")
	req.Header.Set("Authorization", "Bearer "+ch.jwt)
	req.Header.Set("Client-Id", extensionClientId)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}

	// A request like the following will be performed
	/* curl
	-H "Authorization: Bearer [jwt]"
	-H "Client-Id: [extensionClientId]"
	-H "Content-Type: application/json"
	-d '{"target":["broadcast"], "broadcaster_id":"57165160", "is_global_broadcast":false, "message":"{\"foo\":\"bar\"}"}'
	-X POST https://api.twitch.tv/helix/extensions/pubsub
	*/
	resp, err := client.Do(req)
	if capture(err) {
		return
	}
	capture(resp.Body.Close())
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		capture(fmt.Errorf("twitch status code error: %d %s", resp.StatusCode, resp.Status))
	}

	// Twitch Helix "Send Extension Chat Message". The extension version must
	// be the one installed on the channel, or Twitch answers 404.
	requestData = map[string]interface{}{
		"text":              text,
		"extension_id":      extensionClientId,
		"extension_version": extensionVersion,
	}
	requestBody, err = json.Marshal(requestData)
	if capture(err) {
		return
	}
	req, err = http.NewRequest("POST",
		"https://api.twitch.tv/helix/extensions/chat?broadcaster_id="+url.QueryEscape(ch.NumericalId),
		bytes.NewBuffer(requestBody))
	if capture(err) {
		return
	}
	req.Header.Set("User-Agent", "AudD-http-client/1.5.0")
	req.Header.Set("Authorization", "Bearer "+ch.jwt)
	req.Header.Set("Client-Id", extensionClientId)
	req.Header.Set("Content-Type", "application/json")

	// A request like the following will be performed
	/* curl
	-H 'Authorization: Bearer [jwt]' \
	-H 'Client-ID: [extensionClientId]' \
	-H 'Content-Type: application/json' \
	-d '{"text":"This is a normal message.", "extension_id":"[extensionClientId]", "extension_version":"[extensionVersion]"}' \
	-X POST 'https://api.twitch.tv/helix/extensions/chat?broadcaster_id=57165160'
	*/
	resp, err = client.Do(req)
	if capture(err) {
		return
	}
	capture(resp.Body.Close())
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		capture(fmt.Errorf("twitch status code error: %d %s", resp.StatusCode, resp.Status))
		return
	}
}

var knownChannels = map[string]*KnownChannel{}
var channelsStringToInt = map[string]string{}
var knownChannelsMu = &sync.RWMutex{}

// Allows extensions to get the info by Twitch channel integer ID
func getChannelHandler(w http.ResponseWriter, r *http.Request) {
	raven.SetHttpContext(raven.NewHttp(r))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Server", "audd-micro1.0.8")
	chId := r.FormValue("ch_id")
	_, err := strconv.Atoi(chId)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`"ch_id not int"`))
		return
	}
	knownChannelsMu.RLock()
	result, isSet := knownChannels[chId]
	var b []byte
	if isSet {
		// Marshal under the lock: the history changes when songs arrive
		b, err = json.Marshal(result)
	}
	knownChannelsMu.RUnlock()
	if !isSet {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`"not_found"`))
		return
	}
	if capture(err) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

// Allows extensions to get the info by the Twitch channel string ID (i.e. username)
func getChannelByStringHandler(w http.ResponseWriter, r *http.Request) {
	raven.SetHttpContext(raven.NewHttp(r))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Server", "audd-micro1.0.8")
	chId := r.FormValue("ch_id")
	knownChannelsMu.RLock()
	chIntId, isSet := channelsStringToInt[chId]
	knownChannelsMu.RUnlock()
	if !isSet {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`"not_found"`))
		return
	}
	knownChannelsMu.RLock()
	result, isSet := knownChannels[chIntId]
	var b []byte
	var err error
	if isSet {
		// Marshal under the lock: the history changes when songs arrive
		b, err = json.Marshal(result)
	}
	knownChannelsMu.RUnlock()
	if !isSet {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`"not_found"`))
		return
	}
	if capture(err) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

// Returns a text for sending to a chat
func getResultText(result audd.RecognitionResult, includePlaysOn, includeReleased, includeAlbum, includePublisher, includeLink bool) string {
	text := fmt.Sprintf("Playing: %s - %s", result.Artist, result.Title)
	if includePlaysOn {
		text += ". Plays on " + result.Timecode
	}
	if result.Album != "" && result.Album != result.Title && len(result.Album) < 70 && includeAlbum {
		text += ". Album: " + result.Album
	}
	if result.ReleaseDate != "" && includeReleased {
		text += ". Released on " + result.ReleaseDate
	}
	if result.Label != "" && result.Label != result.Artist && includePublisher {
		text += ", ℗ " + result.Label
	}
	if includeLink && result.SongLink != "https://lis.tn/VhpgG" {
		text += ". Listen or download: " + result.SongLink
	}
	return text
}

// Returns a text for sending to a chat with respect to the text length limit
func GetResultText(result audd.RecognitionResult, includePlaysOn, includeLink bool, limit int) string {
	text := getResultText(result, includePlaysOn, true, true, true, includeLink)
	if len(text) > limit {
		text = getResultText(result, includePlaysOn, false, true, true, includeLink)
		if len(text) > limit {
			text = getResultText(result, includePlaysOn, false, false, true, includeLink)
			if len(text) > limit {
				text = getResultText(result, includePlaysOn, false, false, false, includeLink)
			}
		}
	}
	return text
}

var TwitchClient *helix.Client
var LongPollManager *golongpoll.LongpollManager

// Adds the CORS headers to responses so backend and frontend are ok with existing on different domains, e.g. api.audd.io and ext-twitch.tv
func addCorsHeaders(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		fn(w, r)
	}
}

// Starts the server
func startServer() {
	var err error
	if callbacksSecret == "" {
		fmt.Println("callbacksSecret is empty: every callback will be refused with 403. Set it and pass ?secret=... in the callback URL.")
		capture(fmt.Errorf("callbacksSecret is empty; callbacks are refused"))
	}
	extensionSecret, err = base64.StdEncoding.DecodeString(extensionSecretBase64)
	if capture(err) {
		panic(err)
	}
	expiry, err := time.Parse("2006-01-02 15:04:05", twitchOauthTokenExpiry)
	oauthConfig := oauth2.Config{
		ClientID:     extensionClientId,
		ClientSecret: extensionClientSecret,
		Scopes:       []string{"channel:moderate", "chat:edit", "chat:read", "whispers:read", "whispers:edit", "user:read:email"},
		Endpoint:     twitch.Endpoint,
	}
	httpClient := oauthConfig.Client(context.Background(), &oauth2.Token{
		AccessToken:  twitchOauthToken,
		RefreshToken: twitchOauthRefreshToken,
		TokenType:    "Bearer",
		Expiry:       expiry,
	})
	TwitchClient, err = helix.NewClient(&helix.Options{
		ClientID:     extensionClientId,
		ClientSecret: extensionClientSecret,
		HTTPClient:   httpClient,
	})
	if capture(err) {
		panic(err)
	}

	LongPollManager, err = golongpoll.StartLongpoll(golongpoll.Options{
		MaxEventBufferSize:             3,
		EventTimeToLiveSeconds:         60 * 10,
		DeleteEventAfterFirstRetrieval: false,
	})
	capture(err)

	mux := http.NewServeMux()
	mux.HandleFunc("/", raven.RecoveryHandler(apiHandler))
	mux.HandleFunc("/lastSong/callbacks/", raven.RecoveryHandler(callbacksHandler))
	mux.HandleFunc("/lastSong/getChannelById/", raven.RecoveryHandler(getChannelHandler))
	mux.HandleFunc("/lastSong/getChannel/", raven.RecoveryHandler(getChannelByStringHandler))
	if LongPollManager != nil {
		mux.HandleFunc("/lastSong/longpoll/", addCorsHeaders(LongPollManager.SubscriptionHandler))
	}
	// Used by the configuration page after "Sign in with AudD": reads the
	// streamer's API token from the AudD MCP server (see package signin)
	mux.HandleFunc("/lastSong/auddApiToken/", raven.RecoveryHandler(signin.Handler(signin.Config{
		ExtensionSecret: extensionSecret,
		MCPURL:          signin.DefaultMCPURL,
		ClientVersion:   extensionVersion,
	})))

	port := ":8334"
	description := "Last song microservice"

	upg, err := tableflip.New(tableflip.Options{})
	if capture(err) {
		panic(err)
	}
	defer upg.Stop()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGHUP)
		for range sig {
			err := upg.Upgrade()
			if capture(err) {
				sendTgNotification(description + " upgrade FAILED on " + port)
				continue
			}
		}
	}()

	ln, err := upg.Fds.Listen("tcp", "127.0.0.1"+port)
	if capture(err) {
		fmt.Println("Can't listen")
		panic(err)
	}
	var server http.Server
	server.Handler = mux
	fmt.Println("Starting server at " + port)

	go raven.CapturePanic(serverFunction{server.Serve, ln}.ServeForCapture, nil)

	sendTgNotification(description + " has restarted on " + port)

	if err := upg.Ready(); capture(err) {
		panic(err)
	}
	<-upg.Exit()
	time.AfterFunc(30*time.Second, func() {
		os.Exit(1)
	})
}

func main() {
	raven.CapturePanic(startServer, nil)
}

type serverFunction struct {
	f  func(net.Listener) error
	ln net.Listener
}

func (v serverFunction) ServeForCapture() {
	capture(v.f(v.ln))
}

func capture(err error) bool {
	if err == nil {
		return false
	}
	_, file, no, ok := runtime.Caller(1)
	if ok {
		err = fmt.Errorf("%v from %s#%d", err, file, no)
	}
	go raven.CaptureError(err, nil)
	return true
}
func init() {
	err := raven.SetDSN(sentryDsn)
	if err != nil {
		panic(err)
	}
}

func sendTgNotification(text string) {
	bot, err := tg.NewBotAPI(telegramLogsBotId)
	if capture(err) {
		return
	}
	msg := tg.NewMessage(telegramLogsChatId, text)
	captureDouble(bot.Send(msg))
}
func captureDouble(_ interface{}, err error) (r bool) {
	if r = err != nil; r {
		_, file, no, ok := runtime.Caller(1)
		if ok {
			err = fmt.Errorf("%v from %s#%d", err, file, no)
		}
		go raven.CaptureError(err, nil)
	}
	return
}

func captureFunc(f func() error) (r bool) {
	err := f()
	if r = err != nil; r {
		_, file, no, ok := runtime.Caller(1)
		if ok {
			err = fmt.Errorf("%v from %s#%d", err, file, no)
		}
		go raven.CaptureError(err, nil)
		go fmt.Println(err)
	}
	return
}
