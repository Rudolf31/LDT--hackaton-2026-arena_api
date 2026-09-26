package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	arenaapi "arena-portal-backend/api"
)

func TestSpecificity(t *testing.T) {
	cases := []struct {
		pattern string
		want    int
	}{
		{"/api/portal/things/special", 5},
		{"/api/portal/things/{id}", 4},
		{"/{a}/{b}", 1},
	}
	for _, c := range cases {
		if got := specificity(c.pattern); got != c.want {
			t.Fatalf("specificity(%q) = %d, ожидалось %d", c.pattern, got, c.want)
		}
	}
}

// testAmbiguousSpecYAML — два пути одной глубины и метода, один с литеральным
// сегментом, другой с {id}: match() должен всегда предпочитать литеральный,
// независимо от порядка обхода map при разборе paths (I-2, находка ревью).
const testAmbiguousSpecYAML = `
openapi: 3.1.0
info: {title: test, version: "0"}
paths:
  /things/special:
    get:
      operationId: thingsSpecial
  /things/{id}:
    get:
      operationId: thingsByID
`

func TestMatchPrefersLiteralSegmentOverParam(t *testing.T) {
	schemas, err := LoadBodySchemas([]byte(testAmbiguousSpecYAML), 1024, nil)
	if err != nil {
		t.Fatalf("LoadBodySchemas: %v", err)
	}

	op, matched := schemas.match("GET", "/things/special")
	if !matched {
		t.Fatal("ожидалось совпадение для /things/special")
	}
	if op.operationID != "thingsSpecial" {
		t.Fatalf("ожидалась операция thingsSpecial, получена %s (литеральный сегмент должен быть приоритетнее {id})", op.operationID)
	}

	op, matched = schemas.match("GET", "/things/anything-else")
	if !matched {
		t.Fatal("ожидалось совпадение для /things/anything-else через {id}")
	}
	if op.operationID != "thingsByID" {
		t.Fatalf("ожидалась операция thingsByID, получена %s", op.operationID)
	}
}

// TestPatchedCreateFromBriefSchema — D-26: CreateFromBrief подменяется на
// закрытую схему без отменённой анкеты questionnaire. Тело с одним только
// title проходит, а лишнее поле по-прежнему отклоняется (additionalProperties:
// false) — подмена не открывает схему совсем, только убирает анкету.
func TestPatchedCreateFromBriefSchema(t *testing.T) {
	schemas, err := LoadBodySchemas(arenaapi.Spec, 1<<20, nil)
	if err != nil {
		t.Fatalf("LoadBodySchemas: %v", err)
	}
	mw := Body(schemas)

	post := func(body string) *httptest.ResponseRecorder {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/portal/scenarios", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		mw(next).ServeHTTP(rec, req)
		return rec
	}

	rec := post(`{"origin":"brief","mode":"training","title":"Проверка авторства"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("тело без анкеты, только с title, должно пройти проверку: %d, %s", rec.Code, rec.Body.String())
	}

	rec = post(`{"origin":"brief","mode":"training","title":"Проверка","questionnaire":{}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("лишнее поле должно отклоняться (additionalProperties: false): %d, %s", rec.Code, rec.Body.String())
	}

	rec = post(`{"origin":"brief","mode":"training"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("без title (обязательного в подменённой схеме) должно отклоняться: %d, %s", rec.Code, rec.Body.String())
	}
}

// TestPatchedJudgeAnswerRequestSchema — D-62: к ответу судьи добавлен
// обязательный блок оценок client_scores; остальные лишние поля
// по-прежнему отклоняются.
func TestPatchedJudgeAnswerRequestSchema(t *testing.T) {
	schemas, err := LoadBodySchemas(arenaapi.Spec, 1<<20, nil)
	if err != nil {
		t.Fatalf("LoadBodySchemas: %v", err)
	}
	mw := Body(schemas)

	post := func(body string) *httptest.ResponseRecorder {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost,
			"/api/trainer/sessions/7d3f2c9e-0000-4000-8000-000000000001/judge-answer", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		mw(next).ServeHTTP(rec, req)
		return rec
	}
	answer := `{"criteria_set":"harvard_spin_v1","episodes":[],"summary":[]}`

	if rec := post(`{"judge_answer":` + answer + `,"judge_attempts":2,"client_scores":{"criteria_set":"harvard_spin_v1"}}`); rec.Code != http.StatusOK {
		t.Fatalf("тело с блоком оценок должно пройти: %d, %s", rec.Code, rec.Body.String())
	}
	if rec := post(`{"judge_answer":` + answer + `,"judge_attempts":2}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("без client_scores должно отклоняться: %d", rec.Code)
	}
	if rec := post(`{"judge_answer":` + answer + `,"judge_attempts":2,"client_scores":{},"extra":1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("лишнее поле должно отклоняться: %d", rec.Code)
	}
}
