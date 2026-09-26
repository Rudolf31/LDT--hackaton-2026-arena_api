package scenariodoc_test

import (
	"encoding/json"
	"testing"

	"arena-portal-backend/internal/modules/scenariodoc"
)

func TestApplyDifficultyNormalReturnsEquivalentDocument(t *testing.T) {
	doc := mustLoadGolden(t)
	data := mustMarshal(t, doc)
	out, err := scenariodoc.ApplyDifficulty(data, scenariodoc.LevelNormal)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["passport"].(map[string]any)["title"] != doc["passport"].(map[string]any)["title"] {
		t.Fatal("уровень «обычный» не должен менять содержимое документа")
	}
}

func TestApplyDifficultyShiftsOpponentLimitAndStopAt(t *testing.T) {
	doc := mustLoadGolden(t)
	data := mustMarshal(t, doc)

	out, err := scenariodoc.ApplyDifficulty(data, scenariodoc.LevelHard)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}

	price := issueObj(t, got, "price")
	newLimit := price["opponent"].(map[string]any)["limit"].(map[string]any)["value"].(float64)
	if newLimit != 208 { // 200 (базовый предел) + 8 (limit_shift на сложном уровне)
		t.Fatalf("предел оппонента после сдвига должен быть 208, получено %v", newLimit)
	}
	newStart := price["opponent"].(map[string]any)["start"].(float64)
	if newStart != 245 {
		t.Fatalf("стартовое предложение на сложном уровне должно быть 245, получено %v", newStart)
	}

	op := offerPolicyObj(t, got, "bargaining", "price")
	stopValue := op["stop_at"].(map[string]any)["value"].(float64)
	if stopValue != 213 { // 205 (базовая граница) + 8
		t.Fatalf("граница уступки после сдвига должна быть 213, получено %v", stopValue)
	}
}

// TestApplyDifficultyProducesPublishableDocumentOnEveryLevel проверяет, что
// документ, уже применённый к уровню (тот, что уходит клиенту), сам по
// себе корректен: подаём его в Validate и смотрим только диагностики
// LevelNormal — Validate по контракту всегда прогоняет ещё и свои
// собственные easy/hard поверх того, что ей дали, а документ после
// ApplyDifficulty уже содержит финальные значения одного уровня, и второй
// проход применения настроек поверх них — не то, что здесь проверяется.
func TestApplyDifficultyProducesPublishableDocumentOnEveryLevel(t *testing.T) {
	doc := mustLoadGolden(t)
	data := mustMarshal(t, doc)
	for _, level := range []scenariodoc.Level{scenariodoc.LevelEasy, scenariodoc.LevelHard} {
		out, err := scenariodoc.ApplyDifficulty(data, level)
		if err != nil {
			t.Fatalf("уровень %s: %v", level, err)
		}
		diags := scenariodoc.Validate(out)
		for _, d := range diags {
			if d.Level == scenariodoc.LevelNormal && d.Severity == scenariodoc.SeverityError {
				t.Fatalf("уровень %s: документ после ApplyDifficulty не проходит собственную проверку: [%s] %s", level, d.Rule, d.Message)
			}
		}
	}
}
