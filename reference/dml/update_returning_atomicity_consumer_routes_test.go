package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningOutputAtomicityConsumerRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, exec := range []bool{false, true} {
			name := "query"
			if exec {
				name = "exec"
			}
			if prepared {
				name += "/prepared"
			}
			t.Run(name, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					db := backend.DB
					fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
					fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1),(0),(2)")
					query := "UPDATE t SET id=id+1 RETURNING 10/old.id"
					var stmt *sql.Stmt
					var err error
					if prepared {
						stmt, err = db.Prepare(query)
						if err != nil {
							t.Fatal("Prepare evaluated output", err)
						}
						defer func() {
							if err := stmt.Close(); err != nil {
								t.Error("statement close", err)
							}
						}()
					}
					if exec {
						var result sql.Result
						if prepared {
							result, err = stmt.Exec()
						} else {
							result, err = db.Exec(query)
						}
						if result != nil {
							t.Fatal("failure exposed count", result)
						}
					} else {
						var rows *sql.Rows
						if prepared {
							rows, err = stmt.Query()
						} else {
							rows, err = db.Query(query)
						}
						if rows != nil {
							rows.Close()
							t.Fatal("failure exposed cursor")
						}
					}
					fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					rows, err := db.Query("SELECT id FROM t ORDER BY id")
					fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(0)}, {int64(1)}, {int64(2)}}, Bag: false})
				}
			})
		}
	}
}

func TestReferenceUpdateReturningSequenceDemandConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		// Keep currval and transactional draws on the same physical session.
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1),(2),(3)")
		fixture.UpdateReturningReferenceExec(t, db, "CREATE SEQUENCE s START 101 CACHE 1")
		stmt, err := db.Prepare("UPDATE t SET id=id+1 RETURNING nextval('s')")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error("statement close", err)
			}
		}()
		var called bool
		if err = db.QueryRow("SELECT is_called FROM s").Scan(&called); err != nil || called {
			t.Fatal("Prepare drew", called, err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		rows, err := tx.Query("UPDATE t SET id=id+1 RETURNING nextval('s')")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(101)}, {int64(102)}, {int64(103)}}, Bag: true})
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		var next int64
		if err = db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 104 {
			t.Fatal(next, err)
		}
		rows, err = db.Query("SELECT id FROM t ORDER BY id")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(1)}, {int64(2)}, {int64(3)}}, Bag: false})
		rows, err = db.Query("UPDATE t SET id=id+1 WHERE FALSE RETURNING nextval('s')")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{}, Bag: false})
		if err = db.QueryRow("SELECT currval('s')").Scan(&next); err != nil || next != 104 {
			t.Fatal("no-match drew", next, err)
		}
	}
}
