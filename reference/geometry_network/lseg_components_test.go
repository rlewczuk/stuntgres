package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLsegComponents(t *testing.T) {
	lsegScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE lseg_components(id integer,s lseg)", 0))
		run(fixture.Exec("INSERT INTO lseg_components VALUES(1,'1,4,3,2'),(2,NULL)", 2))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				expr string
				want any
			}{{"s[0]", "(1,4)"}, {"s[1]", "(3,2)"}, {"s[-1]", nil}, {"s[2]", nil}, {"s[NULL]", nil}, {"s[0][0]", nil}} {
				s := boxSelect(b, "SELECT "+tc.expr+" AS p FROM lseg_components ORDER BY id", []string{"p"}, []string{"POINT"}, [][]any{{tc.want}, {nil}})
				s.Prepared = prepared
				run(s)
			}
			nested := fixture.Select("SELECT (s[0])[1] AS y FROM lseg_components ORDER BY id", []string{"y"}, []string{"FLOAT8"}, [][]any{{float64(4)}, {nil}})
			nested.Prepared = prepared
			run(nested)
			s := boxSelect(b, "SELECT s[0] AS p FROM lseg_components WHERE FALSE", []string{"p"}, []string{"POINT"}, nil)
			s.Prepared = prepared
			run(s)
			for _, q := range []string{"SELECT (NULL::lseg)[0:1] AS s", "SELECT (lseg '1,4,3,2')[NULL:1] AS s"} {
				s := boxSelect(b, q, []string{"s"}, []string{"LSEG"}, [][]any{{nil}})
				s.Prepared = prepared
				run(s)
			}
		}
		q := "SELECT s[$1] AS p FROM lseg_components WHERE id=1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct{ index, want any }{{0, "(1,4)"}, {1, "(3,2)"}, {-1, nil}, {2, nil}, {nil, nil}, {0, "(1,4)"}} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, boxSelect(b, q, []string{"p"}, []string{"POINT"}, [][]any{{tc.want}}, tc.index))
		}
		check := func(value string) {
			t.Helper()
			s := boxSelect(b, "SELECT id,s FROM lseg_components ORDER BY id", []string{"id", "s"}, []string{"INT4", "LSEG"}, [][]any{{int64(1), value}, {int64(2), nil}})
			run(s)
			run(fixture.Prepared(s))
		}
		// Native cursors retain the old immutable payload across endpoint writes.
		var retained *sql.Rows
		if !b.PG {
			retained, err = c.QueryContext(p.Ctx, "SELECT id,s FROM lseg_components ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		run(fixture.Exec("UPDATE lseg_components SET s[0]='(-5,-6)'::point WHERE id=1", 1))
		check("[(-5,-6),(3,2)]")
		update := "UPDATE lseg_components SET s[$1]=$2::point WHERE id=$3"
		upd, err := c.PrepareContext(p.Ctx, update)
		if err != nil {
			t.Fatal(err)
		}
		defer upd.Close()
		held := fixture.HeldStatement{Executor: c, Statement: upd, SQL: update}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(update, 1, 1, "(8,9)", 1))
		check("[(-5,-6),(8,9)]")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(update, 1, 0, nil, 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(update, 1, 0, "(8,9)", 2))
		check("[(-5,-6),(8,9)]")
		if retained != nil {
			lsegConsumeRetained(t, retained, [][]any{{int64(1), "[(1,4),(3,2)]"}, {int64(2), nil}})
		}
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"UPDATE lseg_components SET s[0:1]=NULL::lseg WHERE id=1", "UPDATE lseg_components SET s[0:1]='1,4,3,2'::lseg WHERE id=2"} {
				s := fixture.Exec(q, 1)
				s.Prepared = prepared
				run(s)
				check("[(-5,-6),(8,9)]")
			}
			for _, tc := range []struct{ query, code, message string }{
				{"UPDATE lseg_components SET s[-1]='(0,0)'::point WHERE id=1", "2202E", "array subscript out of range"},
				{"UPDATE lseg_components SET s[2]='(0,0)'::point WHERE id=1", "2202E", "array subscript out of range"},
				{"UPDATE lseg_components SET s[0][0]='(0,0)'::point WHERE id=1", "2202E", "wrong number of array subscripts"},
				{"UPDATE lseg_components SET s[NULL]='(0,0)'::point WHERE id=1", "22004", "array subscript in assignment must not be null"},
				{"UPDATE lseg_components SET s[NULL]=NULL::point WHERE id=2", "22004", "array subscript in assignment must not be null"},
				{"SELECT (lseg '1,4,3,2')[0:1]", "0A000", "slices of fixed-length arrays not implemented"},
				{"UPDATE lseg_components SET s[0:1]='1,4,3,2'::lseg WHERE id=1", "0A000", "updates on slices of fixed-length arrays not implemented"},
			} {
				run(boxError(tc.query, tc.code, tc.message, "", "", prepared, true))
				check("[(-5,-6),(8,9)]")
			}
			for _, tc := range []struct{ query, code, message, token string }{
				{"SELECT (lseg '1,4,3,2')[TRUE]", "42804", "array subscript must have type integer", "TRUE"},
				{"SELECT (lseg '1,4,3,2')[0][0][0][0][0][0][0]", "54000", "number of array dimensions (7) exceeds the maximum allowed (6)", ""},
				{"UPDATE lseg_components SET s[0]='bad'::point", "22P02", "invalid input syntax for type point: \"bad\"", "'bad'"},
			} {
				pos := ""
				if tc.token != "" {
					pos = fixture.Position(strings.Index(tc.query, tc.token) + 1)
				}
				run(boxError(tc.query, tc.code, tc.message, pos, "", prepared, false))
			}
		}
		run(fixture.Exec("INSERT INTO lseg_components VALUES(3,'4,5,2,3')", 1))
		for _, prepared := range []bool{false, true} {
			run(boxError("UPDATE lseg_components SET s[id]='(0,0)'::point WHERE id<>2", "2202E", "array subscript out of range", "", "", prepared, true))
			run(boxSelect(b, "SELECT id,s FROM lseg_components ORDER BY id", []string{"id", "s"}, []string{"INT4", "LSEG"}, [][]any{{int64(1), "[(-5,-6),(8,9)]"}, {int64(2), nil}, {int64(3), "[(4,5),(2,3)]"}}))
		}
		run(fixture.Exec("DELETE FROM lseg_components WHERE id=3", 1))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("UPDATE lseg_components SET s[0]='(10,11)'::point,s[1]='(12,13)'::point WHERE id=1", 1))
		check("[(10,11),(12,13)]")
		run(fixture.Exec("ROLLBACK", 0))
		check("[(-5,-6),(8,9)]")
	})
}
