//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDefaultRegressionBeforeFix(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			// Separate backend subtests ensure the independent PostgreSQL witness
			// runs even while the native CREATE boundary still fails.
			for _, backend := range []struct {
				name string
				db   *sql.DB
			}{{"native", p.memory}, {"postgres", p.postgres}} {
				t.Run(backend.name, func(t *testing.T) {
					record := &referenceRecorder{}
					run := func(s referenceStep) {
						s.prepared = prepared
						referenceRunStep(t, p.ctx, backend.db, record, s)
					}
					run(referenceExec(`CREATE TABLE d(id integer, i integer DEFAULT 7, s text DEFAULT 'O''Brien', b boolean DEFAULT TRUE, n integer)`, 0))
					run(referenceExec(`INSERT INTO d(id) VALUES(1),(2)`, 2))
					run(referenceExec(`INSERT INTO d VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT)`, 1))
					run(referenceExec(`INSERT INTO d(id,i) VALUES(4,NULL)`, 1))
					run(referenceExec(`INSERT INTO d VALUES(5)`, 1))
					run(referenceSelect(`SELECT * FROM d ORDER BY id`, []string{"id", "i", "s", "b", "n"}, []string{"INT4", "INT4", "TEXT", "BOOL", "INT4"}, [][]any{
						{int64(1), int64(7), "O'Brien", true, nil},
						{int64(2), int64(7), "O'Brien", true, nil},
						{int64(3), int64(7), "O'Brien", true, nil},
						{int64(4), nil, "O'Brien", true, nil},
						{int64(5), int64(7), "O'Brien", true, nil},
					}))
					run(referenceExec(`INSERT INTO d DEFAULT VALUES`, 1))
					bag := referenceSelect(`SELECT * FROM d`, []string{"id", "i", "s", "b", "n"}, []string{"INT4", "INT4", "TEXT", "BOOL", "INT4"}, [][]any{
						{int64(1), int64(7), "O'Brien", true, nil},
						{int64(2), int64(7), "O'Brien", true, nil},
						{int64(3), int64(7), "O'Brien", true, nil},
						{int64(4), nil, "O'Brien", true, nil},
						{int64(5), int64(7), "O'Brien", true, nil},
						{nil, int64(7), "O'Brien", true, nil},
					})
					bag.bag = true
					run(bag)
					run(referenceExec(`CREATE TABLE z()`, 0))
					run(referenceExec(`INSERT INTO z DEFAULT VALUES`, 1))
					run(referenceExec(`CREATE TABLE all_defaults(i integer DEFAULT 7, s text DEFAULT 'O''Brien', b boolean DEFAULT TRUE, n integer)`, 0))
					run(referenceExec(`INSERT INTO all_defaults DEFAULT VALUES`, 1))
					run(referenceSelect(`SELECT * FROM all_defaults`, []string{"i", "s", "b", "n"}, []string{"INT4", "TEXT", "BOOL", "INT4"}, [][]any{{int64(7), "O'Brien", true, nil}}))
					invalidTargets := referenceImplicitError(`INSERT INTO all_defaults(i) DEFAULT VALUES`, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "DEFAULT"`, Position: "29"})
					invalidTargets.errorStage = "execute"
					if prepared {
						invalidTargets.errorStage = "prepare"
					}
					run(invalidTargets)
					run(referenceSelect(`SELECT * FROM all_defaults`, []string{"i", "s", "b", "n"}, []string{"INT4", "TEXT", "BOOL", "INT4"}, [][]any{{int64(7), "O'Brien", true, nil}}))
					invalidWidth := referenceImplicitError(`INSERT INTO d(id,i) VALUES(6)`, referenceDiagnostic{Code: "42601", Message: "INSERT has more target columns than expressions", Position: "18"})
					invalidWidth.errorStage = "execute"
					if prepared {
						invalidWidth.errorStage = "prepare"
					}
					run(invalidWidth)
					run(referenceSelect(`SELECT * FROM z`, []string{}, []string{}, [][]any{{}}))
				})
			}
		})
	}
}

func TestReferenceInsertDefaultMixedParameters(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name string
				db   *sql.DB
			}{{"native", p.memory}, {"postgres", p.postgres}} {
				t.Run(backend.name, func(t *testing.T) {
					record := &referenceRecorder{}
					run := func(s referenceStep) {
						s.prepared = prepared
						referenceRunStep(t, p.ctx, backend.db, record, s)
					}
					run(referenceExec(`CREATE TABLE d(id INT,v INT DEFAULT 7,s TEXT DEFAULT 'fallback',n INT)`, 0))
					names := []string{"id", "v", "s", "n"}
					types := []string{"INT4", "INT4", "TEXT", "INT4"}
					run(referenceSelect(`SELECT * FROM d`, names, types, nil))
					const insert = `INSERT INTO d(s,id,v) VALUES($1,$2,DEFAULT),(DEFAULT,$3,$4)`
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt, err = backend.db.PrepareContext(p.ctx, insert)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					for _, args := range [][]any{{"given", 1, 2, 9}, {"again", 3, 4, nil}} {
						var result sql.Result
						var err error
						if stmt != nil {
							result, err = stmt.ExecContext(p.ctx, args...)
						} else {
							result, err = backend.db.ExecContext(p.ctx, insert, args...)
						}
						if err != nil {
							t.Fatal(insert, err)
						}
						if count, err := result.RowsAffected(); err != nil || count != 2 {
							t.Fatal("RowsAffected", count, err)
						}
					}
					run(referenceSelect(`SELECT * FROM d ORDER BY id`, names, types, [][]any{
						{int64(1), int64(7), "given", nil},
						{int64(2), int64(9), "fallback", nil},
						{int64(3), int64(7), "again", nil},
						{int64(4), nil, "fallback", nil},
					}))
				})
			}
		})
	}
}
