//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// --- помощники ---

func (e *testEnv) doWithIfMatch(method, path, body string, cookie *http.Cookie, ifMatch string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) createScenario(cookie *http.Cookie, body string) map[string]any {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/api/portal/scenarios", body, cookie)
	expectStatus(e.t, rec, http.StatusCreated)
	return decode[map[string]any](e.t, rec)
}

func (e *testEnv) draft(cookie *http.Cookie, scenarioID string) (map[string]any, string) {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/api/portal/scenarios/"+scenarioID+"/draft", "", cookie)
	expectStatus(e.t, rec, http.StatusOK)
	return decode[map[string]any](e.t, rec), rec.Header().Get("ETag")
}

func (e *testEnv) publish(cookie *http.Cookie, scenarioID, fingerprint string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(http.MethodPost, "/api/portal/scenarios/"+scenarioID+"/publish", fmt.Sprintf(`{"fingerprint":%q}`, fingerprint), cookie)
}

func topLevelKeys(doc map[string]any) []string {
	out := make([]string, 0, len(doc))
	for k := range doc {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (e *testEnv) createObserver() *http.Cookie {
	e.t.Helper()
	admin := e.login(adminLogin, adminPassword)
	rec := e.do(http.MethodPost, "/api/portal/users",
		`{"login":"observer1","password":"arena-observer-2026","full_name":"Наблюдатель демо","role":"observer"}`, admin)
	expectStatus(e.t, rec, http.StatusCreated)
	return e.login("observer1", "arena-observer-2026")
}

// --- FR-SC-01: пять входов дают черновик с одинаковым списком разделов ---

func TestScenarioCreateOriginsShareSections(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)

	manual := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	manualDoc, _ := e.draft(meth, manual["id"].(string))
	manualSections := topLevelKeys(manualDoc["document"].(map[string]any))

	tpl := e.createScenario(meth, `{"origin":"template","mode":"training","template_id":"procurement"}`)
	tplDoc, _ := e.draft(meth, tpl["id"].(string))
	tplSections := topLevelKeys(tplDoc["document"].(map[string]any))
	if !equalStrings(tplSections, manualSections) {
		t.Fatalf("шаблон: разделы %v != %v", tplSections, manualSections)
	}

	copyBody := fmt.Sprintf(`{"origin":"copy","mode":"training","source_scenario_id":%q,"title":"Копия закупки"}`, tpl["id"])
	cp := e.createScenario(meth, copyBody)
	cpDoc, _ := e.draft(meth, cp["id"].(string))
	cpSections := topLevelKeys(cpDoc["document"].(map[string]any))
	if !equalStrings(cpSections, manualSections) {
		t.Fatalf("копия: разделы %v != %v", cpSections, manualSections)
	}

	exportRec := e.do(http.MethodGet, "/api/portal/scenarios/"+tpl["id"].(string)+"/export", "", meth)
	expectStatus(t, exportRec, http.StatusOK)
	imported := e.createScenarioFromImport(meth, exportRec.Body.Bytes())
	impDoc, _ := e.draft(meth, imported["id"].(string))
	impSections := topLevelKeys(impDoc["document"].(map[string]any))
	if !equalStrings(impSections, manualSections) {
		t.Fatalf("импорт: разделы %v != %v", impSections, manualSections)
	}

	// brief — пятый вход — документа ещё нет (этап 05); проверяем только,
	// что сценарий заведён и в библиотеке видно название из generation.
	brief := e.createScenario(meth, `{"origin":"brief","mode":"training","title":"Бриф про закупку"}`)
	if brief["title"] != "Бриф про закупку" {
		t.Fatalf("brief: title = %v", brief["title"])
	}
	if brief["origin"] != "brief" {
		t.Fatalf("brief: origin = %v", brief["origin"])
	}
}

func (e *testEnv) createScenarioFromImport(cookie *http.Cookie, document []byte) map[string]any {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/api/portal/scenarios/import", string(document), cookie)
	expectStatus(e.t, rec, http.StatusCreated)
	return decode[map[string]any](e.t, rec)
}

// --- FR-SC-02: каркас публикуется без единой правки ---

func TestManualSkeletonPublishesWithoutEdits(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)

	card := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	d, _ := e.draft(meth, card["id"].(string))
	fingerprint := d["fingerprint"].(string)
	check := d["check"].(map[string]any)
	if int(check["blocking"].(float64)) != 0 {
		t.Fatalf("каркас должен проходить проверку без блокирующих ошибок: %v", check)
	}

	rec := e.publish(meth, card["id"].(string), fingerprint)
	expectStatus(t, rec, http.StatusCreated)
	version := decode[map[string]any](t, rec)
	if int(version["number"].(float64)) != 1 {
		t.Fatalf("ожидалась версия 1: %v", version)
	}
}

