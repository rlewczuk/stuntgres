package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePathComparison(t *testing.T) {
	pathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for i, op := range []string{"=", "<", ">", "<=", ">="} {
			q := "SELECT $1::path " + op + " $2::path AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			cases := []struct {
				left, right any
				want        any
			}{
				{"[1,2]", "[(3,4),(5,6)]", []bool{false, true, false, true, false}[i]},
				{"[(3,4),(5,6)]", "[1,2]", []bool{false, false, true, false, true}[i]},
				{"[(1,2),(3,4)]", "((99,98),(97,96))", []bool{true, false, false, true, true}[i]},
				{nil, "[1,2]", nil}, {"[1,2]", nil, nil}, {nil, nil, nil},
			}
			for _, tc := range cases {
				s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.left, tc.right)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			stmt.Close()
			literal := fixture.Select("SELECT path '[1,2]' "+op+" '[(3,4),(5,6)]' AS v", []string{"v"}, []string{"BOOL"}, [][]any{{[]bool{false, true, false, true, false}[i]}})
			run(literal)
			run(fixture.Prepared(literal))
		}
		run(fixture.Exec("CREATE TABLE path_compare(id integer,p path)", 0))
		for _, op := range []string{"<>", "!=", "~="} {
			for _, q := range []string{"SELECT path '[1,2]' " + op + " path '[3,4]'", "SELECT NULL::path " + op + " NULL::path", "SELECT p " + op + " p FROM path_compare WHERE FALSE"} {
				name := op
				if name == "!=" {
					name = "<>"
				}
				for _, prepared := range []bool{false, true} {
					run(boxError(q, "42883", "operator does not exist: path "+name+" path", fixture.Position(strings.Index(q, " "+op+" ")+2), "No operator matches the given name and argument types. You might need to add explicit type casts.", prepared, false))
				}
			}
		}
		for _, population := range []string{"", "INSERT INTO path_compare VALUES(1,NULL)", "INSERT INTO path_compare VALUES(2,'[1,2]'),(3,'(3,4)')"} {
			if population != "" {
				n := int64(1)
				if strings.Contains(population, "(2,") {
					n = 2
				}
				run(fixture.Exec(population, n))
			}
			for _, tc := range []struct{ q, code, message, hint, token string }{
				{"SELECT p FROM path_compare ORDER BY p", "42883", "could not identify an ordering operator for type path", "Use an explicit ordering operator or modify the query.", "p"},
				{"SELECT DISTINCT p FROM path_compare", "42883", "could not identify an equality operator for type path", "", "p"},
				{"SELECT p FROM path_compare GROUP BY p", "42883", "could not identify an equality operator for type path", "", "p"},
				{"CREATE INDEX path_btree ON path_compare(p)", "42704", "data type path has no default operator class for access method \"btree\"", "You must specify an operator class for the index or define a default operator class for the data type.", ""},
				{"CREATE INDEX path_hash ON path_compare USING hash(p)", "42704", "data type path has no default operator class for access method \"hash\"", "You must specify an operator class for the index or define a default operator class for the data type.", ""},
			} {
				pos := ""
				if tc.token != "" {
					pos = fixture.Position(strings.LastIndex(tc.q, tc.token) + 1)
					if strings.Contains(tc.q, "DISTINCT") {
						pos = fixture.Position(strings.Index(tc.q, " p ") + 2)
					}
				}
				for _, prepared := range []bool{false, true} {
					run(boxError(tc.q, tc.code, tc.message, pos, tc.hint, prepared, strings.HasPrefix(tc.q, "CREATE INDEX")))
				}
			}
		}
	})
}
