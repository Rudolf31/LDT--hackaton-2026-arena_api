//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
)

type idBody struct {
	ID        string `json:"id"`
	SubjectID string `json:"subject_id"`
	Number    string `json:"number"`
}

// createGroup и createPerson — от имени администратора.
func (e *testEnv) createGroup(admin *http.Cookie, name string) string {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/api/portal/groups", `{"name":"`+name+`"}`, admin)
	expectStatus(e.t, rec, http.StatusCreated)
	return decode[idBody](e.t, rec).ID
}

func (e *testEnv) createPerson(admin *http.Cookie, groupID, fullName string) idBody {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/api/portal/people", `{"group_id":"`+groupID+`","full_name":"`+fullName+`"}`, admin)
	expectStatus(e.t, rec, http.StatusCreated)
	return decode[idBody](e.t, rec)
}

func (e *testEnv) userID(admin *http.Cookie, login string) string {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/api/portal/users", "", admin)
	expectStatus(e.t, rec, http.StatusOK)
	for _, u := range decode[[]struct {
		ID    string `json:"id"`
		Login string `json:"login"`
	}](e.t, rec) {
		if u.Login == login {
			return u.ID
		}
	}
	e.t.Fatalf("пользователя %s нет", login)
	return ""
}

func TestSettingsClosedWithoutLoginAndToNonAdmin(t *testing.T) {
	e := newTestEnv(t)

	expectStatus(t, e.do(http.MethodGet, "/api/portal/settings", "", nil), http.StatusUnauthorized)
	expectStatus(t, e.do(http.MethodPatch, "/api/portal/settings", `{"admission_rehearsals":5}`, nil), http.StatusUnauthorized)

	meth := e.login(methLogin, methPassword)
	expectStatus(t, e.do(http.MethodGet, "/api/portal/settings", "", meth), http.StatusOK)
	rec := e.do(http.MethodPatch, "/api/portal/settings", `{"admission_rehearsals":5}`, meth)
	expectStatus(t, rec, http.StatusForbidden)
	if p := decode[map[string]any](t, rec); p["type"] != "forbidden_role" {
		t.Fatalf("ожидался forbidden_role: %v", p)
	}
	// I-4: отказ по роли оставляет строку denied под действием операции.
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'settings_changed' AND outcome = 'denied'`); n != 1 {
		t.Fatalf("ожидалась одна строка отказа settings_changed, получили %d", n)
	}
}

func TestLoginWritesJournalAndRejectsWrongPassword(t *testing.T) {
	e := newTestEnv(t)
	e.login(adminLogin, adminPassword)

	rec := e.do(http.MethodPost, "/api/portal/auth/login", `{"login":"admin","password":"не тот пароль"}`, nil)
	expectStatus(t, rec, http.StatusUnauthorized)
	unknown := e.do(http.MethodPost, "/api/portal/auth/login", `{"login":"nobody","password":"не тот пароль"}`, nil)
	expectStatus(t, unknown, http.StatusUnauthorized)
	if a, b := decode[map[string]any](t, rec)["title"], decode[map[string]any](t, unknown)["title"]; a != b {
		t.Fatalf("ответ выдаёт, существует ли логин: %q против %q", a, b)
	}

	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'login'`); n != 1 {
		t.Fatalf("строк login: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'login_failed'`); n != 2 {
		t.Fatalf("строк login_failed: %d", n)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newTestEnv(t)
	for i := 0; i < 5; i++ {
		expectStatus(t, e.do(http.MethodPost, "/api/portal/auth/login", `{"login":"admin","password":"неверный-пароль"}`, nil), http.StatusUnauthorized)
	}
	rec := e.do(http.MethodPost, "/api/portal/auth/login", `{"login":"admin","password":"`+adminPassword+`"}`, nil)
	expectStatus(t, rec, http.StatusTooManyRequests)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("нет заголовка Retry-After")
	}
}

