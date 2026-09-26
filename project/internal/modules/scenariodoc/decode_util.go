package scenariodoc

import (
	"encoding/json"
	"fmt"
	"math"
)

// asObject разбирает raw как JSON-объект, сохраняя порядок ключей не
// важным (map) — порядок ключей документа проверке не нужен нигде, кроме
// отпечатка (там канонизация делает это отдельно, раздел fingerprint.go).
func asObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	return m, true
}

func asArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var a []json.RawMessage
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, false
	}
	return a, true
}

func asString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

func asBool(raw json.RawMessage) (bool, bool) {
	if len(raw) == 0 {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, false
	}
	return b, true
}

func asNumber(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return f, true
}

// asInt разбирает raw как число; integral=false, если число не целое
// (например 7.5) — вызывающий код сам решает, диагностировать это как
// ошибку или округлить и продолжить лучшим приближением.
func asInt(raw json.RawMessage) (value int, integral bool, ok bool) {
	f, isNum := asNumber(raw)
	if !isNum {
		return 0, false, false
	}
	if math.Trunc(f) != f {
		return int(math.Round(f)), false, true
	}
	return int(f), true, true
}

func asStringArray(raw json.RawMessage) ([]string, bool) {
	arr, ok := asArray(raw)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, el := range arr {
		s, ok := asString(el)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// checkKnownKeys возвращает диагностику "неизвестное поле" для каждого
// ключа объекта, которого нет в allowed (FR-SC-15: неизвестное поле —
// ошибка схемы).
func checkKnownKeys(obj map[string]json.RawMessage, allowed []string, path, rule string) []Diagnostic {
	allowedSet := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		allowedSet[k] = true
	}
	var diags []Diagnostic
	for k := range obj {
		if !allowedSet[k] {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Rule:     rule,
				Path:     ptrChild(path, k),
				Message:  fmt.Sprintf("Неизвестное поле «%s» — документ с ним не сохраняется.", k),
			})
		}
	}
	return diags
}

func missingField(path, rule, field string) Diagnostic {
	return Diagnostic{
		Severity: SeverityError,
		Rule:     rule,
		Path:     ptrChild(path, field),
		Message:  fmt.Sprintf("Обязательное поле «%s» не заполнено.", field),
	}
}

func wrongType(path, rule, field, expected string) Diagnostic {
	return Diagnostic{
		Severity: SeverityError,
		Rule:     rule,
		Path:     ptrChild(path, field),
		Message:  fmt.Sprintf("Поле «%s» должно быть %s.", field, expected),
	}
}

// wrongTypeAt — то же, что wrongType, но path уже указывает точно на
// проблемное значение (вызывающий код не хочет добавлять ещё один сегмент
// пути, например при разборе значения предмета торга по его собственному
// пути participant/target).
func wrongTypeAt(path, rule, expected string) Diagnostic {
	return Diagnostic{
		Severity: SeverityError,
		Rule:     rule,
		Path:     path,
		Message:  fmt.Sprintf("Значение должно быть %s.", expected),
	}
}

const ruleSchema = "schema"

func requireString(obj map[string]json.RawMessage, key, path, rule string) (string, []Diagnostic) {
	raw, present := obj[key]
	if !present {
		return "", []Diagnostic{missingField(path, rule, key)}
	}
	s, ok := asString(raw)
	if !ok {
		return "", []Diagnostic{wrongType(path, rule, key, "строкой")}
	}
	return s, nil
}

func optionalString(obj map[string]json.RawMessage, key string) string {
	if raw, present := obj[key]; present {
		if s, ok := asString(raw); ok {
			return s
		}
	}
	return ""
}

func requireBool(obj map[string]json.RawMessage, key, path, rule string) (bool, []Diagnostic) {
	raw, present := obj[key]
	if !present {
		return false, []Diagnostic{missingField(path, rule, key)}
	}
	b, ok := asBool(raw)
	if !ok {
		return false, []Diagnostic{wrongType(path, rule, key, "true/false")}
	}
	return b, nil
}

func optionalBool(obj map[string]json.RawMessage, key string) bool {
	if raw, present := obj[key]; present {
		if b, ok := asBool(raw); ok {
			return b
		}
	}
	return false
}

func requireInt(obj map[string]json.RawMessage, key, path, rule string) (int, []Diagnostic) {
	raw, present := obj[key]
	if !present {
		return 0, []Diagnostic{missingField(path, rule, key)}
	}
	v, integral, ok := asInt(raw)
	if !ok {
		return 0, []Diagnostic{wrongType(path, rule, key, "числом")}
	}
	if !integral {
		return v, []Diagnostic{wrongType(path, rule, key, "целым числом")}
	}
	return v, nil
}

func requireNumber(obj map[string]json.RawMessage, key, path, rule string) (float64, []Diagnostic) {
	raw, present := obj[key]
	if !present {
		return 0, []Diagnostic{missingField(path, rule, key)}
	}
	v, ok := asNumber(raw)
	if !ok {
		return 0, []Diagnostic{wrongType(path, rule, key, "числом")}
	}
	return v, nil
}

func requireStringArray(obj map[string]json.RawMessage, key, path, rule string) ([]string, []Diagnostic) {
	raw, present := obj[key]
	if !present {
		return nil, []Diagnostic{missingField(path, rule, key)}
	}
	arr, ok := asStringArray(raw)
	if !ok {
		return nil, []Diagnostic{wrongType(path, rule, key, "списком строк")}
	}
	return arr, nil
}

func optionalStringArray(obj map[string]json.RawMessage, key string) []string {
	if raw, present := obj[key]; present {
		if arr, ok := asStringArray(raw); ok {
			return arr
		}
	}
	return nil
}
