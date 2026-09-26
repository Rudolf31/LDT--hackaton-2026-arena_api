package assignments

import (
	"time"

	"arena-portal-backend/internal/api/gen"
)

// statusFacts — всё, из чего выводится статус назначения.
type statusFacts struct {
	Cancelled   bool
	CodeBlocked bool
	DueAt       time.Time
	Sessions    SessionSummary
}

// deriveStatus — статус на экране «Назначения — список» по порядку
// arena-portal-hr.md 10.4: «отменено» → «код заблокирован» → «идёт» →
// «прошёл» → «прервана» → «срок истёк» → «не начал». Не хранится (D-52).
func deriveStatus(f statusFacts, now time.Time) gen.AssignmentStatus {
	switch {
	case f.Cancelled:
		return gen.AssignmentStatusCancelled
	case f.CodeBlocked:
		return gen.AssignmentStatusCodeBlocked
	case f.Sessions.RunningID != nil:
		return gen.AssignmentStatusInProgress
	case f.Sessions.Finished:
		return gen.AssignmentStatusPassed
	case f.Sessions.LastStatus != nil && *f.Sessions.LastStatus == gen.SessionStatusAbandoned:
		return gen.AssignmentStatusAbandoned
	case !now.Before(f.DueAt):
		return gen.AssignmentStatusExpired
	}
	return gen.AssignmentStatusNotStarted
}
