package scenariodoc

// stageByID — быстрый доступ к этапу по id; отсутствующий id возвращает
// nil (ссылка проверяется отдельно правилом 6, здесь только сам обход).
func stageByID(doc *Document, id string) *Stage {
	for i := range doc.Stages {
		if doc.Stages[i].ID == id {
			return &doc.Stages[i]
		}
	}
	return nil
}

// engineEdges — рёбра, которые движок добавляет от каждого незавершающего
// этапа сам, без записи в transitions (раздел 11: «сделка заключена» /
// «терпение кончилось» — движок для всех этапов сразу), плюс переходы
// раздела 15 (попытки «сломать» оппонента) — они срабатывают независимо от
// текущего этапа, если этап не завершающий.
func engineEdges(doc *Document) []string {
	var edges []string
	if doc.EndStages.Deal != "" {
		edges = append(edges, doc.EndStages.Deal)
	}
	if doc.EndStages.NoDeal != "" {
		edges = append(edges, doc.EndStages.NoDeal)
	}
	if doc.WrapupStage != "" {
		edges = append(edges, doc.WrapupStage)
	}
	if doc.Jailbreak.EscalationStage != "" {
		edges = append(edges, doc.Jailbreak.EscalationStage)
	}
	if doc.Jailbreak.EndStage != "" {
		edges = append(edges, doc.Jailbreak.EndStage)
	}
	return edges
}

// stageOutgoing — все id этапов, в которые ведёт данный этап: свои
// transitions и timeout.to (правило 8 считает только их, без рёбер
// движка), плюс — если includeEngine — рёбра движка (правило 2).
func stageOutgoing(doc *Document, stage Stage, includeEngine bool) []string {
	var out []string
	for _, t := range stage.Transitions {
		if t.To != "" {
			out = append(out, t.To)
		}
	}
	if stage.Timeout != nil && stage.Timeout.To != "" {
		out = append(out, stage.Timeout.To)
	}
	if includeEngine && !stage.Terminal {
		out = append(out, engineEdges(doc)...)
	}
	return out
}

// reachableStages — все id этапов, достижимых от start_stage (правило 2),
// с учётом рёбер движка.
func reachableStages(doc *Document) map[string]bool {
	visited := map[string]bool{}
	if doc.StartStage == "" {
		return visited
	}
	queue := []string{doc.StartStage}
	visited[doc.StartStage] = true
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		stage := stageByID(doc, id)
		if stage == nil {
			continue
		}
		for _, next := range stageOutgoing(doc, *stage, true) {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return visited
}

// distinctOutgoing — множество различных id этапов, в которые ведёт этап,
// без рёбер движка (для подсчёта развилок, правило 8).
func distinctOutgoing(doc *Document, stage Stage) map[string]bool {
	set := map[string]bool{}
	for _, id := range stageOutgoing(doc, stage, false) {
		set[id] = true
	}
	return set
}

// isBranching — незавершающий этап с не меньше чем двумя разными
// направлениями среди своих переходов и ограничения по репликам (раздел
// 11.4, «развилка»).
func isBranching(doc *Document, stage Stage) bool {
	if stage.Terminal {
		return false
	}
	return len(distinctOutgoing(doc, stage)) >= 2
}
