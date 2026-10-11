package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceInheritedVisibilityConsumerRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				db := backend.DB
				for _, q := range []string{
					"CREATE TABLE ids(id OID)", "INSERT INTO ids VALUES(23),(NULL)",
					"CREATE VIEW v AS SELECT FALSE AS visible FROM ids",
					"CREATE VIEW w AS SELECT visible FROM v", "CREATE VIEW z AS SELECT visible FROM w",
					"CREATE TABLE destination(visible BOOL)",
					"CREATE TABLE checked(visible BOOL CONSTRAINT known CHECK(visible IS NOT NULL))",
					"INSERT INTO checked VALUES(FALSE)",
				} {
					fixture.UpdateReturningReferenceExec(t, db, q)
				}
				heldStatement, err := db.Prepare("SELECT visible FROM z ORDER BY visible")
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := heldStatement.Close(); err != nil {
						t.Error(err)
					}
				}()
				heldRows, err := heldStatement.Query()
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := heldRows.Close(); err != nil {
						t.Error(err)
					}
				}()
				for phase, expression := range []string{"pg_type_is_visible(id)", "FALSE", "pg_type_is_visible(id)"} {
					fixture.UpdateReturningReferenceExec(t, db, "CREATE OR REPLACE VIEW v AS SELECT "+expression+" AS visible FROM ids")
					want := [][]any{{true}, {nil}}
					if expression == "FALSE" {
						want = [][]any{{false}, {false}}
					}
					for _, view := range []string{"v", "w", "z"} {
						rows, closeQuery, err := fixture.TransactionQuery(context.Background(), db, "SELECT visible FROM "+view+" ORDER BY visible", prepared)
						if closeQuery != nil {
							t.Cleanup(func() {
								if err := closeQuery(); err != nil {
									t.Error(err)
								}
							})
						}
						fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"visible"}, Types: []string{"BOOL"}, Rows: want, Bag: false})
						if closeQuery != nil {
							if err := closeQuery(); err != nil {
								t.Fatal(err)
							}
						}
					}
					rows, err := heldStatement.Query()
					fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"visible"}, Types: []string{"BOOL"}, Rows: want, Bag: false})
					fixture.UpdateReturningReferenceExec(t, db, "DELETE FROM destination")
					query := "INSERT INTO destination SELECT visible FROM z ORDER BY visible"
					if prepared {
						s, err := db.Prepare(query)
						if err != nil {
							t.Fatal(err)
						}
						r, err := s.Exec()
						if closeErr := s.Close(); closeErr != nil {
							t.Fatal(closeErr)
						}
						if err != nil {
							t.Fatal(err)
						}
						if count, err := r.RowsAffected(); err != nil || count != 2 {
							t.Fatal(count, err)
						}
					} else {
						r, err := db.Exec(query)
						if err != nil {
							t.Fatal(err)
						}
						if count, err := r.RowsAffected(); err != nil || count != 2 {
							t.Fatal(count, err)
						}
					}
					rows, err = db.Query("SELECT visible FROM destination ORDER BY visible")
					fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"visible"}, Types: []string{"BOOL"}, Rows: want, Bag: false})
					if expression != "FALSE" {
						query = "INSERT INTO checked SELECT visible FROM z ORDER BY visible"
						if prepared {
							s, prepareErr := db.Prepare(query)
							if prepareErr != nil {
								t.Fatal(prepareErr)
							}
							_, err = s.Exec()
							if closeErr := s.Close(); closeErr != nil {
								t.Fatal(closeErr)
							}
						} else {
							_, err = db.Exec(query)
						}
						fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "23514", Message: `new row for relation "checked" violates check constraint "known"`, Detail: "Failing row contains (null).", Schema: "public", Table: "checked", Constraint: "known"})
						rows, err := db.Query("SELECT visible FROM checked")
						fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"visible"}, Types: []string{"BOOL"}, Rows: [][]any{{false}}, Bag: false})
					}
					if phase == 0 {
						fixture.UpdateReturningReferenceExec(t, db, "CREATE VIEW late_dependent AS SELECT visible FROM v")
					}
					rows, err = db.Query("SELECT visible FROM late_dependent ORDER BY visible")
					fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"visible"}, Types: []string{"BOOL"}, Rows: want, Bag: false})
				}
				fixture.DeleteReferenceConsume(t, backend.PG, heldRows, nil, fixture.Step{Columns: []string{"visible"}, Types: []string{"BOOL"}, Rows: [][]any{{false}, {false}}, Bag: false})
			}
		})
	}
}

func TestReferenceDropViewIndexWrongKindConsumerRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{"DROP VIEW ix", "DROP VIEW IF EXISTS ix", "DROP VIEW v,ix"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", query, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					db := backend.DB
					for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE INDEX ix ON t(id)", "CREATE VIEW v AS SELECT id FROM t"} {
						fixture.UpdateReturningReferenceExec(t, db, q)
					}
					var before bytes.Buffer
					if !backend.PG {
						if err := stuntgres.Dump(context.Background(), p.Native, &before); err != nil {
							t.Fatal(err)
						}
					}
					oids := make(map[string]int64)
					for _, name := range []string{"t", "ix", "v"} {
						var oid int64
						if err := db.QueryRow("SELECT oid FROM pg_class WHERE relname=$1", name).Scan(&oid); err != nil {
							t.Fatal(err)
						}
						oids[name] = oid
					}
					var err error
					if prepared {
						s, prepareErr := db.Prepare(query)
						if prepareErr != nil {
							t.Fatal(prepareErr)
						}
						_, err = s.Exec()
						if closeErr := s.Close(); closeErr != nil {
							t.Fatal(closeErr)
						}
					} else {
						_, err = db.Exec(query)
					}
					fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "42809", Message: `"ix" is not a view`, Hint: "Use DROP INDEX to remove an index."})
					if !backend.PG {
						var after bytes.Buffer
						if err := stuntgres.Dump(context.Background(), p.Native, &after); err != nil {
							t.Fatal(err)
						}
						if before.String() != after.String() {
							t.Fatal("wrong-kind DROP changed persistent definitions/data")
						}
					}
					for name, oid := range oids {
						var retained int64
						if err := db.QueryRow("SELECT oid FROM pg_class WHERE relname=$1", name).Scan(&retained); err != nil || retained != oid {
							t.Fatal(name, retained, oid, err)
						}
					}
					rows, err := db.Query("SELECT id FROM t")
					fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(7)}}, Bag: false})
					rows, err = db.Query("SELECT id FROM v")
					fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(7)}}, Bag: false})
				}
			})
		}
	}
}
