package reference_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func pgSequencesWriteDiagnostic(verb string) fixture.Diagnostic {
	gerund := map[string]string{"insert into": "inserting into", "update": "updating", "delete from": "deleting from"}[verb]
	action := map[string]string{"insert into": "INSERT", "update": "UPDATE", "delete from": "DELETE"}[verb]
	return fixture.Diagnostic{Code: "55000", Message: "cannot " + verb + " view \"pg_sequences\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + gerund + " the view, provide an INSTEAD OF " + action + " trigger or an unconditional ON " + action + " DO INSTEAD rule."}
}

func TestReferencePGSequencesDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			pgSequenceExec(t, p, b, a, "CREATE SEQUENCE pgsv_diag")
			for _, c := range []struct {
				q     string
				want  fixture.Diagnostic
				parse bool
			}{
				{"SELECT oid FROM pg_sequences", fixture.Diagnostic{Code: "42703", Message: `column "oid" does not exist`, Position: "8"}, true},
				{"SELECT log_cnt FROM pg_sequences", fixture.Diagnostic{Code: "42703", Message: `column "log_cnt" does not exist`, Position: "8"}, true},
				{"SELECT is_called FROM pg_sequences", fixture.Diagnostic{Code: "42703", Message: `column "is_called" does not exist`, Position: "8"}, true},
				{"SELECT /*é*/ oid FROM pg_sequences", fixture.Diagnostic{Code: "42703", Message: `column "oid" does not exist`, Position: "14"}, true},
				{"SELECT TRUE::regtype FROM pg_sequences", fixture.Diagnostic{Code: "42846", Message: "cannot cast type boolean to regtype", Position: "12"}, true},
				{"INSERT INTO pg_sequences(sequencename) VALUES('forbidden')", pgSequencesWriteDiagnostic("insert into"), false},
				{"UPDATE pg_sequences SET last_value=1 WHERE FALSE", pgSequencesWriteDiagnostic("update"), false},
				{"DELETE FROM pg_sequences WHERE FALSE", pgSequencesWriteDiagnostic("delete from"), false},
			} {
				t.Run(c.q, func(t *testing.T) {
					c.want.Severity = "ERROR"
					c.want.SeverityUnlocalized = "ERROR"
					for _, prepared := range []bool{false, true} {
						var err error
						stage := "execute"
						if prepared {
							stmt, prepareErr := a.PrepareContext(p.Ctx, c.q)
							err = prepareErr
							stage = "prepare"
							if err == nil {
								stage = "execute"
								if c.parse {
									rows, e := stmt.QueryContext(p.Ctx)
									err = e
									if rows != nil {
										rows.Close()
									}
								} else {
									_, err = stmt.ExecContext(p.Ctx)
								}
								stmt.Close()
							}
						} else if c.parse {
							rows, e := a.QueryContext(p.Ctx, c.q)
							err = e
							if rows != nil {
								rows.Close()
							}
						} else {
							_, err = a.ExecContext(p.Ctx, c.q)
						}
						if err == nil {
							t.Errorf("unexpected success prepared=%v", prepared)
							continue
						}
						got := fixture.ExtractDiagnostic(t, err)
						if !reflect.DeepEqual(got, c.want) {
							t.Errorf("prepared=%v stage=%s diagnostic=%#v want=%#v", prepared, stage, got, c.want)
						}
						wantStage := "execute"
						if prepared {
							wantStage = "prepare"
						}
						if stage != wantStage {
							t.Errorf("phase prepared=%v got=%s want=%s diagnostic=%#v", prepared, stage, wantStage, got)
						}
					}
				})
			}
			t.Run("InferredRegtypeParameter", func(t *testing.T) {
				pgSequenceExec(t, p, b, a, "CREATE SEQUENCE pgsv_diag_int AS INTEGER")
				pgSequenceExec(t, p, b, a, "CREATE SEQUENCE pgsv_diag_small AS SMALLINT")
				defer a.ExecContext(context.Background(), "DROP SEQUENCE pgsv_diag_int,pgsv_diag_small")
				q := "SELECT data_type FROM pg_sequences WHERE data_type=$1"
				stmt, err := a.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, prepared := range []bool{false, true} {
					var executor fixture.Executor = a
					if prepared {
						executor = fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
					}
					if b.PG {
						// pq's inferred REGTYPE bind uses PostgreSQL's OID input
						// codec and unnamed portal. Native has no wire input codec;
						// do not invent that transport diagnostic or portal context.
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "missing_pgsv_type"`, Context: "unnamed portal parameter $1 = '...'"}, "missing_pgsv_type")
						s.SelectRows = true
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
					}
					// Numeric inferred REGTYPE input is a public shared route.
					// On PostgreSQL this also proves recovery of the same handle.
					for _, c := range []struct {
						oid  int64
						name string
					}{{20, "bigint"}, {23, "integer"}, {21, "smallint"}} {
						fixture.PgDescriptionExecutorRows(t, p, b, executor, fixture.Select(q, []string{"data_type"}, []string{"REGTYPE"}, [][]any{{c.name}}, c.oid))
					}
				}
			})
			t.Run("ExplicitSymbolicRegtypeParameter", func(t *testing.T) {
				q := "SELECT data_type FROM pg_sequences WHERE data_type=$1::text::regtype"
				stmt, err := a.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, prepared := range []bool{false, true} {
					var executor fixture.Executor = a
					if prepared {
						executor = fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
					}
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42704", Message: `type "missing_pgsv_type" does not exist`}, "missing_pgsv_type")
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
					fixture.PgDescriptionExecutorRows(t, p, b, executor, fixture.Select(q, []string{"data_type"}, []string{"REGTYPE"}, [][]any{{"bigint"}}, "bigint"))
				}
			})
			owner := pgSequencesOwner(t, p, a)
			for _, s := range []fixture.Step{
				pgSequencesSelect(b, "SELECT * FROM pg_sequences FOR UPDATE", [][]any{pgSequencesDefault(owner, "pgsv_diag", nil)}),
				fixture.Select("SELECT sequencename FROM pg_sequences FOR UPDATE", []string{"sequencename"}, []string{"NAME"}, [][]any{{"pgsv_diag"}}),
				fixture.Select("SELECT last_value FROM pg_sequences WHERE FALSE FOR UPDATE", []string{"last_value"}, []string{"INT8"}, [][]any{}),
			} {
				t.Run(s.Query, func(t *testing.T) {
					fixture.PgDescriptionExecutorRows(t, p, b, a, s)
					fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Prepared(s))
				})
			}
			if !b.PG {
				t.Run("Precanceled", func(t *testing.T) {
					stmt, err := a.PrepareContext(p.Ctx, pgSequencesProbe)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					rows, err := a.QueryContext(ctx, pgSequencesProbe, "pgsv_diag")
					if rows != nil {
						rows.Close()
						t.Fatal("canceled Query exposed Rows")
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					bad, err := a.PrepareContext(ctx, pgSequencesProbe)
					if bad != nil {
						bad.Close()
						t.Fatal("canceled Prepare exposed handle")
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					rows, err = stmt.QueryContext(ctx, "pgsv_diag")
					if rows != nil {
						rows.Close()
						t.Fatal("canceled held Execute exposed Rows")
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					pgSequencesCheck(t, p, b, a, "pgsv_diag", pgSequencesDefault(pgSequencesOwner(t, p, a), "pgsv_diag", nil))
				})
			}
		})
	}
}
