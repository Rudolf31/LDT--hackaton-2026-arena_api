package scenariodoc

import "time"

// atLeastAsFavorable — верно ли, что x не хуже y для участника, с учётом
// направления предмета торга: для lower_better меньше — лучше (x<=y), для
// higher_better больше — лучше (x>=y). Общий примитив для правил 1, 1а, 5,
// 12 — все они сравнивают два числовых/датовых значения одного предмета.
func atLeastAsFavorable(direction Direction, x, y float64) bool {
	if direction == DirectionHigherBetter {
		return x >= y
	}
	return x <= y
}

// orderKey — сравнимое число для IssueValue number/date; у варианта
// choice числового порядка нет (ok=false).
func orderKey(v IssueValue) (float64, bool) {
	switch v.Kind {
	case ValueNumber:
		return v.Number, true
	case ValueDate:
		t, err := time.Parse("2006-01-02", v.Date)
		if err != nil {
			return 0, false
		}
		return float64(t.Unix()) / 86400, true
	default:
		return 0, false
	}
}

// limitFor — значение Limit для конкретного варианта зависимого предмета
// (пустой option — значение по умолчанию или предмет без зависимости).
func limitFor(limit Limit, option string) IssueValue {
	if option != "" && limit.ByOption != nil {
		if v, ok := limit.ByOption[option]; ok {
			return v
		}
	}
	return limit.Value
}

// dependentVariants — варианты зависимого предмета (issues[].limits_depend_on),
// по которым нужно проверять пределы отдельно (раздел 5.2: «для каждого
// варианта предмета, от которого зависят пределы»); пустая строка — сам
// предмет не зависит ни от чего, проверяется одно значение по умолчанию.
func dependentVariants(doc *Document, issue Issue) []string {
	if issue.LimitsDependOn == "" {
		return []string{""}
	}
	dep := issueByID(doc, issue.LimitsDependOn)
	if dep == nil || dep.Type != IssueChoice {
		return []string{""}
	}
	variants := make([]string, 0, len(dep.Options))
	for _, o := range dep.Options {
		variants = append(variants, o.ID)
	}
	if len(variants) == 0 {
		return []string{""}
	}
	return variants
}

// dependentVariantAcceptable — допустим ли вариант зависимого предмета
// обеим сторонам (раздел 5.2: «если этот вариант допустим обеим
// сторонам»); пустой вариант (без зависимости) — всегда допустим.
func dependentVariantAcceptable(doc *Document, issue Issue, variant string) bool {
	if variant == "" {
		return true
	}
	dep := issueByID(doc, issue.LimitsDependOn)
	if dep == nil {
		return true
	}
	return stringInList(dep.Participant.Acceptable, variant) && stringInList(dep.Opponent.Acceptable, variant)
}

// formatIssueValue форматирует значение предмета торга для сообщений
// диагностик: число с единицей, дата по-русски или подпись варианта.
func formatIssueValue(dict LabelDictionary, issue Issue, v IssueValue) string {
	switch v.Kind {
	case ValueNumber:
		s := formatNumber(v.Number)
		if issue.Unit != "" {
			return s + " " + issue.Unit
		}
		return s
	case ValueDate:
		return formatRussianDate(v.Date)
	case ValueOption:
		return "«" + dict.issueOptionLabel(issue.ID, v.Option) + "»"
	default:
		return "?"
	}
}

func stringInList(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
