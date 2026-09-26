package consents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/api/gen"
)

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type store struct{}

func newStore() *store { return &store{} }

// currentTemplates — действующая (последняя) версия каждого вида текста
// (arena-db.sql, consent_texts: «действует последняя версия своего вида»).
func (s *store) currentTemplates(ctx context.Context, q querier) (map[gen.ConsentKind]template, error) {
	rows, err := q.Query(ctx, `
		SELECT DISTINCT ON (kind) id, kind, version, body
		FROM consent_texts
		ORDER BY kind, created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("чтение текстов согласий: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (template, error) {
		var t template
		var kind string
		err := row.Scan(&t.ID, &kind, &t.Version, &t.Body)
		t.Kind = gen.ConsentKind(kind)
		return t, err
	})
	if err != nil {
		return nil, fmt.Errorf("чтение текстов согласий: %w", err)
	}
	out := make(map[gen.ConsentKind]template, len(list))
	for _, t := range list {
		out[t.Kind] = t
	}
	return out, nil
}

func (s *store) insertText(ctx context.Context, tx pgx.Tx, t NewText) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO consent_texts (kind, version, body, body_sha256)
		VALUES ($1, $2, $3, $4)`, string(t.Kind), t.Version, t.Body, sha256Hex(t.Body))
	if err != nil {
		return fmt.Errorf("запись текста согласия %s: %w", t.Kind, err)
	}
	return nil
}

// screenRecord — ответ участника на экране.
type screenRecord struct {
	SubjectID    uuid.UUID
	AssignmentID *uuid.UUID
	Kind         gen.ConsentKind
	Answer       gen.ConsentAnswerValue
	TextID       uuid.UUID
	ShownHMAC    []byte
}

func (s *store) insertScreenRecord(ctx context.Context, tx pgx.Tx, r screenRecord) (Record, error) {
	out := Record{Kind: r.Kind, Answer: r.Answer}
	var version string
	err := tx.QueryRow(ctx, `
		INSERT INTO consent_records (subject_id, assignment_id, kind, answer, text_id, shown_text_hmac)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, answered_at, (SELECT version FROM consent_texts WHERE id = $5)`,
		r.SubjectID, r.AssignmentID, string(r.Kind), string(r.Answer), r.TextID, r.ShownHMAC).
		Scan(&out.ID, &out.AnsweredAt, &version)
	if err != nil {
		return Record{}, fmt.Errorf("запись ответа на согласие: %w", err)
	}
	out.TextVersion = &version
	return out, nil
}

// writtenRecord — отметка HR о письменном согласии на оценку.
type writtenRecord struct {
	SubjectID       uuid.UUID
	DocumentRef     string
	DocumentChannel string
	SignedOn        time.Time
	ValidUntil      *time.Time
	RecordedBy      uuid.UUID
}

func (s *store) insertWritten(ctx context.Context, tx pgx.Tx, w writtenRecord) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO consent_records (subject_id, kind, answer, document_ref, document_channel, signed_on, valid_until, recorded_by)
		VALUES ($1, 'written_assessment', 'granted', $2, $3, $4, $5, $6)
		RETURNING id`,
		w.SubjectID, w.DocumentRef, w.DocumentChannel, w.SignedOn, w.ValidUntil, w.RecordedBy).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("запись письменного согласия: %w", err)
	}
	return id, nil
}

func (s *store) record(ctx context.Context, q querier, id uuid.UUID) (Record, error) {
	var r Record
	var kind, answer string
	err := q.QueryRow(ctx, `
		SELECT c.id, c.kind, c.answer, t.version, c.answered_at,
		       c.document_ref, c.document_channel, c.signed_on, c.valid_until, u.full_name
		FROM consent_records c
		LEFT JOIN consent_texts t ON t.id = c.text_id
		LEFT JOIN portal_users u ON u.id = c.recorded_by
		WHERE c.id = $1`, id).
		Scan(&r.ID, &kind, &answer, &r.TextVersion, &r.AnsweredAt,
			&r.DocumentRef, &r.DocumentChannel, &r.SignedOn, &r.ValidUntil, &r.RecordedByName)
	if err != nil {
		return Record{}, fmt.Errorf("чтение записи согласия: %w", err)
	}
	r.Kind, r.Answer = gen.ConsentKind(kind), gen.ConsentAnswerValue(answer)
	return r, nil
}

// latestUsable — последняя запись вида у участника и годится ли она:
// не отказ и, для письменного согласия, срок не истёк. Для экранных
// видов assignmentID сужает поиск до ответа перед этим назначением;
// письменное согласие у назначения не привязано.
func (s *store) latestUsable(ctx context.Context, q querier, subjectID uuid.UUID, assignmentID *uuid.UUID, kind gen.ConsentKind) (uuid.UUID, bool, error) {
	var id uuid.UUID
	var ok bool
	err := q.QueryRow(ctx, `
		SELECT id, usable AND (valid_until IS NULL OR valid_until >= current_date)
		FROM consent_records
		WHERE subject_id = $1 AND kind = $2
		  AND ($3::uuid IS NULL OR kind = 'written_assessment' OR assignment_id = $3)
		ORDER BY answered_at DESC, id
		LIMIT 1`, subjectID, string(kind), assignmentID).Scan(&id, &ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("чтение записи согласия: %w", err)
	}
	return id, ok, nil
}

func (s *store) recordInfos(ctx context.Context, q querier, ids []uuid.UUID) ([]RecordInfo, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `
		SELECT id, subject_id, assignment_id, kind, usable
		FROM consent_records WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("чтение записей согласия: %w", err)
	}
	defer rows.Close()
	var out []RecordInfo
	for rows.Next() {
		var r RecordInfo
		var kind string
		if err := rows.Scan(&r.ID, &r.SubjectID, &r.AssignmentID, &kind, &r.Usable); err != nil {
			return nil, fmt.Errorf("чтение записей согласия: %w", err)
		}
		r.Kind = gen.ConsentKind(kind)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("чтение записей согласия: %w", err)
	}
	return out, nil
}
