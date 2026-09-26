package scenariodoc_test

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"arena-portal-backend/internal/modules/scenariodoc"
)

// mustLoadGolden разбирает testdata/golden.json заново на каждый вызов:
// карты в Go — ссылочный тип, и тест, портящий документ мутацией на месте,
// не должен задевать соседние тесты.
func mustLoadGolden(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func mustUnmarshal(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func issuesArr(doc map[string]any) []any { return doc["issues"].([]any) }

func issueObj(t *testing.T, doc map[string]any, id string) map[string]any {
	t.Helper()
	for _, raw := range issuesArr(doc) {
		m := raw.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("issue %q not found in golden", id)
	return nil
}

func stageObj(t *testing.T, doc map[string]any, id string) map[string]any {
	t.Helper()
	for _, raw := range doc["stages"].([]any) {
		m := raw.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("stage %q not found in golden", id)
	return nil
}

func factObj(t *testing.T, doc map[string]any, id string) map[string]any {
	t.Helper()
	for _, raw := range doc["facts"].([]any) {
		m := raw.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("fact %q not found in golden", id)
	return nil
}

func moveEffectObj(t *testing.T, doc map[string]any, id string) map[string]any {
	t.Helper()
	for _, raw := range doc["move_effects"].([]any) {
		m := raw.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("move_effect %q not found in golden", id)
	return nil
}

func finalObj(t *testing.T, doc map[string]any, id string) map[string]any {
	t.Helper()
	for _, raw := range doc["finals"].([]any) {
		m := raw.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("final %q not found in golden", id)
	return nil
}

func offerPolicyObj(t *testing.T, doc map[string]any, stageID, issueID string) map[string]any {
	t.Helper()
	stage := stageObj(t, doc, stageID)
	for _, raw := range stage["offer_policy"].([]any) {
		m := raw.(map[string]any)
		if m["issue"] == issueID {
			return m
		}
	}
	t.Fatalf("offer_policy for issue %q not found on stage %q", issueID, stageID)
	return nil
}

// requireDiagnostic проверяет, что среди диагностик есть блокирующая
// ошибка правила rule.
func requireDiagnostic(t *testing.T, diags []scenariodoc.Diagnostic, rule string) scenariodoc.Diagnostic {
	t.Helper()
	for _, d := range diags {
		if d.Rule == rule && d.Severity == scenariodoc.SeverityError {
			return d
		}
	}
	t.Fatalf("ожидалась блокирующая ошибка правила %q, диагностики: %+v", rule, diags)
	return scenariodoc.Diagnostic{}
}

// requireErrorMessage проверяет, что среди диагностик есть блокирующая
// ошибка правила rule с текстом ровно message — в отличие от
// requireDiagnostic, различает соседние диагностики одного правила по
// содержанию, а не только по наличию хоть какой-то ошибки.
func requireErrorMessage(t *testing.T, diags []scenariodoc.Diagnostic, rule, message string) scenariodoc.Diagnostic {
	t.Helper()
	for _, d := range diags {
		if d.Rule == rule && d.Severity == scenariodoc.SeverityError && d.Message == message {
			return d
		}
	}
	t.Fatalf("ожидалась ошибка правила %q с текстом %q, диагностики: %+v", rule, message, diags)
	return scenariodoc.Diagnostic{}
}

func requireNoErrors(t *testing.T, diags []scenariodoc.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == scenariodoc.SeverityError {
			t.Fatalf("неожиданная блокирующая ошибка: [%s] %s: %s", d.Level, d.Rule, d.Message)
		}
	}
}

// bareIdentifierPattern ловит «голые» служебные идентификаторы формата
// сценария (латиница нижнего регистра, цифры, «_», без кириллицы рядом) —
// FR-SC-07 требует подпись вместо них в сообщениях содержательных правил.
// Сообщения уровня "schema" называют сами поля формата (id, op, value) —
// это не идентификаторы содержимого сценария, поэтому они не проверяются.
var bareIdentifierPattern = regexp.MustCompile(`[a-z][a-z0-9_]{2,}`)

func assertNoBareIdentifiers(t *testing.T, diags []scenariodoc.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Rule == "schema" {
			continue
		}
		if m := bareIdentifierPattern.FindString(d.Message); m != "" {
			t.Errorf("сообщение правила %s содержит голый идентификатор %q без подписи: %s", d.Rule, m, d.Message)
		}
	}
}
