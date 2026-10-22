package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCircleBindingsMetadata(t *testing.T) {
	circleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE circle_bind(id integer NOT NULL,v circle,n circle NOT NULL)", 0))
		q := "INSERT INTO circle_bind VALUES ($1,$2,'0,0,1')"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		buf := []byte("(4,5),6")
		for i, v := range []any{"1,2,3", buf, nil, []byte(nil)} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, i+1, v))
		}
		copy(buf, []byte("(9,9),9"))
		want := [][]any{{int64(1), "<(1,2),3>"}, {int64(2), "<(4,5),6>"}, {int64(3), nil}, {int64(4), nil}}
		for _, prepared := range []bool{false, true} {
			s := circleSelect(b, "SELECT id,v FROM circle_bind ORDER BY id", []string{"id", "v"}, []string{"INT4", "CIRCLE"}, want)
			nullable := true
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { circleMetadata(t, b.PG, r, &nullable) }
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
		}
		for _, q := range []string{"SELECT v AS value FROM circle_bind WHERE id=0", "SELECT NULL::circle AS value", "SELECT v AS value FROM circle_bind WHERE id=3"} {
			rows := [][]any{{nil}}
			if q == "SELECT v AS value FROM circle_bind WHERE id=0" {
				rows = [][]any{}
			}
			s := circleSelect(b, q, []string{"value"}, []string{"CIRCLE"}, rows)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				nullable := true
				if q == "SELECT NULL::circle AS value" {
					circleMetadata(t, b.PG, r, nil)
				} else {
					circleMetadata(t, b.PG, r, &nullable)
				}
			}
			run(s)
		}
		notnull := false
		s := circleSelect(b, "SELECT n FROM circle_bind WHERE id=0", []string{"n"}, []string{"CIRCLE"}, [][]any{})
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) { circleMetadata(t, b.PG, r, &notnull) }
		run(s)
		run(fixture.Select("SELECT radius(NULL::circle) AS r", []string{"r"}, []string{"FLOAT8"}, [][]any{{nil}}))
		selectQ := "SELECT $1::circle AS v"
		selectStmt, err := c.PrepareContext(p.Ctx, selectQ)
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		selectHeld := fixture.HeldStatement{Executor: c, Statement: selectStmt, SQL: selectQ}
		for _, v := range []struct {
			input  any
			output any
		}{{"1,2,3", "<(1,2),3>"}, {[]byte("9,8,7"), "<(9,8),7>"}, {nil, nil}, {[]byte(nil), nil}} {
			fixture.RunStep(t, p.Ctx, selectHeld, b.Recorder, circleSelect(b, selectQ, []string{"v"}, []string{"CIRCLE"}, [][]any{{v.output}}, v.input))
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT oid::integer AS oid,typname,typtype::text AS kind,typcategory::text AS category,typlen,typbyval,typalign::text AS align,typstorage::text AS storage,typispreferred,typtypmod,typcollation::integer AS coll,typelem::integer AS elem,typarray::integer AS arr,typinput::oid::integer AS input,typoutput::oid::integer AS output,typreceive::oid::integer AS receive,typsend::oid::integer AS send FROM pg_type WHERE oid=718", []string{"oid", "typname", "kind", "category", "typlen", "typbyval", "align", "storage", "typispreferred", "typtypmod", "coll", "elem", "arr", "input", "output", "receive", "send"}, []string{"INT4", "NAME", "TEXT", "TEXT", "INT2", "BOOL", "TEXT", "TEXT", "BOOL", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(718), "circle", "b", "G", int64(24), false, "d", "p", false, int64(-1), int64(0), int64(0), int64(719), int64(1450), int64(1451), int64(2490), int64(2491)}})
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
			a := fixture.Select("SELECT attname,atttypid::integer AS oid,atttypmod,attcollation::integer AS coll,attnotnull FROM pg_attribute WHERE attrelid='circle_bind'::regclass AND attnum>1 ORDER BY attnum", []string{"attname", "oid", "atttypmod", "coll", "attnotnull"}, []string{"NAME", "INT4", "INT4", "INT4", "BOOL"}, [][]any{{"v", int64(718), int64(-1), int64(0), false}, {"n", int64(718), int64(-1), int64(0), true}})
			if prepared {
				a = fixture.Prepared(a)
			}
			run(a)
		}
	})
}
