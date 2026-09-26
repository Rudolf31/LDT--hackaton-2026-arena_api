//go:build integration

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- помощники этапа 07 ---

type issuedBody struct {
	AssignmentID string `json:"assignment_id"`
	Code         string `json:"code"`
	Link         string `json:"link"`
	Person       struct {
		SubjectID string `json:"subject_id"`
		Number    string `json:"number"`
	} `json:"person"`
}

type createResultBody struct {
	BatchID *string      `json:"batch_id"`
	Created []issuedBody `json:"created"`
	Skipped []struct {
		SubjectID string `json:"subject_id"`
		Reason    string `json:"reason"`
	} `json:"skipped"`
}

type assignmentBody struct {
	ID                     string  `json:"id"`
	Status                 string  `json:"status"`
	Mode                   string  `json:"mode"`
	TrainerProfileID       string  `json:"trainer_profile_id"`
	CancelReason           *string `json:"cancel_reason"`
	RunningSessionAtCancel *bool   `json:"running_session_at_cancel"`
	Attempts               int     `json:"attempts"`
	Code                   *struct {
		FailedAttempts int     `json:"failed_attempts"`
		BlockedAt      *string `json:"blocked_at"`
	} `json:"code"`
}

type enterBody struct {
	Token            string  `json:"token"`
	Next             string  `json:"next"`
	RunningSessionID *string `json:"running_session_id"`
	HasResults       bool    `json:"has_results"`
	CanStartNew      bool    `json:"can_start_new"`
	Participant      struct {
		Number      string `json:"number"`
		DisplayName string `json:"display_name"`
	} `json:"participant"`
	Settings struct {
		Scenario      map[string]any `json:"scenario"`
		Document      map[string]any `json:"document"`
		Profile       map[string]any `json:"profile"`
		ConsentScreen struct {
			CanProceed    bool    `json:"can_proceed"`
			BlockedReason *string `json:"blocked_reason"`
			Texts         []struct {
				Kind   string `json:"kind"`
				TextID string `json:"text_id"`
				Body   string `json:"body"`
			} `json:"texts"`
		} `json:"consent_screen"`
	} `json:"settings"`
}

type startBody struct {
	Session struct {
		ID                string `json:"id"`
		Status            string `json:"status"`
		ExternalAIAllowed bool   `json:"external_ai_allowed"`
	} `json:"session"`
	PrivatePart map[string]any `json:"private_part"`
	Document    map[string]any `json:"document"`
	ModelAccess struct {
		OpenRouterKey    *string `json:"openrouter_key"`
		ModelServerToken *string `json:"model_server_token"`
	} `json:"model_access"`
	Profile struct {
		Settings map[string]any `json:"settings"`
	} `json:"profile"`
}

// hrGroup — новая группа с одним сотрудником по ФИО; доступ к ней выдан
// администратору, который в этих тестах и работает за HR.
func (e *testEnv) hrGroup(admin *http.Cookie, name string, people ...string) (string, []idBody) {
	e.t.Helper()
	group := e.createGroup(admin, name)
	adminID := e.userID(admin, adminLogin)
	expectStatus(e.t, e.do(http.MethodPut, "/api/portal/users/"+adminID+"/group-access", `{"group_ids":["`+group+`"]}`, admin), http.StatusOK)
	var out []idBody
	for _, p := range people {
		out = append(out, e.createPerson(admin, group, p))
	}
	return group, out
}

// publishedVersion — опубликованная версия шаблона «Продажа услуги».
func (e *testEnv) publishedVersion(meth *http.Cookie, mode string) string {
	e.t.Helper()
	card := e.createScenario(meth, `{"origin":"template","mode":"`+mode+`","template_id":"sales"}`)
	id := card["id"].(string)
	d, _ := e.draft(meth, id)
	rec := e.publish(meth, id, d["fingerprint"].(string))
	expectStatus(e.t, rec, http.StatusCreated)
	return decode[map[string]any](e.t, rec)["id"].(string)
}

