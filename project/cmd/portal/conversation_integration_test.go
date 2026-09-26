//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/sessions"
)

// --- помощники этапа 08 ---

// talk — идущая сессия участника: токен, номер сессии, документ версии.
type talk struct {
	e          *testEnv
	token      string
	session    string
	assignment string
	code       string
	subject    string
	document   map[string]any
}

// startTalk — назначение, вход, согласие, старт (этап 07).
func (e *testEnv) startTalk(groupName, person string) talk {
	e.t.Helper()
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)
	e.setKeys(admin, e.defaultProfile(admin).ID)
	_, people := e.hrGroup(admin, groupName, person)
	version := e.publishedVersion(meth, "training")
	issued := e.assignOne(admin, version, people[0].SubjectID, "")
	return e.startOn(issued, people[0].SubjectID)
}

func (e *testEnv) startOn(issued issuedBody, subject string) talk {
	e.t.Helper()
	entered := e.mustEnter(issued.Code)
	rec := e.start(entered.Token, e.consent(entered, true))
	expectStatus(e.t, rec, http.StatusCreated)
	started := decode[startBody](e.t, rec)
	return talk{e: e, token: entered.Token, session: started.Session.ID, assignment: issued.AssignmentID, code: issued.Code,
		subject: subject, document: started.Document}
}

// replyText — текст реплики по seq; по нему I-5 ищет утечки.
func replyText(seq int) string {
	return fmt.Sprintf("Секретная реплика номер %d про цену", seq)
}

// turn — реплика: seq 0 — первая реплика оппонента (О0), дальше участник
// и оппонент по очереди: seq 2n−1 — Уn, seq 2n — Оn.
func turn(seq int) map[string]any {
	ev := map[string]any{"type": "turn", "seq": seq, "text": replyText(seq), "at_ms": seq * 1000, "stage": "opening",
		"violations": []any{}, "revealed_facts": []any{}}
	if seq%2 == 1 {
		ev["speaker"], ev["reply_no"] = "participant", (seq+1)/2
		ev["move_type"], ev["move_source"] = "probe_problem", "judge"
		ev["judge"] = map[string]any{"move": "probe_problem", "confidence": 0.9}
		ev["terms"] = map[string]any{"price": 100 + seq}
	} else {
		ev["speaker"], ev["reply_no"] = "opponent", seq/2
		ev["intent"] = "answer"
		ev["offer"] = map[string]any{"price": 200 - seq}
		ev["opponent_judge"] = map[string]any{"ok": true}
		ev["opponent_judge_comment"] = "Пояснение судьи оппонента " + replyText(seq)
	}
	return ev
}

func turns(from, to int) []any {
	var out []any
	for seq := from; seq <= to; seq++ {
		out = append(out, turn(seq))
	}
	return out
}

func (tk talk) post(events []any) *httptest.ResponseRecorder {
	tk.e.t.Helper()
	body, _ := json.Marshal(map[string]any{"events": events})
	return tk.e.doBearer(http.MethodPost, "/api/trainer/sessions/"+tk.session+"/events", string(body), tk.token)
}

type batchResult struct {
	Accepted      int     `json:"accepted"`
	Duplicates    int     `json:"duplicates"`
	ContiguousSeq int     `json:"contiguous_seq"`
	MissingSeqs   []int   `json:"missing_seqs"`
	SessionStatus string  `json:"session_status"`
	Reopened      bool    `json:"reopened"`
	Note          *string `json:"note"`
}

func (tk talk) mustPost(events []any) batchResult {
	tk.e.t.Helper()
	rec := tk.post(events)
	expectStatus(tk.e.t, rec, http.StatusOK)
	return decode[batchResult](tk.e.t, rec)
}

// firstFinal — финал документа версии и его буква.
func (tk talk) firstFinal() (string, string) {
	f := tk.document["finals"].([]any)[0].(map[string]any)
	return f["id"].(string), f["rank"].(string)
}

