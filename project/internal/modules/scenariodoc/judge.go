package scenariodoc

import (
	"encoding/json"
	"fmt"
)

// FinalText — название финала и эпилог: буква на экране стоит только
// рядом с ними (FR-RS-04).
type FinalText struct {
	Title    string
	Epilogue string
}

// JudgeContext — то немногое из документа, что нужно сессии после
// разговора: подписи финалов для столбцов оценок и данные для повторного
// запроса к судье участника (arena-scoring 8.1). Пределов оппонента,
// этапов и условий переходов здесь нет — их пакет для судьи не несёт
// (arena-api.yaml, JudgeRetryPack).
type JudgeContext struct {
	Brief           json.RawMessage // brief — бриф участника как в документе
	Interests       json.RawMessage // opponent.brief.interests; [] — нет
	FactTexts       map[string]string
	Finals          map[string]FinalText
	ProcessCriteria string // "" — в документе не задан
}

// JudgeContextOf выбирает JudgeContext из документа. Документ уже прошёл
// проверку при публикации, поэтому отсутствующие части не ошибка, а
// пустые значения.
func JudgeContextOf(document []byte) (JudgeContext, error) {
	var tree map[string]any
	if err := json.Unmarshal(document, &tree); err != nil {
		return JudgeContext{}, fmt.Errorf("документ должен быть JSON-объектом")
	}
	out := JudgeContext{FactTexts: map[string]string{}, Finals: map[string]FinalText{}}

	brief, err := json.Marshal(tree["brief"])
	if err != nil {
		return JudgeContext{}, fmt.Errorf("бриф участника: %w", err)
	}
	out.Brief = brief

	interests := asMap(asMap(tree["opponent"])["brief"])["interests"]
	if interests == nil {
		interests = []any{}
	}
	if out.Interests, err = json.Marshal(interests); err != nil {
		return JudgeContext{}, fmt.Errorf("интересы оппонента: %w", err)
	}

	facts, _ := tree["facts"].([]any)
	for _, raw := range facts {
		f := asMap(raw)
		id, _ := f["id"].(string)
		text, _ := f["text"].(string)
		if id != "" {
			out.FactTexts[id] = text
		}
	}
	finals, _ := tree["finals"].([]any)
	for _, raw := range finals {
		f := asMap(raw)
		id, _ := f["id"].(string)
		title, _ := f["title"].(string)
		epilogue, _ := f["epilogue"].(string)
		if id != "" {
			out.Finals[id] = FinalText{Title: title, Epilogue: epilogue}
		}
	}
	out.ProcessCriteria, _ = tree["process_criteria"].(string)
	return out, nil
}
