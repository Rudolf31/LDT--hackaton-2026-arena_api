package httpx

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// scenarioDocRefMarker — подстрока $ref на файл, которого нет и не будет
// (CLAUDE.md, «Известные расхождения»): документ сценария проверяет
// scenariodoc, а не эта схема. При загрузке такой $ref подменяется схемой
// «любой объект» (D-14).
const scenarioDocRefMarker = "arena-scenario.schema.json"

const resourceURL = "mem://arena-api.yaml"

// operation — одна операция контракта: как её найти по методу и пути и как
// проверить тело.
type operation struct {
	operationID  string
	method       string
	pathPattern  string
	matcher      *regexp.Regexp
	schema       *jsonschema.Schema // nil, если requestBody нет (например, GET)
	bodyRequired bool
	maxBodyBytes int64
}

// BodySchemas — схемы тел всех операций контракта, скомпилированные из
// нетронутого api/arena-api.yaml (D-04, D-14). Схема выбирается по методу
// и пути запроса.
type BodySchemas struct {
	ops             []*operation
	byID            map[string]*operation
	defaultMaxBytes int64
}

// LoadBodySchemas разбирает arena-api.yaml (передаётся байтами — в бинарь он
// встроен через api.Spec, D-12), компилирует схему requestBody каждой
// операции библиотекой santhosh-tekuri/jsonschema/v6 и строит по ним
// сопоставление метод+путь → схема. maxBytesByOperationID — исключения из
// общего лимита (например, 5 МБ на запись репетиции); операции без
// исключения получают defaultMaxBytes (1 МБ, arena-portal-hr.md 8.1).
func LoadBodySchemas(yamlContent []byte, defaultMaxBytes int64, maxBytesByOperationID map[string]int64) (*BodySchemas, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(yamlContent, &doc); err != nil {
		return nil, fmt.Errorf("разбор контракта: %w", err)
	}
	doc = sanitizeScenarioDocRefs(doc).(map[string]any)
	patchOutdatedSchemas(doc)

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(resourceURL, doc); err != nil {
		return nil, fmt.Errorf("регистрация контракта как ресурса схемы: %w", err)
	}

	paths, _ := doc["paths"].(map[string]any)
	schemas := &BodySchemas{byID: make(map[string]*operation), defaultMaxBytes: defaultMaxBytes}

	for path, rawItem := range paths {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			rawOp, ok := item[method]
			if !ok {
				continue
			}
			opNode, ok := rawOp.(map[string]any)
			if !ok {
				continue
			}
			operationID, _ := opNode["operationId"].(string)
			if operationID == "" {
				continue
			}

			op := &operation{
				operationID:  operationID,
				method:       strings.ToUpper(method),
				pathPattern:  path,
				matcher:      pathMatcher(path),
				maxBodyBytes: defaultMaxBytes,
			}
			if override, ok := maxBytesByOperationID[operationID]; ok {
				op.maxBodyBytes = override
			}

			if reqBody, ok := opNode["requestBody"].(map[string]any); ok {
				if required, ok := reqBody["required"].(bool); ok {
					op.bodyRequired = required
				}
				if _, hasJSON := jsonBodySchemaNode(reqBody); hasJSON {
					pointer := schemaPointer(path, method)
					sch, err := compiler.Compile(resourceURL + "#" + pointer)
					if err != nil {
						return nil, fmt.Errorf("компиляция схемы %s %s: %w", op.method, path, err)
					}
					op.schema = sch
				}
			}

			schemas.ops = append(schemas.ops, op)
			schemas.byID[operationID] = op
		}
	}

	// paths — обычный map[string]any: порядок обхода Go рандомизирует на
	// каждый запуск процесса. Без этой сортировки match() был бы угадайкой
	// между литеральным сегментом и {param} той же глубины и зависел бы от
	// перезапуска процесса, а не только от самого контракта.
	sort.SliceStable(schemas.ops, func(i, j int) bool {
		if si, sj := specificity(schemas.ops[i].pathPattern), specificity(schemas.ops[j].pathPattern); si != sj {
			return si > sj
		}
		return schemas.ops[i].pathPattern < schemas.ops[j].pathPattern
	})

	return schemas, nil
}

// specificity — число литеральных (не {param}) сегментов пути: чем их
// больше, тем точнее путь и тем раньше его проверяет match().
func specificity(pattern string) int {
	n := 0
	for _, seg := range strings.Split(pattern, "/") {
		if !(strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")) {
			n++
		}
	}
	return n
}

