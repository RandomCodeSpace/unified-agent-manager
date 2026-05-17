package pr

import "testing"

func TestParseGHStateClosedAndUnknown(t *testing.T) {
	got, err := ParseGHState([]byte(`{"state":"CLOSED","isDraft":false,"mergedAt":null}`))
	if err != nil || got != Closed {
		t.Fatalf("%s %v", got, err)
	}
	got, err = ParseGHState([]byte(`{"state":"OTHER","isDraft":false,"mergedAt":null}`))
	if err != nil || got != None {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := ParseGHState([]byte(`bad`)); err == nil {
		t.Fatal("want json error")
	}
}
