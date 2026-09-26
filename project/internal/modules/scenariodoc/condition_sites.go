package scenariodoc

// visitAllConditions вызывает visit для каждого дерева условий в
// документе вместе с JSON Pointer до его корня — общий обход для правил,
// которым нужно посмотреть на каждое условие документа (7, 10, 13).
func visitAllConditions(doc *Document, visit func(cond Condition, path string)) {
	for fi, fact := range doc.Facts {
		factPath := ptrIndex(ptr("facts"), fi)
		visit(fact.RevealWhen, ptrChild(factPath, "reveal_when"))
	}
	for mi, me := range doc.MoveEffects {
		if me.If != nil {
			mePath := ptrIndex(ptr("move_effects"), mi)
			visit(*me.If, ptrChild(mePath, "if"))
		}
	}
	for si, stage := range doc.Stages {
		stagePath := ptrIndex(ptr("stages"), si)
		for ti, tr := range stage.Transitions {
			trPath := ptrIndex(ptrChild(stagePath, "transitions"), ti)
			visit(tr.When, ptrChild(trPath, "when"))
		}
	}
	for fi, final := range doc.Finals {
		finalPath := ptrIndex(ptr("finals"), fi)
		visit(final.When, ptrChild(finalPath, "when"))
	}
	for factID, cond := range doc.Difficulty.Easy.FactConditions {
		visit(cond, ptrChild(ptr("difficulty", "easy", "fact_conditions"), factID))
	}
	for factID, cond := range doc.Difficulty.Hard.FactConditions {
		visit(cond, ptrChild(ptr("difficulty", "hard", "fact_conditions"), factID))
	}
}
