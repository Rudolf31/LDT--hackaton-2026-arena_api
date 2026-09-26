#!/usr/bin/env python3
"""Готовит производную копию контракта для oapi-codegen.

Не трогает api/arena-api.yaml — источник правды. Два независимых препятствия
для генератора, оба решаются здесь:

1. Одиннадцать $ref на arena-scenario.schema.json — файл, которого нет и не
   будет (CLAUDE.md, «Известные расхождения»). Документ сценария — зона
   ответственности модуля scenariodoc (чистые функции, без HTTP), поэтому
   здесь эти поля просто открываются как json.RawMessage через x-go-type:
   OpenAPI/oapi-codegen для генератора кода это не документ, а непрозрачный
   JSON, который разбирает scenariodoc.
2. OpenAPI 3.1 `type: [X, 'null']` — на случай, если генератор версии 3.1 не
   потянет, понижаем до 3.0 (`type: X` + `nullable: true`).

Результат в decisions.md, Q-01.
"""
import copy
import sys
from pathlib import Path

import yaml

HERE = Path(__file__).resolve().parent
SRC = HERE.parent / "arena-api.yaml"
OUT = HERE / "arena-api.codegen.yaml"

# encoding/json уже безусловно импортируется сгенерированным кодом — свой
# x-go-type-import добавлять не нужно, это и даёт задвоенный импорт.
RAW_MESSAGE_OVERRIDE = {
    "x-go-type": "json.RawMessage",
}


def strip_scenario_doc_refs(node):
    if isinstance(node, dict):
        ref = node.get("$ref")
        if isinstance(ref, str) and ref.startswith("arena-scenario.schema.json"):
            return copy.deepcopy(RAW_MESSAGE_OVERRIDE)
        return {k: strip_scenario_doc_refs(v) for k, v in node.items()}
    if isinstance(node, list):
        return [strip_scenario_doc_refs(v) for v in node]
    return node


def strip_null_enum_members(node):
    """enum: [by_hr, consent_withdrawn, null] — генератор превращает null-член
    перечисления в мусорную константу (`X = "<nil>"`). Нулевость поля и так
    выражена через `type: [string, 'null']`; здесь она для проверки тела —
    та идёт отдельным JSON Schema middleware по нетронутому arena-api.yaml
    (CLAUDE.md, D-04), эта копия только для кодогена типов."""
    if isinstance(node, dict):
        e = node.get("enum")
        if isinstance(e, list) and None in e:
            node = dict(node)
            node["enum"] = [x for x in e if x is not None]
        return {k: strip_null_enum_members(v) for k, v in node.items()}
    if isinstance(node, list):
        return [strip_null_enum_members(v) for v in node]
    return node


def strip_multi_primitive_unions(node):
    """type: [string, number, 'null'] и подобные — генератор такое не понимает
    (ни в 3.1, ни после понижения), а объединения типа "строка или число" — это
    оценочные поля (deal_value/target/limit), которые портал не пересчитывает
    и хранит как пришло. Открываем их как json.RawMessage."""
    if isinstance(node, dict):
        t = node.get("type")
        if isinstance(t, list):
            non_null = [x for x in t if x != "null"]
            if len(non_null) > 1:
                return copy.deepcopy(RAW_MESSAGE_OVERRIDE)
        return {k: strip_multi_primitive_unions(v) for k, v in node.items()}
    if isinstance(node, list):
        return [strip_multi_primitive_unions(v) for v in node]
    return node


def downgrade_nullable(node):
    if isinstance(node, dict):
        node = {k: downgrade_nullable(v) for k, v in node.items()}
        t = node.get("type")
        if isinstance(t, list) and "null" in t:
            rest = [x for x in t if x != "null"]
            if len(rest) == 1:
                node["type"] = rest[0]
                node["nullable"] = True
        return node
    if isinstance(node, list):
        return [downgrade_nullable(v) for v in node]
    return node


def main():
    with open(SRC, encoding="utf-8") as f:
        spec = yaml.safe_load(f)

    spec = strip_scenario_doc_refs(spec)
    spec = strip_null_enum_members(spec)
    spec = strip_multi_primitive_unions(spec)

    if "--downgrade-3.0" in sys.argv:
        spec["openapi"] = "3.0.3"
        spec = downgrade_nullable(spec)

    with open(OUT, "w", encoding="utf-8") as f:
        yaml.safe_dump(spec, f, allow_unicode=True, sort_keys=False)

    print(f"написано {OUT}")


if __name__ == "__main__":
    main()
