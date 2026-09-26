package pg

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Violation — код ошибки PostgreSQL, переведённый в доменный вид: вид
// нарушения и имя ограничения. Что это значит по смыслу («сессия уже идёт»,
// «логин занят») решает store.go модуля, который знает свои ограничения —
// platform/pg только классифицирует (arena-portal-backend-architecture.md 3.2).
type Violation struct {
	Kind       ViolationKind
	Constraint string
	cause      *pgconn.PgError
}

func (v *Violation) Error() string {
	return v.cause.Message
}

func (v *Violation) Unwrap() error {
	return v.cause
}

type ViolationKind string

const (
	Unique                ViolationKind = "unique_violation"
	ForeignKey            ViolationKind = "foreign_key_violation"
	Check                 ViolationKind = "check_violation"
	InsufficientPrivilege ViolationKind = "insufficient_privilege"
)

// PostgreSQL SQLSTATE — https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	sqlStateUniqueViolation       = "23505"
	sqlStateForeignKeyViolation   = "23503"
	sqlStateCheckViolation        = "23514"
	sqlStateInsufficientPrivilege = "42501"
)

// AsViolation классифицирует ошибку PostgreSQL. Возвращает false для ошибок,
// которые не относятся ни к одному из четырёх разбираемых видов (например,
// потеря соединения) — такие store пробрасывает как есть.
func AsViolation(err error) (*Violation, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil, false
	}

	var kind ViolationKind
	switch pgErr.Code {
	case sqlStateUniqueViolation:
		kind = Unique
	case sqlStateForeignKeyViolation:
		kind = ForeignKey
	case sqlStateCheckViolation:
		kind = Check
	case sqlStateInsufficientPrivilege:
		kind = InsufficientPrivilege
	default:
		return nil, false
	}

	return &Violation{Kind: kind, Constraint: pgErr.ConstraintName, cause: pgErr}, true
}
