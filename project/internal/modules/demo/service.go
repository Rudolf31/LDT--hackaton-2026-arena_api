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
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/platform/pg"
)

type seeder struct {
	pool  *pgxpool.Pool
	users auth.Provisioner
	staff people.Provisioner
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
