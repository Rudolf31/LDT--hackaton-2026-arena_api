package audit

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/pg"
)

// Filter — фильтры экрана журнала и его выгрузки (UC-A-09).
type Filter struct {
	From          *time.Time
	To            *time.Time
	ActorUserID   *uuid.UUID
	GroupID       *uuid.UUID
	SubjectNumber *string
	Action        *gen.AuditAction
	Outcome       *string
}

// View — строка журнала для экрана: участник номером, не именем.
type View struct {
	row
	ActorName     *string
	GroupName     *string
	SubjectNumber *string
}

type service struct {
	pool     *pgxpool.Pool
	store    *store
	writer   Writer
	users    UserLookup
	subjects SubjectLookup
}

func (s *service) toStoreFilter(ctx context.Context, f Filter) (filter, bool, error) {
	sf := filter{From: f.From, To: f.To, ActorUserID: f.ActorUserID, GroupID: f.GroupID, Outcome: f.Outcome}
	if f.Action != nil {
		a := string(*f.Action)
		sf.Action = &a
	}
	if f.SubjectNumber != nil && strings.TrimSpace(*f.SubjectNumber) != "" {
		id, ok, err := s.subjects.SubjectIDByNumber(ctx, strings.TrimSpace(*f.SubjectNumber))
		if err != nil {
			return filter{}, false, err
		}
		if !ok {
			return filter{}, false, nil
		}
		sf.SubjectID = &id
	}
	return sf, true, nil
}

func (s *service) List(ctx context.Context, f Filter, limit, offset int) ([]View, int, error) {
	sf, matchable, err := s.toStoreFilter(ctx, f)
	if err != nil || !matchable {
		return []View{}, 0, err
	}
	sf.Limit, sf.Offset = limit, offset
	rows, total, err := s.store.list(ctx, s.pool, sf)
	if err != nil {
		return nil, 0, err
	}
	views, err := s.resolve(ctx, rows)
	return views, total, err
}

// Export — журнал в CSV. Сама выгрузка пишет audit_exported с фильтрами
// и числом строк в той же транзакции, в которой читаются строки.
func (s *service) Export(ctx context.Context, actorID uuid.UUID, f Filter) ([]byte, error) {
	sf, matchable, err := s.toStoreFilter(ctx, f)
	if err != nil {
		return nil, err
	}
	var rows []row
	err = pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		if matchable {
			var err error
			if rows, _, err = s.store.list(ctx, tx, sf); err != nil {
				return err
			}
		}
		count := len(rows)
		return s.writer.Write(ctx, tx, Entry{
			ActorKind: ActorUser, ActorUserID: &actorID,
			Action: gen.AuditActionAuditExported, Outcome: OutcomeOK,
			RowsCount: &count, Details: map[string]any{"filters": filterDetails(f)},
		})
	})
	if err != nil {
		return nil, err
	}
	views, err := s.resolve(ctx, rows)
	if err != nil {
		return nil, err
	}
	return toCSV(views)
}

func filterDetails(f Filter) map[string]any {
	d := map[string]any{}
	if f.From != nil {
		d["from"] = f.From.Format(time.RFC3339)
	}
	if f.To != nil {
		d["to"] = f.To.Format(time.RFC3339)
	}
	if f.ActorUserID != nil {
		d["actor_user_id"] = f.ActorUserID.String()
	}
	if f.GroupID != nil {
		d["group_id"] = f.GroupID.String()
	}
	if f.SubjectNumber != nil {
		d["subject_number"] = *f.SubjectNumber
	}
	if f.Action != nil {
		d["action"] = string(*f.Action)
	}
	if f.Outcome != nil {
		d["outcome"] = *f.Outcome
	}
	return d
}

func (s *service) resolve(ctx context.Context, rows []row) ([]View, error) {
	var userIDs, groupIDs, subjectIDs []uuid.UUID
	for _, r := range rows {
		if r.ActorUserID != nil {
			userIDs = append(userIDs, *r.ActorUserID)
		}
		if r.GroupID != nil {
			groupIDs = append(groupIDs, *r.GroupID)
		}
		if r.SubjectID != nil {
			subjectIDs = append(subjectIDs, *r.SubjectID)
		}
	}
	users, err := s.users.UserNames(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	groups, err := s.subjects.GroupNames(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	numbers, err := s.subjects.SubjectNumbers(ctx, subjectIDs)
	if err != nil {
		return nil, err
	}

	out := make([]View, 0, len(rows))
	for _, r := range rows {
		v := View{row: r}
		v.ActorName = lookup(users, r.ActorUserID)
		v.GroupName = lookup(groups, r.GroupID)
		v.SubjectNumber = lookup(numbers, r.SubjectID)
		out = append(out, v)
	}
	return out, nil
}

func lookup(m map[uuid.UUID]string, id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	if v, ok := m[*id]; ok {
		return &v
	}
	return nil
}

func toCSV(views []View) ([]byte, error) {
	var buf bytes.Buffer
	// BOM — чтобы Excel открыл кириллицу без вопросов о кодировке.
	buf.WriteString("\xEF\xBB\xBF")
	w := csv.NewWriter(&buf)
	w.Comma = ';'
	if err := w.Write([]string{"Время", "Кто", "Вид", "Действие", "Итог", "Участник", "Группа", "Сессия", "Строк", "Подробности"}); err != nil {
		return nil, fmt.Errorf("запись CSV журнала: %w", err)
	}
	for _, v := range views {
		details, err := json.Marshal(v.Details)
		if err != nil {
			return nil, fmt.Errorf("details журнала в CSV: %w", err)
		}
		record := []string{
			v.OccurredAt.Format(time.RFC3339),
			deref(v.ActorName),
			v.ActorKind,
			v.Action,
			v.Outcome,
			deref(v.SubjectNumber),
			deref(v.GroupName),
			idString(v.SessionID),
			intString(v.RowsCount),
			string(details),
		}
		for i := range record {
			record[i] = neutralizeFormula(record[i])
		}
		if err := w.Write(record); err != nil {
			return nil, fmt.Errorf("запись CSV журнала: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("запись CSV журнала: %w", err)
	}
	return buf.Bytes(), nil
}

// neutralizeFormula — ячейка, начинающаяся с = + - @, в Excel стала бы
// формулой; апостроф превращает её обратно в текст.
func neutralizeFormula(s string) string {
	if s != "" && strings.ContainsRune("=+-@", rune(s[0])) {
		return "'" + s
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func idString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func intString(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}
