package store

import (
	"strings"
	"testing"
)

func TestCloneTurnTimingsOwnsAndBoundsNativeFacts(t *testing.T) {
	original := []TurnTiming{{ID: "turn", Changes: &TurnChangeCounts{Status: "available", EventID: "native", Files: 1, Additions: 3}}}
	got := CloneTurnTimings(original)
	got[0].Changes.Additions = 9
	if original[0].Changes.Additions != 3 {
		t.Fatal("owned metadata aliases original")
	}
	for _, facts := range []TurnChangeCounts{
		{Status: strings.Repeat("x", 2048)},
		{Status: "available", EventID: strings.Repeat("e", 257), Files: 1},
		{Status: "available", EventID: "e", Files: 1, Additions: 1_000_000_001},
		{Status: "available", EventID: "e", Files: 1, Omitted: 2},
		{Status: "available", EventID: "e", Files: -1},
	} {
		got := CloneTurnTimings([]TurnTiming{{Changes: &facts}})
		if *got[0].Changes != (TurnChangeCounts{Status: "unknown"}) {
			t.Fatalf("corrupt native metadata=%+v", got[0].Changes)
		}
	}
	for _, status := range []string{"unknown", "busy", "unsupported"} {
		got := CloneTurnTimings([]TurnTiming{{Changes: &TurnChangeCounts{Status: status, Files: 99, EventID: "irrelevant"}}})
		if *got[0].Changes != (TurnChangeCounts{Status: status}) {
			t.Fatalf("unavailable claims counts=%+v", got[0].Changes)
		}
	}
}
