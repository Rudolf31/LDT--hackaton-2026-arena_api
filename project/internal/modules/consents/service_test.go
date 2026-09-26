package consents

import (
	"testing"
	"time"
)

func TestDateOfFollowsMoscowCalendar(t *testing.T) {
	// 22:30 UTC 25 сентября — уже 01:30 26 сентября по Москве.
	got := dateOf(time.Date(2026, 9, 25, 22, 30, 0, 0, time.UTC))
	if want := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("дата по Москве: %v, ожидали %v", got, want)
	}
	if signedToday := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC); signedToday.After(got) {
		t.Fatal("сегодняшняя дата подписания не может считаться будущей")
	}
}
