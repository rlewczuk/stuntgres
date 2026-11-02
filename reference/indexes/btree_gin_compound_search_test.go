package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINCompoundSearch(t *testing.T) {
	for _, tc := range []struct {
		name, sqltype, dbtype, class, boundary, lower, upper, away, detail string
		width                                                              int64
		input                                                              []any
		values                                                             []any
		initial, updated, deleted, final                                   [][]int64
		updateID, deleteID                                                 int64
	}{
		{name: "bit", sqltype: "bit(3)", dbtype: "BIT", class: "bit_ops", boundary: "100", lower: "000", upper: "111", away: "000", detail: "100", width: 3,
			input: []any{"000", "011", "100", "100", "111", nil}, values: []any{"000", "011", "100", "100", "111", nil}, updateID: 3, deleteID: 4,
			initial: [][]int64{{1, 2}, {1, 2, 3, 4}, {3, 4}, {3, 4, 5}, {5}, {1, 2, 5}},
			updated: [][]int64{{1, 2, 3}, {1, 2, 3, 4}, {4}, {4, 5}, {5}, {1, 2, 3, 5}},
			deleted: [][]int64{{1, 2, 3}, {1, 2, 3}, nil, {5}, {5}, {1, 2, 3, 5}},
			final:   [][]int64{{1, 2, 3}, {1, 2, 3, 9}, {9}, {5, 9}, {5}, {1, 2, 3, 5}}},
		{name: "varbit", sqltype: "varbit", dbtype: "VARBIT", class: "varbit_ops", boundary: "0", lower: "", upper: "10", away: "", detail: "0", width: -1,
			input: []any{"", "0", "00", "01", "1", "10", nil}, values: []any{"", "0", "00", "01", "1", "10", nil}, updateID: 2, deleteID: 8,
			initial: [][]int64{{1}, {1, 2}, {2}, {2, 3, 4, 5, 6}, {3, 4, 5, 6}, {1, 3, 4, 5, 6}},
			updated: [][]int64{{1, 2}, {1, 2, 8}, {8}, {3, 4, 5, 6, 8}, {3, 4, 5, 6}, {1, 2, 3, 4, 5, 6}},
			deleted: [][]int64{{1, 2}, {1, 2}, nil, {3, 4, 5, 6}, {3, 4, 5, 6}, {1, 2, 3, 4, 5, 6}},
			final:   [][]int64{{1, 2}, {1, 2, 9}, {9}, {3, 4, 5, 6, 9}, {3, 4, 5, 6}, {1, 2, 3, 4, 5, 6}}},
		{name: "enum", sqltype: "compound_search_enum", dbtype: "", class: "enum_ops", boundary: "a", lower: "z", upper: "m", away: "z", detail: "a",
			input: []any{"z", "q", "a", "a", "m", nil}, values: []any{"z", "q", "a", "a", "m", nil}, updateID: 3, deleteID: 4,
			initial: [][]int64{{1, 2}, {1, 2, 3, 4}, {3, 4}, {3, 4, 5}, {5}, {1, 2, 5}},
			updated: [][]int64{{1, 2, 3}, {1, 2, 3, 4}, {4}, {4, 5}, {5}, {1, 2, 3, 5}},
			deleted: [][]int64{{1, 2, 3}, {1, 2, 3}, nil, {5}, {5}, {1, 2, 3, 5}},
			final:   [][]int64{{1, 2, 3}, {1, 2, 3, 9}, {9}, {5, 9}, {5}, {1, 2, 3, 5}}},
		{name: "money", sqltype: "money", dbtype: "MONEY", class: "money_ops", boundary: "0.01", lower: "-92233720368547758.08", upper: "92233720368547758.07", away: "-92233720368547758.08", detail: "$0.01",
			input:  []any{"-92233720368547758.08", "-0.01", "0", "0.01", "0.01", "92233720368547758.07", nil},
			values: []any{"-$92,233,720,368,547,758.08", "-$0.01", "$0.00", "$0.01", "$0.01", "$92,233,720,368,547,758.07", nil}, updateID: 4, deleteID: 5,
			initial: [][]int64{{1, 2, 3}, {1, 2, 3, 4, 5}, {4, 5}, {4, 5, 6}, {6}, {1, 2, 3, 6}},
			updated: [][]int64{{1, 2, 3, 4}, {1, 2, 3, 4, 5}, {5}, {5, 6}, {6}, {1, 2, 3, 4, 6}},
			deleted: [][]int64{{1, 2, 3, 4}, {1, 2, 3, 4}, nil, {6}, {6}, {1, 2, 3, 4, 6}},
			final:   [][]int64{{1, 2, 3, 4}, {1, 2, 3, 4, 9}, {9}, {6, 9}, {6}, {1, 2, 3, 4, 6}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("SET lc_monetary='C'", 0))
				if tc.name == "enum" {
					run(fixture.Exec("CREATE TYPE compound_search_enum AS ENUM('z','a','m')", 0))
					run(fixture.Exec("ALTER TYPE compound_search_enum ADD VALUE 'q' BEFORE 'a'", 0))
				}
				run(fixture.Exec("CREATE TABLE compound_search(id integer CHECK(id>0),v "+tc.sqltype+")", 0))
				insertSQL := "INSERT INTO compound_search VALUES($1,$2::" + tc.sqltype + ")"
				insert := classHeld(t, p, conn, insertSQL)
				var values [][]any
				var all []int64
				for i, value := range tc.input {
					id := int64(i + 1)
					fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, id, value))
					values = append(values, []any{id, tc.values[i]})
					if value != nil {
						all = append(all, id)
					}
				}
				operators := []string{"<", "<=", "=", ">=", ">", "<>"}
				held := make([]fixture.HeldStatement, len(operators))
				queries := make([]string, len(operators))
				for i, op := range operators {
					queries[i] = "SELECT id FROM compound_search WHERE v " + op + " $1::" + tc.sqltype + " AND id >= $2 ORDER BY id"
					held[i] = classHeld(t, p, conn, queries[i])
				}
				check := func(want [][]int64) {
					t.Helper()
					for i, op := range operators {
						step := ginIDs(queries[i], want[i], tc.boundary, 0)
						classRoutes(t, p, b, conn, step)
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, step)
						// The same held statement must release the prior parameter datum and
						// return independently expected metadata even for no-match and NULL.
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(queries[i], nil, tc.boundary, 100))
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(queries[i], nil, nil, 0))
						classRoutes(t, p, b, conn, ginIDs(queries[i], nil, nil, 0))
						run(ginIDs("SELECT id FROM compound_search WHERE v "+op+" '"+tc.boundary+"'::"+tc.sqltype+" ORDER BY id", want[i]))
					}
				}
				check(tc.initial) // Scan, before installation/index publication.
				valueStep := compoundValueStep(b, "SELECT id,v FROM compound_search ORDER BY id", tc.dbtype, tc.width, values)
				classRoutes(t, p, b, conn, valueStep)
				classRoutes(t, p, b, conn, compoundValueStep(b, "SELECT id,v FROM compound_search WHERE false", tc.dbtype, tc.width, nil))
				run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
				run(fixture.Exec("CREATE INDEX compound_search_i ON compound_search USING gin(v "+tc.class+")", 0))
				run(fixture.Prepared(fixture.Exec("CREATE INDEX compound_search_default ON compound_search USING gin(v)", 0)))
				check(tc.initial)
				classRoutes(t, p, b, conn, valueStep)
				run(ginIDs("SELECT id FROM compound_search WHERE v IS NULL ORDER BY id", []int64{int64(len(tc.input))}))
				run(ginIDs("SELECT id FROM compound_search WHERE v<'"+tc.lower+"'::"+tc.sqltype+" ORDER BY id", nil))
				run(ginIDs("SELECT id FROM compound_search WHERE v>'"+tc.upper+"'::"+tc.sqltype+" ORDER BY id", nil))
				run(ginIDs("SELECT id FROM compound_search WHERE v>='"+tc.lower+"'::"+tc.sqltype+" ORDER BY id", all))
				run(ginIDs("SELECT id FROM compound_search WHERE v<='"+tc.upper+"'::"+tc.sqltype+" ORDER BY id", all))
				if tc.name == "bit" {
					run(ginIDs("SELECT id FROM compound_search WHERE v='001'::bit(3) ORDER BY id", nil))
				}
				if tc.name == "varbit" {
					run(ginIDs("SELECT id FROM compound_search WHERE v=''::varbit ORDER BY id", []int64{1}))
					run(ginIDs("SELECT id FROM compound_search WHERE v='000'::varbit ORDER BY id", nil))
					fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 8, tc.boundary))
					check([][]int64{{1}, {1, 2, 8}, {2, 8}, {2, 3, 4, 5, 6, 8}, {3, 4, 5, 6}, {1, 3, 4, 5, 6}})
				}
				if tc.name == "money" {
					classRoutes(t, p, b, conn, fixture.Select("SELECT v::numeric::text AS decimal FROM compound_search ORDER BY id", []string{"decimal"}, []string{"TEXT"}, [][]any{{"-92233720368547758.08"}, {"-0.01"}, {"0.00"}, {"0.01"}, {"0.01"}, {"92233720368547758.07"}, {nil}}))
				}
				updateSQL := "UPDATE compound_search SET v=$1::" + tc.sqltype + " WHERE id=$2"
				update := classHeld(t, p, conn, updateSQL)
				fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.Exec(updateSQL, 1, tc.away, tc.updateID))
				check(tc.updated)
				run(fixture.Prepared(fixture.Exec("DELETE FROM compound_search WHERE id=$1", 1, tc.deleteID)))
				check(tc.deleted)
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 9, tc.boundary))
				check(tc.final)
				// A valid first tuple followed by a rejected tuple may publish neither.
				bad := "INSERT INTO compound_search VALUES(90,$1::" + tc.sqltype + "),(-1,$1::" + tc.sqltype + ")"
				d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "compound_search" violates check constraint "compound_search_id_check"`)
				d.Detail = "Failing row contains (-1, " + tc.detail + ")."
				d.Schema = "public"
				d.Table = "compound_search"
				d.Constraint = "compound_search_id_check"
				direct := fixture.ErrorDiagnostic(bad, d, tc.boundary)
				direct.ErrorStage = "execute"
				classRoutes(t, p, b, conn, direct)
				run(ginIDs("SELECT id FROM compound_search WHERE id=90 ORDER BY id", nil))
				check(tc.final)
				// SQL savepoint/rollback and Go transaction commit share the indexed lane.
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT compound_before", 0))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 10, tc.boundary))
				run(ginIDs("SELECT id FROM compound_search WHERE id=10 AND v=$1::"+tc.sqltype+" ORDER BY id", []int64{10}, tc.boundary))
				run(fixture.Exec("ROLLBACK TO compound_before", 0))
				run(ginIDs("SELECT id FROM compound_search WHERE id=10 ORDER BY id", nil))
				run(fixture.Exec("COMMIT", 0))
				tx, err := conn.BeginTx(p.Ctx, &sql.TxOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(insertSQL, 1, 10, tc.boundary))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, ginIDs("SELECT id FROM compound_search WHERE id=10 ORDER BY id", []int64{10}))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, ginIDs("SELECT id FROM compound_search WHERE id=10 ORDER BY id", nil))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				run(ginIDs("SELECT id FROM compound_search WHERE id=10 ORDER BY id", []int64{10}))
				run(fixture.Exec("DELETE FROM compound_search WHERE id=10", 1))
				check(tc.final)
			})
		})
	}
}
