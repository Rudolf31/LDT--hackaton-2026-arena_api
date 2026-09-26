//go:build integration

package assignments

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
	"arena-portal-backend/internal/modules/scenarios"
	"arena-portal-backend/internal/modules/settings"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/pgtest"
)

// Соседи модуля — заглушки: тест про выпуск кодов, а не про людей,
// профили и версии. Строки, на которые ссылаются внешние ключи
// assignments, заводятся прямо в базе.

type allowAll struct{ auth.GroupAccess }

func (allowAll) HasAccess(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return true, nil }

type fixedPeople struct {
	people.Service
	group uuid.UUID
}

func (p fixedPeople) PersonRefs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]people.PersonRef, error) {
	out := map[uuid.UUID]people.PersonRef{}
	name := "Сотрудник"
	for i, id := range ids {
		out[id] = people.PersonRef{SubjectID: id, Number: fmt.Sprintf("N-%d", i), Present: true,
			GroupID: p.group, DisplayName: &name, HasFullName: true}
	}
	return out, nil
}

func (p fixedPeople) GroupProfile(context.Context, uuid.UUID) (*uuid.UUID, error) { return nil, nil }

type fixedProfiles struct {
	profiles.Service
	id uuid.UUID
}

func (p fixedProfiles) DefaultID(context.Context) (uuid.UUID, error) { return p.id, nil }

type fixedVersions struct {
	scenarios.Versions
	id uuid.UUID
}

func (v fixedVersions) Version(context.Context, uuid.UUID) (scenarios.VersionInfo, error) {
	return scenarios.VersionInfo{ID: v.id, Mode: gen.ModeTraining}, nil
}

type fixture struct {
	pool      *pgxpool.Pool
	userID    uuid.UUID
	groupID   uuid.UUID
	versionID uuid.UUID
	profileID uuid.UUID
	subjects  []uuid.UUID
}

func newFixture(t *testing.T, subjects int) fixture {
	t.Helper()
	ctx := context.Background()
	f := fixture{pool: pgtest.NewDatabase(t)}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.pool.QueryRow(ctx, `INSERT INTO portal_users (login, password_hash, full_name, role)
		VALUES ('hr_user', 'x', 'HR', 'methodologist') RETURNING id`).Scan(&f.userID))
	must(f.pool.QueryRow(ctx, `INSERT INTO trainer_profiles (name, is_default) VALUES ('p', true) RETURNING id`).Scan(&f.profileID))
	must(f.pool.QueryRow(ctx, `INSERT INTO employee_groups (name) VALUES ('g') RETURNING id`).Scan(&f.groupID))
	var scenarioID uuid.UUID
	must(f.pool.QueryRow(ctx, `INSERT INTO scenarios (slug, mode, origin, generation, created_by)
		VALUES ('t', 'training', 'manual', '{}', $1) RETURNING id`, f.userID).Scan(&scenarioID))
	doc := `{"format":"arena-scenario/1","passport":{"id":"t","version":1,"mode":"training","title":"T","sphere":"sales","negotiation_type":"distributive"}}`
	must(f.pool.QueryRow(ctx, `INSERT INTO scenario_versions (scenario_id, number, fingerprint, format, engine_version,
			document, mode, title, sphere, negotiation_type, published_by)
		VALUES ($1, 1, repeat('a', 64), 'arena-scenario/1', 'e', $2, 'training', 'T', 'sales', 'distributive', $3)
		RETURNING id`, scenarioID, doc, f.userID).Scan(&f.versionID))
	rows, err := f.pool.Query(ctx, `INSERT INTO subjects (number, data_key_wrapped)
		SELECT 'N-' || i, '\x00'::bytea FROM generate_series(1, $1) i RETURNING id`, subjects)
	must(err)
	for rows.Next() {
		var id uuid.UUID
		must(rows.Scan(&id))
		f.subjects = append(f.subjects, id)
	}
	must(rows.Err())
	return f
}