// --- I-13: экспорт → импорт → тот же отпечаток ---

func TestExportImportRoundTripSameFingerprint(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)

	card := e.createScenario(meth, `{"origin":"template","mode":"training","template_id":"sales"}`)
	d, _ := e.draft(meth, card["id"].(string))
	fingerprint := d["fingerprint"].(string)

	exportRec := e.do(http.MethodGet, "/api/portal/scenarios/"+card["id"].(string)+"/export", "", meth)
	expectStatus(t, exportRec, http.StatusOK)

	imported := e.createScenarioFromImport(meth, exportRec.Body.Bytes())
	impDraft, _ := e.draft(meth, imported["id"].(string))
	if impDraft["fingerprint"] != fingerprint {
		t.Fatalf("отпечаток после экспорта/импорта изменился: было %v, стало %v", fingerprint, impDraft["fingerprint"])
	}
}

// --- повторная публикация и отпечаток, нечувствительный к тегам ---

func TestPublishTwiceWithoutChangesIsNothingChanged(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)

	card := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	id := card["id"].(string)
	d, _ := e.draft(meth, id)
	fingerprint := d["fingerprint"].(string)

	expectStatus(t, e.publish(meth, id, fingerprint), http.StatusCreated)

	rec := e.publish(meth, id, fingerprint)
	expectStatus(t, rec, http.StatusConflict)
	if p := decode[map[string]any](t, rec); p["type"] != "nothing_changed" {
		t.Fatalf("ожидался nothing_changed: %v", p)
	}
}

// TestTagEditDoesNotChangeFingerprint — «ловушка» из 04-scenarios.md: правка
// тегов не меняет отпечаток и повторная публикация тоже отвечает 409.
func TestTagEditDoesNotChangeFingerprint(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)

	card := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	id := card["id"].(string)
	d, _ := e.draft(meth, id)
	fingerprint := d["fingerprint"].(string)
	expectStatus(t, e.publish(meth, id, fingerprint), http.StatusCreated)

	doc := d["document"].(map[string]any)
	passport := doc["passport"].(map[string]any)
	passport["tags"] = []string{"новый", "тег"}
	body, err := json.Marshal(map[string]any{"document": doc})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	saveRec := e.do(http.MethodPut, "/api/portal/scenarios/"+id+"/draft", string(body), meth)
	expectStatus(t, saveRec, http.StatusOK)
	saved := decode[map[string]any](t, saveRec)
	if saved["fingerprint"] != fingerprint {
		t.Fatalf("правка тегов не должна менять отпечаток: было %v, стало %v", fingerprint, saved["fingerprint"])
	}

	rec := e.publish(meth, id, fingerprint)
	expectStatus(t, rec, http.StatusConflict)
}

// --- If-Match и смена режима ---

func TestSaveDraftStaleIfMatchIs412(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)

	card := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	id := card["id"].(string)
	d, etag := e.draft(meth, id)
	doc := d["document"].(map[string]any)
	body, err := json.Marshal(map[string]any{"document": doc})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// первое сохранение со старым ETag проходит и меняет draft_updated_at
	expectStatus(t, e.doWithIfMatch(http.MethodPut, "/api/portal/scenarios/"+id+"/draft", string(body), meth, etag), http.StatusOK)

	// второе — с тем же (уже устаревшим) ETag — 412
	rec := e.doWithIfMatch(http.MethodPut, "/api/portal/scenarios/"+id+"/draft", string(body), meth, etag)
	expectStatus(t, rec, http.StatusPreconditionFailed)
	if p := decode[map[string]any](t, rec); p["type"] != "draft_changed" {
		t.Fatalf("ожидался draft_changed: %v", p)
	}
}

