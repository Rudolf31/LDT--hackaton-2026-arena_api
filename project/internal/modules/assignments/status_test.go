package assignments

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
)

// TestDeriveStatusOrder — порядок arena-portal-hr.md 10.4: каждое
// следующее условие проверяется, только если предыдущие не сработали.
func TestDeriveStatusOrder(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	future, past := now.Add(time.Hour), now.Add(-time.Hour)
	running := uuid.New()
	abandoned := gen.SessionStatusAbandoned
	completed := gen.SessionStatusCompleted

	cases := []struct {
		name string
		f    statusFacts
		want gen.AssignmentStatus
	}{
		{"отменено важнее всего", statusFacts{Cancelled: true, CodeBlocked: true, DueAt: past,
			Sessions: SessionSummary{RunningID: &running}}, gen.AssignmentStatusCancelled},
		{"код заблокирован важнее идущей", statusFacts{CodeBlocked: true, DueAt: future,
			Sessions: SessionSummary{RunningID: &running}}, gen.AssignmentStatusCodeBlocked},
		{"идёт важнее прошёл", statusFacts{DueAt: past,
			Sessions: SessionSummary{RunningID: &running, Finished: true}}, gen.AssignmentStatusInProgress},
		{"прошёл важнее прервана", statusFacts{DueAt: past,
			Sessions: SessionSummary{Finished: true, LastStatus: &abandoned}}, gen.AssignmentStatusPassed},
		{"прервана важнее срока", statusFacts{DueAt: past,
			Sessions: SessionSummary{LastStatus: &abandoned}}, gen.AssignmentStatusAbandoned},
		{"срок истёк", statusFacts{DueAt: past}, gen.AssignmentStatusExpired},
		{"не начал", statusFacts{DueAt: future}, gen.AssignmentStatusNotStarted},
		{"завершённая без флага не прошёл", statusFacts{DueAt: future,
			Sessions: SessionSummary{LastStatus: &completed, Finished: true}}, gen.AssignmentStatusPassed},
	}
	for _, c := range cases {
		if got := deriveStatus(c.f, now); got != c.want {
			t.Errorf("%s: получено %s, ожидалось %s", c.name, got, c.want)
		}
	}
}

func TestRunningAt(t *testing.T) {
	start := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	s := SessionSummary{Spans: []Span{{Start: start, End: &end}}}
	if !s.RunningAt(start.Add(time.Minute)) || s.RunningAt(end) || s.RunningAt(start.Add(-time.Minute)) {
		t.Fatal("сессия идёт с начала и до конца, не включая конец")
	}
	open := SessionSummary{Spans: []Span{{Start: start}}}
	if !open.RunningAt(end) {
		t.Fatal("идущая сессия идёт и сейчас")
	}
}
