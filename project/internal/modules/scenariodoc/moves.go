package scenariodoc

// baseMove — один из восемнадцати базовых типов хода (раздел 8.1
// arena-scenario-format.md). Список живёт в движке (здесь), а не в
// сценарии: сценарий может только уточнить тип, добавив «потомка»
// (custom_moves), не менять сам список.
type baseMove struct {
	ID    string
	Label string
}

var baseMoves = []baseMove{
	{"greet", "Приветствие"},
	{"probe_situation", "Вопрос о ситуации"},
	{"probe_problem", "Вопрос о проблеме"},
	{"probe_implication", "Вопрос о последствиях"},
	{"anchor", "Первое своё предложение"},
	{"counter_offer", "Встречное предложение"},
	{"concede", "Уступка"},
	{"trade_offer", "Размен"},
	{"justify_with_criteria", "Ссылка на объективный критерий"},
	{"state_alternative", "Ссылка на свой запасной вариант"},
	{"reframe_to_interest", "Разговор об интересах вместо позиций"},
	{"accept", "Согласие"},
	{"reject", "Отказ"},
	{"walkout_threat", "Угроза уйти"},
	{"deadline_pressure", "Давление сроком"},
	{"insult", "Грубость"},
	{"jailbreak_attempt", "Попытка «сломать» оппонента"},
	{"smalltalk", "Разговор не о деле"},
	{"unclear", "Не понятно"},
}

// pressureMoves — ходы давления (раздел 10 arena-scenario-format.md):
// правило на такой ход не может начислять очки доводов (правило 13).
var pressureMoves = map[string]bool{
	"walkout_threat":    true,
	"deadline_pressure": true,
	"insult":            true,
	"jailbreak_attempt": true,
}

// noOfferMoves — ходы, на которых шаг уступки не открывается вовсе
// (раздел 11.3): давление, попытки «сломать» оппонента и вопросы —
// вопрос не просит уступки.
var noOfferMoves = map[string]bool{
	"insult":            true,
	"jailbreak_attempt": true,
	"walkout_threat":    true,
	"deadline_pressure": true,
	"smalltalk":         true,
	"unclear":           true,
	"greet":             true,
	"probe_situation":   true,
	"probe_problem":     true,
	"probe_implication": true,
}

func isBaseMove(id string) bool {
	for _, m := range baseMoves {
		if m.ID == id {
			return true
		}
	}
	return false
}

func baseMoveLabel(id string) (string, bool) {
	for _, m := range baseMoves {
		if m.ID == id {
			return m.Label, true
		}
	}
	return "", false
}

// resolveBaseMove находит базовый тип для id хода: сам id, если это
// базовый тип, иначе — родителя из custom_moves документа (у потомков
// потомков не бывает — раздел 8.2, "только базовый тип").
func resolveBaseMove(doc *Document, moveID string) (string, bool) {
	if isBaseMove(moveID) {
		return moveID, true
	}
	for _, cm := range doc.CustomMoves {
		if cm.ID == moveID {
			if isBaseMove(cm.Parent) {
				return cm.Parent, true
			}
			return "", false
		}
	}
	return "", false
}

// movesMatching возвращает все id ходов (базовые + собственные), которые
// условие/правило на baseID ловит по правилу «родитель ловит потомка»
// (раздел 8.2).
func movesMatching(doc *Document, baseID string) []string {
	matches := []string{baseID}
	for _, cm := range doc.CustomMoves {
		if cm.Parent == baseID {
			matches = append(matches, cm.ID)
		}
	}
	return matches
}

func customMoveLabel(doc *Document, id string) (string, bool) {
	for _, cm := range doc.CustomMoves {
		if cm.ID == id {
			return cm.Label, true
		}
	}
	return "", false
}

// moveLabel — подпись любого типа хода (базового или собственного) для
// сообщений диагностик.
func moveLabel(doc *Document, id string) string {
	if label, ok := baseMoveLabel(id); ok {
		return label
	}
	if label, ok := customMoveLabel(doc, id); ok {
		return label
	}
	return id
}
