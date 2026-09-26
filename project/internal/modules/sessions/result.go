package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/platform/crypto"
)

// Упрощённый вид результата для участника (D-63): ответ завершения и
// повторного ответа судьи. Полный вид — таблицу предметов, вырезание
// скрытого от участника из блока оценок — собирает results (этап 09).

// ResultView — то, что уходит в ParticipantResult.
type ResultView struct {
	Session       sessionRow
	ScenarioTitle string
	VersionNumber int
	Fingerprint   string
	Marks         []MarkView
	Epilogue      *string
	Summary       []json.RawMessage
	Transcript    []TranscriptView
	CanRetryJudge bool
}

type MarkView struct {
	Kind    gen.MarkKind
	Text    string
	Replies []int
}

// TranscriptView — реплика для участника и для судьи: без этапа
// (arena-scoring 12.3). Text nil — ключ участника уничтожен.
type TranscriptView struct {
	Seq     int
	ReplyNo int
	Speaker string
	Text    *string
	AtMs    int
	Offer   json.RawMessage
	Terms   json.RawMessage
}

// viewOf — вид результата по сохранённой сессии.
func (s *service) viewOf(ctx context.Context, id uuid.UUID) (ResultView, error) {
	row, err := s.store.session(ctx, s.pool, id)
	if err != nil {
		return ResultView{}, err
	}
	version, err := s.versions.Version(ctx, row.VersionID)
	if err != nil {
		return ResultView{}, err
	}
	jc, err := s.judgeContext(ctx, row)
	if err != nil {
		return ResultView{}, err
	}
	turns, err := s.store.turns(ctx, s.pool, row.ID)
	if err != nil {
		return ResultView{}, err
	}
	key, err := s.keyOrNil(ctx, row.SubjectID)
	if err != nil {
		return ResultView{}, err
	}
	transcript, err := transcriptOf(key, turns)
	if err != nil {
		return ResultView{}, err
	}
	summary, err := summaryOf(key, row.JudgeAnswerEnc)
	if err != nil {
		return ResultView{}, err
	}

	out := ResultView{
		Session: row, ScenarioTitle: version.Title, VersionNumber: version.Number, Fingerprint: version.Fingerprint,
		Marks: marksOf(row, turns), Summary: summary, Transcript: transcript,
		CanRetryJudge: isClosed(row.Status) && (row.ProcessStatus == string(gen.ProcessStatusPending) ||
			row.ProcessStatus == string(gen.ProcessStatusNotReceived)),
	}
	if row.FinalID != nil {
		if f, ok := jc.Finals[*row.FinalID]; ok && f.Epilogue != "" {
			epilogue := f.Epilogue
			out.Epilogue = &epilogue
		}
	}
	return out, nil
}

// keyOrNil — ключ данных участника; nil — уничтожен отзывом согласия.
func (s *service) keyOrNil(ctx context.Context, subjectID uuid.UUID) ([]byte, error) {
	key, err := s.people.DataKey(ctx, subjectID)
	if errors.Is(err, people.ErrKeyDestroyed) {
		return nil, nil
	}
	return key, err
}

func transcriptOf(key []byte, turns []turnRow) ([]TranscriptView, error) {
	out := make([]TranscriptView, 0, len(turns))
	for _, t := range turns {
		line := TranscriptView{
			Seq: t.Seq, ReplyNo: t.ReplyNo, Speaker: t.Speaker, AtMs: t.AtMs, Offer: t.Offer, Terms: t.Terms,
		}
		if key != nil && t.TextEnc != nil {
			plain, err := crypto.Decrypt(key, t.TextEnc)
			if err != nil {
				return nil, fmt.Errorf("расшифровка реплики %d: %w", t.Seq, err)
			}
			text := string(plain)
			line.Text = &text
		}
		out = append(out, line)
	}
	return out, nil
}

// summaryOf — пункты резюме из ответа судьи как пришли; портал цитаты не
// проверяет (архитектура 8.2).
func summaryOf(key, answerEnc []byte) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	if key == nil || answerEnc == nil {
		return out, nil
	}
	plain, err := crypto.Decrypt(key, answerEnc)
	if err != nil {
		return nil, fmt.Errorf("расшифровка ответа судьи: %w", err)
	}
	var answer struct {
		Summary []json.RawMessage `json:"summary"`
	}
	if err := json.Unmarshal(plain, &answer); err != nil {
		return nil, fmt.Errorf("ответ судьи: %w", err)
	}
	if answer.Summary != nil {
		out = answer.Summary
	}
	return out, nil
}

// marksOf — пометки под шапкой результата для участника
// (arena-portal-hr.md 12.5). «Нарушение не подтверждено» и «Запросов к
// судье» — только у HR.
func marksOf(row sessionRow, turns []turnRow) []MarkView {
	marks := []MarkView{}
	add := func(kind gen.MarkKind, text string, replies []int) {
		marks = append(marks, MarkView{Kind: kind, Text: text, Replies: replies})
	}
	if row.Mode == string(gen.ModeTraining) {
		add(gen.MarkKindTraining, "Тренировка — не основание для кадрового решения", nil)
	}
	if row.IsDemo {
		add(gen.MarkKindDemo, "Демо", nil)
	}
	switch gen.SessionStatus(row.Status) {
	case gen.SessionStatusAbandoned:
		add(gen.MarkKindAbandoned, "Прервана — разговор не завершён, оценки не ставятся", nil)
	case gen.SessionStatusEndedByParticipant:
		add(gen.MarkKindEndedByParticipant, "Прервана участником", nil)
	case gen.SessionStatusTurnLimit:
		add(gen.MarkKindTurnLimit, "Закончились ходы", nil)
	case gen.SessionStatusTimeLimit:
		add(gen.MarkKindTimeLimit, "Закончилось время", nil)
	case gen.SessionStatusConnectionLost:
		add(gen.MarkKindConnectionLost, "Прервана: потеря связи", nil)
	}
	if row.Simplified {
		var replies []int
		for _, t := range turns {
			if t.MoveSource != nil && *t.MoveSource == string(gen.MoveSourceKeywords) {
				replies = append(replies, t.ReplyNo)
			}
		}
		add(gen.MarkKindSimplified, "Упрощённый режим — часть реплик разобрана по ключевым словам", replies)
	}
	if row.StubUsed {
		add(gen.MarkKindStub, "Заглушка — разговор шёл с записанным диалогом", nil)
	}
	if row.OpponentViolation > 0 {
		var replies []int
		for _, t := range turns {
			if t.OpponentViolation {
				replies = append(replies, t.ReplyNo)
			}
		}
		add(gen.MarkKindOpponentViolations, fmt.Sprintf("Оппонент нарушил правила: %d", row.OpponentViolation), replies)
	}
	if row.Lucky {
		add(gen.MarkKindLucky, "Повезло: сделка лучше, чем ведение переговоров", nil)
	}
	switch gen.ProcessStatus(row.ProcessStatus) {
	case gen.ProcessStatusTooShort:
		add(gen.MarkKindTooShort, "Слишком короткий разговор для оценки процесса", nil)
	case gen.ProcessStatusNotReceived:
		add(gen.MarkKindProcessNotReceived, "Разбор процесса не получен", nil)
	}
	return marks
}

// durationMs — длительность закрытой сессии.
func durationMs(started time.Time, ended *time.Time) *int {
	if ended == nil {
		return nil
	}
	ms := int(ended.Sub(started).Milliseconds())
	return &ms
}