func TestMeAndLogout(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)

	rec := e.do(http.MethodGet, "/api/portal/auth/me", "", meth)
	expectStatus(t, rec, http.StatusOK)
	me := decode[struct {
		User struct {
			Role     string   `json:"role"`
			GroupIDs []string `json:"group_ids"`
		} `json:"user"`
	}](t, rec)
	if me.User.Role != "methodologist" || len(me.User.GroupIDs) != 1 {
		t.Fatalf("me: %+v", me)
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "arena_session=") {
		t.Fatal("cookie не продлевается на запросе")
	}

	out := e.do(http.MethodPost, "/api/portal/auth/logout", "", meth)
	expectStatus(t, out, http.StatusNoContent)
	if c := out.Result().Cookies(); len(c) != 1 || c[0].Value != "" || c[0].MaxAge >= 0 {
		t.Fatalf("выход не стёр cookie: %+v", c)
	}
}

func TestDisabledUserLosesAccessOnNextRequest(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)

	expectStatus(t, e.do(http.MethodPatch, "/api/portal/users/"+e.userID(admin, methLogin), `{"is_active":false}`, admin), http.StatusOK)
	expectStatus(t, e.do(http.MethodGet, "/api/portal/auth/me", "", meth), http.StatusUnauthorized)
	expectStatus(t, e.do(http.MethodPost, "/api/portal/auth/login", `{"login":"methodologist","password":"`+methPassword+`"}`, nil), http.StatusUnauthorized)
}

func TestAdminCannotLockThemselfOut(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	id := e.userID(admin, adminLogin)
	expectStatus(t, e.do(http.MethodPatch, "/api/portal/users/"+id, `{"is_active":false}`, admin), http.StatusUnprocessableEntity)
	expectStatus(t, e.do(http.MethodPatch, "/api/portal/users/"+id, `{"role":"observer"}`, admin), http.StatusUnprocessableEntity)
}

func TestCreateUserRejectsTakenLogin(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	body := `{"login":"observer1","full_name":"Наблюдатель","role":"observer","password":"пароль-наблюдателя"}`
	expectStatus(t, e.do(http.MethodPost, "/api/portal/users", body, admin), http.StatusCreated)
	expectStatus(t, e.do(http.MethodPost, "/api/portal/users", body, admin), http.StatusUnprocessableEntity)
	e.login("observer1", "пароль-наблюдателя")
}

// I-3: нет доступа к группе — 403, а не пустой список; отказ в журнал;
// снятый доступ действует на следующем же запросе, без перевхода.
func TestGroupAccessIsCheckedOnEveryRequest(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)

	closed := e.createGroup(admin, "Закрытая группа")
	person := e.createPerson(admin, closed, "Пётр Закрытый")

	rec := e.do(http.MethodGet, "/api/portal/people?group_id="+closed, "", meth)
	expectStatus(t, rec, http.StatusForbidden)
	if p := decode[map[string]any](t, rec); p["type"] != "forbidden_group" {
		t.Fatalf("ожидался forbidden_group: %v", p)
	}
	expectStatus(t, e.do(http.MethodGet, "/api/portal/people/"+person.SubjectID, "", meth), http.StatusForbidden)
	if n := e.count(`SELECT count(*) FROM audit_log WHERE outcome = 'denied' AND group_id = $1`, closed); n != 2 {
		t.Fatalf("ожидалось две строки отказа по группе, получили %d", n)
	}

	// Список без фильтра — только группы с доступом.
	list := decode[struct {
		Items []struct {
			GroupID string `json:"group_id"`
		} `json:"items"`
	}](t, e.do(http.MethodGet, "/api/portal/people", "", meth))
	for _, p := range list.Items {
		if p.GroupID == closed {
			t.Fatal("в общем списке методолога сотрудник группы без доступа")
		}
	}

	methID := e.userID(admin, methLogin)
	expectStatus(t, e.do(http.MethodPut, "/api/portal/users/"+methID+"/group-access", `{"group_ids":["`+closed+`"]}`, admin), http.StatusOK)
	expectStatus(t, e.do(http.MethodGet, "/api/portal/people?group_id="+closed, "", meth), http.StatusOK)

	expectStatus(t, e.do(http.MethodPut, "/api/portal/users/"+methID+"/group-access", `{"group_ids":[]}`, admin), http.StatusOK)
	expectStatus(t, e.do(http.MethodGet, "/api/portal/people?group_id="+closed, "", meth), http.StatusForbidden)

	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'access_granted' AND group_id = $1`, closed); n != 1 {
		t.Fatalf("строк access_granted: %d", n)
	}
	// Демо-группа и закрытая: обе сняты вторым PUT.
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'access_revoked'`); n != 2 {
		t.Fatalf("строк access_revoked: %d", n)
	}
}