func (e *testEnv) assign(hr *http.Cookie, versionID string, subjects []string, extra string) *httptest.ResponseRecorder {
	e.t.Helper()
	due := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"subject_ids":["%s"],"scenario_version_id":%q,"difficulty":"normal","due_at":%q%s}`,
		strings.Join(subjects, `","`), versionID, due, extra)
	return e.do(http.MethodPost, "/api/portal/assignments", body, hr)
}

func (e *testEnv) assignOne(hr *http.Cookie, versionID, subject, extra string) issuedBody {
	e.t.Helper()
	rec := e.assign(hr, versionID, []string{subject}, extra)
	expectStatus(e.t, rec, http.StatusCreated)
	res := decode[createResultBody](e.t, rec)
	if len(res.Created) != 1 {
		e.t.Fatalf("ожидалось одно назначение: %+v", res)
	}
	return res.Created[0]
}

func (e *testEnv) doBearer(method, path, body, token string) *httptest.ResponseRecorder {
	e.t.Helper()
	var r *strings.Reader
	if body != "" {
		r = strings.NewReader(body)
	} else {
		r = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) enter(code string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.doBearer(http.MethodPost, "/api/trainer/enter", `{"code":"`+code+`"}`, "")
}

func (e *testEnv) mustEnter(code string) enterBody {
	e.t.Helper()
	rec := e.enter(code)
	expectStatus(e.t, rec, http.StatusOK)
	return decode[enterBody](e.t, rec)
}

// consent — ответы на все тексты экрана: основной — «ознакомлен» или
// «даю», внешняя нейросеть — по externalAI. Возвращает номера записей.
func (e *testEnv) consent(entered enterBody, externalAI bool) []string {
	e.t.Helper()
	var answers []string
	for _, text := range entered.Settings.ConsentScreen.Texts {
		sum := sha256.Sum256([]byte(text.Body))
		answer := "granted"
		switch {
		case text.Kind == "notice_training":
			answer = "acknowledged"
		case text.Kind == "consent_external_ai" && !externalAI:
			answer = "refused"
		}
		answers = append(answers, fmt.Sprintf(`{"kind":%q,"answer":%q,"text_id":%q,"shown_text_sha256":%q}`,
			text.Kind, answer, text.TextID, hex.EncodeToString(sum[:])))
	}
	rec := e.doBearer(http.MethodPost, "/api/trainer/consents", `{"answers":[`+strings.Join(answers, ",")+`]}`, entered.Token)
	expectStatus(e.t, rec, http.StatusCreated)
	var ids []string
	for _, r := range decode[struct {
		Records []struct {
			ID string `json:"id"`
		} `json:"records"`
	}](e.t, rec).Records {
		ids = append(ids, r.ID)
	}
	return ids
}

func (e *testEnv) start(token string, consentIDs []string) *httptest.ResponseRecorder {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{"consent_ids": consentIDs})
	return e.doBearer(http.MethodPost, "/api/trainer/sessions", string(body), token)
}

func (e *testEnv) setKeys(admin *http.Cookie, profileID string) {
	e.t.Helper()
	expectStatus(e.t, e.do(http.MethodPut, "/api/portal/trainer-profiles/"+profileID+"/keys",
		`{"openrouter_key":"`+testOpenRouterKey+`","model_server_token":"`+testServerToken+`"}`, admin), http.StatusOK)
}

// sameSelectorWrongCode — другой код с тем же селектором (первые четыре
// символа) и верным контрольным символом: попытка засчитывается этому
// коду. Контрольный символ считается так же, как в assignments (D-54).
func sameSelectorWrongCode(t *testing.T, shown string) string {
	t.Helper()
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	body := []byte(strings.ReplaceAll(strings.TrimPrefix(shown, "ARENA-"), "-", "")[:10])
	body[9] = alphabet[(strings.IndexByte(alphabet, body[9])+1)%len(alphabet)]
	sum := 0
	for i, c := range body {
		sum += (2*i + 1) * strings.IndexByte(alphabet, c)
	}
	s := string(body) + string(alphabet[sum%32])
	return "ARENA-" + s[:4] + "-" + s[4:8] + "-" + s[8:]
}

// --- сквозные сценарии ---

// TestTrainerEntryFlow — вход → согласие → старт; повторный старт и
// повторный вход возвращают ту же идущую сессию (FR-AC-03, UC-P-06).
func TestTrainerEntryFlow(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)
	e.setKeys(admin, e.defaultProfile(admin).ID)
	_, people := e.hrGroup(admin, "Группа входа", "Олег Входящий")
	version := e.publishedVersion(meth, "training")

	issued := e.assignOne(admin, version, people[0].SubjectID, "")
	if !strings.HasPrefix(issued.Link, "http") || !strings.Contains(issued.Link, issued.Code) {
		t.Fatalf("ссылка на код: %q", issued.Link)
	}

	entered := e.mustEnter(strings.ToLower(strings.ReplaceAll(issued.Code, "-", " ")))
	if entered.Next != "brief" || entered.RunningSessionID != nil || !entered.CanStartNew {
		t.Fatalf("первый вход: %+v", entered)
	}
	if entered.Participant.Number != people[0].Number || entered.Participant.DisplayName != "Олег Входящий" {
		t.Fatalf("участник: %+v", entered.Participant)
	}
	if entered.Settings.Document["stages"] == nil || entered.Settings.Scenario["brief"] == nil {
		t.Fatal("документ уходит целиком, а часть «до старта» — с брифом (D-50)")
	}
	for _, raw := range entered.Settings.Scenario["issues"].([]any) {
		if _, ok := raw.(map[string]any)["opponent"]; ok {
			t.Fatal("в части «до старта» есть сторона оппонента")
		}
	}

	consentIDs := e.consent(entered, true)
	rec := e.start(entered.Token, consentIDs)
	expectStatus(t, rec, http.StatusCreated)
	started := decode[startBody](t, rec)
	if started.Session.Status != "in_progress" || !started.Session.ExternalAIAllowed {
		t.Fatalf("сессия: %+v", started.Session)
	}
	if started.ModelAccess.OpenRouterKey == nil || *started.ModelAccess.OpenRouterKey != testOpenRouterKey {
		t.Fatal("с согласием на внешнюю нейросеть тренажёр получает ключ OpenRouter")
	}
	if started.PrivatePart["stages"] == nil || started.Document["finals"] == nil {
		t.Fatal("после старта — часть «после старта» и документ целиком")
	}

	rec = e.start(entered.Token, consentIDs)
	expectStatus(t, rec, http.StatusOK)
	if again := decode[startBody](t, rec); again.Session.ID != started.Session.ID {
		t.Fatal("повторный старт должен вернуть идущую сессию")
	}

	reentered := e.mustEnter(issued.Code)
	if reentered.Next != "resume" || reentered.RunningSessionID == nil || *reentered.RunningSessionID != started.Session.ID {
		t.Fatalf("F5: повторный вход ведёт в идущую сессию: %+v", reentered)
	}
	rec = e.doBearer(http.MethodGet, "/api/trainer/sessions/"+started.Session.ID+"/private", "", reentered.Token)
	expectStatus(t, rec, http.StatusOK)
	if private := decode[startBody](t, rec); private.Session.ID != started.Session.ID || private.ModelAccess.OpenRouterKey == nil {
		t.Fatal("повторная выдача после F5 — та же сессия и тот же доступ к моделям")
	}

	if n := e.count(`SELECT count(*) FROM sessions WHERE assignment_id = $1`, issued.AssignmentID); n != 1 {
		t.Fatalf("сессий по назначению: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM sessions WHERE profile_snapshot::text LIKE '%' || $1 || '%'`, testOpenRouterKey); n != 0 {
		t.Fatal("снимок профиля в сессии не содержит ключей (FR-PF-03)")
	}
	list := e.do(http.MethodGet, "/api/portal/assignments?id="+issued.AssignmentID, "", admin)
	expectStatus(t, list, http.StatusOK)
	page := decode[struct {
		Items []assignmentBody `json:"items"`
	}](t, list)
	if len(page.Items) != 1 || page.Items[0].Status != "in_progress" || page.Items[0].Attempts != 1 {
		t.Fatalf("список назначений: %+v", page.Items)
	}

	// Код и ключи — ни в журнале, ни в логе (правило 5, I-9).
	normalized := strings.ReplaceAll(strings.TrimPrefix(issued.Code, "ARENA-"), "-", "")
	for _, secret := range []string{issued.Code, normalized, testOpenRouterKey, testServerToken, "Олег Входящий"} {
		if n := e.count(`SELECT count(*) FROM audit_log WHERE details::text LIKE '%' || $1 || '%'`, secret); n != 0 {
			t.Fatalf("журнал содержит %q", secret)
		}
		if strings.Contains(e.log.String(), secret) {
			t.Fatalf("лог содержит %q", secret)
		}
	}
	for _, action := range []string{"assignment_created", "code_issued"} {
		if n := e.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND subject_id = $2`, action, people[0].SubjectID); n != 1 {
			t.Fatalf("в журнале нет %s", action)
		}
	}
}

// TestTrainerWithoutExternalAIConsentGetsNoOpenRouter — I-9 (тренажёр):
// без согласия на внешнюю нейросеть нет ни адреса, ни ключа OpenRouter;
// если своего сервера моделей в профиле нет, старт отвечает 409.
func TestTrainerWithoutExternalAIConsentGetsNoOpenRouter(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)
	e.setKeys(admin, e.defaultProfile(admin).ID)
	_, people := e.hrGroup(admin, "Группа без облака", "Нина Осторожная", "Пётр Осторожный")
	version := e.publishedVersion(meth, "training")

	onlyCloud := e.assignOne(admin, version, people[0].SubjectID, "")
	entered := e.mustEnter(onlyCloud.Code)
	rec := e.start(entered.Token, e.consent(entered, false))
	expectStatus(t, rec, http.StatusConflict)
	if p := decode[map[string]any](t, rec); p["type"] != "no_model_route" || p["title"] == "" {
		t.Fatalf("ожидался no_model_route: %v", p)
	}

	rec = e.do(http.MethodPost, "/api/portal/trainer-profiles",
		`{"name":"Облако и свой сервер","settings":{"model_server":{"base_url":"https://models.example.ru/v1"}}}`, admin)
	expectStatus(t, rec, http.StatusCreated)
	both := decode[profileBody](t, rec)
	e.setKeys(admin, both.ID)

	issued := e.assignOne(admin, version, people[1].SubjectID, `,"trainer_profile_id":"`+both.ID+`"`)
	entered = e.mustEnter(issued.Code)
	rec = e.start(entered.Token, e.consent(entered, false))
	expectStatus(t, rec, http.StatusCreated)
	started := decode[startBody](t, rec)
	if started.ModelAccess.OpenRouterKey != nil || started.Session.ExternalAIAllowed {
		t.Fatal("без согласия ключа OpenRouter нет (NFR-S-01)")
	}
	if _, ok := started.Profile.Settings["openrouter"]; ok {
		t.Fatal("без согласия в настройках нет и адреса OpenRouter (FR-AC-07)")
	}
	if started.ModelAccess.ModelServerToken == nil || *started.ModelAccess.ModelServerToken != testServerToken {
		t.Fatal("токен своего сервера моделей выдаётся и без согласия на внешнюю нейросеть")
	}
	if strings.Contains(rec.Body.String(), testOpenRouterKey) {
		t.Fatal("ключ OpenRouter попал в ответ")
	}
}

// TestReissueInvalidatesOldCodeImmediately — FR-AC-12: старый код не
// работает сразу, а старый токен больше не начинает сессий.
func TestReissueInvalidatesOldCodeImmediately(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)
	e.setKeys(admin, e.defaultProfile(admin).ID)
	_, people := e.hrGroup(admin, "Группа перевыпуска", "Лев Перевыпущенный")
	version := e.publishedVersion(meth, "training")
	old := e.assignOne(admin, version, people[0].SubjectID, "")
	oldEntry := e.mustEnter(old.Code)

	rec := e.do(http.MethodPost, "/api/portal/assignments/reissue-codes", `{"assignment_ids":["`+old.AssignmentID+`"]}`, admin)
	expectStatus(t, rec, http.StatusOK)
	reissued := decode[createResultBody](t, rec)
	if len(reissued.Created) != 1 || reissued.Created[0].Code == old.Code {
		t.Fatalf("перевыпуск: %+v", reissued)
	}

	rec = e.enter(old.Code)
	expectStatus(t, rec, http.StatusGone)
	if p := decode[map[string]any](t, rec); p["type"] != "code_revoked" {
		t.Fatalf("старый код: %v", p)
	}
	rec = e.start(oldEntry.Token, []string{})
	expectStatus(t, rec, http.StatusGone)

	fresh := e.mustEnter(reissued.Created[0].Code)
	expectStatus(t, e.start(fresh.Token, e.consent(fresh, true)), http.StatusCreated)
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'code_reissued' AND subject_id = $1`, people[0].SubjectID); n != 1 {
		t.Fatal("перевыпуск пишется в журнал")
	}
}

