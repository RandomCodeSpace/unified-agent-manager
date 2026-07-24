package session

import (
	"math"
	"testing"
)

func TestAttachInputFDRejectsPollOverflow(t *testing.T) {
	// Given
	const largestPollFD = uintptr(math.MaxInt32)

	// When
	got, validErr := attachInputFD(largestPollFD)
	_, overflowErr := attachInputFD(largestPollFD + 1)

	// Then
	if validErr != nil || got != math.MaxInt32 {
		t.Fatalf("largest poll fd = %d, %v; want %d, nil", got, validErr, math.MaxInt32)
	}
	if overflowErr == nil {
		t.Fatal("overflowing poll fd was accepted")
	}
}
