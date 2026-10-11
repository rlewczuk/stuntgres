package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueLifecycle(t *testing.T) {
	fixture.UniqueReferenceSchemaCleanup(t, "moved")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, backend.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(extra INT,a INT,b TEXT,CONSTRAINT u UNIQUE NULLS NOT DISTINCT(a,b))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(0,1,'x'),(0,NULL,'n')", 2))
			var constraint, index, parent int64
			if err := a.QueryRowContext(p.Ctx, "SELECT oid,conrelid FROM pg_constraint WHERE conname='u'").Scan(&constraint, &parent); err != nil {
				t.Fatal(err)
			}
			if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='u'").Scan(&index); err != nil {
				t.Fatal(err)
			}
			if constraint <= 0 || index <= 0 || parent <= 0 || constraint == index || index == parent || parent == constraint {
				t.Fatal("distinct identities", constraint, index, parent)
			}
			run(fixture.Exec("ALTER TABLE t RENAME b TO label", 0))
			run(fixture.Exec("ALTER TABLE t RENAME TO renamed", 0))
			var afterConstraint, afterParent, afterIndex int64
			if err := a.QueryRowContext(p.Ctx, "SELECT oid,conrelid FROM pg_constraint WHERE conname='u'").Scan(&afterConstraint, &afterParent); err != nil {
				t.Fatal(err)
			}
			if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='u'").Scan(&afterIndex); err != nil {
				t.Fatal(err)
			}
			if afterConstraint != constraint || afterParent != parent || afterIndex != index {
				t.Fatal("rename changed identities")
			}
			run(fixture.Select("SELECT pg_get_constraintdef(oid),pg_get_constraintdef(oid,true),conbin FROM pg_constraint WHERE conname='u'", []string{"pg_get_constraintdef", "pg_get_constraintdef", "conbin"}, []string{"TEXT", "TEXT", "PG_NODE_TREE"}, [][]any{{"UNIQUE NULLS NOT DISTINCT (a, label)", "UNIQUE NULLS NOT DISTINCT (a, label)", nil}}))
			run(fixture.Select("SELECT relkind,relnatts FROM pg_class WHERE relname='u'", []string{"relkind", "relnatts"}, []string{"CHAR", "INT2"}, [][]any{{"i", int64(2)}}))
			run(fixture.ErrorDiagnostic("INSERT INTO renamed VALUES(9,1,'x')", fixture.UniqueReferenceDuplicate("renamed", "u", "Key (a, label)=(1, x) already exists.")))
			run(fixture.Exec("ALTER TABLE renamed DROP extra RESTRICT", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO renamed VALUES(1,'x')", fixture.UniqueReferenceDuplicate("renamed", "u", "Key (a, label)=(1, x) already exists.")))
			run(fixture.Exec("CREATE SCHEMA moved", 0))
			run(fixture.Exec("CREATE TABLE moved.u(v INT)", 0))
			run(fixture.ImplicitError("ALTER TABLE renamed SET SCHEMA moved", fixture.Diagnostic{Code: "42P07", Message: `relation "u" already exists in schema "moved"`}))
			run(fixture.ErrorDiagnostic("INSERT INTO renamed VALUES(1,'x')", fixture.UniqueReferenceDuplicate("renamed", "u", "Key (a, label)=(1, x) already exists.")))
			run(fixture.Exec("DROP TABLE moved.u", 0))
			run(fixture.Exec("ALTER TABLE renamed SET SCHEMA moved", 0))
			want := fixture.UniqueReferenceDuplicate("renamed", "u", "Key (a, label)=(1, x) already exists.")
			want.Schema = "moved"
			run(fixture.ErrorDiagnostic("INSERT INTO moved.renamed VALUES(1,'x')", want))
			heldQuery := "INSERT INTO moved.renamed VALUES($1,$2)"
			held, err := a.PrepareContext(p.Ctx, heldQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(fixture.Exec("ALTER TABLE moved.renamed DROP CONSTRAINT u", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: held, SQL: heldQuery}, backend.Recorder, fixture.Exec(heldQuery, 1, int64(1), "x"))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='u'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("CREATE TABLE moved.u(v INT)", 0))
			run(fixture.Exec("DROP SCHEMA moved CASCADE", 0))
			run(fixture.Exec("CREATE TABLE comp(a INT,b INT,c INT,CONSTRAINT comp_u UNIQUE(a,b),CONSTRAINT c_u UNIQUE(c))", 0))
			run(fixture.Exec("INSERT INTO comp VALUES(1,1,7)", 1))
			run(fixture.Exec("ALTER TABLE comp DROP b RESTRICT", 0))
			run(fixture.Exec("INSERT INTO comp VALUES(1,8)", 1))
			run(fixture.ErrorDiagnostic("INSERT INTO comp VALUES(2,7)", fixture.UniqueReferenceDuplicate("comp", "c_u", "Key (c)=(7) already exists.")))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='c_u' OR conname='comp_u' ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"c_u"}}))
			run(fixture.Exec("DROP TABLE comp", 0))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='c_u' OR relname='comp_u'", []string{"relname"}, []string{"NAME"}, nil))
		})
	}
}

func TestReferenceUniqueNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,CONSTRAINT u UNIQUE NULLS NOT DISTINCT(a,b))", 0))
			run(fixture.Exec("CREATE TABLE d(a INT CONSTRAINT d_u UNIQUE)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'x'),(NULL,'n')", 2))
			run(fixture.Exec("INSERT INTO d VALUES(NULL),(NULL),(2)", 3))
			run(fixture.Exec("ALTER TABLE t RENAME b TO label", 0))
			var dump bytes.Buffer
			held, err := backend.DB.PrepareContext(p.Ctx, "INSERT INTO t VALUES($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if !backend.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Count(dump.String(), `CONSTRAINT "u" UNIQUE NULLS NOT DISTINCT (a, label)`) != 1 || strings.Contains(dump.String(), "CREATE INDEX") {
					t.Fatal("backing index serialized twice", dump.String())
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				var again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
					t.Fatal("noncanonical UNIQUE replay", err, again.String())
				}
			}
			// PostgreSQL has no native control API; these are the independently expected
			// SQL outcomes for the persistent UNIQUE objects and rows in both modes.
			run(fixture.Select("SELECT a,label FROM t ORDER BY a NULLS LAST", []string{"a", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {nil, "n"}}))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: backend.DB, Statement: held, SQL: "INSERT INTO t VALUES($1,$2)"}, backend.Recorder, fixture.ErrorDiagnostic("INSERT INTO t VALUES($1,$2)", fixture.UniqueReferenceDuplicate("t", "u", "Key (a, label)=(1, x) already exists."), int64(1), "x"))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(NULL,'n')", fixture.UniqueReferenceDuplicate("t", "u", "Key (a, label)=(null, n) already exists.")))
			run(fixture.Exec("INSERT INTO d VALUES(NULL)", 1))
			if !backend.PG {
				old, err := backend.DB.QueryContext(p.Ctx, "SELECT a,label FROM t ORDER BY a NULLS LAST")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				bad := dump.String() + "INSERT INTO public.t VALUES(1,'x');\n"
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
				want := fixture.UniqueReferenceDuplicate("t", "u", "Key (a, label)=(1, x) already exists.")
				fixture.AssertDiagnostic(t, err, &want)
				for _, expected := range []struct {
					a     any
					label string
				}{{int64(1), "x"}, {nil, "n"}} {
					if !old.Next() {
						t.Fatal("retained row lost", old.Err())
					}
					var a any
					var label string
					if err := old.Scan(&a, &label); err != nil || a != expected.a || label != expected.label {
						t.Fatal(a, label, err)
					}
				}
				if old.Next() || old.Err() != nil {
					t.Fatal(old.Err())
				}
				old.Close()
				run(fixture.Select("SELECT a FROM d ORDER BY a NULLS LAST", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {nil}, {nil}, {nil}}))
			}
		})
	}
}