// TestCodeBlocksAfterFailedAttemptsAndAdminUnblocks — NFR-S-02: попытки
// считаются по селектору, на пороге код блокируется насовсем; снимает
// блокировку только администратор.
func TestCodeBlocksAfterFailedAttemptsAndAdminUnblocks(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)
	expectStatus(t, e.do(http.MethodPatch, "/api/portal/settings", `{"code_max_failed_attempts":3}`, admin), http.StatusOK)
	_, people := e.hrGroup(admin, "Группа блокировки", "Ян Забывчивый")
	version := e.publishedVersion(meth, "training")
	issued := e.assignOne(admin, version, people[0].SubjectID, "")

	wrong := sameSelectorWrongCode(t, issued.Code)
	for range 3 {
		rec := e.enter(wrong)
		expectStatus(t, rec, http.StatusNotFound)
		if p := decode[map[string]any](t, rec); p["type"] != "code_not_found" {
			t.Fatalf("неверный код: %v", p)
		}
	}
	rec := e.enter(issued.Code)
	expectStatus(t, rec, http.StatusForbidden)
	if p := decode[map[string]any](t, rec); p["type"] != "code_blocked" {
		t.Fatalf("заблокированный код: %v", p)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'code_blocked' AND subject_id = $1`, people[0].SubjectID); n != 1 {
		t.Fatal("блокировка пишется в журнал")
	}
	list := e.do(http.MethodGet, "/api/portal/assignments?status=code_blocked", "", admin)
	expectStatus(t, list, http.StatusOK)
	page := decode[struct {
		Items []assignmentBody `json:"items"`
	}](t, list)
	if len(page.Items) != 1 || page.Items[0].ID != issued.AssignmentID || page.Items[0].Code.FailedAttempts != 3 {
		t.Fatalf("фильтр по статусу «код заблокирован»: %+v", page.Items)
	}

	// Опечатка в контрольном символе — не попытка (D-54).
	typo := issued.Code[:len(issued.Code)-1] + map[bool]string{true: "1", false: "0"}[strings.HasSuffix(issued.Code, "0")]
	expectStatus(t, e.enter(typo), http.StatusNotFound)

	unblock := "/api/portal/assignments/" + issued.AssignmentID + "/unblock-code"
	expectStatus(t, e.do(http.MethodPost, unblock, "", meth), http.StatusForbidden)
	rec = e.do(http.MethodPost, unblock, "", admin)
	expectStatus(t, rec, http.StatusOK)
	if a := decode[assignmentBody](t, rec); a.Status != "not_started" || a.Code.BlockedAt != nil || a.Code.FailedAttempts != 0 {
		t.Fatalf("после снятия блокировки: %+v", a)
	}
	e.mustEnter(issued.Code)
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'code_unblocked' AND outcome = 'ok'`); n != 1 {
		t.Fatal("снятие блокировки пишется в журнал")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'code_unblocked' AND outcome = 'denied'`); n != 1 {
		t.Fatal("отказ методологу пишется в журнал")
	}
}

// TestAssignmentsNeedGroupAccess — I-3 для адресов назначений: без доступа
// к группе — 403 и строка denied, а не пустой список; снятый доступ
// действует без перевхода.
func TestAssignmentsNeedGroupAccess(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)
	observer := e.createObserver()
	group, people := e.hrGroup(admin, "Закрытая группа", "Роза Закрытая")
	version := e.publishedVersion(meth, "training")
	issued := e.assignOne(admin, version, people[0].SubjectID, "")
	aid := issued.AssignmentID
	due := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)

	cases := []struct {
		name, method, path, body string
	}{
		{"список по группе", http.MethodGet, "/api/portal/assignments?group_id=" + group, ""},
		{"одно назначение", http.MethodGet, "/api/portal/assignments?id=" + aid, ""},
		{"по сотруднику", http.MethodGet, "/api/portal/assignments?subject_id=" + people[0].SubjectID, ""},
		{"назначить", http.MethodPost, "/api/portal/assignments", fmt.Sprintf(
			`{"subject_ids":[%q],"scenario_version_id":%q,"difficulty":"normal","due_at":%q}`, people[0].SubjectID, version, due)},
		{"перевыпустить", http.MethodPost, "/api/portal/assignments/reissue-codes", `{"assignment_ids":["` + aid + `"]}`},
		{"продлить", http.MethodPatch, "/api/portal/assignments/" + aid, `{"due_at":"` + due + `"}`},
		{"отменить", http.MethodPost, "/api/portal/assignments/" + aid + "/cancel", ""},
	}
	before := e.count(`SELECT count(*) FROM audit_log WHERE outcome = 'denied' AND group_id = $1`, group)
	for _, c := range cases {
		rec := e.do(c.method, c.path, c.body, observer)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: ожидался 403, получили %d: %s", c.name, rec.Code, rec.Body.String())
		}
		if p := decode[map[string]any](t, rec); p["type"] != "forbidden_group" {
			t.Fatalf("%s: %v", c.name, p)
		}
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE outcome = 'denied' AND group_id = $1`, group); n-before != len(cases) {
		t.Fatalf("отказов в журнале %d, ожидалось %d", n-before, len(cases))
	}
	// Без фильтра — только доступные группы, а не 403.
	rec := e.do(http.MethodGet, "/api/portal/assignments", "", observer)
	expectStatus(t, rec, http.StatusOK)
	if n := decode[struct {
		Page struct{ Total int } `json:"page"`
	}](t, rec).Page.Total; n != 0 {
		t.Fatalf("наблюдатель без доступа видит %d назначений", n)
	}

	observerID := e.userID(admin, "observer1")
	expectStatus(t, e.do(http.MethodPut, "/api/portal/users/"+observerID+"/group-access", `{"group_ids":["`+group+`"]}`, admin), http.StatusOK)
	expectStatus(t, e.do(http.MethodGet, "/api/portal/assignments?group_id="+group, "", observer), http.StatusOK)
	expectStatus(t, e.do(http.MethodPut, "/api/portal/users/"+observerID+"/group-access", `{"group_ids":[]}`, admin), http.StatusOK)
	expectStatus(t, e.do(http.MethodGet, "/api/portal/assignments?group_id="+group, "", observer), http.StatusForbidden)
}

