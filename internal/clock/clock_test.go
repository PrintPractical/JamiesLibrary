package clock

import (
	"testing"
	"time"
)

func TestNowIsTheSystemClockByDefault(t *testing.T) {
	before := time.Now()
	got := Now()
	if got.Before(before) || time.Since(got) > time.Minute {
		t.Fatalf("Now() = %v, system time is %v", got, before)
	}
}

func TestSetSourceReplacesAndRestores(t *testing.T) {
	fixed := time.Date(2030, 2, 3, 4, 5, 6, 0, time.UTC)
	restore := SetSource(func() time.Time { return fixed })
	if got := Now(); !got.Equal(fixed) {
		t.Fatalf("Now() = %v, want %v", got, fixed)
	}
	restore()
	if got := Now(); got.Equal(fixed) {
		t.Fatal("restore did not put the system clock back")
	}
}
