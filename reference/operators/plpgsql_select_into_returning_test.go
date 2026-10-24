package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSelectIntoReturning(t *testing.T) {
	t.Run("record_row_and_list", selectIntoReturningShapes)
	for _, producer := range []string{"insert", "update", "delete"} {
		for _, strict := range []bool{false, true} {
			for _, n := range []int{0, 1, 3} {
				t.Run(fmt.Sprintf("%s/strict_%t/rows_%d", producer, strict, n), func(t *testing.T) {
					plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						clause := ""
						if strict {
							clause = "STRICT "
						}
						query := "INSERT INTO si_dml SELECT id+10 FROM si_source WHERE id<=n RETURNING id"
						if producer == "update" {
							query = "UPDATE si_dml SET id=id+10 WHERE id<=n RETURNING id"
						}
						if producer == "delete" {
							query = "DELETE FROM si_dml WHERE id<=n RETURNING id"
						}
						userOperatorExec(t, p, b, c, "CREATE TABLE si_source(id integer)", "CREATE TABLE si_dml(id integer)",
							"CREATE FUNCTION si_dml_capture(n integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=99; status text:='ok'; BEGIN BEGIN "+query+" INTO "+clause+"x; EXCEPTION WHEN no_data_found THEN status:='P0002'; WHEN too_many_rows THEN status:='P0003'; END; RETURN status||':'||coalesce(x::text,'NULL')||':'||FOUND; END$$")
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO si_source VALUES(1),(2),(3)", 3))
						if producer != "insert" {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO si_dml VALUES(1),(2),(3)", 3))
						}
						want := "ok:NULL:false"
						if strict && n == 0 {
							want = "P0002:99:false"
						}
						if n == 1 {
							want = "ok:11:true"
							if producer == "delete" {
								want = "ok:1:true"
							}
						}
						if n == 3 {
							want = "P0003:99:true"
						}
						// Roll back each successful call too, so direct/prepared
						// routes see exactly the same input, without hiding the
						// function's own protected rollback on cardinality failure.
						for _, prepared := range []bool{false, true} {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
							s := fixture.Select("SELECT si_dml_capture($1) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{want}}, n)
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
							rows := [][]any{}
							if producer != "insert" {
								rows = [][]any{{int64(1)}, {int64(2)}, {int64(3)}}
							}
							if n == 1 {
								switch producer {
								case "insert":
									rows = [][]any{{int64(11)}}
								case "update":
									rows = [][]any{{int64(2)}, {int64(3)}, {int64(11)}}
								case "delete":
									rows = [][]any{{int64(2)}, {int64(3)}}
								}
							}
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM si_dml ORDER BY id", []string{"id"}, []string{"INT4"}, rows))
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
						}
					})
				})
			}
		}
	}
}

func selectIntoReturningShapes(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE si_return_shape(i integer,t text)",
			"CREATE FUNCTION si_return_shapes() RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; row_target si_return_shape%ROWTYPE; a integer; z text; s text; BEGIN INSERT INTO si_return_shape VALUES(7,'first') RETURNING i AS key,t AS label INTO STRICT r; s:=r.key||':'||r.label; UPDATE si_return_shape SET t='changed' RETURNING i,t INTO a,z; s:=s||':'||a||':'||z; DELETE FROM si_return_shape RETURNING i,t INTO row_target; s:=s||':'||row_target.i||':'||row_target.t; UPDATE si_return_shape SET i=9 RETURNING i AS fresh,t AS new_text INTO r; RETURN s||':'||coalesce(r.fresh::text,'NULL')||':'||coalesce(r.new_text,'NULL')||':'||FOUND; END$$")
		plpgsqlValue(t, p, b, c, "SELECT si_return_shapes() AS v", "TEXT", "7:first:7:changed:7:changed:NULL:NULL:false")
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM si_return_shape", "INT8", int64(0))
	})
}
