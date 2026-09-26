package schema

// RetentionField — одна строка таблицы NFR-PR-03: поле → категория данных →
// основание → срок хранения → кто удаляет (arena-portal-hr.md 13.5).
type RetentionField struct {
	Table     string
	Column    string
	Category  string
	Basis     string
	Retention string
	DeletedBy string
}

// Retention — таблица NFR-PR-03. Заполняется по мере реализации модулей
// (каждый этап добавляет строки для своих таблиц), полностью — до этапа 11
// «Демо и приёмка», не после (arena-portal-hr.md 13.5). TestRetentionMatchesSchema
// в retention_test.go сверяет её со столбцами схемы `arena` и роняет сборку
// на первом же столбце без строки здесь.
var Retention = []RetentionField{}
