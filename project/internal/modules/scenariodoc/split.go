package scenariodoc

import (
	"encoding/json"
	"fmt"
)

// Parts — документ, разложенный по таблице раздела 16
// arena-scenario-format.md: что клиент-тренажёр получает до старта
// (Public) и после (Private). Документ клиенту уходит и целиком (D-50);
// части нужны, чтобы заполнить поля контракта settings.scenario и
// private_part тем же содержимым.
type Parts struct {
	Public  json.RawMessage
	Private json.RawMessage
}

// privateKeys — верхние поля документа, которые уходят в часть «после
// старта» как есть. issues, opponent и move_effects собираются отдельно.
var privateKeys = []string{
	"state", "facts", "custom_moves", "evidence_weights", "jailbreak", "process_criteria",
	"start_stage", "stages", "end_stages", "wrapup_stage", "finals",
}

// publicIssueKeys — поля открытого предмета до старта: без стороны
// оппонента, без зависимых пределов и без revealed_by_fact.
var publicIssueKeys = []string{"id", "label", "type", "unit", "direction", "options", "participant"}

// publicPassportKeys — паспорт до старта: без тегов и происхождения
// (они только порталу).
var publicPassportKeys = []string{"id", "version", "title", "mode", "sphere", "negotiation_type"}

// Split раскладывает документ — уже с применёнными настройками уровня
// (ApplyDifficulty) — на две части раздела 16. level и fingerprint
// кладутся в часть «до старта» как есть: уровень назначения и отпечаток
// версии в самом документе не хранятся.
func Split(document []byte, level Level, fingerprint string) (Parts, error) {
	var tree map[string]any
	if err := json.Unmarshal(document, &tree); err != nil {
		return Parts{}, fmt.Errorf("документ должен быть JSON-объектом")
	}

	public := map[string]any{
		"format":      tree["format"],
		"fingerprint": fingerprint,
		"difficulty":  string(level),
		"brief":       tree["brief"],
		"passport":    pick(asMap(tree["passport"]), publicPassportKeys),
		"issues":      []any{},
	}
	opponent := asMap(tree["opponent"])
	public["opponent_card"] = opponent["card"]

	private := map[string]any{"opponent_brief": opponent["brief"]}
	for _, k := range privateKeys {
		if v, ok := tree[k]; ok {
			private[k] = v
		}
	}

	issues, _ := tree["issues"].([]any)
	var open []any
	for _, raw := range issues {
		issue := asMap(raw)
		if _, hidden := issue["revealed_by_fact"]; hidden {
			continue
		}
		open = append(open, pick(issue, publicIssueKeys))
	}
	if open != nil {
		public["issues"] = open
	}
	private["issues"] = issues

	effects, _ := tree["move_effects"].([]any)
	stripped := make([]any, 0, len(effects))
	for _, raw := range effects {
		effect := asMap(raw)
		clean := make(map[string]any, len(effect))
		for k, v := range effect {
			if k != "note" {
				clean[k] = v
			}
		}
		stripped = append(stripped, clean)
	}
	private["move_effects"] = stripped

	pub, err := json.Marshal(public)
	if err != nil {
		return Parts{}, fmt.Errorf("часть документа до старта: %w", err)
	}
	priv, err := json.Marshal(private)
	if err != nil {
		return Parts{}, fmt.Errorf("часть документа после старта: %w", err)
	}
	return Parts{Public: pub, Private: priv}, nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func pick(m map[string]any, keys []string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}
