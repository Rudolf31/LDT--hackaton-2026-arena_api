package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/scenariodoc"
	"arena-portal-backend/internal/platform/crypto"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/pg"
)

// minParticipantTurns — меньше трёх реплик участника «процесс» не
// считается (arena-portal-hr.md 10.2, arena-scoring 3.4).
const minParticipantTurns = 3

type finishBody struct {
	Status        string          `json:"status"`
	LastSeq       int             `json:"last_seq"`
	FinalID       *string         `json:"final_id"`
	BreakStage    *string         `json:"break_stage"`
	BreakTurn     *int            `json:"break_turn"`
	JudgeAnswer   json.RawMessage `json:"judge_answer"`
	JudgeAttempts int             `json:"judge_attempts"`
	ClientScores  json.RawMessage `json:"client_scores"`
}

// Finish — завершение сессии (архитектура 8): итоговый статус, причина,
// оценки с клиента по столбцам и ответ судьи в шифр. Повтор возвращает
// сохранённое, тело повтора не сравнивается. Прерванную сервером сессию
// завершить можно — её дописали поздние события (arena-scoring 3.4).
func (s *service) Finish(ctx context.Context, sessionID uuid.UUID, raw []byte) (ResultView, error) {
	t, err := participant(ctx)
	if err != nil {
		return ResultView{}, err
	}
	var body finishBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return ResultView{}, httpx.NewError(httpx.KindInvalidBody, "Тело завершения не разобралось.")
	}
	body.JudgeAnswer, body.ClientScores = nullToNil(body.JudgeAnswer), nullToNil(body.ClientScores)

	err = pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.ownSession(ctx, tx, t, sessionID)
		if err != nil {
			return err
		}
		if isClosed(row.Status) {
			return nil
		}
		if err := s.requireTurns(ctx, tx, row.ID, body.LastSeq); err != nil {
			return err
		}
		g, err := s.gradeFor(ctx, tx, row, body)
		if err != nil {
			return err
		}
		if err := s.store.finish(ctx, tx, row.ID, g); err != nil {
			return gradeViolation(err)
		}
		return nil
	})
	if err != nil {
		return ResultView{}, err
	}
	return s.viewOf(ctx, sessionID)
}

// requireTurns — на портале есть все ходы до last_seq, иначе 409 со
// списком недостающих: клиент досылает события и повторяет (FR-AC-11).
func (s *service) requireTurns(ctx context.Context, tx pgx.Tx, id uuid.UUID, lastSeq int) error {
	seqs, err := s.store.seqs(ctx, tx, id)
	if err != nil {
		return err
	}
	have := make(map[int]bool, len(seqs))
	for _, seq := range seqs {
		have[seq] = true
	}
	missing := []int{}
	for seq := 0; seq <= lastSeq; seq++ {
		if !have[seq] {
			missing = append(missing, seq)
		}
	}
	if len(missing) > 0 {
		return httpx.NewError(httpx.KindMissingTurns, "Не все реплики дошли до портала — дошлите их и завершите сессию ещё раз.").
			WithData(map[string]any{"missing_seqs": missing})
	}
	return nil
}

