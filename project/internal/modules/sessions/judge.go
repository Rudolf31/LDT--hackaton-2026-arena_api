package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/pg"
)

// Повторный запрос к судье участника (архитектура 8.4) делает клиент:
// портал отдаёт данные для запроса и принимает ответ вместе с блоком
// оценок, пересчитанным клиентом (D-62). Портал к моделям не ходит.

// JudgePack — данные для повторного запроса (arena-api.yaml, JudgeRetryPack).
// Пределов оппонента, этапов и условий переходов здесь нет.
type JudgePack struct {
	SessionID      uuid.UUID
	CriteriaSet    string
	Transcript     []TranscriptView
	JudgeDecisions []json.RawMessage
	OfferLog       []json.RawMessage
	CodeEpisodes   []json.RawMessage
	Interests      json.RawMessage
	RevealedFacts  []FactText
	Brief          json.RawMessage
	Access         access
}

type FactText struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// judgeGate — «процесс» ещё можно получить: сессия закрыта, а разбора нет.
// Сохранённый и проверенный ответ больше не перезапрашивается.
func judgeGate(row sessionRow) error {
	switch {
	case row.Status == string(gen.SessionStatusInProgress):
		return httpx.NewError(httpx.KindSessionRunning, "Разговор ещё идёт — сначала завершите сессию.")
	case row.Status == string(gen.SessionStatusAbandoned):
		return httpx.NewError(httpx.KindSessionFinished, "Разговор не завершён — оценки не ставятся.")
	}
	switch gen.ProcessStatus(row.ProcessStatus) {
	case gen.ProcessStatusPending, gen.ProcessStatusNotReceived:
		return nil
	case gen.ProcessStatusOk:
		return httpx.NewError(httpx.KindAlreadyAnswered, "Разбор процесса уже получен — повторно он не запрашивается.")
	case gen.ProcessStatusTooShort:
		return httpx.NewError(httpx.KindSessionFinished, "Слишком короткий разговор для оценки процесса.")
	}
	return httpx.NewError(httpx.KindSessionFinished, "Для этой сессии разбор процесса не запрашивается.")
}

// JudgeRetry — пакет для повторного запроса к судье (UC-P-08).
func (s *service) JudgeRetry(ctx context.Context, sessionID uuid.UUID) (JudgePack, error) {
	t, err := participant(ctx)
	if err != nil {
		return JudgePack{}, err
	}
	row, err := s.store.session(ctx, s.pool, sessionID)
	if errors.Is(err, errSessionNotFound) {
		return JudgePack{}, errSessionMissing()
	}
	if err != nil {
		return JudgePack{}, err
	}
	if row.SubjectID != *t.SubjectID || row.AssignmentID == nil || *row.AssignmentID != *t.AssignmentID {
		return JudgePack{}, errSessionMissing()
	}
	if err := judgeGate(row); err != nil {
		return JudgePack{}, err
	}
	acc, err := s.accessFor(ctx, row)
	if err != nil {
		return JudgePack{}, err
	}
	jc, err := s.judgeContext(ctx, row)
	if err != nil {
		return JudgePack{}, err
	}
	turns, err := s.store.turns(ctx, s.pool, row.ID)
	if err != nil {
		return JudgePack{}, err
	}
	key, err := s.keyOrNil(ctx, row.SubjectID)
	if err != nil {
		return JudgePack{}, err
	}
	transcript, err := transcriptOf(key, turns)
	if err != nil {
		return JudgePack{}, err
	}

	pack := JudgePack{
		SessionID: row.ID, CriteriaSet: row.CriteriaSet, Transcript: transcript,
		JudgeDecisions: []json.RawMessage{}, OfferLog: []json.RawMessage{}, RevealedFacts: []FactText{},
		Interests: jc.Interests, Brief: jc.Brief, Access: acc,
	}
	seenFacts := map[string]bool{}
	for _, tr := range turns {
		if tr.Judge != nil {
			if pack.JudgeDecisions, err = appendJSON(pack.JudgeDecisions, map[string]any{
				"seq": tr.Seq, "reply_no": tr.ReplyNo, "judge": json.RawMessage(tr.Judge),
			}); err != nil {
				return JudgePack{}, err
			}
		}
		if offer := firstNonNil(tr.Offer, tr.Terms); offer != nil {
			if pack.OfferLog, err = appendJSON(pack.OfferLog, map[string]any{
				"seq": tr.Seq, "reply_no": tr.ReplyNo, "speaker": tr.Speaker, "offer": json.RawMessage(offer),
			}); err != nil {
				return JudgePack{}, err
			}
		}
		for _, id := range tr.RevealedFacts {
			if !seenFacts[id] {
				seenFacts[id] = true
				pack.RevealedFacts = append(pack.RevealedFacts, FactText{ID: id, Text: jc.FactTexts[id]})
			}
		}
	}
	if pack.CodeEpisodes, err = codeEpisodes(row.Scores); err != nil {
		return JudgePack{}, err
	}
	return pack, nil
}

