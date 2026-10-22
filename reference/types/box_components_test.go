package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBoxComponents(t *testing.T) {
	boxScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE box_components(id integer,b box)", 0))
		run(fixture.Exec("INSERT INTO box_components VALUES(1,'(3,4),(1,2)'),(2,NULL)", 2))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				expr string
				want any
			}{
				{"b[0]", "(3,4)"}, {"b[1]", "(1,2)"}, {"b[-1]", nil}, {"b[2]", nil}, {"b[NULL]", nil}, {"b[0][0]", nil},
			} {
				s := boxSelect(b, "SELECT "+tc.expr+" AS p FROM box_components ORDER BY id", []string{"p"}, []string{"POINT"}, [][]any{{tc.want}, {nil}})
				s.Prepared = prepared
				run(s)
			}
			s := boxSelect(b, "SELECT b[0] AS p FROM box_components WHERE FALSE", []string{"p"}, []string{"POINT"}, nil)
			s.Prepared = prepared
			run(s)
			for _, q := range []string{"SELECT (NULL::box)[0:1] AS b", "SELECT (box '3,4,1,2')[NULL:1] AS b"} {
				s := boxSelect(b, q, []string{"b"}, []string{"BOX"}, [][]any{{nil}})
				s.Prepared = prepared
				run(s)
			}
		}
		q := "SELECT b[$1] AS p FROM box_components WHERE id=1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct{ index, want any }{{0, "(3,4)"}, {1, "(1,2)"}, {-1, nil}, {2, nil}, {nil, nil}, {0, "(3,4)"}} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, boxSelect(b, q, []string{"p"}, []string{"POINT"}, [][]any{{tc.want}}, tc.index))
		}
		check := func(value, high, low string) {
			t.Helper()
			s := boxSelect(b, "SELECT id,b,b[0] AS hi,b[1] AS lo FROM box_components ORDER BY id", []string{"id", "b", "hi", "lo"}, []string{"INT4", "BOX", "POINT", "POINT"}, [][]any{{int64(1), value, high, low}, {int64(2), nil, nil, nil}})
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("UPDATE box_components SET b[0]='(-5,-6)'::point WHERE id=1", 1))
		check("(-5,-6),(1,2)", "(-5,-6)", "(1,2)")
		update := "UPDATE box_components SET b[$1]=$2::point WHERE id=$3"
		upd, err := c.PrepareContext(p.Ctx, update)
		if err != nil {
			t.Fatal(err)
		}
		defer upd.Close()
		held := fixture.HeldStatement{Executor: c, Statement: upd, SQL: update}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(update, 1, 1, "(8,9)", 1))
		check("(-5,-6),(8,9)", "(-5,-6)", "(8,9)")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(update, 1, 0, nil, 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(update, 1, 0, "(8,9)", 2))
		check("(-5,-6),(8,9)", "(-5,-6)", "(8,9)")
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{
				"UPDATE box_components SET b[0:1]=NULL::box WHERE id=1",
				"UPDATE box_components SET b[0:1]='3,4,1,2'::box WHERE id=2",
			} {
				s := fixture.Exec(query, 1)
				s.Prepared = prepared
				run(s)
				check("(-5,-6),(8,9)", "(-5,-6)", "(8,9)")
			}
		}
		for _, tc := range []struct{ query, code, message string }{
			{"UPDATE box_components SET b[-1]='(0,0)'::point WHERE id=1", "2202E", "array subscript out of range"},
			{"UPDATE box_components SET b[2]='(0,0)'::point WHERE id=1", "2202E", "array subscript out of range"},
			{"UPDATE box_components SET b[0][0]='(0,0)'::point WHERE id=1", "2202E", "wrong number of array subscripts"},
			{"UPDATE box_components SET b[NULL]='(0,0)'::point WHERE id=1", "22004", "array subscript in assignment must not be null"},
			{"UPDATE box_components SET b[NULL]=NULL::point WHERE id=2", "22004", "array subscript in assignment must not be null"},
			{"SELECT (box '3,4,1,2')[0:1]", "0A000", "slices of fixed-length arrays not implemented"},
			{"UPDATE box_components SET b[0:1]='3,4,1,2'::box WHERE id=1", "0A000", "updates on slices of fixed-length arrays not implemented"},
		} {
			for _, prepared := range []bool{false, true} {
				run(boxError(tc.query, tc.code, tc.message, "", "", prepared, true))
				check("(-5,-6),(8,9)", "(-5,-6)", "(8,9)")
			}
		}
		for _, tc := range []struct{ query, code, message, token string }{
			{"SELECT (box '3,4,1,2')[TRUE]", "42804", "array subscript must have type integer", "TRUE"},
			{"SELECT (box '3,4,1,2')[0][0][0][0][0][0][0]", "54000", "number of array dimensions (7) exceeds the maximum allowed (6)", ""},
			{"UPDATE box_components SET b[0]='bad'::point", "22P02", "invalid input syntax for type point: \"bad\"", "'bad'"},
		} {
			for _, prepared := range []bool{false, true} {
				position := ""
				if tc.token != "" {
					position = fixture.Position(strings.Index(tc.query, tc.token) + 1)
				}
				run(boxError(tc.query, tc.code, tc.message, position, "", prepared, false))
			}
		}
		// Both candidate rows are non-NULL: id1 succeeds first, id3 fails.
		// NULL-base no-op behavior cannot accidentally mask the atomicity probe.
		run(fixture.Exec("INSERT INTO box_components VALUES(3,'4,5,2,3')", 1))
		for _, prepared := range []bool{false, true} {
			run(boxError("UPDATE box_components SET b[id]='(0,0)'::point WHERE id<>2", "2202E", "array subscript out of range", "", "", prepared, true))
			run(boxSelect(b, "SELECT id,b FROM box_components ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOX"}, [][]any{{int64(1), "(-5,-6),(8,9)"}, {int64(2), nil}, {int64(3), "(4,5),(2,3)"}}))
		}
		run(fixture.Exec("DELETE FROM box_components WHERE id=3", 1))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("UPDATE box_components SET b[0]='(10,11)'::point,b[1]='(12,13)'::point WHERE id=1", 1))
		check("(10,11),(12,13)", "(10,11)", "(12,13)")
		run(fixture.Exec("ROLLBACK", 0))
		check("(-5,-6),(8,9)", "(-5,-6)", "(8,9)")
	})
}
