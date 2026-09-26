package httpx

import (
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"arena-portal-backend/internal/api/gen"
)

// fieldErrorsFromValidation переводит дерево ошибок jsonschema/v6 в плоский
// список полей: путь (JSON Pointer) и фраза по-русски (CLAUDE.md, «Язык»;
// arena-portal-hr.md 8.1: «errors[] — поля с path и message обычными
// словами»).
func fieldErrorsFromValidation(err error) []gen.FieldError {
	var verr *jsonschema.ValidationError
	if !errors.As(err, &verr) {
		return []gen.FieldError{{Path: "", Message: "тело не прошло проверку по схеме"}}
	}

	var out []gen.FieldError
	collectLeaves(verr, &out)
	if len(out) == 0 {
		out = append(out, gen.FieldError{Path: "", Message: "тело не прошло проверку по схеме"})
	}
	return out
}

func collectLeaves(verr *jsonschema.ValidationError, out *[]gen.FieldError) {
	if len(verr.Causes) == 0 {
		*out = append(*out, gen.FieldError{
			Path:    instanceLocationPointer(verr.InstanceLocation),
			Message: translateKind(verr.ErrorKind),
		})
		return
	}
	for _, cause := range verr.Causes {
		collectLeaves(cause, out)
	}
}

func instanceLocationPointer(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	escaped := make([]string, len(tokens))
	for i, t := range tokens {
		escaped[i] = escapeJSONPointerToken(t)
	}
	return "/" + strings.Join(escaped, "/")
}

// translateKind переводит конкретный вид ошибки схемы в русскую фразу.
// Каталог покрывает ключевые слова, реально встречающиеся в контракте;
// незнакомый вид получает общую, но по-прежнему русскую фразу — молчаливых
// отказов и служебных английских текстов на экране быть не должно
// (CLAUDE.md: правило 8 и раздел «Язык»).
func translateKind(k jsonschema.ErrorKind) string {
	switch e := k.(type) {
	case *kind.Required:
		if len(e.Missing) == 1 {
			return fmt.Sprintf("обязательное поле %s отсутствует", quoteList(e.Missing))
		}
		return fmt.Sprintf("обязательные поля отсутствуют: %s", quoteList(e.Missing))
	case *kind.AdditionalProperties:
		if len(e.Properties) == 1 {
			return fmt.Sprintf("поле %s не ожидается здесь", quoteList(e.Properties))
		}
		return fmt.Sprintf("поля не ожидаются здесь: %s", quoteList(e.Properties))
	case *kind.Type:
		return fmt.Sprintf("неверный тип значения: получено %s, а нужно %s", e.Got, strings.Join(e.Want, " или "))
	case *kind.Enum:
		return "значение не входит в список допустимых"
	case *kind.Const:
		return "значение не совпадает с единственно допустимым"
	case *kind.MinLength:
		return fmt.Sprintf("текст короче минимальной длины (%d символов, нужно не меньше %d)", e.Got, e.Want)
	case *kind.MaxLength:
		return fmt.Sprintf("текст длиннее максимальной длины (%d символов, нужно не больше %d)", e.Got, e.Want)
	case *kind.Pattern:
		return "значение не подходит по формату"
	case *kind.Minimum:
		got, _ := e.Got.Float64()
		want, _ := e.Want.Float64()
		return fmt.Sprintf("значение %v меньше допустимого минимума %v", got, want)
	case *kind.Maximum:
		got, _ := e.Got.Float64()
		want, _ := e.Want.Float64()
		return fmt.Sprintf("значение %v больше допустимого максимума %v", got, want)
	case *kind.ExclusiveMinimum:
		got, _ := e.Got.Float64()
		want, _ := e.Want.Float64()
		return fmt.Sprintf("значение %v должно быть строго больше %v", got, want)
	case *kind.ExclusiveMaximum:
		got, _ := e.Got.Float64()
		want, _ := e.Want.Float64()
		return fmt.Sprintf("значение %v должно быть строго меньше %v", got, want)
	case *kind.MultipleOf:
		want, _ := e.Want.Float64()
		return fmt.Sprintf("значение должно быть кратно %v", want)
	case *kind.MinItems:
		return fmt.Sprintf("элементов меньше минимума: %d, нужно не меньше %d", e.Got, e.Want)
	case *kind.MaxItems:
		return fmt.Sprintf("элементов больше максимума: %d, нужно не больше %d", e.Got, e.Want)
	case *kind.MinProperties:
		return fmt.Sprintf("полей меньше минимума: %d, нужно не меньше %d", e.Got, e.Want)
	case *kind.MaxProperties:
		return fmt.Sprintf("полей больше максимума: %d, нужно не больше %d", e.Got, e.Want)
	case *kind.UniqueItems:
		return "элементы списка повторяются, а должны быть уникальны"
	case *kind.Format:
		return fmt.Sprintf("значение не подходит под формат %s", e.Want)
	case *kind.InvalidJsonValue:
		return "значение не является допустимым JSON"
	default:
		path := k.KeywordPath()
		if len(path) > 0 {
			return fmt.Sprintf("значение не проходит проверку по правилу «%s»", strings.Join(path, "/"))
		}
		return "значение не проходит проверку по схеме"
	}
}

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = "«" + s + "»"
	}
	return strings.Join(quoted, ", ")
}
