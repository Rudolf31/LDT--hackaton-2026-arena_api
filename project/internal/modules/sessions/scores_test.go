package sessions

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/httpx"
)

func strp(s string) *string { return &s }

var testRules = scoreRules{CriteriaSet: "harvard_spin_v1", Finals: map[string]bool{"deal": true}, FinalID: strp("deal")}

// validBlock — блок оценок в форме arena-scoring 9.
func validBlock() map[string]any {
	return map[string]any{
		"criteria_set": "harvard_spin_v1", "scoring_profile": "normal", "lucky": false, "judge_attempts": 1,
		"result": map[string]any{"final_id": "deal", "rank": "A", "number": 61, "max_number": 95,
			"issues": []any{map[string]any{"issue": "price", "value": 18600, "score": 53.8}}},
		"process": map[string]any{"number": 83, "total_cap": nil,
			"criteria": []any{map[string]any{"id": "diagnosis", "points": 10, "band": 4, "caps": []any{}}},
			"episodes": []any{map[string]any{"indicator": "diag_implication_question", "reply": 2,
				"quote": "А что для вас будет?", "source": "judge", "counted": true}}},
		"marks": []any{},
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseScoresValid(t *testing.T) {
	sc, err := parseScores(mustJSON(t, validBlock()), testRules, "/client_scores")
	if err != nil {
		t.Fatalf("корректный блок отклонён: %v", err)
	}
	if *sc.Rank != "A" || *sc.Number != 61 || *sc.MaxNumber != 95 || *sc.ProcessNumber != 83 || sc.Bands["diagnosis"] != 4 {
		t.Fatalf("значения разложены неверно: %+v", sc)
	}
	if strings.Contains(string(sc.Cleaned), "quote") {
		t.Fatalf("в блоке для sessions.scores остались цитаты: %s", sc.Cleaned)
	}
	if !strings.Contains(string(sc.Cleaned), "total_cap") {
		t.Fatal("total_cap — законное поле, его не вырезаем")
	}
}

// TestParseScoresRejects — по строке на каждую проверку архитектуры 8.2.
func TestParseScoresRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(b map[string]any)
		path   string
	}{
		{"число вне 0–100", func(b map[string]any) { b["result"].(map[string]any)["number"] = 101 }, "/client_scores/result/number"},
		{"дробное число", func(b map[string]any) { b["process"].(map[string]any)["number"] = 83.5 }, "/client_scores/process/number"},
		{"буква вне перечисления", func(b map[string]any) { b["result"].(map[string]any)["rank"] = "Z" }, "/client_scores/result/rank"},
		{"буква без финала", func(b map[string]any) { b["result"].(map[string]any)["final_id"] = nil }, "/client_scores/result/rank"},
		{"неизвестный финал", func(b map[string]any) { b["result"].(map[string]any)["final_id"] = "nope" }, "/client_scores/result/final_id"},
		{"полоса 5", func(b map[string]any) {
			b["process"].(map[string]any)["criteria"] = []any{map[string]any{"id": "diagnosis", "band": 5}}
		}, "/client_scores/process/criteria/0/band"},
		{"неизвестный критерий", func(b map[string]any) {
			b["process"].(map[string]any)["criteria"] = []any{map[string]any{"id": "charisma", "band": 2}}
		}, "/client_scores/process/criteria/0/id"},
		{"другой набор критериев", func(b map[string]any) { b["criteria_set"] = "other_v2" }, "/client_scores/criteria_set"},
		{"профиль оценки", func(b map[string]any) { b["scoring_profile"] = "long" }, "/client_scores/scoring_profile"},
		{"общий итог двух оценок", func(b map[string]any) { b["total"] = 72 }, "/client_scores/total"},
		{"среднее глубоко внутри", func(b map[string]any) { b["result"].(map[string]any)["average"] = 72 }, "/client_scores/result/average"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := validBlock()
			c.mutate(b)
			_, err := parseScores(mustJSON(t, b), testRules, "/client_scores")
			var he *httpx.Error
			if !errors.As(err, &he) || he.Kind != httpx.KindValidationFailed {
				t.Fatalf("ожидалось 422, получено %v", err)
			}
			if !slices.ContainsFunc(he.Errors, func(fe gen.FieldError) bool { return fe.Path == c.path }) {
				t.Fatalf("нет ошибки по пути %s: %+v", c.path, he.Errors)
			}
			if he.Title == "" {
				t.Fatal("пустой title — молчаливый отказ")
			}
		})
	}
}

func TestParseScoresRequiresBlock(t *testing.T) {
	if _, err := parseScores(nil, testRules, "/client_scores"); err == nil {
		t.Fatal("без блока оценок завершение не принимается (D-61)")
	}
}

func TestGaps(t *testing.T) {
	cases := []struct {
		seqs       []int
		contiguous int
		missing    []int
	}{
		{nil, -1, []int{}},
		{[]int{0, 1, 2}, 2, []int{}},
		{[]int{0, 1, 3, 5}, 1, []int{2, 4}},
		{[]int{2}, -1, []int{0, 1}},
	}
	for _, c := range cases {
		got, missing := gaps(c.seqs)
		if got != c.contiguous || !slices.Equal(missing, c.missing) {
			t.Fatalf("gaps(%v) = %d, %v; ожидалось %d, %v", c.seqs, got, missing, c.contiguous, c.missing)
		}
	}
}
