package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePointComponents(t *testing.T) {
	pointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE point_components(id integer,p point)", 0))
		run(fixture.Exec("INSERT INTO point_components VALUES(1,'1,2'),(2,NULL)", 2))
		for _, tc := range []struct {
			expr string
			want any
		}{{"p[0]", float64(1)}, {"p[1]", float64(2)}, {"p[-1]", nil}, {"p[2]", nil}, {"p[NULL]", nil}, {"p[0][0]", nil}} {
			s := boxSelect(b, "SELECT "+tc.expr+" AS v FROM point_components ORDER BY id", []string{"v"}, []string{"FLOAT8"}, [][]any{{tc.want}, {nil}})
			run(s)
			run(fixture.Prepared(s))
		}
		// Reach the changed BOX/LSEG caller behavior during POINT-only discovery too.
		nested := fixture.Select("SELECT ((box '3,4,1,2')[0])[0] AS x,((lseg '1,4,3,2')[0])[1] AS y", []string{"x", "y"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(3), float64(4)}})
		run(nested)
		run(fixture.Prepared(nested))
		for _, q := range []string{"SELECT (NULL::point)[0:1] AS p", "SELECT (point '1,2')[NULL:1] AS p"} {
			s := boxSelect(b, q, []string{"p"}, []string{"POINT"}, [][]any{{nil}})
			run(s)
			run(fixture.Prepared(s))
		}
		q := "SELECT p[$1] AS v FROM point_components WHERE id=1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct{ index, want any }{{0, float64(1)}, {1, float64(2)}, {-1, nil}, {2, nil}, {nil, nil}, {0, float64(1)}} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"FLOAT8"}, [][]any{{tc.want}}, tc.index))
		}
		check := func(value string) {
			s := boxSelect(b, "SELECT id,p FROM point_components ORDER BY id", []string{"id", "p"}, []string{"INT4", "POINT"}, [][]any{{int64(1), value}, {int64(2), nil}})
			run(s)
			run(fixture.Prepared(s))
		}
		var retained *sql.Rows
		if !b.PG {
			retained, err = c.QueryContext(p.Ctx, "SELECT id,p FROM point_components ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		run(fixture.Exec("UPDATE point_components SET p[0]=-5 WHERE id=1", 1))
		check("(-5,2)")
		update := "UPDATE point_components SET p[$1]=$2::float8 WHERE id=$3"
		upd, err := c.PrepareContext(p.Ctx, update)
		if err != nil {
			t.Fatal(err)
		}
		defer upd.Close()
		held := fixture.HeldStatement{Executor: c, Statement: upd, SQL: update}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(update, 1, 1, 9, 1))
		check("(-5,9)")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(update, 1, 0, nil, 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(update, 1, 0, 8, 2))
		if retained != nil {
			fixture.PointConsumeRetained(t, retained, [][]any{{int64(1), "(1,2)"}, {int64(2), nil}})
		}
		for _, prepared := range []bool{false, true} {
			for _, assignment := range []string{"p[-1]=NULL::float8", "p[2]=NULL::float8", "p[0][0]=NULL::float8", "p[0:1]=NULL::point"} {
				s := fixture.Exec("UPDATE point_components SET "+assignment+" WHERE id=1", 1)
				s.Prepared = prepared
				run(s)
			}
			for _, assignment := range []string{"p[-1]=8", "p[2]=8", "p[0][0]=8", "p[0:1]=point '8,9'"} {
				s := fixture.Exec("UPDATE point_components SET "+assignment+" WHERE id=2", 1)
				s.Prepared = prepared
				run(s)
			}
			for _, tc := range []struct{ query, code, message string }{
				{"UPDATE point_components SET p[-1]=0 WHERE id=1", "2202E", "array subscript out of range"},
				{"UPDATE point_components SET p[2]=0 WHERE id=1", "2202E", "array subscript out of range"},
				{"UPDATE point_components SET p[0][0]=0 WHERE id=1", "2202E", "wrong number of array subscripts"},
				{"UPDATE point_components SET p[NULL]=0 WHERE id=1", "22004", "array subscript in assignment must not be null"},
				{"UPDATE point_components SET p[NULL]=NULL::float8 WHERE id=2", "22004", "array subscript in assignment must not be null"},
				{"SELECT (point '1,2')[0:1]", "0A000", "slices of fixed-length arrays not implemented"},
				{"UPDATE point_components SET p[0:1]=point '8,9' WHERE id=1", "0A000", "updates on slices of fixed-length arrays not implemented"},
			} {
				run(boxError(tc.query, tc.code, tc.message, "", "", prepared, true))
				check("(-5,9)")
			}
			bad := "SELECT (point '1,2')[TRUE]"
			run(boxError(bad, "42804", "array subscript must have type integer", fixture.Position(strings.Index(bad, "TRUE")+1), "", prepared, false))
		}
		run(fixture.Exec("INSERT INTO point_components VALUES(3,'3,4')", 1))
		for _, prepared := range []bool{false, true} {
			run(boxError("UPDATE point_components SET p[id]=0 WHERE id<>2", "2202E", "array subscript out of range", "", "", prepared, true))
			run(boxSelect(b, "SELECT id,p FROM point_components ORDER BY id", []string{"id", "p"}, []string{"INT4", "POINT"}, [][]any{{int64(1), "(-5,9)"}, {int64(2), nil}, {int64(3), "(3,4)"}}))
		}
		run(fixture.Exec("DELETE FROM point_components WHERE id=3", 1))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("UPDATE point_components SET p[0]=10,p[1]=11 WHERE id=1", 1))
		check("(10,11)")
		run(fixture.Exec("ROLLBACK", 0))
		check("(-5,9)")
	})
}
