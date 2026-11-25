package main

import (
	"encoding/json"
	"testing"

	"github.com/AudDMusic/audd-go"
)

func TestHistoryItemJSONKeepsOldFieldsAndAddsTime(t *testing.T) {
	result := audd.RecognitionResult{
		Artist:   "Alan Walker, A$AP Rocky",
		Title:    "Live Fast (PUBGM)",
		SongLink: "https://lis.tn/LiveFastPUBGM",
		Score:    100,
	}
	item := newHistoryItem(result, "2026-11-21 20:15:07", 111)
	b, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{
		"artist":      "Alan Walker, A$AP Rocky",
		"title":       "Live Fast (PUBGM)",
		"song_link":   "https://lis.tn/LiveFastPUBGM",
		"score":       float64(100),
		"song_length": "2026-11-21 20:15:07", // what extension versions before 0.1.0 read
		"played_at":   float64(1795292107),   // the same time, UTC, in Unix seconds
		"play_length": float64(111),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v, want %#v", k, got[k], v)
		}
	}
}

func TestHistoryItemWithoutParsableTimestamp(t *testing.T) {
	item := newHistoryItem(audd.RecognitionResult{Title: "x"}, "not a time", 0)
	b, _ := json.Marshal(item)
	var got map[string]interface{}
	_ = json.Unmarshal(b, &got)
	if _, ok := got["played_at"]; ok {
		t.Errorf("played_at should be left out when the timestamp can't be parsed: %s", b)
	}
	if _, ok := got["play_length"]; ok {
		t.Errorf("play_length should be left out when AudD didn't send it: %s", b)
	}
}

func TestRotatedQueueWraps(t *testing.T) {
	q := NewRotatedQueue(3)
	for i := 0; i < 4; i++ {
		q.AddElement(&HistoryItem{PlayLength: i})
	}
	if q.OldestElement != 1 {
		t.Fatalf("OldestElement = %d, want 1", q.OldestElement)
	}
	if q.Elements[0].PlayLength != 3 || q.Elements[1].PlayLength != 1 {
		t.Fatalf("unexpected ring contents")
	}
}

func TestRouteUsersParams(t *testing.T) {
	cases := []struct {
		route   string
		logins  []string
		ids     []string
		wantErr bool
	}{
		{route: "monstercat", logins: []string{"monstercat"}},
		// All-digit routes stay logins: no guessing which channel was meant.
		{route: "57165160", logins: []string{"57165160"}},
		{route: "id:57165160", ids: []string{"57165160"}},
		{route: "id:", wantErr: true},
		{route: "id:monstercat", wantErr: true},
		{route: "id:-1", wantErr: true},
	}
	for _, c := range cases {
		p, err := routeUsersParams(c.route)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: want an error, got %+v", c.route, p)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.route, err)
			continue
		}
		if !equal(p.Logins, c.logins) || !equal(p.IDs, c.ids) {
			t.Errorf("%q: logins %v ids %v, want logins %v ids %v", c.route, p.Logins, p.IDs, c.logins, c.ids)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCallbackSecretRefusedWhenEmpty(t *testing.T) {
	// callbacksSecret is empty in the repository: nothing may pass.
	if callbacksSecret == "" {
		for _, s := range []string{"", "anything"} {
			if callbackSecretMatches(s) {
				t.Errorf("secret %q accepted with an empty callbacksSecret", s)
			}
		}
		return
	}
	if !callbackSecretMatches(callbacksSecret) || callbackSecretMatches(callbacksSecret+"x") {
		t.Error("constant-time comparison is wrong")
	}
}

func TestFlexIntReadsAnyNumberForm(t *testing.T) {
	var cb SuccessResult
	for body, want := range map[string]int{
		`{"result":{"play_length":111}}`:   111,
		`{"result":{"play_length":111.0}}`: 111,
		`{"result":{"play_length":"95"}}`:  95,
		`{"result":{"play_length":null}}`:  0,
		`{"result":{"play_length":"x"}}`:   0,
		`{"result":{}}`:                    0,
	} {
		cb = SuccessResult{}
		if err := json.Unmarshal([]byte(body), &cb); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if int(cb.Result.PlayLength) != want {
			t.Errorf("%s: play_length %d, want %d", body, cb.Result.PlayLength, want)
		}
	}
}

func TestStringID(t *testing.T) {
	if got := stringID("id:57165160", "mishasamin"); got != "mishasamin" {
		t.Errorf("id: route: %q", got)
	}
	if got := stringID("monstercat", "monstercat"); got != "monstercat" {
		t.Errorf("login route: %q", got)
	}
}

func TestPubSubMessageLeavesOutExtraMetadata(t *testing.T) {
	item := newHistoryItem(audd.RecognitionResult{
		Artist: "A", Title: "T", SongLink: "https://lis.tn/x",
		AppleMusic: &audd.AppleMusicResult{}, Spotify: &audd.SpotifyResult{},
	}, "2026-11-21 20:15:07", 0)
	b, _ := json.Marshal(pubsubSongOf(item))
	var got map[string]interface{}
	_ = json.Unmarshal(b, &got)
	for _, k := range []string{"apple_music", "spotify"} {
		if _, ok := got[k]; ok {
			t.Errorf("PubSub message has %s: %s", k, b)
		}
	}
	for _, k := range []string{"artist", "title", "song_link", "song_length", "played_at"} {
		if _, ok := got[k]; !ok {
			t.Errorf("PubSub message lacks %s: %s", k, b)
		}
	}
}
