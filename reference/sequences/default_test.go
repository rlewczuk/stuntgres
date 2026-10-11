package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDefaultRegressionBeforeFix(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					record := backend.Recorder
					run := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, record, s)
					}
					run(fixture.Exec(`CREATE TABLE d(id integer, i integer DEFAULT 7, s text DEFAULT 'O''Brien', b boolean DEFAULT TRUE, n integer)`, 0))
					fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, `SELECT * FROM d`, []string{"id", "i", "s", "b", "n"}, []string{"INT4", "INT4", "TEXT", "BOOL", "INT4"}, []bool{true, true, true, true, true}, 0)
					run(fixture.Exec(`INSERT INTO d(id) VALUES(1),(2)`, 2))
					run(fixture.Exec(`INSERT INTO d VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT)`, 1))
					run(fixture.Exec(`INSERT INTO d(id,i) VALUES(4,NULL)`, 1))
					run(fixture.Exec(`INSERT INTO d VALUES(5)`, 1))
					run(fixture.Select(`SELECT * FROM d ORDER BY id`, []string{"id", "i", "s", "b", "n"}, []string{"INT4", "INT4", "TEXT", "BOOL", "INT4"}, [][]any{
						{int64(1), int64(7), "O'Brien", true, nil},
						{int64(2), int64(7), "O'Brien", true, nil},
						{int64(3), int64(7), "O'Brien", true, nil},
						{int64(4), nil, "O'Brien", true, nil},
						{int64(5), int64(7), "O'Brien", true, nil},
					}))
					run(fixture.Exec(`INSERT INTO d DEFAULT VALUES`, 1))
					bag := fixture.Select(`SELECT * FROM d`, []string{"id", "i", "s", "b", "n"}, []string{"INT4", "INT4", "TEXT", "BOOL", "INT4"}, [][]any{
						{int64(1), int64(7), "O'Brien", true, nil},
						{int64(2), int64(7), "O'Brien", true, nil},
						{int64(3), int64(7), "O'Brien", true, nil},
						{int64(4), nil, "O'Brien", true, nil},
						{int64(5), int64(7), "O'Brien", true, nil},
						{nil, int64(7), "O'Brien", true, nil},
					})
					bag.Bag = true
					run(bag)
					run(fixture.Exec(`CREATE TABLE z()`, 0))
					run(fixture.Exec(`INSERT INTO z DEFAULT VALUES`, 1))
					run(fixture.Exec(`CREATE TABLE all_defaults(i integer DEFAULT 7, s text DEFAULT 'O''Brien', b boolean DEFAULT TRUE, n integer)`, 0))
					run(fixture.Exec(`INSERT INTO all_defaults DEFAULT VALUES`, 1))
					run(fixture.Select(`SELECT * FROM all_defaults`, []string{"i", "s", "b", "n"}, []string{"INT4", "TEXT", "BOOL", "INT4"}, [][]any{{int64(7), "O'Brien", true, nil}}))
					invalidTargets := fixture.ImplicitError(`INSERT INTO all_defaults(i) DEFAULT VALUES`, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "DEFAULT"`, Position: "29"})
					invalidTargets.ErrorStage = "execute"
					if prepared {
						invalidTargets.ErrorStage = "prepare"
					}
					run(invalidTargets)
					run(fixture.Select(`SELECT * FROM all_defaults`, []string{"i", "s", "b", "n"}, []string{"INT4", "TEXT", "BOOL", "INT4"}, [][]any{{int64(7), "O'Brien", true, nil}}))
					invalidWidth := fixture.ImplicitError(`INSERT INTO d(id,i) VALUES(6)`, fixture.Diagnostic{Code: "42601", Message: "INSERT has more target columns than expressions", Position: "18"})
					invalidWidth.ErrorStage = "execute"
					if prepared {
						invalidWidth.ErrorStage = "prepare"
					}
					run(invalidWidth)
					run(fixture.Select(`SELECT * FROM z`, []string{}, []string{}, [][]any{{}}))
				})
			}
		})
	}
}

func TestReferenceInsertDefaultMixedParameters(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					record := backend.Recorder
					run := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, record, s)
					}
					run(fixture.Exec(`CREATE TABLE d(id INT,v INT DEFAULT 7,s TEXT DEFAULT 'fallback',n INT)`, 0))
					names := []string{"id", "v", "s", "n"}
					types := []string{"INT4", "INT4", "TEXT", "INT4"}
					run(fixture.Select(`SELECT * FROM d`, names, types, nil))
					fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, `SELECT * FROM d`, names, types, []bool{true, true, true, true}, 0)
					const insert = `INSERT INTO d(s,id,v) VALUES($1,$2,DEFAULT),(DEFAULT,$3,$4)`
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt, err = backend.DB.PrepareContext(p.Ctx, insert)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					for _, args := range [][]any{{"given", 1, 2, 9}, {"again", 3, 4, nil}} {
						var result sql.Result
						var err error
						if stmt != nil {
							result, err = stmt.ExecContext(p.Ctx, args...)
						} else {
							result, err = backend.DB.ExecContext(p.Ctx, insert, args...)
						}
						if err != nil {
							t.Fatal(insert, err)
						}
						if count, err := result.RowsAffected(); err != nil || count != 2 {
							t.Fatal("RowsAffected", count, err)
						}
					}
					run(fixture.Select(`SELECT * FROM d ORDER BY id`, names, types, [][]any{
						{int64(1), int64(7), "given", nil},
						{int64(2), int64(9), "fallback", nil},
						{int64(3), int64(7), "again", nil},
						{int64(4), nil, "fallback", nil},
					}))
					fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, `SELECT * FROM d ORDER BY id`, names, types, []bool{true, true, true, true}, 4)
				})
			}
		})
	}
}
