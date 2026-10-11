package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedRefreshTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			refreshSeed(run)
			for _, target := range []string{"refresh_mv", "refresh_missing"} {
				for _, suffix := range []string{"WITH DATA", "WITH NO DATA"} {
					tx, err := conn.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					fixture.QueryLockRollbackCleanup(t, tx)
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW "+target+" "+suffix, fixture.ControlsReferenceDiagnostic("25006", "cannot execute REFRESH MATERIALIZED VIEW in a read-only transaction")))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !b.PG {
				for _, suffix := range []string{"WITH DATA", "WITH NO DATA"} {
					tx, err := conn.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					fixture.QueryLockRollbackCleanup(t, tx)
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW refresh_mv "+suffix, fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")))
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted()))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(materializedViewBag("refresh_mv"))
				}
				return
			}
			// PostgreSQL transactional refresh is a server complement, not native DDL TX support.
			for _, commit := range []bool{false, true} {
				tx, err := conn.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				fixture.QueryLockRollbackCleanup(t, tx)
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO mv_base VALUES(3,'c')", 1))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv", 0))
				four := refreshBag("refresh_mv", [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}})
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, four)
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					run(four)
				} else {
					run(materializedViewBag("refresh_mv"))
				}
			}
			for _, commit := range []bool{false, true} {
				tx, err := conn.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				fixture.QueryLockRollbackCleanup(t, tx)
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv WITH NO DATA", 0))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					run(fixture.MaterializedViewUnpopulated("refresh_mv"))
				} else {
					run(refreshBag("refresh_mv", [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}))
				}
			}
		})
	}
}

func TestReferenceMaterializedRefreshBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			refreshSeed(run)
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_no AS SELECT i FROM mv_base WITH NO DATA", 0))
			for _, prepared := range []bool{false, true} {
				for _, target := range []string{"refresh_mv", "refresh_no", "refresh_missing"} {
					q := "REFRESH MATERIALIZED VIEW CONCURRENTLY " + target
					want := fixture.ControlsReferenceDiagnostic("0A000", "concurrent materialized view refresh is unsupported")
					want.Position = "27"
					if b.PG {
						switch target {
						case "refresh_mv":
							want = fixture.ControlsReferenceDiagnostic("55000", `cannot refresh materialized view "public.refresh_mv" concurrently`)
							want.Hint = "Create a unique index with no WHERE clause on one or more columns of the materialized view."
						case "refresh_no":
							want = fixture.ControlsReferenceDiagnostic("0A000", "CONCURRENTLY cannot be used when the materialized view is not populated")
						case "refresh_missing":
							want = fixture.ControlsReferenceDiagnostic("42P01", `relation "refresh_missing" does not exist`)
						}
					}
					s := fixture.ErrorDiagnostic(q, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared && !b.PG {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
				for _, target := range []string{"refresh_mv", "refresh_no"} {
					want := fixture.ControlsReferenceDiagnostic("0A000", "concurrent materialized view refresh is unsupported")
					want.Position = "27"
					if b.PG {
						if target == "refresh_no" {
							want = fixture.ControlsReferenceDiagnostic("0A000", "CONCURRENTLY cannot be used when the materialized view is not populated")
						} else {
							want = fixture.ControlsReferenceDiagnostic("42601", "CONCURRENTLY and WITH NO DATA options cannot be used together")
						}
					}
					s := fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW CONCURRENTLY "+target+" WITH NO DATA", want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared && !b.PG {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
			for _, tc := range []struct {
				suffix, token string
				position      int
			}{
				{"refresh_mv,refresh_no", ",", 37},
				{"refresh_mv(1)", "(", 37},
				{"refresh_mv WITH DATA extra", "extra", 48},
				{"IF EXISTS refresh_mv", "EXISTS", 30},
			} {
				want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+tc.token+`"`)
				want.Position = fixture.Position(tc.position)
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW "+tc.suffix, want)
					s.Prepared = prepared
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
			run(materializedViewBag("refresh_mv"))
			run(fixture.MaterializedViewUnpopulated("refresh_no"))
			for _, q := range []string{"INSERT INTO refresh_mv VALUES(9,'x')", "UPDATE refresh_mv SET i=9", "DELETE FROM refresh_mv"} {
				run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42809", `cannot change materialized view "refresh_mv"`)))
			}
		})
	}
}