func jsonBodySchemaNode(reqBody map[string]any) (map[string]any, bool) {
	content, ok := reqBody["content"].(map[string]any)
	if !ok {
		return nil, false
	}
	appJSON, ok := content["application/json"].(map[string]any)
	if !ok {
		return nil, false
	}
	schema, ok := appJSON["schema"].(map[string]any)
	return schema, ok
}

// schemaPointer строит JSON Pointer к requestBody.content.application/json.schema
// операции внутри всего документа (arena-api.yaml зарегистрирован целиком —
// так $ref на #/components/schemas/... резолвится сам, без ручной подмены).
func schemaPointer(path, method string) string {
	segments := []string{"paths", path, method, "requestBody", "content", "application/json", "schema"}
	escaped := make([]string, len(segments))
	for i, s := range segments {
		escaped[i] = escapeJSONPointerToken(s)
	}
	return "/" + strings.Join(escaped, "/")
}

func escapeJSONPointerToken(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

// pathMatcher превращает шаблон пути OpenAPI ("/api/portal/scenarios/{scenarioId}")
// в регулярное выражение для сопоставления с r.URL.Path.
func pathMatcher(pattern string) *regexp.Regexp {
	segments := strings.Split(pattern, "/")
	for i, seg := range segments {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			segments[i] = "[^/]+"
		} else {
			segments[i] = regexp.QuoteMeta(seg)
		}
	}
	return regexp.MustCompile("^" + strings.Join(segments, "/") + "$")
}

// match ищет операцию по методу и пути запроса.
func (s *BodySchemas) match(method, path string) (*operation, bool) {
	method = strings.ToUpper(method)
	for _, op := range s.ops {
		if op.method == method && op.matcher.MatchString(path) {
			return op, true
		}
	}
	return nil, false
}

// ByOperationID отдаёт операцию по её operationId — используется ручными
// адресами авторства (D-05), у которых нет записи в контракте и которые
// сами решают лимит тела.
func (s *BodySchemas) ByOperationID(id string) (*operation, bool) {
	op, ok := s.byID[id]
	return op, ok
}

// patchOutdatedSchemas закрывает расхождение контракта, которое CLAUDE.md
// («Известные расхождения») просит не чинить в самом arena-api.yaml (D-26):
// CreateFromBrief там всё ещё требует отменённую анкету questionnaire
// (FR-SC-03 снята). Схема подменяется на закрытую — без анкеты, вместо неё
// название сценария (`title`), из которого его позже назовёт генерация
// (`scenarios.generation`, arena-portal-hr.md 11.1). Правится только это
// дерево в памяти — сам arena-api.yaml не трогаем. Контракта без
// components.schemas.CreateFromBrief (например, урезанной спецификации в
// тестах платформы) не касается — там патчить нечего.
//
// Форма тела здесь и разбор ветки origin=brief в
// scenarios/transport.go:createFieldsFromRequest — два независимых описания
// одной и той же формы (JSON Schema для проверки тут, анонимная Go-структура
// там для разбора: сгенерированный CreateFromBrief по этой же причине не
// годится). Ничто их не связывает, кроме этого комментария — при добавлении
// или переименовании поля в CreateFromBrief проверьте оба места (найдено в
// код-ревью 04).
func patchOutdatedSchemas(doc map[string]any) {
	components, ok := doc["components"].(map[string]any)
	if !ok {
		return
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		return
	}
	if _, ok := schemas["CreateFromBrief"]; !ok {
		return
	}
	schemas["CreateFromBrief"] = map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"origin", "mode", "title"},
		"properties": map[string]any{
			"origin": map[string]any{"const": "brief"},
			"mode":   map[string]any{"$ref": "#/components/schemas/Mode"},
			"title":  map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
		},
	}
}

// sanitizeScenarioDocRefs рекурсивно заменяет $ref на arena-scenario.schema.json
// схемой «любой объект» (D-14). Возвращает новое дерево, исходное не меняет.
func sanitizeScenarioDocRefs(node any) any {
	switch v := node.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok && strings.Contains(ref, scenarioDocRefMarker) {
			return map[string]any{"type": "object"}
		}
		out := make(map[string]any, len(v))
		for k, child := range v {
			out[k] = sanitizeScenarioDocRefs(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = sanitizeScenarioDocRefs(child)
		}
		return out
	default:
		return node
	}
}
