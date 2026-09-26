#!/usr/bin/env python3
"""Инвариант I-2 / FR-RS-04: «результат» и «процесс» нигде не складываются
и не усредняются в одно число, и нет поля с общим итогом.

Эвристика, не доказательство: ловит запрещённые имена общих полей и явную
арифметику между result_number/ResultNumber и process_number/ProcessNumber.
Живые имена полей — из ScoreColumns в arena-api.yaml (result_number,
result_max, result_rank, process_status, process_number).
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

FORBIDDEN_FIELD_NAMES = re.compile(
    r"\b(total_score|overall_score|combined_score|sum_score|final_score|"
    r"TotalScore|OverallScore|CombinedScore|SumScore|FinalScore)\b"
)

SCORE_ARITHMETIC = re.compile(
    r"(result_number|ResultNumber)\b.{0,40}[-+*/].{0,40}\b(process_number|ProcessNumber)"
    r"|(process_number|ProcessNumber)\b.{0,40}[-+*/].{0,40}\b(result_number|ResultNumber)"
)

SCAN_SUFFIXES = {".go", ".yaml", ".yml", ".sql"}
SKIP_DIRS = {".git", "vendor", "node_modules"}


def files():
    for path in ROOT.rglob("*"):
        if path.is_dir() or path.suffix not in SCAN_SUFFIXES:
            continue
        if any(part in SKIP_DIRS for part in path.parts):
            continue
        yield path


def main():
    violations = []
    for path in files():
        try:
            text = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        rel = path.relative_to(ROOT)
        for lineno, line in enumerate(text.splitlines(), start=1):
            if FORBIDDEN_FIELD_NAMES.search(line):
                violations.append(f"{rel}:{lineno}: запрещённое имя общего поля оценки — {line.strip()}")
            if SCORE_ARITHMETIC.search(line):
                violations.append(f"{rel}:{lineno}: похоже на арифметику между result_number и process_number — {line.strip()}")

    if violations:
        print("I-2 / FR-RS-04 нарушен: две оценки нигде не складываются и не усредняются.\n")
        for v in violations:
            print(" -", v)
        sys.exit(1)

    print(f"I-2: проверено {sum(1 for _ in files())} файлов, запрещённых сложений/усреднений не найдено.")


if __name__ == "__main__":
    main()