// codeEpisodes — эпизоды, найденные кодом, и сработавшие потолки из
// сохранённого блока оценок (arena-scoring 8.1).
func codeEpisodes(scores []byte) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	if scores == nil {
		return out, nil
	}
	var block struct {
		Process *struct {
			Episodes []json.RawMessage `json:"episodes"`
			Criteria []struct {
				ID   string            `json:"id"`
				Caps []json.RawMessage `json:"caps"`
			} `json:"criteria"`
		} `json:"process"`
	}
	if err := json.Unmarshal(scores, &block); err != nil {
		return nil, fmt.Errorf("блок оценок сессии: %w", err)
	}
	if block.Process == nil {
		return out, nil
	}
	for _, ep := range block.Process.Episodes {
		var e struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal(ep, &e); err == nil && e.Source == "code" {
			out = append(out, ep)
		}
	}
	for _, c := range block.Process.Criteria {
		for _, cp := range c.Caps {
			var err error
			if out, err = appendJSON(out, map[string]any{"criterion": c.ID, "cap": cp}); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

type judgeAnswerBody struct {
	JudgeAnswer   json.RawMessage `json:"judge_answer"`
	JudgeAttempts int             `json:"judge_attempts"`
	ClientScores  json.RawMessage `json:"client_scores"`
}

// JudgeAnswer — ответ судьи, полученный повторно, с пересчитанным
// клиентом блоком: «процесс» становится ok (arena-portal-hr.md 10.2).
func (s *service) JudgeAnswer(ctx context.Context, sessionID uuid.UUID, raw []byte) (ResultView, error) {
	t, err := participant(ctx)
	if err != nil {
		return ResultView{}, err
	}
	var body judgeAnswerBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return ResultView{}, httpx.NewError(httpx.KindInvalidBody, "Тело ответа судьи не разобралось.")
	}
	err = pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.ownSession(ctx, tx, t, sessionID)
		if err != nil {
			return err
		}
		if err := judgeGate(row); err != nil {
			return err
		}
		if err := checkAnswerSet(body.JudgeAnswer, row.CriteriaSet); err != nil {
			return err
		}
		jc, err := s.judgeContext(ctx, row)
		if err != nil {
			return err
		}
		sc, err := parseScores(body.ClientScores, scoreRules{
			CriteriaSet: row.CriteriaSet, Finals: finalIDs(jc), FinalID: row.FinalID,
		}, "/client_scores")
		if err != nil {
			return err
		}
		if !sc.HasProcess {
			return httpx.NewError(httpx.KindValidationFailed, "В блоке нет оценки «процесс», посчитанной по ответу судьи.").
				WithErrors([]gen.FieldError{{Path: "/client_scores/process", Message: "Пришлите оценку «процесс»."}})
		}
		g := grade{ProcessNumber: sc.ProcessNumber, Lucky: sc.Lucky, Scores: sc.Cleaned,
			JudgeAttempts: max(body.JudgeAttempts, row.JudgeAttempts)}
		if g.CriteriaBands, err = bandsJSON(sc.Bands); err != nil {
			return err
		}
		if g.JudgeAnswerEnc, err = s.encryptFor(ctx, row.SubjectID, body.JudgeAnswer); err != nil {
			return err
		}
		if err := s.store.processAnswered(ctx, tx, row.ID, g); err != nil {
			return gradeViolation(err)
		}
		return nil
	})
	if err != nil {
		return ResultView{}, err
	}
	return s.viewOf(ctx, sessionID)
}

func appendJSON(list []json.RawMessage, v any) ([]json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("пакет для судьи: %w", err)
	}
	return append(list, b), nil
}

func firstNonNil(a, b []byte) []byte {
	if a != nil {
		return a
	}
	return b
}
