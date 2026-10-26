#!/usr/bin/env python3
"""Count tracked production code without blank lines, comments, docs, or tests."""

from __future__ import annotations

import argparse
import dataclasses
import json
import pathlib
import re
import subprocess
import sys
from collections import Counter, defaultdict
from collections.abc import Iterable, Iterator


@dataclasses.dataclass(frozen=True)
class Language:
    name: str
    extensions: tuple[str, ...] = ()
    filenames: tuple[str, ...] = ()
    line_comments: tuple[str, ...] = ()
    block_comments: tuple[tuple[str, str], ...] = ()
    strings: tuple[str, ...] = ('"', "'")
    hash_comment: bool = False


LANGUAGES = (
    Language("Go", (".go",), line_comments=("//",), block_comments=(("/*", "*/"),), strings=('"', "'", "`")),
    Language("Python", (".py",), hash_comment=True, strings=('"', "'")),
    Language("Shell", (".sh", ".bash", ".zsh"), hash_comment=True, strings=('"', "'", "`")),
    Language("TypeScript", (".ts", ".tsx"), line_comments=("//",), block_comments=(("/*", "*/"),), strings=('"', "'", "`")),
    Language("JavaScript", (".js", ".jsx", ".mjs", ".cjs"), line_comments=("//",), block_comments=(("/*", "*/"),), strings=('"', "'", "`")),
    Language("C", (".c", ".h"), line_comments=("//",), block_comments=(("/*", "*/"),), strings=('"', "'")),
    Language("Rust", (".rs",), line_comments=("//",), block_comments=(("/*", "*/"),), strings=('"', "'")),
    Language("Java", (".java",), line_comments=("//",), block_comments=(("/*", "*/"),), strings=('"', "'")),
    Language("Kotlin", (".kt", ".kts"), line_comments=("//",), block_comments=(("/*", "*/"),), strings=('"', "'")),
    Language("Ruby", (".rb",), hash_comment=True, strings=('"', "'", "`")),
    Language("TOML", (".toml",), hash_comment=True, strings=('"', "'")),
    Language("YAML", (".yaml", ".yml"), hash_comment=True, strings=('"', "'")),
    Language("JSON", (".json", ".jsonl"), strings=('"',)),
    Language("Makefile", filenames=("Makefile", "GNUmakefile"), hash_comment=True, strings=('"', "'", "`")),
    Language("Dockerfile", filenames=("Dockerfile",), hash_comment=True, strings=('"', "'", "`")),
)


EXCLUDED_PARTS = {
    ".git",
    ".github",
    ".pytest_cache",
    ".mypy_cache",
    ".ruff_cache",
    "node_modules",
    "vendor",
    "dist",
    "site",
    "docs",
    "doc",
    "test",
    "tests",
    "testdata",
    "fixtures",
    "__pycache__",
}


TEST_PATTERNS = (
    re.compile(r"(^|/)test_[^/]+\.py$"),
    re.compile(r"(^|/)[^/]+_test\.py$"),
    re.compile(r"(^|/)[^/]+_test\.go$"),
    re.compile(r"(^|/)[^/]+\.(test|spec)\.(js|jsx|ts|tsx)$"),
    re.compile(r"(^|/)conftest\.py$"),
)


@dataclasses.dataclass
class FileCount:
    path: str
    language: str
    code: int
    blank: int
    comment: int


@dataclasses.dataclass
class LexerState:
    block_end: str | None = None
    triple_quote: str | None = None