// scores — блок оценок в форме arena-scoring 9 (D-61).
func (tk talk) scores(withProcess bool) map[string]any {
	final, rank := tk.firstFinal()
	block := map[string]any{
		"criteria_set": "harvard_spin_v1", "scoring_profile": "normal", "lucky": false,
		"result": map[string]any{"final_id": final, "rank": rank, "number": 61, "max_number": 95,
			"issues": []any{map[string]any{"issue": "price", "value": 18600, "score": 53.8}}},
		"marks": []any{},
	}
	if withProcess {
		block["process"] = map[string]any{"number": 83, "total_cap": nil,
			"criteria": []any{map[string]any{"id": "diagnosis", "band": 4, "caps": []any{}},
				map[string]any{"id": "conduct", "band": 3, "caps": []any{}}},
			"episodes": []any{
				map[string]any{"indicator": "diag_implication_question", "reply": 2, "source": "judge",
					"counted": true, "quote": "Секретная цитата судьи"},
				map[string]any{"indicator": "disc_counter_step", "reply": 3, "source": "code", "counted": true}},
			"dropped": []any{}}
	}
	return block
}

func judgeAnswer() map[string]any {
	return map[string]any{
		"criteria_set": "harvard_spin_v1",
		"episodes": []any{map[string]any{"indicator": "diag_implication_question", "reply": 2, "speaker": "participant",
			"quote": "Секретная цитата судьи", "context": nil, "interest": nil, "why": "Вопрос о последствиях."}},
		"summary": []any{map[string]any{"kind": "miss", "observation": "Перед согласием вы назвали только цену.",
			"reply": 3, "speaker": "participant", "quote": "Секретная цитата судьи", "instead": "Проговорить все условия."}},
	}
}

func (tk talk) finish(body map[string]any) *httptest.ResponseRecorder {
	tk.e.t.Helper()
	raw, _ := json.Marshal(body)
	return tk.e.doBearer(http.MethodPost, "/api/trainer/sessions/"+tk.session+"/finish", string(raw), tk.token)
}

type resultView struct {
	Header struct {
		Status           string `json:"status"`
		ParticipantTurns int    `json:"participant_turns"`
		ScenarioTitle    string `json:"scenario_title"`
	} `json:"header"`
	Marks []struct {
		Kind string `json:"kind"`
	} `json:"marks"`
	ScoreColumns map[string]any `json:"score_columns"`
	Epilogue     *string        `json:"epilogue"`
	Summary      []any          `json:"summary"`
	Transcript   []struct {
		Seq  int     `json:"seq"`
		Text *string `json:"text"`
	} `json:"transcript"`
	CanRetryJudge bool `json:"can_retry_judge"`
}

func problemType(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[map[string]any](t, rec)["type"].(string)
}

