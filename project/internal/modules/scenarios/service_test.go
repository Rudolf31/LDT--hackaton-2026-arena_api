package scenarios

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/scenariodoc"
)

func strPtr(s string) *string { return &s }

func TestSetPassportAndAuthoring(t *testing.T) {
	tree, err := decodeTree(scenariodoc.Skeleton())
	if err != nil {
		t.Fatalf("decodeTree: %v", err)
	}
	setPassport(tree, gen.ModeAssessment, gen.OriginManual, strPtr("  Моё название  "))
	if got := passportField(tree, "mode"); got != "assessment" {
		t.Fatalf("mode = %q", got)
	}
	if got := passportField(tree, "origin"); got != "manual" {
		t.Fatalf("origin = %q", got)
	}
	if got := passportField(tree, "title"); got != "Моё название" {
		t.Fatalf("title должен быть обрезан по пробелам: %q", got)
	}

	setAuthoring(tree, map[string]any{"source_template": "procurement"})
	auth, ok := tree["authoring"].(map[string]any)
	if !ok {
		t.Fatal("authoring должен остаться объектом")
	}
	if auth["source_template"] != "procurement" {
		t.Fatalf("source_template не записан: %v", auth)
	}
	// brief/notes из каркаса должны остаться нетронутыми — setAuthoring
	// заменяет только перечисленные ключи.
	if _, ok := auth["brief"]; !ok {
		t.Fatal("setAuthoring не должен терять существующие ключи authoring")
	}
}

func TestSetPassportEmptyTitleKeepsOriginal(t *testing.T) {
	tree, err := decodeTree(scenariodoc.Skeleton())
	if err != nil {
		t.Fatalf("decodeTree: %v", err)
	}
	before := passportField(tree, "title")
	setPassport(tree, gen.ModeTraining, gen.OriginManual, strPtr("   "))
	if got := passportField(tree, "title"); got != before {
		t.Fatalf("пустой title (после обрезки пробелов) не должен менять название: было %q, стало %q", before, got)
	}
}

func TestSetPassportVersion(t *testing.T) {
	tree, err := decodeTree(scenariodoc.Skeleton())
	if err != nil {
		t.Fatalf("decodeTree: %v", err)
	}
	setPassportVersion(tree, 5)
	encoded, err := encodeTree(tree)
	if err != nil {
		t.Fatalf("encodeTree: %v", err)
	}
	var doc struct {
		Passport struct {
			Version int `json:"version"`
		} `json:"passport"`
	}
	if err := json.Unmarshal(encoded, &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if doc.Passport.Version != 5 {
		t.Fatalf("version = %d, ожидалось 5", doc.Passport.Version)
	}
}

func TestScenarioStatus(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		row  scenarioRow
		want string
	}{
		{"archived побеждает published", scenarioRow{ArchivedAt: &now, VersionsCount: 3}, "archived"},
		{"published без архива", scenarioRow{VersionsCount: 1}, "published"},
		{"draft без версий", scenarioRow{}, "draft"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := scenarioStatus(c.row); got != c.want {
				t.Fatalf("scenarioStatus = %q, ожидалось %q", got, c.want)
			}
		})
	}
}

func TestDraftDiffers(t *testing.T) {
	fp := "aa"
	row := scenarioRow{DraftFingerprint: &fp}
	if draftDiffers(scenarioRow{}, nil) {
		t.Fatal("без черновика — не отличается")
	}
	if !draftDiffers(row, nil) {
		t.Fatal("черновик есть, версий нет — отличается")
	}
	if draftDiffers(row, &versionRow{Fingerprint: "aa"}) {
		t.Fatal("тот же отпечаток — не отличается")
	}
	if !draftDiffers(row, &versionRow{Fingerprint: "bb"}) {
		t.Fatal("другой отпечаток — отличается")
	}
}

func TestDraftETagStableAndDistinct(t *testing.T) {
	if got := draftETag(nil); got != `""` {
		t.Fatalf("draftETag(nil) = %q", got)
	}
	t1 := time.Date(2026, 1, 1, 10, 0, 0, 1000, time.UTC)
	t2 := t1.Add(time.Microsecond)
	if draftETag(&t1) == draftETag(&t2) {
		t.Fatal("разные моменты должны давать разные ETag")
	}
	t1copy := t1
	if draftETag(&t1) != draftETag(&t1copy) {
		t.Fatal("одинаковый момент должен давать одинаковый ETag")
	}
}

func TestToCheckResultCountsBlockingOnly(t *testing.T) {
	diags := []scenariodoc.Diagnostic{
		{Severity: scenariodoc.SeverityError, Rule: "schema", Path: "/a", Message: "плохо"},
		{Severity: scenariodoc.SeverityWarning, Rule: "7", Path: "/b", Message: "предупреждение"},
	}
	cr := toCheckResult(diags)
	if cr.Blocking != 1 {
		t.Fatalf("blocking = %d, ожидалось 1", cr.Blocking)
	}
	if len(cr.Messages) != 2 {
		t.Fatalf("messages = %d, ожидалось 2", len(cr.Messages))
	}
	if cr.EngineVersion != scenariodoc.EngineVersion {
		t.Fatalf("engine_version = %q", cr.EngineVersion)
	}
}

func TestHasSchemaErrorOnlyOnErrorSeverity(t *testing.T) {
	if hasSchemaError(nil) {
		t.Fatal("без диагностик схемной ошибки нет")
	}
	if hasSchemaError([]scenariodoc.Diagnostic{{Severity: scenariodoc.SeverityWarning, Rule: "schema"}}) {
		t.Fatal("предупреждение с rule=schema не должно блокировать сохранение")
	}
	if !hasSchemaError([]scenariodoc.Diagnostic{{Severity: scenariodoc.SeverityError, Rule: "schema"}}) {
		t.Fatal("ошибка с rule=schema должна блокировать сохранение")
	}
	if hasSchemaError([]scenariodoc.Diagnostic{{Severity: scenariodoc.SeverityError, Rule: "7"}}) {
		t.Fatal("блокирующая ошибка правила 7 — не схемная")
	}
}

func TestExtractCopiedFrom(t *testing.T) {
	id := uuid.New()
	tree, err := decodeTree(scenariodoc.Skeleton())
	if err != nil {
		t.Fatalf("decodeTree: %v", err)
	}
	setAuthoring(tree, map[string]any{"copied_from": map[string]any{"scenario_id": id.String(), "version": 3}})
	encoded, err := encodeTree(tree)
	if err != nil {
		t.Fatalf("encodeTree: %v", err)
	}
	got, version, ok := extractCopiedFrom(encoded)
	if !ok {
		t.Fatal("copied_from должен разобраться")
	}
	if got != id || version != 3 {
		t.Fatalf("copied_from = %s/%d, ожидалось %s/3", got, version, id)
	}

	if _, _, ok := extractCopiedFrom(scenariodoc.Skeleton()); ok {
		t.Fatal("у каркаса нет copied_from")
	}
}
