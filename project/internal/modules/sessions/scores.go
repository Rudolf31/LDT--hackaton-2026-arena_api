package sessions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/httpx"
)

// Блок оценок приходит с клиента готовым (D-61): портал проверяет только
// форму по таблице архитектуры 8.2 и раскладывает значения по столбцам
// (8.3). Ни пересчёта, ни сверки с логом ходов здесь нет и быть не должно
// (CLAUDE.md, «Чего не предлагать»).

// criteria — шесть критериев набора harvard_spin_v1 (arena-portal-hr.md 12.2).
var criteria = []string{"diagnosis", "interests", "criteria", "alternative", "discipline", "conduct"}

var ranks = []string{"S", "A", "B", "C", "D", "F"}

// sumKeys — имена полей с общим итогом двух оценок. Такого поля нет и не
// будет (FR-RS-04, I-2): блок с ним не принимается. Совпадение имени
// точное — total_cap («общий потолок процесса») законен.
var sumKeys = []string{"total", "total_score", "overall", "overall_score", "sum", "average", "mean", "combined"}

// scoreRules — с чем сверяется блок.
type scoreRules struct {
	CriteriaSet string
	Finals      map[string]bool
	// FinalID — финал, с которым должен совпасть result.final_id: из тела
	// завершения или сохранённый в сессии.
	FinalID *string
}

// parsedScores — значения блока для столбцов sessions и сам блок без цитат.
type parsedScores struct {
	ScoringProfile string
	Lucky          bool
	FinalID        *string
	Rank           *string
	Number         *int
	MaxNumber      *int
	HasProcess     bool
	ProcessNumber  *int
	Bands          map[string]int
	Cleaned        []byte
}

