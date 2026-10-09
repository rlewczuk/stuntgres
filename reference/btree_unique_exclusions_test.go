package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceUniqueBtreeExcludedDefinitionsDiagnostics(t *testing.T) {
	for _, c := range []struct {
		name, query, message, position string
		execute                        bool
	}{
		{"concurrent", "CREATE INDEX CONCURRENTLY i ON t(n)", "concurrent indexes are unsupported", "14", false},
		{"expression", "CREATE INDEX ix ON t((c+1))", "expression indexes are unsupported", "22", false},
		{"spaced-expression", "CREATE INDEX ix ON t ((c+1))", "expression indexes are unsupported", "23", false},
		{"unicode-expression", "/*é*/ CREATE INDEX ix ON t((c+1))", "expression indexes are unsupported", "28", false},
		{"partial", "CREATE INDEX i ON t(n) WHERE n>0", "partial indexes are unsupported", "24", false},
		{"collation", `CREATE INDEX i ON t(s COLLATE "C")`, "index collations are unsupported", "23", false},
		{"opclass", "CREATE INDEX i ON t(n int4_ops)", "index operator classes are unsupported", "23", false},
		{"include", "CREATE INDEX i ON t(n) INCLUDE(s)", "included index columns are unsupported", "24", false},
		{"storage", "CREATE INDEX i ON t(n) WITH(fillfactor=80)", "index storage parameters are unsupported", "24", false},
		{"tablespace", "CREATE INDEX i ON t(n) TABLESPACE pg_default", "index tablespaces are unsupported", "24", false},
		{"method", "CREATE INDEX i ON t USING hash(n)", "non-btree indexes are unsupported", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(n INT,c INT,s TEXT)", 0))
					run(referenceExec("INSERT INTO t VALUES(2,1,'one')", 1))
					var before bytes.Buffer
					if !b.pg {
						if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
							t.Fatal(err)
						}
					}
					indexName := "i"
					if strings.Contains(c.query, "INDEX ix ") {
						indexName = "ix"
					}
					for _, prepared := range []bool{false, true} {
						s := referenceExec(c.query, 0)
						if !b.pg {
							s = referenceImplicitError(c.query, referenceDiagnostic{Code: "0A000", Message: c.message, Position: c.position})
							s.errorStage = "execute"
							if prepared && !c.execute {
								s.errorStage = "prepare"
							}
						}
						if prepared {
							s = referencePrepared(s)
						}
						run(s)
						if b.pg {
							// PostgreSQL independently accepts each valid excluded native syntax.
							// Remove its actual index before the second route and common controls.
							run(referenceSelect("SELECT relname,relkind FROM pg_class WHERE relname=$1", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{indexName, "i"}}, indexName))
							run(referenceExec("DROP INDEX "+indexName, 0))
						}
					}
					if !b.pg {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
							t.Fatal("excluded definition changed persistent state", err, after.String())
						}
					}
					run(referenceSelect("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='i' OR relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
					run(referenceExec("CREATE INDEX i ON t(n)", 0))
					run(referenceExec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
					run(referenceSelect("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
				})
			}
		})
	}
}
