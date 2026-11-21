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
