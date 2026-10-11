package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityReviewDependencyTransactions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, throughSequence := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared=%t/commit=%t/sequence=%t", prepared, commit, throughSequence), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
							run(fixture.Exec("CREATE TABLE t(a INT,b INT,c INT)", 0))
							run(fixture.Exec("INSERT INTO t VALUES(7,9,11)", 1))
							run(fixture.Exec("CREATE SEQUENCE owned OWNED BY t.b", 0))
							definition := "CREATE VIEW v AS SELECT b,c FROM t"
							if throughSequence {
								definition = "CREATE VIEW v AS SELECT c,last_value FROM t,owned"
							}
							run(fixture.Exec(definition, 0))
							run(fixture.Exec("CREATE VIEW w AS SELECT * FROM v", 0))
							tx, err := backend.DB.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							tr := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s) }
							tr(fixture.Exec("ALTER TABLE t DROP b CASCADE, DROP c RESTRICT, ALTER a SET NOT NULL", 0))
							tr(fixture.Exec("INSERT INTO t VALUES(8)", 1))
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
							}
							if err != nil {
								t.Fatal(err)
							}
							if commit {
								fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 2)
								run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
								run(fixture.Exec("CREATE SEQUENCE owned", 0))
								run(fixture.Exec("CREATE VIEW v AS SELECT a FROM t", 0))
								run(fixture.Exec("CREATE VIEW w AS SELECT * FROM v", 0))
							} else {
								run(fixture.Select("SELECT * FROM t", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(9), int64(11)}}))
								names := []string{"b", "c"}
								expected := [][]any{{int64(9), int64(11)}}
								types := []string{"INT4", "INT4"}
								if throughSequence {
									names = []string{"c", "last_value"}
									types = []string{"INT4", "INT8"}
									expected = [][]any{{int64(11), int64(1)}}
								}
								fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, "SELECT * FROM t", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, []bool{true, true, true}, 1)
								run(fixture.Select("SELECT * FROM w", names, types, expected))
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
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					for _, q := range []string{"CREATE TABLE t(a INT,b INT,c INT)", "CREATE SEQUENCE owned OWNED BY t.b", "CREATE VIEW v AS SELECT b,c FROM t", "CREATE VIEW w AS SELECT * FROM v", "CREATE VIEW z AS SELECT c FROM t"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO t VALUES(7,9,11)", 1))
					step := fixture.ImplicitError("ALTER TABLE t DROP b CASCADE, DROP c RESTRICT, ALTER a SET NOT NULL", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column c of table t because other objects depend on it", Detail: "view z depends on column c of table t", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
					step.Prepared = prepared
					step.ErrorStage = "execute"
					run(step)
					fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, "SELECT * FROM t", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, []bool{true, true, true}, 1)
					run(fixture.Select("SELECT * FROM w", []string{"b", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(11)}}))
					run(fixture.Select("SELECT * FROM z", []string{"c"}, []string{"INT4"}, [][]any{{int64(11)}}))
					run(fixture.Select("SELECT nextval('owned')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
				})
			}
		})
	}
}