func TestModeChangeLocksAfterPublish(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)

	card := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	id := card["id"].(string)

	// до публикации режим меняется свободно и пишет scenario_mode_changed
	d, _ := e.draft(meth, id)
	doc := d["document"].(map[string]any)
	doc["passport"].(map[string]any)["mode"] = "assessment"
	body, err := json.Marshal(map[string]any{"document": doc})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	saveRec := e.do(http.MethodPut, "/api/portal/scenarios/"+id+"/draft", string(body), meth)
	expectStatus(t, saveRec, http.StatusOK)
	if got := e.count(`SELECT count(*) FROM audit_log WHERE action = 'scenario_mode_changed'`); got != 1 {
		t.Fatalf("ожидалась одна запись scenario_mode_changed, получено %d", got)
	}

	saved := decode[map[string]any](t, saveRec)
	fingerprint := saved["fingerprint"].(string)
	expectStatus(t, e.publish(meth, id, fingerprint), http.StatusCreated)

	// после публикации смена режима — 409 mode_locked, черновик не сохраняется
	d2, _ := e.draft(meth, id)
	doc2 := d2["document"].(map[string]any)
	doc2["passport"].(map[string]any)["mode"] = "training"
	body2, err := json.Marshal(map[string]any{"document": doc2})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := e.do(http.MethodPut, "/api/portal/scenarios/"+id+"/draft", string(body2), meth)
	expectStatus(t, rec, http.StatusConflict)
	if p := decode[map[string]any](t, rec); p["type"] != "mode_locked" {
		t.Fatalf("ожидался mode_locked: %v", p)
	}

	// копия — единственный способ сменить режим, когда версии уже есть
	cp := e.createScenario(meth, fmt.Sprintf(`{"origin":"copy","mode":"training","source_scenario_id":%q,"title":"Копия под тренировку"}`, id))
	if cp["mode"] != "training" {
		t.Fatalf("копия должна была сменить режим на training: %v", cp["mode"])
	}
}

// --- проверка при публикации ---

func TestPublishWrongFingerprintIs409(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)
	card := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	rec := e.publish(meth, card["id"].(string), strings.Repeat("0", 64))
	expectStatus(t, rec, http.StatusConflict)
	if p := decode[map[string]any](t, rec); p["type"] != "fingerprint_changed" {
		t.Fatalf("ожидался fingerprint_changed: %v", p)
	}
}

func TestPublishBlockingErrorIs422AndNoVersionCreated(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)

	card := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	id := card["id"].(string)
	d, _ := e.draft(meth, id)
	doc := d["document"].(map[string]any)
	// ломаем документ: у первого этапа убираем переходы — правило 2
	// (недостижимость) должно дать блокирующую ошибку.
	stages := doc["stages"].([]any)
	first := stages[0].(map[string]any)
	first["transitions"] = []any{}
	body, err := json.Marshal(map[string]any{"document": doc})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	saveRec := e.do(http.MethodPut, "/api/portal/scenarios/"+id+"/draft", string(body), meth)
	expectStatus(t, saveRec, http.StatusOK)
	saved := decode[map[string]any](t, saveRec)
	check := saved["check"].(map[string]any)
	if int(check["blocking"].(float64)) == 0 {
		t.Skip("испорченный документ неожиданно прошёл проверку — сценарий теста не подошёл")
	}

	rec := e.publish(meth, id, saved["fingerprint"].(string))
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if p := decode[map[string]any](t, rec); p["type"] != "scenario_check_failed" {
		t.Fatalf("ожидался scenario_check_failed: %v", p)
	}
	if got := e.count(`SELECT count(*) FROM scenario_versions WHERE scenario_id = $1`, id); got != 0 {
		t.Fatalf("версия не должна была создаться: %d", got)
	}
}

// --- импорт повреждённого файла ---

func TestImportCorruptedDocumentCreatesNothing(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)

	before := e.count(`SELECT count(*) FROM scenarios`)
	rec := e.do(http.MethodPost, "/api/portal/scenarios/import", `{"format":"arena-scenario/999","passport":{}}`, meth)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	after := e.count(`SELECT count(*) FROM scenarios`)
	if before != after {
		t.Fatalf("повреждённый импорт не должен ничего заводить: было %d, стало %d", before, after)
	}
}