// gradeFor раскладывает тело завершения по столбцам (архитектура 8.3)
// и ставит статус «процесса» по arena-portal-hr.md 10.2.
func (s *service) gradeFor(ctx context.Context, tx pgx.Tx, row sessionRow, body finishBody) (grade, error) {
	jc, err := s.judgeContext(ctx, row)
	if err != nil {
		return grade{}, err
	}
	sc, err := parseScores(body.ClientScores, scoreRules{
		CriteriaSet: row.CriteriaSet, Finals: finalIDs(jc), FinalID: body.FinalID,
	}, "/client_scores")
	if err != nil {
		return grade{}, err
	}
	if err := checkAnswerSet(body.JudgeAnswer, row.CriteriaSet); err != nil {
		return grade{}, err
	}

	g := grade{
		Status: body.Status, BreakStage: body.BreakStage, BreakTurn: body.BreakTurn,
		FinalID: sc.FinalID, ResultRank: sc.Rank, ResultNumber: sc.Number, ResultMax: sc.MaxNumber,
		ScoringProfile: sc.ScoringProfile, Lucky: sc.Lucky, Scores: sc.Cleaned, JudgeAttempts: body.JudgeAttempts,
	}
	if sc.FinalID != nil {
		title := jc.Finals[*sc.FinalID].Title
		g.FinalTitle = &title
	}
	switch {
	case row.ParticipantTurns < minParticipantTurns:
		g.ProcessStatus = string(gen.ProcessStatusTooShort)
	case body.JudgeAnswer != nil:
		if !sc.HasProcess {
			return grade{}, httpx.NewError(httpx.KindValidationFailed, "Ответ судьи получен, а оценки «процесс» в блоке нет.").
				WithErrors([]gen.FieldError{{Path: "/client_scores/process", Message: "Пришлите оценку «процесс», посчитанную по ответу судьи."}})
		}
		g.ProcessStatus = string(gen.ProcessStatusOk)
		g.ProcessNumber = sc.ProcessNumber
		if g.CriteriaBands, err = bandsJSON(sc.Bands); err != nil {
			return grade{}, err
		}
	case body.JudgeAttempts == 0:
		g.ProcessStatus = string(gen.ProcessStatusPending)
	default:
		g.ProcessStatus = string(gen.ProcessStatusNotReceived)
	}
	if g.ProcessStatus != string(gen.ProcessStatusOk) {
		g.Lucky = false // «повезло» — это «процесс» ниже порога, без числа его нет
	}

	if body.Status != string(gen.ClosingStatusCompleted) {
		if g.BreakStage == nil {
			if g.BreakStage, err = s.store.lastStage(ctx, tx, row.ID); err != nil {
				return grade{}, err
			}
		}
		if g.BreakTurn == nil {
			turns := row.ParticipantTurns
			g.BreakTurn = &turns
		}
	}
	if g.JudgeAnswerEnc, err = s.encryptFor(ctx, row.SubjectID, body.JudgeAnswer); err != nil {
		return grade{}, err
	}
	return g, nil
}

// judgeContext — подписи финалов, бриф, интересы и факты документа версии
// с уровнем сессии.
func (s *service) judgeContext(ctx context.Context, row sessionRow) (scenariodoc.JudgeContext, error) {
	version, err := s.versions.Version(ctx, row.VersionID)
	if err != nil {
		return scenariodoc.JudgeContext{}, err
	}
	doc, err := scenariodoc.ApplyDifficulty(version.Document, scenariodoc.Level(row.Difficulty))
	if err != nil {
		return scenariodoc.JudgeContext{}, fmt.Errorf("применение уровня к версии сценария: %w", err)
	}
	return scenariodoc.JudgeContextOf(doc)
}

func finalIDs(jc scenariodoc.JudgeContext) map[string]bool {
	out := make(map[string]bool, len(jc.Finals))
	for id := range jc.Finals {
		out[id] = true
	}
	return out
}

// checkAnswerSet — судья работал по тому же набору критериев, что и сессия.
func checkAnswerSet(answer json.RawMessage, criteriaSet string) error {
	if answer == nil {
		return nil
	}
	var a struct {
		CriteriaSet string `json:"criteria_set"`
	}
	if err := json.Unmarshal(answer, &a); err != nil {
		return httpx.NewError(httpx.KindInvalidBody, "Ответ судьи не разобрался.")
	}
	if a.CriteriaSet != criteriaSet {
		return httpx.NewError(httpx.KindValidationFailed, "Судья работал по другому набору критериев.").
			WithErrors([]gen.FieldError{{Path: "/judge_answer/criteria_set",
				Message: fmt.Sprintf("Ожидается набор %q — как в сценарии этой сессии.", criteriaSet)}})
	}
	return nil
}

// encryptFor — шифр ключом участника; ключ уничтожен отзывом согласия —
// nil, текст не сохраняется (архитектура 9.3).
func (s *service) encryptFor(ctx context.Context, subjectID uuid.UUID, plain []byte) ([]byte, error) {
	if plain == nil {
		return nil, nil
	}
	key, err := s.people.DataKey(ctx, subjectID)
	if errors.Is(err, people.ErrKeyDestroyed) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	enc, err := crypto.Encrypt(key, plain)
	if err != nil {
		return nil, fmt.Errorf("шифрование ответа судьи: %w", err)
	}
	return enc, nil
}

// gradeViolation — ограничения оценок в базе как ошибка клиента, а не 500.
// До них дело доходить не должно: parseScores проверяет то же раньше.
func gradeViolation(err error) error {
	if v, ok := pg.AsViolation(err); ok && v.Kind == pg.Check {
		return httpx.NewError(httpx.KindValidationFailed,
			"Оценки противоречат друг другу — проверьте букву, финал и оценку «процесс».").WithDetail(v.Constraint)
	}
	return fmt.Errorf("запись итога сессии: %w", err)
}
