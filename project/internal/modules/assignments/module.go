package assignments

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
	"arena-portal-backend/internal/modules/scenarios"
	"arena-portal-backend/internal/modules/settings"
)

// Deps — соседи модуля (архитектура 3.3: assignments → people, profiles,
// scenarios, audit) и сводка сессий от sessions, которая собирается
// позже и приходит через пересылку в cmd/portal (D-52).
type Deps struct {
	Audit    audit.Writer
	Access   auth.GroupAccess
	People   people.Service
	Profiles profiles.Service
	Versions scenarios.Versions
	Settings settings.Service
	Sessions SessionFacts
	// CodeSecret — ARENA_CODE_HMAC_SECRET; ключ кодов выводится из него
	// своей меткой (D-54).
	CodeSecret []byte
	// TrainerURL — адрес клиента-тренажёра для ссылки на код (D-53).
	TrainerURL string
}

type Module struct {
	Transport *Transport
	service   *service
}

func New(pool *pgxpool.Pool, d Deps) *Module {
	svc := &service{
		pool: pool, store: newStore(), audit: d.Audit, access: d.Access, people: d.People,
		profiles: d.Profiles, versions: d.Versions, settings: d.Settings, sessions: d.Sessions,
		hasher: newCodeHasher(d.CodeSecret), trainerURL: d.TrainerURL, now: time.Now, newCode: newCode,
	}
	return &Module{Transport: &Transport{service: svc}, service: svc}
}

// Entry — вход по коду и состояние назначения для sessions.
func (m *Module) Entry() Entry { return m.service }

// Canceller — отмена назначений при отзыве согласия, для people.
func (m *Module) Canceller() people.AssignmentCanceller { return m.service }
