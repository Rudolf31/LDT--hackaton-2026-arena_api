package profiles

import (
	"context"

	"arena-portal-backend/internal/api/gen"
)

// StubChecker — «Проверить профиль» без настоящих запросов к моделям:
// четыре строки «не проверялось» (arena-portal-backend-architecture.md
// 10.3 — единственная оставшаяся заглушка портала). Целевая проверка —
// короткий запрос к трём моделям и к синтезу речи (FR-PF-06, SHOULD).
type StubChecker struct{}

func (StubChecker) Check(_ context.Context, p ProfileWithKeys) ([]CheckLine, error) {
	var route *gen.ModelRoute
	if p.Settings.PrimaryRoute != nil {
		r := gen.ModelRoute(*p.Settings.PrimaryRoute)
		route = &r
	}
	targets := []gen.ProfileCheckResultRowsTarget{
		gen.ProfileCheckResultRowsTargetOpponent,
		gen.ProfileCheckResultRowsTargetParticipantJudge,
		gen.ProfileCheckResultRowsTargetOpponentJudge,
		gen.ProfileCheckResultRowsTargetTts,
	}
	lines := make([]CheckLine, 0, len(targets))
	for _, target := range targets {
		message := "Не проверялось — проверка профиля пока не подключена."
		lineRoute := route
		if target == gen.ProfileCheckResultRowsTargetTts {
			lineRoute = nil
		}
		lines = append(lines, CheckLine{Target: target, Route: lineRoute, OK: false, Message: &message})
	}
	return lines, nil
}
