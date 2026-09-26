package scenariodoc

// ConditionCatalog — справочник видов узлов условия для конструктора
// (раздел 12.2 arena-scenario-format.md). Первая версия конструктора не
// показывает узел stage, шкалу credit в meter и уточнение interest в
// argument (в примерах спецификации они не нужны) — но Validate и
// DescribeCondition их полностью поддерживают, каталог лишь уже, чем сам
// формат.
func ConditionCatalog() []ConditionKindSpec {
	return []ConditionKindSpec{
		{
			Kind:        ConditionMeter,
			Label:       "Шкала",
			Comparisons: numberComparisons,
			ValueKind:   ConditionValueInteger,
		},
		{
			Kind:        ConditionFlag,
			Label:       "Отметка",
			Comparisons: nil,
			ValueKind:   ConditionValueBool,
		},
		{
			Kind:        ConditionFact,
			Label:       "Скрытый факт",
			Comparisons: nil,
			ValueKind:   ConditionValueBool,
		},
		{
			Kind:        ConditionMove,
			Label:       "Тип хода",
			Comparisons: nil,
			ValueKind:   ConditionValueCount,
		},
		{
			Kind:        ConditionArgument,
			Label:       "Довод",
			Comparisons: nil,
			ValueKind:   ConditionValueEvidence,
		},
		{
			Kind:        ConditionOffer,
			Label:       "Предмет торга",
			Comparisons: append(append([]Comparison{}, numberComparisons...), choiceComparisons...),
			ValueKind:   ConditionValueIssue,
		},
		{
			Kind:        ConditionDeal,
			Label:       "Сделка",
			Comparisons: nil,
			ValueKind:   ConditionValueNone,
		},
	}
}
