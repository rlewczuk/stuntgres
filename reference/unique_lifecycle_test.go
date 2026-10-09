package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceUniqueLifecycle(t *testing.T) {
	uniqueReferenceSchemaCleanup(t, "moved")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, _ := referenceLockConnections(t, p.ctx, backend.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(extra INT,a INT,b TEXT,CONSTRAINT u UNIQUE NULLS NOT DISTINCT(a,b))", 0))
			run(referenceExec("INSERT INTO t VALUES(0,1,'x'),(0,NULL,'n')", 2))
			var constraint, index, parent int64
			if err := a.QueryRowContext(p.ctx, "SELECT oid,conrelid FROM pg_constraint WHERE conname='u'").Scan(&constraint, &parent); err != nil {
				t.Fatal(err)
			}
			if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='u'").Scan(&index); err != nil {
				t.Fatal(err)
			}
			if constraint <= 0 || index <= 0 || parent <= 0 || constraint == index || index == parent || parent == constraint {
				t.Fatal("distinct identities", constraint, index, parent)
			}
			run(referenceExec("ALTER TABLE t RENAME b TO label", 0))
			run(referenceExec("ALTER TABLE t RENAME TO renamed", 0))
			var afterConstraint, afterParent, afterIndex int64
			if err := a.QueryRowContext(p.ctx, "SELECT oid,conrelid FROM pg_constraint WHERE conname='u'").Scan(&afterConstraint, &afterParent); err != nil {
				t.Fatal(err)
			}
			if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='u'").Scan(&afterIndex); err != nil {
				t.Fatal(err)
			}
			if afterConstraint != constraint || afterParent != parent || afterIndex != index {
				t.Fatal("rename changed identities")
			}
			run(referenceSelect("SELECT pg_get_constraintdef(oid),pg_get_constraintdef(oid,true),conbin FROM pg_constraint WHERE conname='u'", []string{"pg_get_constraintdef", "pg_get_constraintdef", "conbin"}, []string{"TEXT", "TEXT", "PG_NODE_TREE"}, [][]any{{"UNIQUE NULLS NOT DISTINCT (a, label)", "UNIQUE NULLS NOT DISTINCT (a, label)", nil}}))
			run(referenceSelect("SELECT relkind,relnatts FROM pg_class WHERE relname='u'", []string{"relkind", "relnatts"}, []string{"CHAR", "INT2"}, [][]any{{"i", int64(2)}}))
			run(referenceErrorDiagnostic("INSERT INTO renamed VALUES(9,1,'x')", uniqueReferenceDuplicate("renamed", "u", "Key (a, label)=(1, x) already exists.")))
			run(referenceExec("ALTER TABLE renamed DROP extra RESTRICT", 0))
			run(referenceErrorDiagnostic("INSERT INTO renamed VALUES(1,'x')", uniqueReferenceDuplicate("renamed", "u", "Key (a, label)=(1, x) already exists.")))
			run(referenceExec("CREATE SCHEMA moved", 0))
			run(referenceExec("CREATE TABLE moved.u(v INT)", 0))
			run(referenceImplicitError("ALTER TABLE renamed SET SCHEMA moved", referenceDiagnostic{Code: "42P07", Message: `relation "u" already exists in schema "moved"`}))
			run(referenceErrorDiagnostic("INSERT INTO renamed VALUES(1,'x')", uniqueReferenceDuplicate("renamed", "u", "Key (a, label)=(1, x) already exists.")))
			run(referenceExec("DROP TABLE moved.u", 0))
			run(referenceExec("ALTER TABLE renamed SET SCHEMA moved", 0))
			want := uniqueReferenceDuplicate("renamed", "u", "Key (a, label)=(1, x) already exists.")
			want.Schema = "moved"
			run(referenceErrorDiagnostic("INSERT INTO moved.renamed VALUES(1,'x')", want))
			heldQuery := "INSERT INTO moved.renamed VALUES($1,$2)"
			held, err := a.PrepareContext(p.ctx, heldQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(referenceExec("ALTER TABLE moved.renamed DROP CONSTRAINT u", 0))
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: a, statement: held, query: heldQuery}, backend.recorder, referenceExec(heldQuery, 1, int64(1), "x"))
			run(referenceSelect("SELECT relname FROM pg_class WHERE relname='u'", []string{"relname"}, []string{"NAME"}, nil))
			run(referenceExec("CREATE TABLE moved.u(v INT)", 0))
			run(referenceExec("DROP SCHEMA moved CASCADE", 0))
			run(referenceExec("CREATE TABLE comp(a INT,b INT,c INT,CONSTRAINT comp_u UNIQUE(a,b),CONSTRAINT c_u UNIQUE(c))", 0))
			run(referenceExec("INSERT INTO comp VALUES(1,1,7)", 1))
			run(referenceExec("ALTER TABLE comp DROP b RESTRICT", 0))
			run(referenceExec("INSERT INTO comp VALUES(1,8)", 1))
			run(referenceErrorDiagnostic("INSERT INTO comp VALUES(2,7)", uniqueReferenceDuplicate("comp", "c_u", "Key (c)=(7) already exists.")))
			run(referenceSelect("SELECT conname FROM pg_constraint WHERE conname='c_u' OR conname='comp_u' ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"c_u"}}))
			run(referenceExec("DROP TABLE comp", 0))
			run(referenceSelect("SELECT relname FROM pg_class WHERE relname='c_u' OR relname='comp_u'", []string{"relname"}, []string{"NAME"}, nil))
		})
	}
}

func TestReferenceUniqueNativeControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT,b TEXT,CONSTRAINT u UNIQUE NULLS NOT DISTINCT(a,b))", 0))
			run(referenceExec("CREATE TABLE d(a INT CONSTRAINT d_u UNIQUE)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'x'),(NULL,'n')", 2))
			run(referenceExec("INSERT INTO d VALUES(NULL),(NULL),(2)", 3))
			run(referenceExec("ALTER TABLE t RENAME b TO label", 0))
			var dump bytes.Buffer
			held, err := backend.db.PrepareContext(p.ctx, "INSERT INTO t VALUES($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if !backend.pg {
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Count(dump.String(), `CONSTRAINT "u" UNIQUE NULLS NOT DISTINCT (a, label)`) != 1 || strings.Contains(dump.String(), "CREATE INDEX") {
					t.Fatal("backing index serialized twice", dump.String())
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				var again bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
					t.Fatal("noncanonical UNIQUE replay", err, again.String())
				}
			}
			// PostgreSQL has no native control API; these are the independently expected
			// SQL outcomes for the persistent UNIQUE objects and rows in both modes.
			run(referenceSelect("SELECT a,label FROM t ORDER BY a NULLS LAST", []string{"a", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {nil, "n"}}))
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: backend.db, statement: held, query: "INSERT INTO t VALUES($1,$2)"}, backend.recorder, referenceErrorDiagnostic("INSERT INTO t VALUES($1,$2)", uniqueReferenceDuplicate("t", "u", "Key (a, label)=(1, x) already exists."), int64(1), "x"))
			run(referenceErrorDiagnostic("INSERT INTO t VALUES(NULL,'n')", uniqueReferenceDuplicate("t", "u", "Key (a, label)=(null, n) already exists.")))
			run(referenceExec("INSERT INTO d VALUES(NULL)", 1))
			if !backend.pg {
				old, err := backend.db.QueryContext(p.ctx, "SELECT a,label FROM t ORDER BY a NULLS LAST")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				bad := dump.String() + "INSERT INTO public.t VALUES(1,'x');\n"
				err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad))
				want := uniqueReferenceDuplicate("t", "u", "Key (a, label)=(1, x) already exists.")
				referenceAssertDiagnostic(t, err, &want)
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
				run(referenceSelect("SELECT a FROM d ORDER BY a NULLS LAST", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {nil}, {nil}, {nil}}))
			}
		})
	}
}
