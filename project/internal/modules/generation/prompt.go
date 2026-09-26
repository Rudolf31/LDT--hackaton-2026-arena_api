package generation

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"

	"arena-portal-backend/internal/modules/scenariodoc"
)

//go:embed prompt.md
var promptTemplate string

// BuildSystemPrompt собирает системный промпт генерации один раз при
// сборке портала (cmd/portal/main.go вызывает это перед ai.NewGenerator,
// D-33): сжатое описание формата (prompt.md) + эталонный документ шаблона
// «продажа услуги» из scenariodoc.Templates() + справочник видов условий
// из scenariodoc.ConditionCatalog(). Составление промпта — не дело
// platform/ai (тот пакет не знает про scenariodoc) и не дело самого
// запроса к модели (одна и та же строка на всё время жизни процесса,
// пересобирать её на каждый вызов незачем).
func BuildSystemPrompt() (string, error) {
	example, err := exampleTemplateJSON()
	if err != nil {
		return "", err
	}
	catalog, err := conditionCatalogText()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(promptTemplate, example, catalog), nil
}

// exampleTemplateJSON — документ шаблона «продажа услуги» (sales),
// отформатированный для читаемости в промпте: он и так проходит
// scenariodoc.Validate без блокирующих ошибок (проверено тестом пакета
// scenariodoc), поэтому годится эталоном.
func exampleTemplateJSON() (string, error) {
	for _, t := range scenariodoc.Templates() {
		if t.ID != "sales" {
			continue
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, t.Document, "", "  "); err != nil {
			return "", fmt.Errorf("форматирование шаблона sales для промпта: %w", err)
		}
		return pretty.String(), nil
	}
	return "", fmt.Errorf("шаблон sales не найден среди scenariodoc.Templates()")
}

// conditionKindKeys — ConditionKind не несёт своего JSON-ключа (это int,
// дискриминатор в документе — сам ключ верхнего уровня, см. prompt.md);
// каталог из ConditionCatalog нужен здесь только как список того, что
// валидатор точно поддерживает, — соответствие ключам взято из
// internal/modules/scenariodoc/conditions.go (conditionDiscriminators).
var conditionKindKeys = map[scenariodoc.ConditionKind]string{
	scenariodoc.ConditionMeter:    "meter",
	scenariodoc.ConditionFlag:     "flag",
	scenariodoc.ConditionFact:     "fact",
	scenariodoc.ConditionMove:     "move",
	scenariodoc.ConditionArgument: "argument",
	scenariodoc.ConditionOffer:    "offer",
	scenariodoc.ConditionDeal:     "deal",
}

// conditionCatalogText — виды узлов условия строкой: ключ, подпись, какие
// сравнения допустимы, какого вида значение. Только справочно — сама
// грамматика условий уже описана в prompt.md; каталог напоминает, что
// именно валидатор поддерживает.
func conditionCatalogText() (string, error) {
	var buf bytes.Buffer
	for _, k := range scenariodoc.ConditionCatalog() {
		key := conditionKindKeys[k.Kind]
		fmt.Fprintf(&buf, "- %q (%s)", key, k.Label)
		if len(k.Comparisons) > 0 {
			fmt.Fprintf(&buf, ", сравнения: %v", k.Comparisons)
		}
		fmt.Fprintf(&buf, ", значение: %s\n", k.ValueKind)
	}
	return buf.String(), nil
}
