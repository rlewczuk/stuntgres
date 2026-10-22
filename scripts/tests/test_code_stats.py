import importlib.machinery
import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "code_stats"
loader = importlib.machinery.SourceFileLoader("code_stats", str(SCRIPT))
spec = importlib.util.spec_from_loader(loader.name, loader)
code_stats = importlib.util.module_from_spec(spec)
loader.exec_module(code_stats)


class CodeStatsTests(unittest.TestCase):
    def test_go_comments_and_literals(self):
        source = ('// heading\n\npackage p /* inline */\n'
                  '/* block\n comment */ var x = "// not comment"\n'
                  'var s = `/* literal\n// literal\n`\n'
                  '// trailing comment')
        self.assertEqual(code_stats.count_lines(source, "go"), (9, 5))

    def test_c_comments_and_final_line(self):
        source = '/* header */\nconst char *s = "/* string */"; // end\n\nint n;'
        self.assertEqual(code_stats.count_lines(source, "c"), (4, 2))
        self.assertEqual(code_stats.count_lines("", "go"), (0, 0))
        self.assertEqual(code_stats.count_lines("\n \t\n", "go"), (2, 0))

    def test_python_multiline_strings_are_not_comments(self):
        source = '# comment\nx = "# literal"  # comment\ns = """text\n# literal\n"""\n\n'
        self.assertEqual(code_stats.count_lines(source, "python"), (6, 4))

    def test_shell_quoted_hash(self):
        source = '#!/bin/sh\n# comment\necho "# literal" # comment\necho \'# literal\'\n'
        self.assertEqual(code_stats.count_lines(source, "hash"), (4, 2))

    def test_repository_selection_totals_and_readonly(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            def git(*args):
                return subprocess.run(["git", "-C", str(root), *args], check=True,
                                      capture_output=True).stdout
            git("init", "-q")
            files = {
                "main.go": "// header\npackage main\n",
                "main_test.go": "package main\n\n// comment\nfunc test() {}\n",
                "reference/helper.go": "package reference\n",
                "scripts/tests/test_example.py": "# comment\nx = 1\n",
                "odd name\nfile.go": "package main",
                ".cswd/hidden.go": "package hidden\n",
                ".omp/hidden.go": "package hidden\n",
                "README.md": "not source\n",
            }
            for name, content in files.items():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(content)
            (root / "external.go").symlink_to(root / "main.go")
            git("add", ".")
            (root / "untracked.go").write_text("package ignored\n")
            (root / "main.go").write_text("// changed\npackage main\nvar n = 1\n")
            before = {p.relative_to(root): (p.read_bytes(), p.stat().st_mtime_ns)
                      for p in root.rglob("*") if p.is_file()}
            result = code_stats.collect(root)
            self.assertEqual(result["production"], (2, 4, 3))
            self.assertEqual(result["tests"], (3, 7, 4))
            after = {p.relative_to(root): (p.read_bytes(), p.stat().st_mtime_ns)
                     for p in root.rglob("*") if p.is_file()}
            self.assertEqual(before, after)

    def test_missing_tracked_source_fails_without_partial_report(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(["git", "-C", str(root), "init", "-q"], check=True)
            source = root / "missing.go"
            source.write_text("package missing\n")
            subprocess.run(["git", "-C", str(root), "add", "."], check=True)
            source.unlink()
            result = subprocess.run(
                [str(SCRIPT), str(root)], capture_output=True, text=True,
            )
            self.assertEqual(result.returncode, 1)
            self.assertEqual(result.stdout, "")
            self.assertIn("missing.go", result.stderr)


if __name__ == "__main__":
    unittest.main()