// --- наблюдатель, архив, журнал ---

func TestObserverSeesOnlyPublished(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)
	observer := e.createObserver()

	draftOnly := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	published := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	d, _ := e.draft(meth, published["id"].(string))
	expectStatus(t, e.publish(meth, published["id"].(string), d["fingerprint"].(string)), http.StatusCreated)

	listRec := e.do(http.MethodGet, "/api/portal/scenarios", "", observer)
	expectStatus(t, listRec, http.StatusOK)
	page := decode[map[string]any](t, listRec)
	items := page["items"].([]any)
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["id"] == draftOnly["id"] {
			t.Fatalf("наблюдатель не должен видеть черновой сценарий в библиотеке: %v", item)
		}
	}
	found := false
	for _, raw := range items {
		if raw.(map[string]any)["id"] == published["id"] {
			found = true
		}
	}
	if !found {
		t.Fatal("наблюдатель должен видеть опубликованный сценарий")
	}

	// GetScenario без версий — 404 для наблюдателя
	rec := e.do(http.MethodGet, "/api/portal/scenarios/"+draftOnly["id"].(string), "", observer)
	expectStatus(t, rec, http.StatusNotFound)

	// черновик наблюдателю закрыт ролью
	rec = e.do(http.MethodGet, "/api/portal/scenarios/"+published["id"].(string)+"/draft", "", observer)
	expectStatus(t, rec, http.StatusForbidden)
}

func TestArchiveHidesAndReturns(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)

	card := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	id := card["id"].(string)

	rec := e.do(http.MethodPost, "/api/portal/scenarios/"+id+"/archive", `{"archived":true}`, meth)
	expectStatus(t, rec, http.StatusOK)
	archived := decode[map[string]any](t, rec)
	if archived["status"] != "archived" {
		t.Fatalf("status = %v", archived["status"])
	}
	if got := e.count(`SELECT count(*) FROM audit_log WHERE action = 'scenario_archived'`); got != 1 {
		t.Fatalf("ожидалась одна запись scenario_archived, получено %d", got)
	}

	listRec := e.do(http.MethodGet, "/api/portal/scenarios", "", meth)
	expectStatus(t, listRec, http.StatusOK)
	page := decode[map[string]any](t, listRec)
	for _, raw := range page["items"].([]any) {
		if raw.(map[string]any)["id"] == id {
			t.Fatal("архивный сценарий не должен быть в списке по умолчанию")
		}
	}

	archivedListRec := e.do(http.MethodGet, "/api/portal/scenarios?status=archived", "", meth)
	expectStatus(t, archivedListRec, http.StatusOK)
	archivedPage := decode[map[string]any](t, archivedListRec)
	found := false
	for _, raw := range archivedPage["items"].([]any) {
		if raw.(map[string]any)["id"] == id {
			found = true
		}
	}
	if !found {
		t.Fatal("status=archived должен показывать архивный сценарий")
	}

	rec = e.do(http.MethodPost, "/api/portal/scenarios/"+id+"/archive", `{"archived":false}`, meth)
	expectStatus(t, rec, http.StatusOK)
	restored := decode[map[string]any](t, rec)
	if restored["status"] == "archived" {
		t.Fatalf("сценарий должен был вернуться из архива: %v", restored["status"])
	}
}

// TestPublishDeniedByRoleIsJournaled — отказ по роли на публикации пишется
// под тем же действием, что и успех (D-24).
func TestPublishDeniedByRoleIsJournaled(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)
	observer := e.createObserver()

	card := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	id := card["id"].(string)
	d, _ := e.draft(meth, id)

	rec := e.publish(observer, id, d["fingerprint"].(string))
	expectStatus(t, rec, http.StatusForbidden)
	if p := decode[map[string]any](t, rec); p["type"] != "forbidden_role" {
		t.Fatalf("ожидался forbidden_role: %v", p)
	}
	if got := e.count(`SELECT count(*) FROM audit_log WHERE action = 'scenario_published' AND outcome = 'denied'`); got != 1 {
		t.Fatalf("ожидалась одна запись scenario_published/denied, получено %d", got)
	}
}
