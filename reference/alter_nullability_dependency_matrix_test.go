//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceAlterNullabilityReviewDependencyTransactions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, throughSequence := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared=%t/commit=%t/sequence=%t", prepared, commit, throughSequence), func(t *testing.T) {
					p := newReferencePair(t)
					for _, backend := range []struct {
						name   string
						db     *sql.DB
						record *referenceRecorder
					}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
						t.Run(backend.name, func(t *testing.T) {
							run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
							run(referenceExec("CREATE TABLE t(a INT,b INT,c INT)", 0))
							run(referenceExec("INSERT INTO t VALUES(7,9,11)", 1))
							run(referenceExec("CREATE SEQUENCE owned OWNED BY t.b", 0))
							definition := "CREATE VIEW v AS SELECT b,c FROM t"
							if throughSequence {
								definition = "CREATE VIEW v AS SELECT c,last_value FROM t,owned"
							}
							run(referenceExec(definition, 0))
							run(referenceExec("CREATE VIEW w AS SELECT * FROM v", 0))
							tx, err := backend.db.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							tr := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, tx, backend.record, s) }
							tr(referenceExec("ALTER TABLE t DROP b CASCADE, DROP c RESTRICT, ALTER a SET NOT NULL", 0))
							tr(referenceExec("INSERT INTO t VALUES(8)", 1))
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
							}
							if err != nil {
								t.Fatal(err)
							}
							if commit {
								run(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
								run(referenceExec("CREATE SEQUENCE owned", 0))
								run(referenceExec("CREATE VIEW v AS SELECT a FROM t", 0))
								run(referenceExec("CREATE VIEW w AS SELECT * FROM v", 0))
							} else {
								run(referenceSelect("SELECT * FROM t", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(9), int64(11)}}))
								names := []string{"b", "c"}
								expected := [][]any{{int64(9), int64(11)}}
								types := []string{"INT4", "INT4"}
								if throughSequence {
									names = []string{"c", "last_value"}
									types = []string{"INT4", "INT8"}
									expected = [][]any{{int64(11), int64(1)}}
								}
								run(referenceSelect("SELECT * FROM w", names, types, expected))
							}
						})
					}
				})
			}
		}
	}
}

func TestReferenceAlterNullabilityReviewRemainingDependency(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
					for _, q := range []string{"CREATE TABLE t(a INT,b INT,c INT)", "CREATE SEQUENCE owned OWNED BY t.b", "CREATE VIEW v AS SELECT b,c FROM t", "CREATE VIEW w AS SELECT * FROM v", "CREATE VIEW z AS SELECT c FROM t"} {
						run(referenceExec(q, 0))
					}
					run(referenceExec("INSERT INTO t VALUES(7,9,11)", 1))
					step := referenceImplicitError("ALTER TABLE t DROP b CASCADE, DROP c RESTRICT, ALTER a SET NOT NULL", referenceDiagnostic{Code: "2BP01", Message: "cannot drop column c of table t because other objects depend on it", Detail: "view z depends on column c of table t", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
					step.prepared = prepared
					step.errorStage = "execute"
					run(step)
					run(referenceSelect("SELECT * FROM w", []string{"b", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(11)}}))
					run(referenceSelect("SELECT * FROM z", []string{"c"}, []string{"INT4"}, [][]any{{int64(11)}}))
					run(referenceSelect("SELECT nextval('owned')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
				})
			}
		})
	}
}
