//go:build integration

package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/profiles"
)

const (
	testOpenRouterKey = "sk-or-v1-secret-openrouter-key-QZWR"
	testServerToken   = "own-server-secret-token-KXVY"
)

type profileBody struct {
	ID                    string         `json:"id"`
	Name                  string         `json:"name"`
	IsDefault             bool           `json:"is_default"`
	Revision              int            `json:"revision"`
	Settings              map[string]any `json:"settings"`
	HasOpenRouterKey      bool           `json:"has_openrouter_key"`
	OpenRouterKeyLast4    *string        `json:"openrouter_key_last4"`
	ModelServerTokenLast4 *string        `json:"model_server_token_last4"`
	SameKeyProfiles       []string       `json:"same_key_profiles"`
}

func (e *testEnv) defaultProfile(admin *http.Cookie) profileBody {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/api/portal/trainer-profiles", "", admin)
	expectStatus(e.t, rec, http.StatusOK)
	list := decode[[]profileBody](e.t, rec)
	if len(list) == 0 || !list[0].IsDefault {
		e.t.Fatalf("профиль по умолчанию должен идти первым: %+v", list)
	}
	return list[0]
}

func TestDefaultProfileIsSeededFirstAndNotArchivable(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	def := e.defaultProfile(admin)
	if def.OpenRouterKeyLast4 != nil || def.HasOpenRouterKey {
		t.Fatal("профиль по умолчанию заводится без ключей")
	}

	rec := e.do(http.MethodPost, "/api/portal/trainer-profiles/"+def.ID+"/archive", "", admin)
	expectStatus(t, rec, http.StatusConflict)
	if p := decode[map[string]any](t, rec); p["title"] == "" {
		t.Fatal("у отказа должна быть русская фраза")
	}
	if n := e.count(`SELECT count(*) FROM trainer_profiles WHERE is_default AND archived_at IS NULL`); n != 1 {
		t.Fatalf("профиль по умолчанию должен остаться: %d", n)
	}
}

