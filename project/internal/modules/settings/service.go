package settings

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/platform/pg"
)

type service struct {
	pool     *pgxpool.Pool
	store    *store
	audit    audit.Writer
	demoMode bool
}

func newService(pool *pgxpool.Pool, auditWriter audit.Writer, demoMode bool) *service {
	return &service{pool: pool, store: newStore(), audit: auditWriter, demoMode: demoMode}
}

func (s *service) Get(ctx context.Context) (Settings, error) {
	r, err := s.store.get(ctx, s.pool)
	if err != nil {
		return Settings{}, err
	}
	return s.toSettings(r), nil
}

func (s *service) Update(ctx context.Context, patch Patch, actorUserID *uuid.UUID) (Settings, error) {
	var result Settings
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		r, err := s.store.update(ctx, tx, patch, actorUserID)
		if err != nil {
			return err
		}
		result = s.toSettings(r)

		actorKind := audit.ActorSystem
		if actorUserID != nil {
			actorKind = audit.ActorUser
		}

		return s.audit.Write(ctx, tx, audit.Entry{
			ActorKind:   actorKind,
			ActorUserID: actorUserID,
			Action:      gen.AuditActionSettingsChanged,
			Outcome:     audit.OutcomeOK,
			Details: map[string]any{
				"admission_rehearsals":     patch.AdmissionRehearsals,
				"abandon_timeout_minutes":  patch.AbandonTimeoutMinutes,
				"code_max_failed_attempts": patch.CodeMaxFailedAttempts,
			},
		})
	})
	if err != nil {
		return Settings{}, err
	}
	return result, nil
}

func (s *service) toSettings(r row) Settings {
	return Settings{
		AdmissionRehearsals:   r.AdmissionRehearsals,
		AbandonTimeoutMinutes: r.AbandonTimeoutMinutes,
		CodeMaxFailedAttempts: r.CodeMaxFailedAttempts,
		DemoMode:              s.demoMode,
		UpdatedAt:             &r.UpdatedAt,
		UpdatedByName:         r.UpdatedByName,
	}
}

var _ Service = (*service)(nil)
