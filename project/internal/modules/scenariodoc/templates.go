package scenariodoc

import (
	"embed"
	"sort"
)

//go:embed templates/*.json
var templateFS embed.FS

var templateMeta = []struct {
	ID          string
	Title       string
	Description string
	Sphere      Sphere
	File        string
}{
	{"procurement", "Закупка партии товара к сроку",
		"Торг за цену и срок поставки с давлением дедлайна монтажа — опора на предложения конкурентов.",
		SphereProcurement, "templates/procurement.json"},
	{"internal_promotion", "Повышение внутри компании",
		"Разговор с руководителем о повышении и окладе — опора на внутренние правила компании, а не на рынок.",
		SphereInternalPromotion, "templates/internal_promotion.json"},
	{"internal_client", "Конфликт с внутренним заказчиком",
		"Пересмотр сроков и объёма работ с внутренним заказчиком, недовольным предыдущим результатом.",
		SphereInternalClient, "templates/internal_client.json"},
	{"sales", "Продажа услуги клиенту",
		"Продажа услуги внешнему клиенту, который сравнивает предложение с конкурентами и торгуется по цене.",
		SphereSales, "templates/sales.json"},
	{"contractor_deadline", "Пересогласование срока с подрядчиком",
		"Подрядчик просит сдвинуть срок — торг за компенсацию и новые обязательства.",
		SphereContractorDeadline, "templates/contractor_deadline.json"},
	{"resource_split", "Дележ ресурса между подразделениями",
		"Раздел ограниченного общего ресурса между двумя подразделениями с разными аргументами.",
		SphereResourceSplit, "templates/resource_split.json"},
	{"demo", "Короткая демонстрация тренажёра",
		"Укороченный сценарий для быстрого показа тренажёра — не входит в список шести шаблонов сфер.",
		SphereOther, "templates/demo.json"},
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
		out = append(out, Template{ID: m.ID, Title: m.Title, Description: m.Description, Sphere: m.Sphere, Document: data})
	}
	return out
}

// MainWeights — три наибольших веса опор довода документа, по убыванию
// (UC-M-02, карточка шаблона). Опора «без опоры» (`none`) в список не
// входит: правило 13 требует, чтобы её вес был строго 0. При равенстве
// весов порядок — как в evidenceKeys (раздел 9 arena-scenario-format.md),
// чтобы список был воспроизводим между вызовами. Документ не обязан быть
// вполне валидным — decodeDocument переносим значения по мере разбора,
// diagnostics здесь не нужны: карточка шаблона показывает то, что есть.
func MainWeights(document []byte) []EvidenceWeight {
	doc, _ := decodeDocument(document)
	out := make([]EvidenceWeight, 0, len(evidenceKeys)-1)
	for _, key := range evidenceKeys {
		if key == "none" {
			continue
		}
		w, _ := doc.EvidenceWeights.byKey(key)
		out = append(out, EvidenceWeight{Evidence: key, Label: evidenceLabel(key), Weight: w})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Weight > out[j].Weight })
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}
