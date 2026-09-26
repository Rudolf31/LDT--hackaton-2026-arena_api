package scenariodoc

import "embed"

//go:embed templates/*.json
var templateFS embed.FS

var templateMeta = []struct {
	ID     string
	Title  string
	Sphere Sphere
	File   string
}{
	{"procurement", "Закупка партии товара к сроку", SphereProcurement, "templates/procurement.json"},
	{"internal_promotion", "Повышение внутри компании", SphereInternalPromotion, "templates/internal_promotion.json"},
	{"internal_client", "Конфликт с внутренним заказчиком", SphereInternalClient, "templates/internal_client.json"},
	{"sales", "Продажа услуги клиенту", SphereSales, "templates/sales.json"},
	{"contractor_deadline", "Пересогласование срока с подрядчиком", SphereContractorDeadline, "templates/contractor_deadline.json"},
	{"resource_split", "Дележ ресурса между подразделениями", SphereResourceSplit, "templates/resource_split.json"},
	{"demo", "Короткая демонстрация тренажёра", SphereOther, "templates/demo.json"},
}

// Templates — шесть шаблонов сфер и демо-шаблон (раздел 18
// arena-scenario-format.md, FR-SC-12). Каждый документ проходит Validate
// без блокирующих ошибок на всех трёх уровнях сложности — это проверяет
// тест пакета, а не сам вызов Templates.
func Templates() []Template {
	out := make([]Template, 0, len(templateMeta))
	for _, m := range templateMeta {
		data, err := templateFS.ReadFile(m.File)
		if err != nil {
			panic("scenariodoc: шаблон " + m.File + " не найден: " + err.Error())
		}
		out = append(out, Template{ID: m.ID, Title: m.Title, Sphere: m.Sphere, Document: data})
	}
	return out
}