// TestProviderKeysNeverLeakToPortal — I-9 (часть портала): ключ целиком
// не возвращается ни одной роли; не-администратор не видит и последних
// символов; ни ключа, ни его символов нет в журнале и в логе.
func TestProviderKeysNeverLeakToPortal(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)
	observer := e.createObserver()
	def := e.defaultProfile(admin)

	rec := e.do(http.MethodPut, "/api/portal/trainer-profiles/"+def.ID+"/keys",
		`{"openrouter_key":"`+testOpenRouterKey+`","model_server_token":"`+testServerToken+`"}`, admin)
	expectStatus(t, rec, http.StatusOK)
	saved := decode[profileBody](t, rec)
	if saved.OpenRouterKeyLast4 == nil || *saved.OpenRouterKeyLast4 != "QZWR" {
		t.Fatalf("администратор видит последние 4 символа: %+v", saved.OpenRouterKeyLast4)
	}
	if saved.Revision != def.Revision {
		t.Fatal("смена ключей не растит ревизию (D-44)")
	}

	paths := []string{"/api/portal/trainer-profiles", "/api/portal/trainer-profiles/" + def.ID}
	for _, who := range []struct {
		name   string
		cookie *http.Cookie
		admin  bool
	}{{"admin", admin, true}, {"methodologist", meth, false}, {"observer", observer, false}} {
		for _, path := range paths {
			rec := e.do(http.MethodGet, path, "", who.cookie)
			expectStatus(t, rec, http.StatusOK)
			body := rec.Body.String()
			if strings.Contains(body, testOpenRouterKey) || strings.Contains(body, testServerToken) {
				t.Fatalf("%s %s: ключ целиком в ответе", who.name, path)
			}
			if !who.admin && (strings.Contains(body, "last4") || strings.Contains(body, "QZWR") || strings.Contains(body, "KXVY")) {
				t.Fatalf("%s %s: не-администратор видит последние символы ключа: %s", who.name, path, body)
			}
		}
	}

	rec = e.do(http.MethodGet, "/api/portal/trainer-profiles?key_last4=QZWR", "", meth)
	expectStatus(t, rec, http.StatusForbidden)
	rec = e.do(http.MethodGet, "/api/portal/trainer-profiles?key_last4=QZWR", "", admin)
	expectStatus(t, rec, http.StatusOK)
	if found := decode[[]profileBody](t, rec); len(found) != 1 || found[0].ID != def.ID {
		t.Fatalf("поиск по last4: %+v", found)
	}

	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'profile_key_changed'`); n != 1 {
		t.Fatalf("строк profile_key_changed: %d", n)
	}
	for _, secret := range []string{testOpenRouterKey, testServerToken, "QZWR", "KXVY"} {
		if n := e.count(`SELECT count(*) FROM audit_log WHERE details::text LIKE '%' || $1 || '%'`, secret); n != 0 {
			t.Fatalf("журнал содержит %q", secret)
		}
	}
	// В логе нет ключей целиком; последние символы в нём законно бывают —
	// в адресе поиска ?key_last4=… администратора.
	for _, secret := range []string{testOpenRouterKey, testServerToken} {
		if strings.Contains(e.log.String(), secret) {
			t.Fatalf("лог содержит ключ")
		}
	}
	var enc []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT openrouter_key_enc FROM trainer_profiles WHERE id = $1`, def.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(enc), testOpenRouterKey) {
		t.Fatal("ключ должен храниться зашифрованным")
	}

	// Снимок для тренажёра: без согласия — ни адреса, ни ключа OpenRouter.
	svc := profiles.New(e.pool, audit.New(), testConfig().MasterKey, nil, noRunningSessionsYet{}).Service()
	id := uuid.MustParse(def.ID)
	without, err := svc.SnapshotWithKeys(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	if without.OpenRouterKey != nil || without.Settings.Openrouter != nil || without.Routes.OpenRouter {
		t.Fatalf("без согласия на внешнюю нейросеть в снимке есть OpenRouter: %+v", without)
	}
	with, err := svc.SnapshotWithKeys(context.Background(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	if with.OpenRouterKey == nil || *with.OpenRouterKey != testOpenRouterKey || with.Settings.Openrouter == nil {
		t.Fatal("с согласием тренажёр получает ключ OpenRouter целиком")
	}
	if with.ModelServerToken != nil {
		t.Fatal("токен нашего сервера не отдаётся, когда в профиле нет адреса нашего сервера")
	}

	// Удаление ключа: null.
	rec = e.do(http.MethodPut, "/api/portal/trainer-profiles/"+def.ID+"/keys", `{"openrouter_key":null}`, admin)
	expectStatus(t, rec, http.StatusOK)
	if p := decode[profileBody](t, rec); p.OpenRouterKeyLast4 != nil || p.ModelServerTokenLast4 == nil {
		t.Fatalf("null удаляет только свой ключ: %+v", p)
	}
}

func TestProfileCreateCopyPatchAndSettingsRules(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	def := e.defaultProfile(admin)
	expectStatus(t, e.do(http.MethodPut, "/api/portal/trainer-profiles/"+def.ID+"/keys", `{"openrouter_key":"`+testOpenRouterKey+`"}`, admin), http.StatusOK)

	// Пустые настройки — всё наследуется из профиля по умолчанию.
	rec := e.do(http.MethodPost, "/api/portal/trainer-profiles",
		`{"name":"Короткие сессии","settings":{"limits":{"max_turns":10},"camera":{"enabled":true,"emotion_labels_to_opponent":true}}}`, admin)
	expectStatus(t, rec, http.StatusCreated)
	short := decode[profileBody](t, rec)
	if short.Revision != 1 || short.IsDefault {
		t.Fatalf("новый профиль: %+v", short)
	}
	camera := short.Settings["camera"].(map[string]any)
	if camera["emotion_labels_to_opponent"] != true {
		t.Fatal("I-1: camera.emotion_labels_to_opponent — законное поле и сохраняется")
	}

	// Копия не переносит ключи.
	rec = e.do(http.MethodPost, "/api/portal/trainer-profiles", `{"name":"Копия","copy_from":"`+def.ID+`"}`, admin)
	expectStatus(t, rec, http.StatusCreated)
	if cp := decode[profileBody](t, rec); cp.HasOpenRouterKey || cp.Settings["models"] == nil {
		t.Fatalf("копия переносит настройки, но не ключи: %+v", cp)
	}

	expectStatus(t, e.do(http.MethodPost, "/api/portal/trainer-profiles", `{"name":"Копия"}`, admin), http.StatusUnprocessableEntity)

	// emotion на любой глубине — 400 до обработчика.
	rec = e.do(http.MethodPatch, "/api/portal/trainer-profiles/"+short.ID, `{"settings":{"camera":{"emotion":true}}}`, admin)
	expectStatus(t, rec, http.StatusBadRequest)

	// Адрес со встроенным логином — отказ по схеме.
	rec = e.do(http.MethodPatch, "/api/portal/trainer-profiles/"+short.ID,
		`{"settings":{"openrouter":{"base_url":"https://user:pass@openrouter.ai/api/v1"}}}`, admin)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("адрес с логином должен отклоняться, получили %d", rec.Code)
	}

	// Выключить унаследованный OpenRouter при основном маршруте OpenRouter — 422.
	rec = e.do(http.MethodPatch, "/api/portal/trainer-profiles/"+short.ID, `{"settings":{"openrouter":null}}`, admin)
	expectStatus(t, rec, http.StatusUnprocessableEntity)

	// Правка растит ревизию и пишется в журнал.
	rec = e.do(http.MethodPatch, "/api/portal/trainer-profiles/"+short.ID,
		`{"settings":{"openrouter":null,"model_server":{"base_url":"https://models.example.org/v1"},"primary_route":"own_server"}}`, admin)
	expectStatus(t, rec, http.StatusOK)
	if p := decode[profileBody](t, rec); p.Revision != 2 {
		t.Fatalf("ревизия после правки: %d", p.Revision)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'profile_saved' AND outcome = 'ok'`); n < 3 {
		t.Fatalf("строк profile_saved: %d", n)
	}

	svc := profiles.New(e.pool, audit.New(), testConfig().MasterKey, nil, noRunningSessionsYet{}).Service()
	eff, err := svc.Effective(context.Background(), uuid.MustParse(short.ID))
	if err != nil {
		t.Fatal(err)
	}
	if eff.Routes.OpenRouter || !eff.Routes.OwnServer || eff.Settings.Models == nil || eff.Settings.Models.Opponent == nil {
		t.Fatalf("итоговые настройки: OpenRouter выключен, модели унаследованы: %+v", eff)
	}
	snap, err := svc.SnapshotWithKeys(context.Background(), uuid.MustParse(short.ID), true)
	if err != nil {
		t.Fatal(err)
	}
	if snap.OpenRouterKey != nil {
		t.Fatal("ключ OpenRouter не отдаётся профилю, где маршрут OpenRouter выключен")
	}

	// Архив, затем правка архивного — 409.
	expectStatus(t, e.do(http.MethodPost, "/api/portal/trainer-profiles/"+short.ID+"/archive", "", admin), http.StatusOK)
	expectStatus(t, e.do(http.MethodPatch, "/api/portal/trainer-profiles/"+short.ID, `{"name":"Другое"}`, admin), http.StatusConflict)
	rec = e.do(http.MethodGet, "/api/portal/trainer-profiles", "", admin)
	for _, p := range decode[[]profileBody](t, rec) {
		if p.ID == short.ID {
			t.Fatal("архивный профиль без include_archived не показывается")
		}
	}

	// Проверка профиля — заглушка «не проверялось», четыре строки.
	rec = e.do(http.MethodPost, "/api/portal/trainer-profiles/"+def.ID+"/check", "", admin)
	expectStatus(t, rec, http.StatusOK)
	check := decode[struct {
		Rows []struct {
			Ok      bool   `json:"ok"`
			Message string `json:"message"`
		} `json:"rows"`
	}](t, rec)
	if len(check.Rows) != 4 || check.Rows[0].Message == "" {
		t.Fatalf("проверка профиля: %+v", check)
	}
}

func TestProfileWritesAreAdminOnly(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)
	def := e.defaultProfile(admin)

	expectStatus(t, e.do(http.MethodPost, "/api/portal/trainer-profiles", `{"name":"Чужой"}`, meth), http.StatusForbidden)
	expectStatus(t, e.do(http.MethodPut, "/api/portal/trainer-profiles/"+def.ID+"/keys", `{"openrouter_key":"`+testOpenRouterKey+`"}`, meth), http.StatusForbidden)
	expectStatus(t, e.do(http.MethodGet, "/api/portal/trainer-profiles/"+def.ID, "", meth), http.StatusOK)
	expectStatus(t, e.do(http.MethodGet, "/api/portal/trainer-profiles/"+uuid.NewString(), "", meth), http.StatusNotFound)
}

func TestWrittenConsentNeedsGroupAccessAndIsJournaled(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)
	meth := e.login(methLogin, methPassword)

	closed := e.createGroup(admin, "Закрытая группа")
	person := e.createPerson(admin, closed, "Пётр Закрытый")
	body := `{"document_ref":"№ 17 от 01.09.2026","document_channel":"paper","signed_on":"2026-09-01","valid_until":"2027-09-01"}`

	rec := e.do(http.MethodPost, "/api/portal/people/"+person.SubjectID+"/written-consents", body, meth)
	expectStatus(t, rec, http.StatusForbidden)
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'written_consent_recorded' AND outcome = 'denied'`); n != 1 {
		t.Fatalf("отказ по группе должен быть в журнале: %d", n)
	}

	adminID := e.userID(admin, adminLogin)
	expectStatus(t, e.do(http.MethodPut, "/api/portal/users/"+adminID+"/group-access", `{"group_ids":["`+closed+`"]}`, admin), http.StatusOK)

	rec = e.do(http.MethodPost, "/api/portal/people/"+person.SubjectID+"/written-consents", body, admin)
	expectStatus(t, rec, http.StatusCreated)
	got := decode[map[string]any](t, rec)
	if got["kind"] != "written_assessment" || got["recorded_by_name"] == nil || got["valid_until"] != "2027-09-01" {
		t.Fatalf("ответ: %v", got)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'written_consent_recorded' AND outcome = 'ok' AND subject_id = $1`, person.SubjectID); n != 1 {
		t.Fatalf("строк written_consent_recorded: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE details::text LIKE '%№ 17%'`); n != 0 {
		t.Fatal("реквизиты документа в журнал не пишутся (D-25)")
	}

	bad := `{"document_ref":"№ 18","document_channel":"edo","signed_on":"2026-09-01","valid_until":"2026-08-01"}`
	expectStatus(t, e.do(http.MethodPost, "/api/portal/people/"+person.SubjectID+"/written-consents", bad, admin), http.StatusUnprocessableEntity)
}

func TestTrainerConsentsWaitForTrainerToken(t *testing.T) {
	e := newTestEnv(t)
	// Вход по токену тренажёра появляется в этапе 07 (D-48).
	rec := e.do(http.MethodPost, "/api/trainer/consents",
		`{"answers":[{"kind":"notice_training","answer":"acknowledged","text_id":"`+uuid.NewString()+`","shown_text_sha256":"`+strings.Repeat("a", 64)+`"}]}`, nil)
	expectStatus(t, rec, http.StatusNotImplemented)
}
