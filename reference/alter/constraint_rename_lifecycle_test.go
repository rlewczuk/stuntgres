package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConstraintRenameKindsAndIdentity(t *testing.T) {
	for _, k := range constraintRenameKinds {
		t.Run(k.kind, func(t *testing.T) {
			constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t("+k.declaration+")", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
				before := constraintRenameCapture(t, p, c, "old", k.indexed)
				if before.definition != k.definition {
					t.Fatalf("definition got %q want %q", before.definition, k.definition)
				}
				run(fixture.Prepared(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT old TO new", 0)))
				after := constraintRenameCapture(t, p, c, "new", k.indexed)
				if after != before {
					t.Fatal("rename changed identity/definition", before, after)
				}
				constraintRenameNames(run, "new", k)
				run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='old'", []string{"conname"}, []string{"NAME"}, nil))
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='old'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Select("SELECT k,payload FROM t", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
				q, d := constraintRenameViolation(k, "new")
				run(fixture.ErrorDiagnostic(q, d))
				run(fixture.Select("SELECT k,payload FROM t", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
				if k.kind == "p" {
					run(fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid='t'::regclass::oid ORDER BY conname", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{"new", "p"}, {"t_k_not_null", "n"}}))
				}
			})
		})
	}
	t.Run("composite_null_policy", func(t *testing.T) {
		constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,CONSTRAINT old UNIQUE NULLS NOT DISTINCT(b,a))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(NULL,'keep')", 1))
			before := constraintRenameCapture(t, p, c, "old", true)
			run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT old TO new", 0))
			if after := constraintRenameCapture(t, p, c, "new", true); before != after || after.definition != "UNIQUE NULLS NOT DISTINCT (b, a)" {
				t.Fatal(before, after)
			}
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(NULL,'keep')", fixture.UniqueReferenceDuplicate("t", "new", "Key (b, a)=(keep, null) already exists.")))
		})
	})
}

func TestReferenceConstraintRenameCheckOrdering(t *testing.T) {
	constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT,CONSTRAINT a CHECK(k>0),CONSTRAINT b CHECK(k>1))", 0))
		run(fixture.Exec("INSERT INTO t VALUES(2)", 1))
		a := constraintRenameCapture(t, p, c, "a", false)
		bb := constraintRenameCapture(t, p, c, "b", false)
		fail := func(name string) {
			d := fixture.CheckViolation("t", name, "0")
			d.Severity, d.SeverityUnlocalized = "ERROR", "ERROR"
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(0)", d))
		}
		fail("a")
		run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT a TO z", 0))
		fail("b")
		run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT b TO zz", 0))
		fail("z")
		if constraintRenameCapture(t, p, c, "z", false) != a || constraintRenameCapture(t, p, c, "zz", false) != bb {
			t.Fatal("CHECK definition or identity changed")
		}
	})
}

func TestReferenceConstraintRenamePreparedMetadata(t *testing.T) {
	constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT old UNIQUE,payload TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
		queries := []string{"SELECT k,payload FROM t", "INSERT INTO t VALUES($1,$2)", "ALTER TABLE t RENAME CONSTRAINT old TO new", "INSERT INTO t VALUES(1,'ignored') ON CONFLICT ON CONSTRAINT old DO NOTHING"}
		held := make([]*sql.Stmt, len(queries))
		for i, q := range queries {
			var err error
			held[i], err = c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(q, err)
			}
			defer held[i].Close()
		}
		execute := func(i int, s fixture.Step) {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held[i], SQL: queries[i]}, b.Recorder, s)
		}
		var retained *sql.Rows
		if !b.PG {
			var err error
			retained, err = held[0].QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		execute(2, fixture.Exec(queries[2], 0))
		selectStep := fixture.Select(queries[0], []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}})
		run(selectStep)
		execute(0, selectStep)
		duplicate := fixture.UniqueReferenceDuplicate("t", "new", "Key (k)=(1) already exists.")
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'bad')", duplicate))
		execute(1, fixture.ErrorDiagnostic(queries[1], duplicate, int64(1), "bad"))
		execute(2, fixture.ErrorDiagnostic(queries[2], constraintRenameMissing("old")))
		execute(3, fixture.ErrorDiagnostic(queries[3], constraintRenameMissing("old")))
		run(fixture.Exec("INSERT INTO t VALUES(1,'ignored') ON CONFLICT ON CONSTRAINT new DO NOTHING", 0))
		run(fixture.Select("SELECT conname,contype,conbin,pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='new'", []string{"conname", "contype", "conbin", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "PG_NODE_TREE", "TEXT"}, [][]any{{"new", "u", nil, "UNIQUE (k)"}}))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='new'", []string{"relname"}, []string{"NAME"}, [][]any{{"new"}}))
		if retained != nil {
			if !retained.Next() {
				t.Fatal(retained.Err())
			}
			var k int64
			var payload string
			if err := retained.Scan(&k, &payload); err != nil || k != 1 || payload != "keep" {
				t.Fatal(k, payload, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal(retained.Err())
			}
		}
	})
}