def command_output(root: pathlib.Path, *argv: str) -> str:
    completed = subprocess.run(
        argv,
        cwd=root,
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    return completed.stdout


def tracked_files(root: pathlib.Path, include_untracked: bool) -> list[pathlib.Path]:
    raw = command_output(root, "git", "ls-files", "-z")
    names = [name for name in raw.split("\0") if name]
    if include_untracked:
        raw = command_output(root, "git", "ls-files", "--others", "--exclude-standard", "-z")
        names.extend(name for name in raw.split("\0") if name)
    unique = sorted(set(names))
    return [root / name for name in unique]


def relative(root: pathlib.Path, path: pathlib.Path) -> str:
    return path.relative_to(root).as_posix()


def is_excluded(root: pathlib.Path, path: pathlib.Path) -> bool:
    rel = relative(root, path)
    parts = pathlib.PurePosixPath(rel).parts
    if any(part.lower() in EXCLUDED_PARTS for part in parts[:-1]):
        return True
    return any(pattern.search(rel.lower()) for pattern in TEST_PATTERNS)


def detect_language(path: pathlib.Path) -> Language | None:
    name = path.name
    suffix = path.suffix.lower()
    for language in LANGUAGES:
        if name in language.filenames or suffix in language.extensions:
            return language
    if name.startswith("Dockerfile"):
        return next(item for item in LANGUAGES if item.name == "Dockerfile")
    if name.lower().endswith("makefile"):
        return next(item for item in LANGUAGES if item.name == "Makefile")
    return None


def find_unquoted(line: str, needle: str, strings: tuple[str, ...], start: int = 0) -> int:
    quote: str | None = None
    escaped = False
    index = start
    while index < len(line):
        char = line[index]
        if escaped:
            escaped = False
            index += 1
            continue
        if char == "\\" and quote != "`":
            escaped = True
            index += 1
            continue
        if quote is not None:
            if char == quote:
                quote = None
            index += 1
            continue
        if char in strings:
            quote = char
            index += 1
            continue
        if line.startswith(needle, index):
            return index
        index += 1
    return -1


def strip_c_style(line: str, language: Language, state: LexerState) -> tuple[str, bool]:
    output: list[str] = []
    cursor = 0
    saw_comment = False
    while cursor < len(line):
        if state.block_end is not None:
            end = line.find(state.block_end, cursor)
            saw_comment = True
            if end < 0:
                return "".join(output), saw_comment
            cursor = end + len(state.block_end)
            state.block_end = None
            continue
        candidates: list[tuple[int, str, str | None]] = []
        for marker in language.line_comments:
            index = find_unquoted(line, marker, language.strings, cursor)
            if index >= 0:
                candidates.append((index, marker, None))
        for begin, end in language.block_comments:
            index = find_unquoted(line, begin, language.strings, cursor)
            if index >= 0:
                candidates.append((index, begin, end))
        if not candidates:
            output.append(line[cursor:])
            break
        index, marker, end = min(candidates, key=lambda item: item[0])
        output.append(line[cursor:index])
        saw_comment = True
        if end is None:
            break
        close = line.find(end, index + len(marker))
        if close < 0:
            state.block_end = end
            break
        cursor = close + len(end)
    return "".join(output), saw_comment


def strip_hash(line: str, language: Language) -> tuple[str, bool]:
    index = find_unquoted(line, "#", language.strings)
    if index < 0:
        return line, False
    if index == 0 and line.startswith("#!"):
        return line, False
    return line[:index], True


def strip_python_triples(line: str, state: LexerState) -> tuple[str, bool]:
    output: list[str] = []
    cursor = 0
    comment = False
    while cursor < len(line):
        if state.triple_quote is not None:
            end = line.find(state.triple_quote, cursor)
            comment = True
            if end < 0:
                return "".join(output), comment
            cursor = end + 3
            state.triple_quote = None
            continue
        found = [(line.find(token, cursor), token) for token in ('"""', "'''")]
        found = [(index, token) for index, token in found if index >= 0]
        if not found:
            output.append(line[cursor:])
            break
        index, token = min(found)
        prefix = line[cursor:index]
        output.append(prefix)
        close = line.find(token, index + 3)
        if close < 0:
            state.triple_quote = token
            comment = comment or not prefix.strip()
            break
        cursor = close + 3
    return "".join(output), comment


def count_text(text: str, language: Language) -> tuple[int, int, int]:
    code = blank = comment = 0
    state = LexerState()
    for original in text.splitlines():
        if not original.strip() and state.block_end is None and state.triple_quote is None:
            blank += 1
            continue
        line = original
        saw_comment = False
        if language.name == "Python":
            line, triple = strip_python_triples(line, state)
            saw_comment = saw_comment or triple
        if language.block_comments or language.line_comments:
            line, marked = strip_c_style(line, language, state)
            saw_comment = saw_comment or marked
        if language.hash_comment:
            line, marked = strip_hash(line, language)
            saw_comment = saw_comment or marked
        if line.strip():
            code += 1
        elif saw_comment or state.block_end is not None or state.triple_quote is not None:
            comment += 1
        else:
            blank += 1
    return code, blank, comment


def count_file(root: pathlib.Path, path: pathlib.Path, language: Language) -> FileCount | None:
    try:
        data = path.read_bytes()
    except OSError as error:
        raise RuntimeError(f"cannot read {relative(root, path)}: {error}") from error
    if b"\0" in data:
        return None
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        return None
    code, blank, comment = count_text(text, language)
    return FileCount(relative(root, path), language.name, code, blank, comment)


def collect(root: pathlib.Path, include_untracked: bool) -> list[FileCount]:
    results: list[FileCount] = []
    for path in tracked_files(root, include_untracked):
        if not path.is_file() or is_excluded(root, path):
            continue
        language = detect_language(path)
        if language is None:
            continue
        result = count_file(root, path, language)
        if result is not None:
            results.append(result)
    return results


def summarize(results: Iterable[FileCount]) -> dict[str, dict[str, int]]:
    totals: dict[str, Counter[str]] = defaultdict(Counter)
    for result in results:
        totals[result.language].update(files=1, code=result.code, blank=result.blank, comment=result.comment)
    return {name: dict(values) for name, values in sorted(totals.items())}


def write_human(results: list[FileCount], verbose: bool) -> int:
    totals = summarize(results)
    print(f"{'Language':<16} {'Files':>7} {'Code':>10} {'Blank':>10} {'Comment':>10}")
    print("-" * 57)
    for language, values in totals.items():
        print(f"{language:<16} {values['files']:>7} {values['code']:>10} {values['blank']:>10} {values['comment']:>10}")
    code = sum(item.code for item in results)
    blank = sum(item.blank for item in results)
    comment = sum(item.comment for item in results)
    print("-" * 57)
    print(f"{'TOTAL':<16} {len(results):>7} {code:>10} {blank:>10} {comment:>10}")
    if verbose:
        print("\nLargest production files:")
        for item in sorted(results, key=lambda value: (-value.code, value.path))[:30]:
            print(f"{item.code:>7}  {item.language:<12}  {item.path}")
    return code


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=pathlib.Path, default=pathlib.Path.cwd())
    parser.add_argument("--minimum", type=int, default=0, help="fail when production code is below this value")
    parser.add_argument("--include-untracked", action="store_true", help="include non-ignored untracked files")
    parser.add_argument("--json", action="store_true", help="emit a JSON report")
    parser.add_argument("--verbose", action="store_true", help="show the largest production files")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    root = args.root.resolve()
    try:
        results = collect(root, args.include_untracked)
    except (OSError, subprocess.CalledProcessError, RuntimeError) as error:
        print(f"code-metrics: {error}", file=sys.stderr)
        return 2
    code = sum(item.code for item in results)
    if args.json:
        payload = {"root": str(root), "production_code": code, "files": len(results), "languages": summarize(results)}
        print(json.dumps(payload, indent=2, sort_keys=True))
    else:
        write_human(results, args.verbose)
    if code < args.minimum:
        print(f"code-metrics: production code {code:,} is below required minimum {args.minimum:,}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
