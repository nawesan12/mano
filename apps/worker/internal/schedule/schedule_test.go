package schedule

import (
	"testing"
	"time"
)

func TestNext(t *testing.T) {
	ar, _ := time.LoadLocation("America/Argentina/Buenos_Aires")
	before := time.Date(2026, 10, 15, 6, 30, 0, 0, ar)
	after := time.Date(2026, 10, 15, 7, 0, 0, 0, ar)
	n1, _ := Next(before, "07:00", ar)
	n2, _ := Next(after, "07:00", ar)
	if !n1.Equal(time.Date(2026, 10, 15, 7, 0, 0, 0, ar)) || !n2.Equal(time.Date(2026, 10, 16, 7, 0, 0, 0, ar)) {
		t.Fatal(n1, n2)
	}
}
