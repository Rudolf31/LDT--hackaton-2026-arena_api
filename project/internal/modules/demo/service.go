package demo

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/db/seed"
	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/consents"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
	"arena-portal-backend/internal/platform/pg"
)

type seeder struct {
	pool     *pgxpool.Pool
	users    auth.Provisioner
	staff    people.Provisioner
	profiles profiles.Provisioner
	consents consents.Provisioner
}

// Baseline — что завела базовая заливка при этом старте.
type Baseline struct {
	DefaultProfileCreated bool
	ConsentTextsCreated   int
}

// EnsureBaseline — профиль тренажёра по умолчанию и тексты согласий
// версии 1, если их ещё нет (D-46). Отдельно от SeedIfEmpty и независимо
// от ARENA_DEMO: без профиля по умолчанию не создать назначение, без
// текстов — не показать экран согласия, а уже развёрнутая база с
// пользователями иначе их никогда бы не получила. Одна транзакция.
func (m *Module) EnsureBaseline(ctx context.Context) (Baseline, error) {
	base, err := seed.LoadBase()
	if err != nil {
		return Baseline{}, err
	}
	texts, err := seed.LoadConsentTexts()
	if err != nil {
		return Baseline{}, err
	}
	newTexts := make([]consents.NewText, 0, len(texts))
	for _, t := range texts {
		newTexts = append(newTexts, consents.NewText{Kind: gen.ConsentKind(t.Kind), Version: t.Version, Body: t.Body})
	}

	var out Baseline
	err = pg.WithTx(ctx, m.seeder.pool, func(ctx context.Context, tx pgx.Tx) error {
		created, err := m.seeder.profiles.EnsureDefaultTx(ctx, tx, base.DefaultProfile.Name, base.DefaultProfile.Settings)
		if err != nil {
			return fmt.Errorf("заливка профиля по умолчанию: %w", err)
		}
		out.DefaultProfileCreated = created
		if out.ConsentTextsCreated, err = m.seeder.consents.EnsureTextsTx(ctx, tx, newTexts); err != nil {
			return fmt.Errorf("заливка текстов согласий: %w", err)
		}
		return nil
	})
	if err != nil {
		return Baseline{}, err
	}
	return out, nil
}

// SeedIfEmpty заливает базовый набор, если в портале нет ни одного
// пользователя, — независимо от ARENA_DEMO (D-23): иначе в портал некому
// войти. Всё одной транзакцией: половина заливки хуже, чем никакой.
// Возвращает логины заведённых пользователей (пусто — база уже не пустая).
func (m *Module) SeedIfEmpty(ctx context.Context) ([]string, error) {
	return m.seeder.run(ctx)
}

func (s *seeder) run(ctx context.Context) ([]string, error) {
	base, err := seed.LoadBase()
	if err != nil {
		return nil, err
	}

	var logins []string
	err = pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		has, err := s.users.HasUsers(ctx, tx)
		if err != nil || has {
			return err
		}

		userIDs := make(map[string]uuid.UUID, len(base.Users))
		var grantor uuid.UUID
		for _, u := range base.Users {
			id, err := s.users.CreateUserTx(ctx, tx, auth.NewUser{
				Login: u.Login, Password: u.Password, FullName: u.FullName, Role: gen.Role(u.Role),
			}, nil)
			if err != nil {
				return fmt.Errorf("заливка пользователя %s: %w", u.Login, err)
			}
			userIDs[u.Login] = id
			logins = append(logins, u.Login)
			if gen.Role(u.Role) == gen.Admin && grantor == uuid.Nil {
				grantor = id
			}
		}
		if grantor == uuid.Nil {
			return fmt.Errorf("в db/seed/base.json нет администратора — некому выдать доступ к группам")
		}

		groupIDs := make(map[string]uuid.UUID, len(base.Groups))
		for _, g := range base.Groups {
			department := g.Department
			id, err := s.staff.CreateGroupTx(ctx, tx, people.NewGroup{Name: g.Name, Department: &department}, nil)
			if err != nil {
				return fmt.Errorf("заливка группы: %w", err)
			}
			groupIDs[g.Name] = id
			for _, login := range g.AccessFor {
				userID, ok := userIDs[login]
				if !ok {
					return fmt.Errorf("в db/seed/base.json доступ к группе выдан неизвестному логину %s", login)
				}
				if err := s.users.GrantAccessTx(ctx, tx, userID, id, grantor, nil); err != nil {
					return fmt.Errorf("заливка доступа к группе: %w", err)
				}
			}
		}

		for i, p := range base.People {
			groupID, ok := groupIDs[p.Group]
			if !ok {
				return fmt.Errorf("в db/seed/base.json сотрудник №%d ссылается на незаведённую группу", i+1)
			}
			if _, err := s.staff.CreatePersonTx(ctx, tx, people.NewPerson{
				GroupID: groupID, FullName: p.FullName, Pseudonym: p.Pseudonym,
				PersonnelNo: p.PersonnelNo, JobTitle: p.JobTitle,
			}, nil); err != nil {
				return fmt.Errorf("заливка сотрудника №%d: %w", i+1, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return logins, nil
}