func (e *testEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

// closer — задание закрытия брошенных сессий с управляемым моментом
// запуска портала (I-15); той же службой, что собирает buildApp.
func (e *testEnv) closer() sessions.Closer {
	return sessions.New(e.pool, sessions.Deps{Audit: audit.New()}).Closer()
}

// --- сценарии ---

// TestConversationFlow — сквозной путь этапа 08: вход → согласие → старт →
// 20 событий → завершение; строки в sessions и turns, повторная пачка не
// дублирует ходы, повтор завершения возвращает сохранённое.
func TestConversationFlow(t *testing.T) {
	e := newTestEnv(t)
	tk := e.startTalk("Группа разговора", "Павел Разговорчивый")

	for from := 0; from < 20; from += 5 {
		events := turns(from, from+4)
		if from == 10 {
			events = append(events, map[string]any{"type": "incident", "event_id": "11111111-1111-4111-8111-111111111111",
				"component": "tts", "reply_no": 5, "at_ms": 10500, "route_to": nil, "detail": "Синтез речи не ответил"})
		}
		res := tk.mustPost(events)
		if res.Duplicates != 0 || res.ContiguousSeq != from+4 || res.SessionStatus != "in_progress" {
			t.Fatalf("пачка с %d: %+v", from, res)
		}
	}
	again := tk.mustPost(append(turns(10, 14), map[string]any{"type": "incident",
		"event_id": "11111111-1111-4111-8111-111111111111", "component": "tts", "at_ms": 10500}))
	if again.Accepted != 0 || again.Duplicates != 6 || len(again.MissingSeqs) != 0 {
		t.Fatalf("повторная пачка: %+v", again)
	}
	if n := e.count(`SELECT count(*) FROM turns WHERE session_id = $1`, tk.session); n != 20 {
		t.Fatalf("строк turns: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM turns WHERE session_id = $1 AND text_enc IS NOT NULL
		AND (speaker = 'participant' OR opponent_judge_comment_enc IS NOT NULL)`, tk.session); n != 20 {
		t.Fatal("тексты и пояснения судьи оппонента пишутся шифром ключом участника")
	}
	if n := e.count(`SELECT participant_turns FROM sessions WHERE id = $1`, tk.session); n != 10 {
		t.Fatalf("participant_turns = %d", n)
	}
	if n := e.count(`SELECT jsonb_array_length(incidents) FROM sessions WHERE id = $1`, tk.session); n != 1 {
		t.Fatalf("отказов компонентов: %d", n)
	}

	body := map[string]any{"status": "completed", "last_seq": 19, "final_id": nil, "judge_attempts": 1,
		"judge_answer": judgeAnswer(), "client_scores": tk.scores(true)}
	body["final_id"], _ = tk.firstFinal()
	rec := tk.finish(body)
	expectStatus(t, rec, http.StatusOK)
	res := decode[resultView](t, rec)
	if res.Header.Status != "completed" || res.ScoreColumns["process_status"] != "ok" ||
		res.ScoreColumns["process_number"] != float64(83) || res.ScoreColumns["final_title"] == nil || res.Epilogue == nil {
		t.Fatalf("итог: %+v", res)
	}
	if len(res.Transcript) != 20 || res.Transcript[7].Text == nil || *res.Transcript[7].Text != replyText(7) {
		t.Fatal("транскрипт участника — расшифрованные реплики по порядку seq")
	}
	if len(res.Summary) != 1 {
		t.Fatalf("резюме из ответа судьи: %v", res.Summary)
	}
	for _, m := range res.Marks {
		if m.Kind == "process_not_received" || m.Kind == "abandoned" {
			t.Fatalf("лишняя пометка %s", m.Kind)
		}
	}
	if n := e.count(`SELECT count(*) FROM sessions WHERE id = $1 AND status = 'completed' AND result_rank IS NOT NULL
		AND result_number = 61 AND result_max = 95 AND criteria_bands = '{"diagnosis":4,"conduct":3}'::jsonb
		AND scoring_profile = 'normal' AND judge_answer_enc IS NOT NULL AND judge_attempts = 1
		AND scores IS NOT NULL AND scores::text NOT LIKE '%quote%'`, tk.session); n != 1 {
		t.Fatal("столбцы оценок разложены не по архитектуре 8.3 или в scores остались цитаты")
	}

	// Повтор завершения — сохранённое, тело повтора не сравнивается.
	body["status"] = "time_limit"
	rec = tk.finish(body)
	expectStatus(t, rec, http.StatusOK)
	if decode[resultView](t, rec).Header.Status != "completed" {
		t.Fatal("повтор завершения должен вернуть сохранённый итог")
	}
	rec = tk.post(turns(20, 20))
	expectStatus(t, rec, http.StatusConflict)
	if problemType(t, rec) != "session_finished" {
		t.Fatal("к завершённой сессии реплики не дописываются")
	}

	// I-5: ни текстов реплик, ни пояснений, ни цитат — ни в журнале, ни в логе.
	for _, secret := range []string{replyText(3), "Пояснение судьи оппонента", "Секретная цитата судьи", "Павел Разговорчивый"} {
		if n := e.count(`SELECT count(*) FROM audit_log WHERE details::text LIKE '%' || $1 || '%'`, secret); n != 0 {
			t.Fatalf("журнал содержит %q", secret)
		}
		if strings.Contains(e.log.String(), secret) {
			t.Fatalf("лог содержит %q", secret)
		}
	}
}

// TestPortalOutageDeliversBufferedTurns — FR-AC-11: портал недоступен
// с 7-го хода, клиент копит буфер; завершение раньше досылки — 409 со
// списком недостающих, после досылки все ходы в базе и итоговый статус
// верный. Отменённое назначение идущей сессии не мешает.
func TestPortalOutageDeliversBufferedTurns(t *testing.T) {
	e := newTestEnv(t)
	tk := e.startTalk("Группа обрыва", "Лидия Буферная")
	tk.mustPost(turns(0, 6))

	admin := e.login(adminLogin, adminPassword)
	expectStatus(t, e.do(http.MethodPost, "/api/portal/assignments/"+tk.assignment+"/cancel", "", admin), http.StatusOK)

	final, _ := tk.firstFinal()
	body := map[string]any{"status": "ended_by_participant", "last_seq": 19, "final_id": final, "judge_attempts": 1,
		"judge_answer": judgeAnswer(), "client_scores": tk.scores(true)}
	rec := tk.finish(body)
	expectStatus(t, rec, http.StatusConflict)
	p := decode[map[string]any](t, rec)
	if p["type"] != "missing_turns" || p["title"] == "" {
		t.Fatalf("ответ до досылки: %v", p)
	}
	res := tk.mustPost(turns(7, 19))
	if res.Accepted != 13 || res.ContiguousSeq != 19 {
		t.Fatalf("досылка буфера: %+v", res)
	}
	rec = tk.finish(body)
	expectStatus(t, rec, http.StatusOK)
	if n := e.count(`SELECT count(*) FROM turns WHERE session_id = $1`, tk.session); n != 20 {
		t.Fatalf("после досылки ходов: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM sessions WHERE id = $1 AND status = 'ended_by_participant'
		AND break_turn = 10 AND break_stage = 'opening'`, tk.session); n != 1 {
		t.Fatal("итоговый статус и точка обрыва после поздней досылки")
	}
}

// TestAbandonedSessionClosedAndReopened — I-15 и поздние события
// (архитектура 7.2, 7.3).
func TestAbandonedSessionClosedAndReopened(t *testing.T) {
	e := newTestEnv(t)
	tk := e.startTalk("Группа брошенных", "Кирилл Пропавший")
	tk.mustPost(turns(0, 3))
	e.exec(`UPDATE sessions SET last_event_at = now() - interval '20 minutes' WHERE id = $1`, tk.session)
	closer := e.closer()

	// Портал запущен минуту назад: 20 минут без событий — это время, пока
	// портала не было; таймаут 10 минут ещё не истёк (FR-ST-03).
	if n, err := closer.CloseAbandoned(t.Context(), time.Now().Add(-time.Minute), 10); err != nil || n != 0 {
		t.Fatalf("время недоступности портала вошло в таймаут: %d, %v", n, err)
	}
	if n, err := closer.CloseAbandoned(t.Context(), time.Now().Add(-30*time.Minute), 10); err != nil || n != 1 {
		t.Fatalf("брошенная сессия не закрыта: %d, %v", n, err)
	}
	if n := e.count(`SELECT count(*) FROM sessions WHERE id = $1 AND status = 'abandoned' AND ended_at IS NOT NULL
		AND process_status = 'not_scored' AND break_turn = 2 AND break_stage = 'opening' AND result_rank IS NULL`, tk.session); n != 1 {
		t.Fatal("закрытая сервером сессия: статус, точка обрыва, без оценок")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'session_closed_by_server' AND session_id = $1
		AND actor_kind = 'system'`, tk.session); n != 1 {
		t.Fatal("в журнале нет session_closed_by_server")
	}

	// Пустая пачка — «клиент жив», но заново не открывает.
	if res := tk.mustPost([]any{}); res.Reopened || res.SessionStatus != "abandoned" {
		t.Fatalf("пустая пачка: %+v", res)
	}
	res := tk.mustPost(turns(4, 5))
	if !res.Reopened || res.SessionStatus != "in_progress" {
		t.Fatalf("поздние события должны открыть сессию: %+v", res)
	}
	if n := e.count(`SELECT count(*) FROM sessions WHERE id = $1 AND status = 'in_progress' AND ended_at IS NULL
		AND process_status = 'pending' AND reopened_count = 1`, tk.session); n != 1 {
		t.Fatal("открытая заново сессия")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'session_reopened' AND session_id = $1`, tk.session); n != 1 {
		t.Fatal("в журнале нет session_reopened")
	}

	// Снова брошена; тем временем по назначению началась другая попытка —
	// поздние ходы пишутся, статус остаётся «прервана».
	e.exec(`UPDATE sessions SET last_event_at = now() - interval '20 minutes' WHERE id = $1`, tk.session)
	if n, err := closer.CloseAbandoned(t.Context(), time.Now().Add(-30*time.Minute), 10); err != nil || n != 1 {
		t.Fatalf("повторное закрытие: %d, %v", n, err)
	}
	other := e.startOn(issuedBody{AssignmentID: tk.assignment, Code: tk.code}, tk.subject)
	if other.session == tk.session {
		t.Fatal("новая попытка должна быть новой сессией")
	}
	res = tk.mustPost(turns(6, 7))
	if res.Reopened || res.SessionStatus != "abandoned" || res.Note == nil || res.Accepted != 2 {
		t.Fatalf("при идущей другой сессии: %+v", res)
	}
	if n := e.count(`SELECT count(*) FROM turns WHERE session_id = $1`, tk.session); n != 8 {
		t.Fatalf("поздние ходы записаны: %d", n)
	}
}

// TestJudgeRetryFlow — «процесс» не получен → пакет для судьи → ответ
// с пересчитанным блоком → ok; повтор — 409 (arena-portal-hr.md 10.2).
func TestJudgeRetryFlow(t *testing.T) {
	e := newTestEnv(t)
	tk := e.startTalk("Группа судьи", "Инна Терпеливая")
	tk.mustPost(turns(0, 9))
	final, _ := tk.firstFinal()

	rec := e.doBearer(http.MethodGet, "/api/trainer/sessions/"+tk.session+"/judge-retry", "", tk.token)
	expectStatus(t, rec, http.StatusConflict)

	rec = tk.finish(map[string]any{"status": "turn_limit", "last_seq": 9, "final_id": final, "judge_attempts": 2,
		"judge_answer": nil, "client_scores": tk.scores(false)})
	expectStatus(t, rec, http.StatusOK)
	res := decode[resultView](t, rec)
	if res.ScoreColumns["process_status"] != "not_received" || res.ScoreColumns["process_number"] != nil || !res.CanRetryJudge {
		t.Fatalf("без ответа судьи: %+v", res.ScoreColumns)
	}
	if !slices.ContainsFunc(res.Marks, func(m struct {
		Kind string `json:"kind"`
	}) bool {
		return m.Kind == "process_not_received"
	}) {
		t.Fatal("нет пометки «Разбор процесса не получен»")
	}

	rec = e.doBearer(http.MethodGet, "/api/trainer/sessions/"+tk.session+"/judge-retry", "", tk.token)
	expectStatus(t, rec, http.StatusOK)
	pack := decode[map[string]any](t, rec)
	if len(pack["transcript"].([]any)) != 10 || len(pack["judge_decisions"].([]any)) != 5 ||
		len(pack["offer_log"].([]any)) != 10 || pack["participant_brief"] == nil || pack["model_access"] == nil {
		t.Fatalf("пакет для судьи: %v", pack)
	}
	raw := rec.Body.String()
	for _, hidden := range []string{`"stages"`, `"limit"`, `"transitions"`, `"finals"`} {
		if strings.Contains(raw, hidden) {
			t.Fatalf("в пакете для судьи есть %s — пределы оппонента и этапы ему не нужны", hidden)
		}
	}

	answer := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"judge_answer": judgeAnswer(), "judge_attempts": 3, "client_scores": tk.scores(true)})
		return e.doBearer(http.MethodPost, "/api/trainer/sessions/"+tk.session+"/judge-answer", string(body), tk.token)
	}
	rec = answer()
	expectStatus(t, rec, http.StatusOK)
	res = decode[resultView](t, rec)
	if res.ScoreColumns["process_status"] != "ok" || res.ScoreColumns["process_number"] != float64(83) || res.CanRetryJudge {
		t.Fatalf("после ответа судьи: %+v", res.ScoreColumns)
	}
	if n := e.count(`SELECT judge_attempts FROM sessions WHERE id = $1`, tk.session); n != 3 {
		t.Fatalf("judge_attempts = %d", n)
	}
	rec = answer()
	expectStatus(t, rec, http.StatusConflict)
	if problemType(t, rec) != "already_answered" {
		t.Fatal("сохранённый и проверенный ответ не перезапрашивается")
	}
}

