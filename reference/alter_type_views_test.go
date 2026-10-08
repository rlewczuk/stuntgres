//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAlterTypeViewDependencies(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"where", "SELECT b FROM t WHERE a=1"},
		{"order", "SELECT b FROM t ORDER BY a"},
		{"join", "SELECT t.b FROM t JOIN u ON t.a=u.c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferencePair(t)
			for _, b := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
					run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,7)", 1))
					run(referenceExec("CREATE TABLE u(c INT)", 0))
					run(referenceExec("INSERT INTO u VALUES(1)", 1))
					run(referenceExec("CREATE VIEW v AS "+tc.query, 0))
					run(referenceImplicitError("ALTER TABLE t ALTER a TYPE BIGINT", referenceDiagnostic{Code: "0A000", Message: "cannot alter type of a column used by a view or rule", Detail: `rule _RETURN on view v depends on column "a"`}))
					run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
					run(referenceSelect("SELECT * FROM v", []string{"b"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}

func TestReferenceAlterTypeViewIdentityCollationAndCascade(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE SCHEMA s", 0))
			run(referenceExec("CREATE TABLE t(c VARCHAR(5),discard INT,keep INT)", 0))
			run(referenceExec("INSERT INTO t VALUES('seven',1,9)", 1))
			run(referenceExec(`CREATE VIEW s."Odd View" AS SELECT c,discard FROM public.t`, 0))
			run(referenceExec("CREATE VIEW v_keep AS SELECT keep FROM t", 0))
			for _, action := range []string{"TYPE VARCHAR(5)", `TYPE VARCHAR(5) COLLATE "C"`, `TYPE VARCHAR(5) COLLATE "default"`} {
				failure := referenceImplicitError("ALTER TABLE public.t ALTER c "+action, referenceDiagnostic{Code: "0A000", Message: "cannot alter type of a column used by a view or rule", Detail: `rule _RETURN on view s."Odd View" depends on column "c"`})
				failure.errorStage = "execute"
				run(failure)
				if b.name == "postgres" {
					run(referenceSelect("SELECT atttypmod,attcollation::int FROM pg_attribute WHERE attrelid='t'::regclass AND attname='c'", []string{"atttypmod", "attcollation"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(100)}}))
				}
				run(referenceSelect(`SELECT * FROM s."Odd View"`, []string{"c", "discard"}, []string{"VARCHAR", "INT4"}, [][]any{{"seven", int64(1)}}))
			}
			// DROP runs first and removes the view which also blocked c.
			run(referenceExec("ALTER TABLE t ALTER c TYPE TEXT, DROP discard CASCADE", 0))
			run(referenceSelect("SELECT * FROM t", []string{"c", "keep"}, []string{"TEXT", "INT4"}, [][]any{{"seven", int64(9)}}))
			run(referenceSelect("SELECT keep FROM v_keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(9)}}))
			absent := referenceImplicitError(`SELECT * FROM s."Odd View"`, referenceDiagnostic{Code: "42P01", Message: `relation "s.Odd View" does not exist`, Position: "15"})
			absent.selectRows, absent.errorStage = true, "execute"
			run(absent)
		})
	}
}
