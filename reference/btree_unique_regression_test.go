package reference_test

import "testing"

// These are the complete public SQL assertion sets formerly in the native-only
// Btree comparator and maintenance fixtures. Unique backing uses these same
// typed ordering and committed-batch publication seams.
func TestReferenceUniqueBtreeRegression(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(n INTEGER,s TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5))
			run(referenceExec("CREATE INDEX t_n ON t(n)", 0))
			run(referencePrepared(referenceExec("CREATE INDEX t_s ON t USING btree(s)", 0)))
			for _, c := range []struct {
				query string
				rows  [][]any
				bag   bool
			}{
				{"SELECT n,s FROM t WHERE n=2", [][]any{{int64(2), "b"}, {int64(2), "b"}}, true},
				{"SELECT n,s FROM t WHERE n>=2", [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}}, true},
				{"SELECT n,s FROM t WHERE n IS NULL", [][]any{{nil, nil}}, true},
				{"SELECT n,s FROM t WHERE s>'a'", [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}}, true},
				{"SELECT n,s FROM t ORDER BY n ASC NULLS LAST", [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {nil, nil}}, false},
				{"SELECT n,s FROM t ORDER BY n DESC", [][]any{{nil, nil}, {int64(3), "c"}, {int64(2), "b"}, {int64(2), "b"}, {int64(1), "a"}}, false},
			} {
				s := referenceSelect(c.query, []string{"n", "s"}, []string{"INT4", "TEXT"}, c.rows)
				s.bag = c.bag
				run(s)
				run(referencePrepared(s))
			}
			s := referencePrepared(referenceSelect("SELECT n,s FROM t WHERE n=$1", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "b"}, {int64(2), "b"}}, int64(2)))
			s.bag = true
			run(s)
			for _, c := range []struct{ query, code, message string }{{"CREATE INDEX t_n ON t(n)", "42P07", `relation "t_n" already exists`}, {"CREATE INDEX retry ON missing(n)", "42P01", `relation "missing" does not exist`}, {"CREATE INDEX retry ON t(missing)", "42703", `column "missing" does not exist`}} {
				s := referenceImplicitError(c.query, referenceDiagnostic{Code: c.code, Message: c.message})
				s.errorStage = "execute"
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE INDEX retry ON t(n)", 0))
		})
	}
}

func TestReferenceUniqueBtreeMaintenance(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(n INT,s TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5))
			run(referenceExec("CREATE INDEX ni ON t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0))
			run(referenceExec("CREATE INDEX si ON t USING btree(s)", 0))
			run(referenceExec("INSERT INTO t VALUES(2,'b')", 1))
			query := "INSERT INTO t VALUES($1,$2)"
			held, err := b.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, args := range [][]any{{int64(0), ""}, {nil, nil}} {
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: held, query: query}, b.recorder, referenceExec(query, 1, args...))
			}
			run(referenceExec("INSERT INTO t SELECT n,s FROM t WHERE n=3", 1))
			run(referenceSelect("SELECT n,s FROM t ORDER BY n ASC NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), ""}, {int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {int64(3), "c"}, {nil, nil}, {nil, nil}}))
			for _, c := range []struct {
				query string
				arg   int64
				want  [][]any
			}{
				{"SELECT n,s FROM t WHERE n<$1", 2, [][]any{{int64(0), ""}, {int64(1), "a"}}},
				{"SELECT n,s FROM t WHERE n<=$1", 1, [][]any{{int64(0), ""}, {int64(1), "a"}}},
				{"SELECT n,s FROM t WHERE n>$1", 2, [][]any{{int64(3), "c"}, {int64(3), "c"}}},
				{"SELECT n,s FROM t WHERE n>=$1", 3, [][]any{{int64(3), "c"}, {int64(3), "c"}}},
				{"SELECT n,s FROM t WHERE n=$1", 9, nil},
			} {
				s := referencePrepared(referenceSelect(c.query, []string{"n", "s"}, []string{"INT4", "TEXT"}, c.want, c.arg))
				s.bag = true
				run(s)
			}
			run(referenceExec("UPDATE t SET n=10 WHERE n=1", 1))
			run(referenceExec("DELETE FROM t WHERE n=3", 2))
			run(referenceSelect("SELECT n FROM t ORDER BY n ASC NULLS FIRST", []string{"n"}, []string{"INT4"}, [][]any{{nil}, {nil}, {int64(0)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(10)}}))
		})
	}
}

func TestReferenceUniqueBtreeLifecycle(t *testing.T) {
	uniqueReferenceSchemaCleanup(t, "moved")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, q := range []string{"CREATE TABLE t(extra INT,n INT,s TEXT)", "INSERT INTO t VALUES(0,1,'a'),(0,2,'b'),(0,10,'c')", "CREATE INDEX ni ON t(n)", "CREATE INDEX si ON t(s)", "UPDATE t SET n=3 WHERE n=2", "DELETE FROM t WHERE n=1", "ALTER TABLE t RENAME n TO key", "ALTER TABLE t RENAME TO renamed", "ALTER TABLE renamed DROP extra", "ALTER TABLE renamed ALTER key TYPE TEXT USING key::text", "CREATE SCHEMA moved", "CREATE TABLE moved.ni(n INT)"} {
				count := int64(0)
				switch q {
				case "INSERT INTO t VALUES(0,1,'a'),(0,2,'b'),(0,10,'c')":
					count = 3
				case "UPDATE t SET n=3 WHERE n=2", "DELETE FROM t WHERE n=1":
					count = 1
				}
				run(referenceExec(q, count))
			}
			run(referenceImplicitError("ALTER TABLE renamed SET SCHEMA moved", referenceDiagnostic{Code: "42P07", Message: `relation "ni" already exists in schema "moved"`}))
			run(referenceSelect("SELECT key FROM renamed ORDER BY key", []string{"key"}, []string{"TEXT"}, [][]any{{"10"}, {"3"}}))
			run(referenceExec("DROP TABLE moved.ni", 0))
			run(referenceExec("ALTER TABLE renamed SET SCHEMA moved", 0))
			run(referenceSelect("SELECT key FROM moved.renamed ORDER BY key", []string{"key"}, []string{"TEXT"}, [][]any{{"10"}, {"3"}}))
			run(referenceExec("ALTER TABLE moved.renamed DROP s", 0))
			run(referenceSelect("SELECT relname FROM pg_class WHERE relname='si'", []string{"relname"}, []string{"NAME"}, nil))
			run(referenceExec("DROP TABLE moved.renamed RESTRICT", 0))
			run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ni'", []string{"relname"}, []string{"NAME"}, nil))
			run(referenceExec("CREATE TABLE moved.ni(n INT)", 0))
			run(referenceExec("DROP SCHEMA moved CASCADE", 0))
		})
	}
}