func TestAdminSeesPersonButNotResultsWithoutAccess(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	group := e.createGroup(admin, "Группа без доступа админа")
	person := e.createPerson(admin, group, "Ольга Проверочная")

	rec := e.do(http.MethodGet, "/api/portal/people/"+person.SubjectID, "", admin)
	expectStatus(t, rec, http.StatusOK)
	if card := decode[map[string]any](t, rec); card["results_visible"] != false {
		t.Fatalf("results_visible у администратора без доступа: %v", card["results_visible"])
	}
	if !strings.HasPrefix(person.Number, "N-") || len(person.Number) != 8 {
		t.Fatalf("номер участника не в формате N-XXXXXX: %q", person.Number)
	}
}

func TestConsentWithdrawalDestroysLinkAndKey(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	group := e.createGroup(admin, "Группа отзыва")
	person := e.createPerson(admin, group, "Вера Отзывающая")
	path := "/api/portal/people/" + person.SubjectID + "/consent-withdrawal"

	expectStatus(t, e.do(http.MethodPost, path, `{"scope":"all","document_ref":"№ 1 от 01.09.2026","confirm_name":"Кто-то другой"}`, admin), http.StatusUnprocessableEntity)
	if n := e.count(`SELECT count(*) FROM people WHERE subject_id = $1`, person.SubjectID); n != 1 {
		t.Fatal("неверное подтверждение не должно ничего удалять")
	}

	rec := e.do(http.MethodPost, path, `{"scope":"all","document_ref":"№ 1 от 01.09.2026","confirm_name":"Вера Отзывающая"}`, admin)
	expectStatus(t, rec, http.StatusOK)
	if r := decode[map[string]any](t, rec); r["number"] != person.Number {
		t.Fatalf("в ответе не тот номер: %v", r)
	}

	if n := e.count(`SELECT count(*) FROM people WHERE subject_id = $1`, person.SubjectID); n != 0 {
		t.Fatal("строка people осталась")
	}
	if n := e.count(`SELECT count(*) FROM subjects WHERE id = $1 AND data_key_wrapped IS NULL AND key_destroyed_at IS NOT NULL`, person.SubjectID); n != 1 {
		t.Fatal("ключ данных не уничтожен")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'consent_withdrawn' AND subject_id = $1`, person.SubjectID); n != 1 {
		t.Fatal("нет строки consent_withdrawn")
	}
	expectStatus(t, e.do(http.MethodGet, "/api/portal/people/"+person.SubjectID, "", admin), http.StatusNotFound)
}

func TestExternalAIWithdrawal(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	person := e.createPerson(admin, e.createGroup(admin, "Группа ИИ"), "Семён Осторожный")
	path := "/api/portal/people/" + person.SubjectID + "/consent-withdrawal"

	expectStatus(t, e.do(http.MethodPost, path, `{"scope":"external_ai","document_ref":"№ 2"}`, admin), http.StatusOK)
	expectStatus(t, e.do(http.MethodPost, path, `{"scope":"external_ai","document_ref":"№ 2"}`, admin), http.StatusConflict)
	if n := e.count(`SELECT count(*) FROM people WHERE subject_id = $1 AND external_ai_withdrawn_at IS NOT NULL`, person.SubjectID); n != 1 {
		t.Fatal("отметка external_ai_withdrawn_at не поставлена")
	}
}

// I-5: имён сотрудников нет ни в details журнала, ни в логе.
func TestNamesNeverReachJournalOrLog(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	group := e.createGroup(admin, "Группа имён")
	names := []string{"Эльвира Уникальнова", "Тимофей Редкофамильный"}
	var subjects []idBody
	for _, n := range names {
		subjects = append(subjects, e.createPerson(admin, group, n))
	}
	expectStatus(t, e.do(http.MethodPatch, "/api/portal/people/"+subjects[0].SubjectID, `{"full_name":"Эльвира Переименованная"}`, admin), http.StatusOK)
	expectStatus(t, e.do(http.MethodPost, "/api/portal/people/"+subjects[1].SubjectID+"/consent-withdrawal",
		`{"scope":"all","document_ref":"заявление Тимофей Редкофамильный","confirm_name":"Тимофей Редкофамильный"}`, admin), http.StatusOK)
	e.do(http.MethodGet, "/api/portal/audit-log", "", admin)

	for _, name := range append(names, "Эльвира Переименованная", "Уникальнова", "Редкофамильный") {
		if n := e.count(`SELECT count(*) FROM audit_log WHERE details::text LIKE '%' || $1 || '%'`, name); n != 0 {
			t.Errorf("имя %q попало в details журнала", name)
		}
		if strings.Contains(e.log.String(), name) {
			t.Errorf("имя %q попало в лог", name)
		}
	}
}

func TestGroupsListAndUpdate(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)
	id := e.createGroup(admin, "Группа для правки")

	expectStatus(t, e.do(http.MethodPost, "/api/portal/groups", `{"name":"Группа для правки"}`, admin), http.StatusUnprocessableEntity)
	expectStatus(t, e.do(http.MethodPost, "/api/portal/groups", `{"name":"Чужая"}`, meth), http.StatusForbidden)

	rec := e.do(http.MethodPatch, "/api/portal/groups/"+id, `{"archived":true}`, admin)
	expectStatus(t, rec, http.StatusOK)
	if g := decode[map[string]any](t, rec); g["archived_at"] == nil {
		t.Fatal("группа не ушла в архив")
	}
	expectStatus(t, e.do(http.MethodPost, "/api/portal/people", `{"group_id":"`+id+`","full_name":"В архив"}`, admin), http.StatusConflict)

	groups := decode[[]struct {
		Name      string `json:"name"`
		HasAccess bool   `json:"has_access"`
	}](t, e.do(http.MethodGet, "/api/portal/groups", "", meth))
	if len(groups) != 1 || !groups[0].HasAccess {
		t.Fatalf("методолог должен видеть одну неархивную демо-группу с доступом: %+v", groups)
	}
}

func TestAuditLogListAndExport(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	person := e.createPerson(admin, e.createGroup(admin, "Группа журнала"), "Журнал Проверочный")

	rec := e.do(http.MethodGet, "/api/portal/audit-log?subject_number="+person.Number, "", admin)
	expectStatus(t, rec, http.StatusOK)
	page := decode[struct {
		Items []struct {
			Action        string `json:"action"`
			SubjectNumber string `json:"subject_number"`
			ActorName     string `json:"actor_name"`
		} `json:"items"`
		Page struct {
			Total int `json:"total"`
		} `json:"page"`
	}](t, rec)
	if page.Page.Total != 1 || page.Items[0].Action != "person_saved" || page.Items[0].SubjectNumber != person.Number || page.Items[0].ActorName != "Администратор демо" {
		t.Fatalf("журнал по номеру: %+v", page)
	}

	exp := e.do(http.MethodPost, "/api/portal/audit-log/export", `{"subject_number":"`+person.Number+`"}`, admin)
	expectStatus(t, exp, http.StatusOK)
	if !strings.Contains(exp.Body.String(), person.Number) || !strings.HasPrefix(exp.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("выгрузка: %s", exp.Body.String())
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'audit_exported' AND rows_count = 1`); n != 1 {
		t.Fatal("выгрузка не записала audit_exported с числом строк")
	}

	expectStatus(t, e.do(http.MethodGet, "/api/portal/audit-log", "", e.login(methLogin, methPassword)), http.StatusForbidden)
}

func TestSeedRunsOnlyOnEmptyDatabase(t *testing.T) {
	e := newTestEnv(t)
	if n := e.count(`SELECT count(*) FROM portal_users`); n != 2 {
		t.Fatalf("после заливки пользователей %d", n)
	}
	if n := e.count(`SELECT count(*) FROM people`); n != 4 {
		t.Fatalf("после заливки сотрудников %d", n)
	}
	if _, err := buildApp(t.Context(), e.pool, testConfig(), discardLogger()); err != nil {
		t.Fatalf("повторная сборка: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM portal_users`); n != 2 {
		t.Fatalf("повторный старт залил пользователей ещё раз: %d", n)
	}
}
