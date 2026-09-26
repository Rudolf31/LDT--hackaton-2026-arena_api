// Package sessions — сессии тренажёра: вход по коду, старт, повторная
// выдача документа и доступа к моделям после F5 (этап 07); события хода,
// завершение и судья — этап 08.
package sessions

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/modules/assignments"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/consents"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
	"arena-portal-backend/internal/modules/scenarios"
)

// Deps — соседи модуля (архитектура 3.3: sessions → assignments,
// consents, profiles, scenarios, people).
type Deps struct {
	Entry    assignments.Entry
	Versions scenarios.Versions
	People   people.Service
	Profiles profiles.Service
	Consents consents.Service
	Tokens   auth.TrainerTokens
}

type Module struct {
	Transport *Transport
	service   *service
}

func New(pool *pgxpool.Pool, d Deps) *Module {
	svc := &service{
		pool: pool, store: newStore(), entry: d.Entry, versions: d.Versions, people: d.People,
		profiles: d.Profiles, consents: d.Consents, tokens: d.Tokens, now: time.Now,
	}
	return &Module{Transport: &Transport{service: svc}, service: svc}
}

// Audience — кто пришёл за экраном согласия (consents, D-48).
func (m *Module) Audience() consents.AudienceResolver { return m.service }

// Facts — сводка сессий по назначениям (assignments, D-52).
func (m *Module) Facts() assignments.SessionFacts { return m.service }

// VersionCounts — число сессий на версиях сценария (scenarios).
func (m *Module) VersionCounts() scenarios.VersionSessionCounter { return m.service }

// Running — идущие сессии по профилю тренажёра (profiles).
func (m *Module) Running() profiles.SessionCounter { return m.service }