func (f fixture) service(codes func() (string, error)) *service {
	m := New(f.pool, Deps{
		Audit: audit.New(), Access: allowAll{}, People: fixedPeople{group: f.groupID},
		Profiles: fixedProfiles{id: f.profileID}, Versions: fixedVersions{id: f.versionID},
		Settings:   settings.New(f.pool, audit.New(), false).Service(),
		CodeSecret: make([]byte, 32), TrainerURL: "http://trainer.test",
	})
	m.service.newCode = codes
	return m.service
}

// collidingCodes — каждый второй код начинается с одного и того же
// селектора: все, кроме первого такого, совпадают с уже действующим.
func collidingCodes() (func() (string, error), *int) {
	calls := 0
	return func() (string, error) {
		calls++
		code, err := newCode()
		if err != nil || calls%2 == 0 {
			return code, err
		}
		body := "AAAA" + code[4:codeRandomLen]
		return body + string(checkChar(body)), nil
	}, &calls
}

// TestBatchOf500SurvivesSelectorCollisions — «Готово, когда»: пачка на
// 500 кодов выпускается без отката при совпадениях селектора
// (архитектура 6.3).
func TestBatchOf500SurvivesSelectorCollisions(t *testing.T) {
	f := newFixture(t, 500)
	codes, calls := collidingCodes()
	svc := f.service(codes)
	ctx := context.Background()

	res, err := svc.Create(ctx, actor.Actor{UserID: f.userID, Role: gen.Methodologist}, CreateInput{
		SubjectIDs: f.subjects, VersionID: f.versionID, Difficulty: gen.DifficultyNormal, DueAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 500 || len(res.Skipped) != 0 {
		t.Fatalf("создано %d, пропущено %d", len(res.Created), len(res.Skipped))
	}
	if *calls <= 500 {
		t.Fatalf("совпадений селектора не было (%d вызовов генератора) — тест ничего не проверил", *calls)
	}
	var assignments, active, selectors int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM assignments`).Scan(&assignments); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT selector_hash) FROM access_codes WHERE revoked_at IS NULL`).
		Scan(&active, &selectors); err != nil {
		t.Fatal(err)
	}
	if assignments != 500 || active != 500 || selectors != 500 {
		t.Fatalf("назначений %d, действующих кодов %d, разных селекторов %d", assignments, active, selectors)
	}
	seen := map[string]bool{}
	for _, c := range res.Created {
		if seen[c.Code] {
			t.Fatalf("код %s выдан дважды", c.Code)
		}
		seen[c.Code] = true
		normalized, _ := normalizeCode(c.Code)
		var n int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM access_codes WHERE code_hash = $1 AND revoked_at IS NULL`,
			svc.hasher.full(normalized)).Scan(&n); err != nil || n != 1 {
			t.Fatalf("код %s не найден по HMAC: %d %v", c.Code, n, err)
		}
	}
}

// TestIssueCodeGivesUpAfterFiveCollisions — генератор, который всегда
// попадает в занятый селектор, не зацикливает выпуск.
func TestIssueCodeGivesUpAfterFiveCollisions(t *testing.T) {
	f := newFixture(t, 2)
	svc := f.service(func() (string, error) {
		fresh, err := newCode()
		body := "BBBB" + fresh[4:codeRandomLen]
		return body + string(checkChar(body)), err
	})
	ctx := context.Background()
	a := actor.Actor{UserID: f.userID, Role: gen.Methodologist}
	in := CreateInput{SubjectIDs: f.subjects[:1], VersionID: f.versionID, Difficulty: gen.DifficultyNormal, DueAt: time.Now().Add(time.Hour)}
	if _, err := svc.Create(ctx, a, in); err != nil {
		t.Fatal(err)
	}
	in.SubjectIDs = f.subjects[1:]
	if _, err := svc.Create(ctx, a, in); err == nil {
		t.Fatal("пять совпадений подряд должны закончиться ошибкой, а не бесконечным выпуском")
	}
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM assignments`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("неудачное назначение откатилось целиком: назначений %d (%v)", n, err)
	}
}