// TestAssessmentAssignments — FR-AC-05: в оценке без ФИО не назначаем
// (частичный успех); старт оценки — только с письменным согласием (D-51),
// попытка одна.
func TestAssessmentAssignments(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)
	e.setKeys(admin, e.defaultProfile(admin).ID)
	group, people := e.hrGroup(admin, "Группа оценки", "Инна Оцениваемая")
	rec := e.do(http.MethodPost, "/api/portal/people", `{"group_id":"`+group+`","pseudonym":"Аноним-1"}`, admin)
	expectStatus(t, rec, http.StatusCreated)
	anonymous := decode[idBody](t, rec)
	version := e.publishedVersion(meth, "assessment")

	rec = e.assign(admin, version, []string{people[0].SubjectID, anonymous.SubjectID}, "")
	expectStatus(t, rec, http.StatusCreated)
	res := decode[createResultBody](t, rec)
	if len(res.Created) != 1 || len(res.Skipped) != 1 || res.Skipped[0].SubjectID != anonymous.SubjectID ||
		res.Skipped[0].Reason != "не назначено — нет ФИО" || res.BatchID == nil {
		t.Fatalf("частичный успех: %+v", res)
	}
	expectStatus(t, e.assign(admin, version, []string{anonymous.SubjectID}, ""), http.StatusUnprocessableEntity)

	entered := e.mustEnter(res.Created[0].Code)
	if entered.Settings.ConsentScreen.CanProceed {
		t.Fatal("без письменного согласия экран не пускает дальше")
	}
	expectStatus(t, e.do(http.MethodPost, "/api/portal/people/"+people[0].SubjectID+"/written-consents",
		`{"document_ref":"№ 7 от 01.09.2026","document_channel":"paper","signed_on":"2026-09-01"}`, admin), http.StatusCreated)
	entered = e.mustEnter(res.Created[0].Code)
	started := e.start(entered.Token, e.consent(entered, true))
	expectStatus(t, started, http.StatusCreated)
	if n := e.count(`SELECT count(*) FROM sessions WHERE assignment_id = $1 AND mode = 'assessment' AND written_consent_id IS NOT NULL`,
		res.Created[0].AssignmentID); n != 1 {
		t.Fatal("оценочная сессия ссылается на письменное согласие")
	}
}

