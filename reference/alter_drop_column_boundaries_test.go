package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterDropColumnShapePositions(t *testing.T) {
	for _, column := range []string{"a", "b", "c"} {
		t.Run(column, func(t *testing.T) {
			p := newReferenceScenario(t)
			p.step(t, referenceExec("CREATE TABLE t(a INT,b TEXT,c BIGINT)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(1,'two',3)", 1))
			p.step(t, referenceExec("CREATE TABLE clone(LIKE t)", 0))
			p.step(t, referenceExec("ALTER TABLE t DROP "+column, 0))
			names := map[string][]string{"a": {"b", "c"}, "b": {"a", "c"}, "c": {"a", "b"}}[column]
			types := map[string][]string{"a": {"TEXT", "INT8"}, "b": {"INT4", "INT8"}, "c": {"INT4", "TEXT"}}[column]
			rows := map[string][][]any{"a": {{"two", int64(3)}}, "b": {{int64(1), int64(3)}}, "c": {{int64(1), "two"}}}[column]
			p.step(t, referenceSelect("SELECT * FROM t", names, types, rows))
			for _, backend := range p.backends() {
				referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, false, "SELECT * FROM clone", []string{"a", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, []bool{true, true, true}, 0)
			}
		})
	}
}

func TestReferenceAlterDropColumnParserBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT NOT NULL,b TEXT,oid INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,'keep',7)", 1))
	before := referenceDDLNativeDump(t, p)
	for _, tc := range []struct {
		q, code, message, position string
	}{
		{"ALTER TABLE t DROP COLUMN )", "42601", `syntax error at or near ")"`, "27"},
		{`ALTER TABLE t DROP COLUMN "quo""te\\tail"`, "42703", `column "quo"te\\tail" of relation "t" does not exist`, ""},
	} {
		for _, prepared := range []bool{false, true} {
			for _, backend := range p.backends() {
				stage := "execute"
				if prepared && tc.code != "42703" {
					stage = "prepare"
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceDDLPositionError(tc.q, tc.code, tc.message, tc.position, prepared, stage))
				if referenceDDLNativeDump(t, p) != before {
					t.Fatal("rejected drop action changed native catalog", tc.q)
				}
			}
		}
	}
	for _, system := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
		p.step(t, roleReferenceError("ALTER TABLE t DROP IF EXISTS "+system, "0A000", `cannot drop system column "`+system+`"`, "", false, "execute"))
		if referenceDDLNativeDump(t, p) != before {
			t.Fatal("system drop changed catalog", system)
		}
	}
	p.step(t, referenceExec("ALTER TABLE t DROP oid", 0))
	failure := referenceImplicitError("INSERT INTO t VALUES(NULL,'bad')", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"})
	failure.errorStage = "execute"
	p.step(t, failure)
	p.step(t, referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
}

func TestReferenceAlterDropColumnDependentIndexAndView(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,'keep')", 1))
	p.step(t, referenceExec("CREATE UNIQUE INDEX idx ON t(b)", 0))
	p.step(t, referenceExec("CREATE VIEW v AS SELECT b FROM t", 0))
	before := referenceDDLNativeDump(t, p)
	failure := referenceImplicitError("ALTER TABLE t DROP b RESTRICT", referenceDiagnostic{Code: "2BP01", Message: "cannot drop column b of table t because other objects depend on it", Detail: "view v depends on column b of table t", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	failure.errorStage = "execute"
	p.step(t, failure)
	if referenceDDLNativeDump(t, p) != before {
		t.Fatal("failed restrictive drop changed native catalog")
	}
	p.step(t, referenceSelect("SELECT * FROM v", []string{"b"}, []string{"TEXT"}, [][]any{{"keep"}}))
	p.step(t, referenceExec("ALTER TABLE t DROP b CASCADE", 0))
	p.step(t, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
	p.step(t, referenceSelect("SELECT relname FROM pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil))
	failure = referenceDDLPositionError("SELECT * FROM v", "42P01", `relation "v" does not exist`, "15", false, "execute")
	failure.selectRows = true
	p.step(t, failure)
}

func TestReferenceAlterDropColumnZeroColumnControls(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE z(v TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO z VALUES('one'),('two')", 2))
	p.step(t, referenceExec("ALTER TABLE ONLY(z) DROP v", 0))
	empty := referenceSelect("SELECT * FROM z", []string{}, []string{}, [][]any{{}, {}})
	for _, prepared := range []bool{false, true} {
		s := empty
		s.prepared = prepared
		p.step(t, s)
	}
	canonical := referenceDDLNativeDump(t, p)
	if !strings.Contains(canonical, `CREATE TABLE "public"."z" ();`) || strings.Count(canonical, `INSERT INTO "public"."z" DEFAULT VALUES;`) != 2 || strings.Contains(canonical, "VALUES ()") {
		t.Fatal(canonical)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(canonical)); err != nil {
		t.Fatal(err)
	}
	if referenceDDLNativeDump(t, p) != canonical {
		t.Fatal("zero shape replay changed canonical bytes")
	}
	if p.postgres != nil {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("DROP TABLE z", 0))
		// pq reports the final one-row DEFAULT VALUES INSERT, not a utility count.
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec(canonical, 1))
	}
	for _, prepared := range []bool{false, true} {
		s := empty
		s.prepared = prepared
		p.step(t, s)
	}
	p.step(t, referenceDDLPositionError("INSERT INTO z VALUES ()", "42601", `syntax error at or near ")"`, "23", false, "execute"))
	p.step(t, empty)
	p.step(t, referencePrepared(empty))
	for _, script := range []string{"CREATE TABLE bad(v INT NOT NULL); INSERT INTO bad DEFAULT VALUES;", "CREATE TABLE bad(); INSERT INTO bad(v) DEFAULT VALUES;", "CREATE TABLE bad(); ALTER TABLE bad DROP v;"} {
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
			t.Fatal("invalid Restore accepted", script)
		}
		for _, prepared := range []bool{false, true} {
			s := empty
			s.prepared = prepared
			p.step(t, s)
		}
	}
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.native, strings.NewReader(canonical)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if referenceDDLNativeDump(t, p) != canonical {
		t.Fatal("canceled Restore changed zero-column occurrences")
	}
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, prepared, "SELECT * FROM z", []string{}, []string{}, []bool{}, 2)
		}
	}
}
