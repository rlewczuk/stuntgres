package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSVectorFunctionsCharArrayConsumers(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
				run(fixture.Exec(`CREATE TABLE fts_vf_chars(id integer,w "char"[] DEFAULT '{A,B,NULL}')`, 0))
				run(fixture.Exec(`INSERT INTO fts_vf_chars VALUES(1,$1::"char"[])`, 1, "[-1:0][3:4]={{AB,B},{NULL,D}}"))
				run(fixture.Exec(`INSERT INTO fts_vf_chars VALUES(2,$1::"char"[])`, 1, []byte("{}")))
				run(fixture.Exec(`INSERT INTO fts_vf_chars VALUES(3,$1::"char"[])`, 1, nil))
				run(fixture.Exec(`INSERT INTO fts_vf_chars(id) VALUES(4)`, 1))
				run(fixture.Exec(`CREATE VIEW fts_vf_chars_view AS SELECT id,w FROM fts_vf_chars`, 0))
				check := fixture.Select(`SELECT id,w FROM fts_vf_chars_view ORDER BY id`,
					[]string{"id", "w"}, []string{"INT4", "_CHAR"},
					[][]any{{int64(1), "[-1:0][3:4]={{A,B},{NULL,D}}"}, {int64(2), "{}"}, {int64(3), nil}, {int64(4), "{A,B,NULL}"}})
				check.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if len(columns) != 2 || columns[0].DatabaseTypeName() != "INT4" || columns[1].DatabaseTypeName() != "_CHAR" {
						t.Fatal("char array view metadata", columns)
					}
				}
				run(check)
				stmt, err := b.DB.PrepareContext(p.Ctx, `SELECT $1::"char"[] AS w`)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: `SELECT $1::"char"[] AS w`}
				for _, tc := range []struct{ input, want any }{
					{"[0:1]={AB,NULL}", "[0:1]={A,NULL}"},
					{[]byte("{D,B}"), "{D,B}"},
					{nil, nil},
					{"{}", "{}"},
					{"{A}", "{A}"},
				} {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL,
						[]string{"w"}, []string{"_CHAR"}, [][]any{{tc.want}}, tc.input))
				}
				if !b.PG {
					rangeControlCycle(t, p, b, []fixture.Step{check}, [][]bool{{true, true}}, nil)
				}
			})
		})
	}
}