// TestCancelledAssignmentLetsRunningSessionFinish — отмена: код не
// работает сразу, идущая сессия доигрывается (архитектура 9.4); отзыв
// согласия отменяет назначения и коды (UC-A-08).
func TestCancelledAssignmentLetsRunningSessionFinish(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)
	e.setKeys(admin, e.defaultProfile(admin).ID)
	_, people := e.hrGroup(admin, "Группа отмены", "Фёдор Отменённый", "Зоя Отозвавшая")
	version := e.publishedVersion(meth, "training")

	issued := e.assignOne(admin, version, people[0].SubjectID, "")
	entered := e.mustEnter(issued.Code)
	rec := e.start(entered.Token, e.consent(entered, true))
	expectStatus(t, rec, http.StatusCreated)
	session := decode[startBody](t, rec).Session.ID

	rec = e.do(http.MethodPost, "/api/portal/assignments/"+issued.AssignmentID+"/cancel", "", admin)
	expectStatus(t, rec, http.StatusOK)
	cancelled := decode[assignmentBody](t, rec)
	if cancelled.Status != "cancelled" || cancelled.RunningSessionAtCancel == nil || !*cancelled.RunningSessionAtCancel {
		t.Fatalf("отмена при идущей сессии: %+v", cancelled)
	}
	expectStatus(t, e.do(http.MethodPost, "/api/portal/assignments/"+issued.AssignmentID+"/cancel", "", admin), http.StatusOK)
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'assignment_cancelled' AND outcome = 'ok'`); n != 1 {
		t.Fatalf("повторная отмена не пишется в журнал: %d", n)
	}

	expectStatus(t, e.doBearer(http.MethodGet, "/api/trainer/sessions/"+session+"/private", "", entered.Token), http.StatusOK)
	rec = e.enter(issued.Code)
	expectStatus(t, rec, http.StatusGone)
	if p := decode[map[string]any](t, rec); p["type"] != "assignment_cancelled" {
		t.Fatalf("код отменённого назначения: %v", p)
	}
	due := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)
	expectStatus(t, e.do(http.MethodPatch, "/api/portal/assignments/"+issued.AssignmentID, `{"due_at":"`+due+`"}`, admin), http.StatusGone)

	withdrawn := e.assignOne(admin, version, people[1].SubjectID, "")
	expectStatus(t, e.do(http.MethodPost, "/api/portal/people/"+people[1].SubjectID+"/consent-withdrawal",
		`{"scope":"all","document_ref":"№ 2 от 02.09.2026","confirm_name":"Зоя Отозвавшая"}`, admin), http.StatusOK)
	expectStatus(t, e.enter(withdrawn.Code), http.StatusGone)
	if n := e.count(`SELECT count(*) FROM assignments WHERE id = $1 AND cancel_reason = 'consent_withdrawn'`, withdrawn.AssignmentID); n != 1 {
		t.Fatal("отзыв согласия отменяет назначение")
	}
	if n := e.count(`SELECT count(*) FROM access_codes WHERE assignment_id = $1 AND revoke_reason = 'consent_withdrawn'`, withdrawn.AssignmentID); n != 1 {
		t.Fatal("отзыв согласия отзывает код")
	}
}

// TestTrainerEnterRateLimit — ввод кода: 10 в минуту с адреса (8.1).
func TestTrainerEnterRateLimit(t *testing.T) {
	e := newTestEnv(t)
	for i := range 10 {
		if rec := e.enter("ARENA-0000-0000-00X"); rec.Code != http.StatusNotFound {
			t.Fatalf("попытка %d: %d", i+1, rec.Code)
		}
	}
	rec := e.enter("ARENA-0000-0000-00X")
	expectStatus(t, rec, http.StatusTooManyRequests)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 без Retry-After")
	}
}
