package scenariodoc_test

import (
	"testing"

	"arena-portal-backend/internal/modules/scenariodoc"
)

// Эти тесты закрывают регресс трёх багов кодревью в schema.go: require*
// (requireString/requireInt/requireBool) отдаёт диагностику на месте, где
// вызывающий код когда-то отбрасывал её через "_", а поле схемы всё равно
// молча получало нулевое значение. Каждый тест проверяет, что нужная
// строка проверки схемы (или зависящего от неё содержательного правила)
// действительно приходит в списке диагностик — а не просто что документ
// не публикуется.

func TestSchemaStageMissingIDAndWrongTypedLabel(t *testing.T) {
	doc := mustLoadGolden(t)
	stage := stageObj(t, doc, "walkout")
	delete(stage, "id")
	stage["label"] = 42.0
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireErrorMessage(t, diags, "schema", "Обязательное поле «id» не заполнено.")
	requireErrorMessage(t, diags, "schema", "Поле «label» должно быть строкой.")
}

func TestSchemaFactMissingLabelReportsDiagnostic(t *testing.T) {
	doc := mustLoadGolden(t)
	fact := factObj(t, doc, "warehouse_shortage")
	delete(fact, "label")
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireErrorMessage(t, diags, "schema", "Обязательное поле «label» не заполнено.")
}

func TestSchemaFactWrongTypedTextReportsDiagnostic(t *testing.T) {
	doc := mustLoadGolden(t)
	fact := factObj(t, doc, "cash_gap")
	fact["text"] = 123.0
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireErrorMessage(t, diags, "schema", "Поле «text» должно быть строкой.")
}

// TestSchemaOfferPolicyWrongTypedFieldNotTreatedAsSet — regression для
// второй находки: раньше op.Has* выставлялся в true даже когда значение
// не разобралось по типу, и правило 12 ошибочно решало, что поле заполнено
// автором. Строка requires_reciprocity: "yes" (не bool) должна дать не
// только саму ошибку схемы, но и — раз поле фактически не задано — ошибку
// правила 12 о незаполненной встречной уступке.
func TestSchemaOfferPolicyWrongTypedFieldNotTreatedAsSet(t *testing.T) {
	doc := mustLoadGolden(t)
	op := offerPolicyObj(t, doc, "bargaining", "price")
	op["requires_reciprocity"] = "yes"
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireErrorMessage(t, diags, "schema", "Поле «requires_reciprocity» должно быть true/false.")
	requireErrorMessage(t, diags, "12", "В этапе «Торг за цену» для предмета «Цена за метр» не заполнено: встречная уступка.")
}

func TestSchemaAuthoringBriefWrongTypeReportsDiagnostic(t *testing.T) {
	doc := mustLoadGolden(t)
	authoring := doc["authoring"].(map[string]any)
	authoring["brief"] = 123.0
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireErrorMessage(t, diags, "schema", "Поле «brief» должно быть строкой.")
}

func TestSchemaAuthoringBriefMissingReportsDiagnostic(t *testing.T) {
	doc := mustLoadGolden(t)
	authoring := doc["authoring"].(map[string]any)
	delete(authoring, "brief")
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireErrorMessage(t, diags, "schema", "Обязательное поле «brief» не заполнено.")
}

func TestSchemaAuthoringCopiedFromWrongTypeReportsDiagnostic(t *testing.T) {
	doc := mustLoadGolden(t)
	authoring := doc["authoring"].(map[string]any)
	authoring["copied_from"] = "v3"
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireErrorMessage(t, diags, "schema", "Значение должно быть объектом {scenario_id, version}.")
}
