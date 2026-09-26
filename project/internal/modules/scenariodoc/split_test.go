package scenariodoc_test

import (
	"encoding/json"
	"strings"
	"testing"

	"arena-portal-backend/internal/modules/scenariodoc"
)

// TestSplitKeepsOpponentSideOutOfPublicPart — раздел 16: до старта ни
// одного предела оппонента, скрытого предмета, текста правил и пояснений
// методолога; после старта — всё это, кроме пояснений note.
func TestSplitKeepsOpponentSideOutOfPublicPart(t *testing.T) {
	doc := mustLoadGolden(t)
	issues := issuesArr(doc)
	hidden := map[string]any{}
	for k, v := range issues[0].(map[string]any) {
		hidden[k] = v
	}
	hidden["id"] = "hidden_issue"
	hidden["revealed_by_fact"] = "some_fact"
	doc["issues"] = append(issues, hidden)
	effects := doc["move_effects"].([]any)
	effects[0].(map[string]any)["note"] = "пояснение методолога"

	parts, err := scenariodoc.Split(mustMarshal(t, doc), scenariodoc.LevelHard, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	public := mustUnmarshal(t, parts.Public)
	private := mustUnmarshal(t, parts.Private)

	if public["difficulty"] != "hard" || public["fingerprint"] != strings.Repeat("a", 64) {
		t.Fatalf("уровень и отпечаток должны быть в части до старта: %v %v", public["difficulty"], public["fingerprint"])
	}
	passport := public["passport"].(map[string]any)
	for _, k := range []string{"tags", "origin"} {
		if _, ok := passport[k]; ok {
			t.Fatalf("в паспорте до старта не должно быть %q", k)
		}
	}
	for _, raw := range public["issues"].([]any) {
		issue := raw.(map[string]any)
		if issue["id"] == "hidden_issue" {
			t.Fatal("скрытый предмет попал в часть до старта")
		}
		for _, k := range []string{"opponent", "limits_depend_on", "revealed_by_fact"} {
			if _, ok := issue[k]; ok {
				t.Fatalf("у предмета %v до старта есть %q", issue["id"], k)
			}
		}
	}
	for _, k := range []string{"stages", "facts", "finals", "state", "opponent_brief", "authoring"} {
		if _, ok := public[k]; ok {
			t.Fatalf("в части до старта есть %q", k)
		}
	}
	if _, isLevel := public["difficulty"].(string); !isLevel {
		t.Fatal("до старта уходит уровень назначения, а не настройки уровней")
	}

	if len(private["issues"].([]any)) != len(issues)+1 {
		t.Fatal("после старта нужны все предметы, и скрытые тоже")
	}
	for _, k := range []string{"opponent_brief", "state", "facts", "stages", "finals", "start_stage", "end_stages", "move_effects"} {
		if _, ok := private[k]; !ok {
			t.Fatalf("в части после старта нет %q", k)
		}
	}
	for _, k := range []string{"authoring", "difficulty", "passport"} {
		if _, ok := private[k]; ok {
			t.Fatalf("в части после старта не должно быть %q", k)
		}
	}
	raw, _ := json.Marshal(private["move_effects"])
	if strings.Contains(string(raw), "пояснение методолога") {
		t.Fatal("пояснения note из правил должны убираться")
	}
}
