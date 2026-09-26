//go:build integration

package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/platform/pgtest"
)

func TestUpdateWritesAuditEntryInSameTransaction(t *testing.T) {
	pool := pgtest.NewDatabase(t)
	auditModule := audit.New()
	svc := newService(pool, auditModule, false)

	rehearsals := 7
	result, err := svc.Update(context.Background(), Patch{AdmissionRehearsals: &rehearsals}, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.AdmissionRehearsals != 7 {
		t.Fatalf("порог не сохранился: %d", result.AdmissionRehearsals)
	}

	var count int
	err = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'settings_changed'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if count != 1 {
		t.Fatalf("ожидалась ровно одна строка settings_changed, получили %d", count)
	}
}

type failingAuditWriter struct{}

func (failingAuditWriter) Write(ctx context.Context, tx pgx.Tx, entry audit.Entry) error {
	return errors.New("запись в журнал нарочно не удалась (тест)")
}

// TestUpdateRollsBackWhenAuditFails — I-4: запись в журнал и само действие
// в одной транзакции. Если журнал не пишется, действие не должно
// сохраниться — иначе это уже не «одна транзакция», а два независимых шага.
func TestUpdateRollsBackWhenAuditFails(t *testing.T) {
	pool := pgtest.NewDatabase(t)
	svc := &service{pool: pool, store: newStore(), audit: failingAuditWriter{}, demoMode: false}

	before, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	rehearsals := before.AdmissionRehearsals + 1
	if _, err := svc.Update(context.Background(), Patch{AdmissionRehearsals: &rehearsals}, nil); err == nil {
		t.Fatal("ожидалась ошибка — audit.Write нарочно упал")
	}

	after, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.AdmissionRehearsals != before.AdmissionRehearsals {
		t.Fatalf("настройки изменились несмотря на отказ журнала: было %d, стало %d", before.AdmissionRehearsals, after.AdmissionRehearsals)
	}
}
