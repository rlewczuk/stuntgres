import contextlib
import hashlib
import importlib.machinery
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest


def load(name):
    script = Path(__file__).resolve().parents[1] / name
    loader = importlib.machinery.SourceFileLoader(name, str(script))
    module = importlib.util.module_from_spec(importlib.util.spec_from_loader(loader.name, loader))
    loader.exec_module(module)
    return module


report = load("perf_suite_report")
steplog = load("perf_steplog")


def run(main, argv):
    out = io.StringIO()
    with contextlib.redirect_stdout(out):
        status = main(argv)
    return status, out.getvalue()


class SuiteReportTests(unittest.TestCase):
    def test_packages_tail_and_tests(self):
        with tempfile.TemporaryDirectory() as d:
            root = "/w/repo"
            events = [
                {"Action": "pass", "Package": "m/reference/slow", "Test": "TestA", "Elapsed": 9.0},
                {"Action": "pass", "Package": "m/reference/slow", "Test": "TestA/x", "Elapsed": 6.0},
                {"Action": "pass", "Package": "m/reference/slow", "Test": "TestA/y", "Elapsed": 3.0},
                {"Action": "pass", "Package": "m/reference/slow", "Elapsed": 9.5},
                {"Action": "pass", "Package": "m", "Elapsed": 1.0},
                {"Action": "output", "Package": "m", "Output": "ok"},
            ]
            Path(d, "run.json").write_text("".join(json.dumps(e) + "\n" for e in events))
            Path(d, "times").write_text(f"9.6 14.0 1.0 204800 {root}/reference/slow\n1.1 1.0 0.1 1024 {root}\n")
            Path(d, "times.starts").write_text(f"100.0 {root}\n100.5 {root}/reference/slow\n")
            status, out = run(report.main, [d, "reference/slow"])
            self.assertEqual(status, 0)
            profile = hashlib.md5(f"{root}/reference/slow".encode()).hexdigest()[:12] + ".cpu"
            line = next(l for l in out.splitlines() if "m/reference/slow" in l and "|" in l)
            self.assertIn("15.0", line)  # user+sys
            self.assertIn("200", line)  # max RSS MB
            self.assertIn("10.1", line)  # end: start 0.5 + wall 9.6
            self.assertIn(profile, line)
            self.assertIn("total CPU of test binaries: 16 s over 2 binaries", out)
            self.assertIn("2 leaves", out)
            self.assertIn("6.000 TestA/x", out)


class StepLogTests(unittest.TestCase):
    def write_log(self, d, lines):
        path = Path(d, "log.txt")
        path.write_text("".join("\t".join(l) + "\n" for l in lines))
        return str(path)

    def test_subset_and_unmatched(self):
        with tempfile.TemporaryDirectory() as d:
            log = self.write_log(d, [
                ("R", "TestSmall/f/memory", "step1"),
                ("Q", "TestSmall/f/memory", '"SELECT 1"'),
                ("R", "TestSmall/g/memory", "only-small"),
                ("R", "TestBig/f/memory", "step1"),
                ("R", "TestBig/f/memory", "extra"),
                ("Q", "TestBig/f/memory", '"SELECT 1"'),
                ("R", "TestBig/g/memory", "other"),
            ])
            status, out = run(steplog.main, ["subset", log, "TestSmall", "TestBig"])
            self.assertEqual(status, 1)
            self.assertRegex(out, r"SUBSET\s+f\s")
            self.assertRegex(out, r"UNMATCHED\s+g\s.*unmatched 1")
            self.assertIn("only-small", out)

    def test_order_and_ignore(self):
        with tempfile.TemporaryDirectory() as d:
            log = self.write_log(d, [
                ("R", "TestSmall/f/memory", "a"),
                ("R", "TestSmall/f/memory", "b"),
                ("Q", "TestSmall/f/memory", '"ROLLBACK"'),
                ("R", "TestBig/f/memory", "b"),
                ("R", "TestBig/f/memory", "a"),
            ])
            status, out = run(steplog.main, ["subset", log, "TestSmall", "TestBig", "--ignore", "^\"ROLLBACK\""])
            self.assertEqual(status, 1)  # b after a is missing from an in-order scan
            self.assertIn("unmatched 1", out)

    def test_same(self):
        with tempfile.TemporaryDirectory() as d:
            a = self.write_log(d, [("R", "TestX/f", "1"), ("R", "TestY/f", "2")])
            b = str(Path(d, "b.txt"))
            Path(b).write_text("R\tTestX/f\t1\nR\tTestY/f\t3\n")
            self.assertEqual(run(steplog.main, ["same", a, b, "TestX"])[0], 0)
            self.assertEqual(run(steplog.main, ["same", a, b, "TestY"])[0], 1)

    def test_install_remove_round_trip(self):
        with tempfile.TemporaryDirectory() as d:
            fixture = Path(d, "reference/fixture")
            fixture.mkdir(parents=True)
            original = ("package fixture\n\n" + steplog.RUNSTEP + "\treturn Observed{}\n}\n\n"
                        + steplog.BEGIN + "\treturn nil, nil\n}\n")
            Path(fixture, "fixture.go").write_text(original)
            run(steplog.main, ["install", "--root", d])
            installed = Path(fixture, "fixture.go").read_text()
            self.assertIn(steplog.RUNSTEP + steplog.RUNSTEP_HOOK, installed)
            self.assertIn(steplog.BEGIN + steplog.BEGIN_HOOK, installed)
            self.assertTrue(Path(d, steplog.LOGGER).exists())
            run(steplog.main, ["remove", "--root", d])
            self.assertEqual(Path(fixture, "fixture.go").read_text(), original)
            self.assertFalse(Path(d, steplog.LOGGER).exists())


if __name__ == "__main__":
    unittest.main()
