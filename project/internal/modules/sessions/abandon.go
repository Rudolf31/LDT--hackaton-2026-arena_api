package sessions

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/platform/pg"
)

// CloseAbandoned — Closer: сессии без событий дольше таймаута становятся
// «прерванными» без оценок; по каждой — session_closed_by_server в журнал
// той же транзакцией (NFR-S-03).
func (s *service) CloseAbandoned(ctx context.Context, portalStartedAt time.Time, timeoutMinutes int) (int, error) {
	var closed int
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := s.store.closeAbandoned(ctx, tx, portalStartedAt, timeoutMinutes)
		if err != nil {
			return err
		}
		for _, c := range rows {
			sessionID, subjectID := c.ID, c.SubjectID
			if err := s.audit.Write(ctx, tx, audit.Entry{
				ActorKind: audit.ActorSystem, Action: gen.AuditActionSessionClosedByServer, Outcome: audit.OutcomeOK,
				SubjectID: &subjectID, SessionID: &sessionID, GroupID: c.GroupID,
				Details: map[string]any{"timeout_minutes": timeoutMinutes},
			}); err != nil {
				return err
			}
		}
		closed = len(rows)
		return nil
	})
	return closed, err
}

var _ Closer = (*service)(nil)