// TestShortSessionAndFinishChecks — меньше трёх реплик участника —
// too_short; блок оценок проверяется по форме (архитектура 8.2).
func TestShortSessionAndFinishChecks(t *testing.T) {
	e := newTestEnv(t)
	tk := e.startTalk("Группа коротких", "Олег Краткий")
	tk.mustPost(turns(0, 3))
	final, _ := tk.firstFinal()

	bad := tk.scores(true)
	bad["total"] = 72
	rec := tk.finish(map[string]any{"status": "completed", "last_seq": 3, "final_id": final, "judge_attempts": 1,
		"judge_answer": judgeAnswer(), "client_scores": bad})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	rec = tk.finish(map[string]any{"status": "completed", "last_seq": 3, "final_id": final, "judge_attempts": 0,
		"judge_answer": nil, "client_scores": nil})
	expectStatus(t, rec, http.StatusUnprocessableEntity)

	rec = tk.finish(map[string]any{"status": "completed", "last_seq": 3, "final_id": final, "judge_attempts": 1,
		"judge_answer": judgeAnswer(), "client_scores": tk.scores(true)})
	expectStatus(t, rec, http.StatusOK)
	res := decode[resultView](t, rec)
	if res.ScoreColumns["process_status"] != "too_short" || res.ScoreColumns["process_number"] != nil ||
		res.ScoreColumns["result_rank"] == nil {
		t.Fatalf("короткая сессия: %+v", res.ScoreColumns)
	}
	rec = e.doBearer(http.MethodGet, "/api/trainer/sessions/"+tk.session+"/judge-retry", "", tk.token)
	expectStatus(t, rec, http.StatusConflict)
}

