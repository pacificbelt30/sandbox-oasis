import importlib.util
import pathlib
import sys
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).with_name("code-metrics.py")
SPEC = importlib.util.spec_from_file_location("code_metrics", SCRIPT)
code_metrics = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules[SPEC.name] = code_metrics
SPEC.loader.exec_module(code_metrics)


class CodeMetricsTests(unittest.TestCase):
    def language(self, name):
        return next(item for item in code_metrics.LANGUAGES if item.name == name)

    def test_go_comments_blank_and_strings(self):
        source = '''package sample

// comment
var URL = "https://example.test/a//b"
/* block
comment */
func Value() int { return 1 } // trailing
'''
        code, blank, comment = code_metrics.count_text(source, self.language("Go"))
        self.assertEqual(code, 3)
        self.assertEqual(blank, 1)
        self.assertEqual(comment, 3)

    def test_python_docstring_hash_and_shebang(self):
        source = '''#!/usr/bin/env python3
"""module documentation
continued
"""

value = "# is data"
# comment
print(value)  # trailing
'''
        code, blank, comment = code_metrics.count_text(source, self.language("Python"))
        self.assertEqual(code, 3)
        self.assertEqual(blank, 1)
        self.assertEqual(comment, 4)

    def test_test_and_document_paths_are_excluded(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            for relative in ("doc/guide.md", "pkg/value_test.go", "tests/test_app.py", "pkg/value.go"):
                path = root / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("value\n")
            self.assertTrue(code_metrics.is_excluded(root, root / "doc/guide.md"))
            self.assertTrue(code_metrics.is_excluded(root, root / "pkg/value_test.go"))
            self.assertTrue(code_metrics.is_excluded(root, root / "tests/test_app.py"))
            self.assertFalse(code_metrics.is_excluded(root, root / "pkg/value.go"))

    def test_language_detection(self):
        self.assertEqual(code_metrics.detect_language(pathlib.Path("main.go")).name, "Go")
        self.assertEqual(code_metrics.detect_language(pathlib.Path("Dockerfile.worker")).name, "Dockerfile")
        self.assertEqual(code_metrics.detect_language(pathlib.Path("GNUmakefile")).name, "Makefile")
        self.assertIsNone(code_metrics.detect_language(pathlib.Path("README.md")))


if __name__ == "__main__":
    unittest.main()
