package scenariodoc

// Validate проверяет документ по схеме и пятнадцати правилам (раздел 17
// arena-scenario-format.md) на всех трёх уровнях сложности за один вызов
// — так того требует правило 11 и так её вызывает портал при сохранении
// черновика и перед публикацией. Diagnostic.Level показывает, на каком
// уровне найдена проблема.
//
// Структурные диагностики схемы считаются один раз на базовом документе:
// ApplyDifficulty меняет только уже проверенные типизированные значения
// (числа, ссылки на существующие предметы и факты) и не может сама по
// себе завести неизвестное поле или сломать тип — так что заново гонять
// разбор JSON на трёх уровнях было бы утроением одинаковых сообщений без
// новой информации. Содержательные правила 1–10, которые зависят от
// значений, меняющихся по уровню (зона сделки, шаг уступки, стиль
// раскрытия факта…), пересчитываются на каждом уровне отдельно.
func Validate(document []byte) []Diagnostic {
	doc, schemaDiags := decodeDocument(document)
	var all []Diagnostic
	for _, d := range schemaDiags {
		d.Level = LevelNormal
		all = append(all, d)
	}

	dict := NewLabelDictionary(doc)
	all = append(all, tagLevel(runRules(doc, dict), LevelNormal)...)

	for _, level := range []Level{LevelEasy, LevelHard} {
		adjusted := applyDifficultyDoc(doc, level)
		adjustedDict := NewLabelDictionary(adjusted)
		all = append(all, tagLevel(runRules(adjusted, adjustedDict), level)...)
	}

	return all
}

// Publishable — нет ни одной блокирующей ошибки среди диагностик
// (FR-SC-08: публикация блокируется при любой ошибке).
func Publishable(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == SeverityError {
			return false
		}
	}
	return true
}

func tagLevel(diags []Diagnostic, level Level) []Diagnostic {
	for i := range diags {
		diags[i].Level = level
	}
	return diags
}

// runRules прогоняет все пятнадцать содержательных правил на одном
// разобранном документе (уже — при необходимости — с применённым уровнем
// сложности).
func runRules(doc *Document, dict LabelDictionary) []Diagnostic {
	var diags []Diagnostic
	diags = append(diags, checkDealZone(doc, dict)...)              // 1, 1а
	diags = append(diags, checkReachability(doc)...)                // 2
	diags = append(diags, checkDeadEnds(doc)...)                    // 3
	diags = append(diags, checkFinalsReachability(doc, dict)...)    // 4
	diags = append(diags, checkOpponentLimit(doc, dict)...)         // 5
	diags = append(diags, checkReferences(doc, dict)...)            // 6
	diags = append(diags, checkConditionsCorrectness(doc, dict)...) // 7 (часть — в conditions.go)
	diags = append(diags, checkBranchingAndFinals(doc)...)          // 8
	diags = append(diags, checkCustomMoves(doc)...)                 // 9
	diags = append(diags, checkFacts(doc, dict)...)                 // 10 + предупреждение
	diags = append(diags, checkOfferPolicyFilled(doc, dict)...)     // 12
	diags = append(diags, checkPressureNoCredit(doc, dict)...)      // 13
	diags = append(diags, checkInterests(doc)...)                   // 14
	diags = append(diags, checkMisc(doc)...)                        // 15
	return diags
}
