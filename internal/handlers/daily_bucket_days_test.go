package handlers

import (
	"testing"
	"time"
)

// TestDailyBucketDaysUTCWalksConsecutiveDays pins the window computation to UTC days.
// Deriving days from local instants shifts them by the local offset, which
// skips one UTC day when the local hour is less than the offset (early
// morning). Around the DST seam, the gap widens.
func TestDailyBucketDaysUTCWalksConsecutiveDays(t *testing.T) {
	locCET := time.FixedZone("CET", +1*3600) // UTC+1
	locEST := time.FixedZone("EST", -5*3600) // UTC-5

	tests := []struct {
		name string
		now  time.Time
	}{
		{name: "8am CET", now: time.Date(2026, 10, 26, 8, 17, 0, 0, locCET)},
		{name: "13pm CET", now: time.Date(2026, 10, 26, 13, 17, 0, 0, locCET)},
		{name: "8am EST", now: time.Date(2026, 10, 26, 8, 17, 0, 0, locEST)},
		{name: "midnight UTC", now: time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			days := dailyBucketDays(tt.now, 7)
			if got, want := len(days), 7; got != want {
				t.Errorf("len(days) = %d, want %d", got, want)
			}
			// Last day must be the current UTC day
			wantLast := tt.now.UTC().Format("2006-01-02")
			if got := days[len(days)-1].Format("2006-01-02"); got != wantLast {
				t.Errorf("last day = %s, want %s", got, wantLast)
			}
			// Consecutive days with no gaps: buckets[0] is oldest, buckets[6] is today UTC
			for i := range days {
				wantIdx := 20 + i // Oct 20, 21, 22, 23, 24, 25, 26
				want := time.Date(2026, 10, wantIdx, 0, 0, 0, 0, time.UTC)
				if !days[i].Equal(want) {
					t.Errorf("bucket %d = %s, want %s", i, days[i].Format("2006-01-02"), want.Format("2006-01-02"))
				}
			}
		})
	}
}