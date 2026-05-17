package pr

import "testing"

func TestParseGHState(t *testing.T) {
	got, err := ParseGHState([]byte(`{"state":"OPEN","isDraft":true,"mergedAt":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != Draft {
		t.Fatalf("got %s", got)
	}
	got, err = ParseGHState([]byte(`{"state":"MERGED","isDraft":false,"mergedAt":"2024-01-01T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != Merged {
		t.Fatalf("got %s", got)
	}
}
