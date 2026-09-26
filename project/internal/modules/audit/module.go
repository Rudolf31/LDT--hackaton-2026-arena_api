package audit

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Module — единственная реализация Writer. Своего пула не держит: пишет
// только через tx, которую даёт вызывающий сценарий использования.
type Module struct {
	store *store
}

func New() *Module {
	return &Module{store: newStore()}
}

func (m *Module) Write(ctx context.Context, tx pgx.Tx, entry Entry) error {
	return m.store.insert(ctx, tx, entry)
}

var _ Writer = (*Module)(nil)

// NewTransport собирает адреса чтения журнала. Имена пользователей,
// названия групп и номера участников приходят из контрактов auth и people.
func (m *Module) NewTransport(pool *pgxpool.Pool, users UserLookup, subjects SubjectLookup) *Transport {
	return &Transport{service: &service{pool: pool, store: m.store, writer: m, users: users, subjects: subjects}}
}