// TestEventsRejectEmotionAndComment — I-1 на событиях хода и пояснение
// судьи оппонента не на своём месте; ничего не пишется.
func TestEventsRejectEmotionAndComment(t *testing.T) {
	e := newTestEnv(t)
	tk := e.startTalk("Группа эмоций", "Вера Спокойная")

	ev := turn(1)
	ev["engine_step"] = map[string]any{"steps": []any{map[string]any{"emotion": "злость"}}}
	rec := tk.post([]any{turn(0), ev})
	expectStatus(t, rec, http.StatusBadRequest)
	if strings.Contains(e.log.String(), "злость") {
		t.Fatal("тело с emotion попало в лог (I-1)")
	}

	opp := turn(0)
	opp["opponent_judge"] = map[string]any{"ok": true, "comment": "цитата"}
	expectStatus(t, tk.post([]any{opp}), http.StatusBadRequest)

	wrongSide := turn(0)
	wrongSide["move_type"], wrongSide["move_source"] = "probe_problem", "judge"
	expectStatus(t, tk.post([]any{wrongSide}), http.StatusUnprocessableEntity)

	if n := e.count(`SELECT count(*) FROM turns WHERE session_id = $1`, tk.session); n != 0 {
		t.Fatalf("отклонённые пачки оставили %d строк", n)
	}
}
