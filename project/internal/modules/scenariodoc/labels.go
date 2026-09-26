package scenariodoc

// LabelDictionary — подписи из конкретного документа: имя оппонента,
// подписи отметок/фактов/этапов/предметов торга (и их вариантов) и типов
// ходов (базовых и собственных). Строится один раз на документ
// (NewLabelDictionary) и переиспользуется для перевода всех деревьев
// условий документа в фразы (DescribeCondition) и для подстановки подписей
// вместо голых идентификаторов в сообщениях правил (FR-SC-07) — дешевле,
// чем искать подпись по документу на каждое сообщение.
type LabelDictionary struct {
	OpponentName   string
	Flags          map[string]string
	Facts          map[string]string
	Stages         map[string]string
	Issues         map[string]string
	IssueOptions   map[string]map[string]string
	IssueTypes     map[string]IssueType
	IssueDirection map[string]Direction
	Moves          map[string]string
	Interests      map[string]string
}

func NewLabelDictionary(doc *Document) LabelDictionary {
	dict := LabelDictionary{
		OpponentName:   doc.Opponent.Card.Name,
		Flags:          map[string]string{},
		Facts:          map[string]string{},
		Stages:         map[string]string{},
		Issues:         map[string]string{},
		IssueOptions:   map[string]map[string]string{},
		IssueTypes:     map[string]IssueType{},
		IssueDirection: map[string]Direction{},
		Moves:          map[string]string{},
		Interests:      map[string]string{},
	}
	for _, f := range doc.State.Flags {
		dict.Flags[f.ID] = f.Label
	}
	for _, f := range doc.Facts {
		dict.Facts[f.ID] = f.Label
	}
	for _, s := range doc.Stages {
		dict.Stages[s.ID] = s.Label
	}
	for _, is := range doc.Issues {
		dict.Issues[is.ID] = is.Label
		dict.IssueTypes[is.ID] = is.Type
		dict.IssueDirection[is.ID] = is.Direction
		opts := map[string]string{}
		for _, o := range is.Options {
			opts[o.ID] = o.Label
		}
		dict.IssueOptions[is.ID] = opts
	}
	for _, m := range baseMoves {
		dict.Moves[m.ID] = m.Label
	}
	for _, cm := range doc.CustomMoves {
		dict.Moves[cm.ID] = cm.Label
	}
	for _, in := range doc.Opponent.Brief.Interests {
		dict.Interests[in.ID] = in.Label
	}
	return dict
}

func labelOr(m map[string]string, id string) string {
	if l, ok := m[id]; ok && l != "" {
		return l
	}
	return id
}

func (d LabelDictionary) flagLabel(id string) string     { return labelOr(d.Flags, id) }
func (d LabelDictionary) factLabel(id string) string     { return labelOr(d.Facts, id) }
func (d LabelDictionary) stageLabel(id string) string    { return labelOr(d.Stages, id) }
func (d LabelDictionary) issueLabel(id string) string    { return labelOr(d.Issues, id) }
func (d LabelDictionary) moveLabelOf(id string) string   { return labelOr(d.Moves, id) }
func (d LabelDictionary) interestLabel(id string) string { return labelOr(d.Interests, id) }

func (d LabelDictionary) issueOptionLabel(issueID, optionID string) string {
	if opts, ok := d.IssueOptions[issueID]; ok {
		if l, ok := opts[optionID]; ok && l != "" {
			return l
		}
	}
	return optionID
}