// parseScores проверяет форму блока оценок. path — указатель на блок
// в теле запроса (/client_scores). Все нарушения собираются разом.
func parseScores(raw json.RawMessage, rules scoreRules, path string) (parsedScores, error) {
	var out parsedScores
	var errs []gen.FieldError
	add := func(p, msg string) { errs = append(errs, gen.FieldError{Path: path + p, Message: msg}) }

	if isNull(raw) {
		return out, httpx.NewError(httpx.KindValidationFailed, "Нет блока оценок — сессию нельзя завершить без них.").
			WithErrors([]gen.FieldError{{Path: path, Message: "Пришлите блок оценок, посчитанный тренажёром."}})
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var block map[string]any
	if err := dec.Decode(&block); err != nil || block == nil {
		return out, httpx.NewError(httpx.KindValidationFailed, "Блок оценок должен быть объектом.").
			WithErrors([]gen.FieldError{{Path: path, Message: "Ожидается JSON-объект."}})
	}

	findSumKeys(block, "", add)

	if cs, _ := block["criteria_set"].(string); cs != rules.CriteriaSet {
		add("/criteria_set", fmt.Sprintf("Набор критериев должен быть %q — как в сценарии этой сессии.", rules.CriteriaSet))
	}
	switch sp, _ := block["scoring_profile"].(string); sp {
	case "normal", "short":
		out.ScoringProfile = sp
	default:
		add("/scoring_profile", "Профиль оценки — normal или short.")
	}
	if v, ok := block["lucky"]; ok && v != nil {
		b, isBool := v.(bool)
		if !isBool {
			add("/lucky", "Отметка «повезло» — true или false.")
		}
		out.Lucky = b
	}

	result, ok := block["result"].(map[string]any)
	if !ok {
		add("/result", "Нет оценки «результат».")
	} else {
		out.FinalID = optString(result["final_id"], "/result/final_id", "Финал — строка или null.", add)
		out.Rank = optString(result["rank"], "/result/rank", "Буква — строка или null.", add)
		if out.Rank != nil && !slices.Contains(ranks, *out.Rank) {
			add("/result/rank", "Буква финала — одна из S, A, B, C, D, F.")
		}
		if (out.Rank == nil) != (out.FinalID == nil) {
			add("/result/rank", "Буква ставится только вместе с финалом, а финал — только с буквой.")
		}
		if out.FinalID != nil && !rules.Finals[*out.FinalID] {
			add("/result/final_id", "Такого финала нет в сценарии этой сессии.")
		}
		if !samePtr(out.FinalID, rules.FinalID) {
			add("/result/final_id", "Финал в блоке оценок не совпадает с финалом сессии.")
		}
		out.Number = optScore(result["number"], "/result/number", add)
		out.MaxNumber = optScore(result["max_number"], "/result/max_number", add)
	}

	switch process := block["process"].(type) {
	case nil:
	case map[string]any:
		out.HasProcess = true
		out.ProcessNumber = optScore(process["number"], "/process/number", add)
		if out.ProcessNumber == nil {
			add("/process/number", "Нет числа «процесса».")
		}
		out.Bands = parseBands(process["criteria"], add)
	default:
		add("/process", "Оценка «процесс» — объект или null.")
	}

	if len(errs) > 0 {
		return parsedScores{}, httpx.NewError(httpx.KindValidationFailed, "Блок оценок заполнен неверно.").WithErrors(errs)
	}
	cleaned, err := json.Marshal(stripQuotes(block))
	if err != nil {
		return parsedScores{}, fmt.Errorf("блок оценок: %w", err)
	}
	out.Cleaned = cleaned
	return out, nil
}

// parseBands — полосы шести критериев: известные id, целые 0–4, без повторов.
func parseBands(v any, add func(string, string)) map[string]int {
	list, ok := v.([]any)
	if !ok {
		add("/process/criteria", "Нет полос критериев.")
		return nil
	}
	bands := make(map[string]int, len(list))
	for i, raw := range list {
		p := fmt.Sprintf("/process/criteria/%d", i)
		c, ok := raw.(map[string]any)
		if !ok {
			add(p, "Критерий — объект с id и band.")
			continue
		}
		id, _ := c["id"].(string)
		if !slices.Contains(criteria, id) {
			add(p+"/id", "Неизвестный критерий: ожидается один из "+strings.Join(criteria, ", ")+".")
			continue
		}
		if _, dup := bands[id]; dup {
			add(p+"/id", "Критерий повторяется.")
			continue
		}
		band, ok := intIn(c["band"], 0, 4)
		if !ok {
			add(p+"/band", "Полоса критерия — целое число от 0 до 4.")
			continue
		}
		bands[id] = band
	}
	return bands
}

func optString(v any, p, msg string, add func(string, string)) *string {
	if v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok || s == "" {
		add(p, msg)
		return nil
	}
	return &s
}

// optScore — целое 0–100 или null: столбцы smallint, экраны считают проценты.
func optScore(v any, p string, add func(string, string)) *int {
	if v == nil {
		return nil
	}
	n, ok := intIn(v, 0, 100)
	if !ok {
		add(p, "Оценка — целое число от 0 до 100.")
		return nil
	}
	return &n
}

func intIn(v any, lo, hi int64) (int, bool) {
	num, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := num.Int64()
	if err != nil || n < lo || n > hi {
		return 0, false
	}
	return int(n), true
}

func samePtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func findSumKeys(v any, p string, add func(string, string)) {
	switch node := v.(type) {
	case map[string]any:
		for k, child := range node {
			if slices.Contains(sumKeys, k) {
				add(p+"/"+k, "Общего итога двух оценок нет и не будет: «результат» и «процесс» не складываются.")
			}
			findSumKeys(child, p+"/"+k, add)
		}
	case []any:
		for i, child := range node {
			findSumKeys(child, fmt.Sprintf("%s/%d", p, i), add)
		}
	}
}

// stripQuotes — блок без цитат (архитектура 8.3): цитаты живут только
// в шифре ответа судьи и текстах реплик, поэтому после уничтожения ключа
// участника блок остаётся читаемым и обезличенным.
func stripQuotes(v any) any {
	switch node := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		for k, child := range node {
			if k == "quote" {
				continue
			}
			out[k] = stripQuotes(child)
		}
		return out
	case []any:
		out := make([]any, len(node))
		for i, child := range node {
			out[i] = stripQuotes(child)
		}
		return out
	}
	return v
}

// bandsJSON — полосы для sessions.criteria_bands; nil — «процесса» нет.
func bandsJSON(bands map[string]int) ([]byte, error) {
	if bands == nil {
		return nil, nil
	}
	return json.Marshal(bands)
}

func isNull(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null"
}
