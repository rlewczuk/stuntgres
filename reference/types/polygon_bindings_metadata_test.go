package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePolygonBindingsMetadata(t *testing.T) {
	polygonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE polygon_bind(id integer NOT NULL,v polygon,n polygon NOT NULL)", 0))
		q := "SELECT $1::polygon AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		// Native precancellation is an ordinary Go boundary, not a fabricated
		// PostgreSQL server/transport SQLSTATE. Shared held reuse follows.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, query := range []func() (*sql.Rows, error){
				func() (*sql.Rows, error) { return c.QueryContext(ctx, q, polygonTriangle) },
				func() (*sql.Rows, error) { return stmt.QueryContext(ctx, polygonTriangle) },
			} {
				rows, err := query()
				var state interface{ SQLState() string }
				if rows != nil || !errors.Is(err, context.Canceled) || errors.As(err, &state) {
					if rows != nil {
						rows.Close()
					}
					t.Fatal("cancellation crossed SQL boundary", err)
				}
			}
		}
		for _, tc := range []struct{ input, output any }{{polygonTriangle, polygonTriangle}, {[]byte(polygonRotated), polygonRotated}, {nil, nil}, {[]byte(nil), nil}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, polygonSelect(b, q, []string{"v"}, []string{"POLYGON"}, [][]any{{tc.output}}, tc.input))
		}
		for _, input := range []any{"bad", "", []byte{}} {
			text := ""
			if s, ok := input.(string); ok {
				text = s
			}
			d := polygonInputDiagnostic(text)
			d.Context = "unnamed portal parameter $1 = '...'"
			if text == "" {
				d.Context = "unnamed portal parameter $1 = ''"
			}
			s := fixture.ErrorDiagnostic(q, d, input)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, polygonSelect(b, q, []string{"v"}, []string{"POLYGON"}, [][]any{{polygonTriangle}}, polygonTriangle))
		}
		buf := []byte("0,0,1,1")
		run(fixture.Prepared(fixture.Exec("INSERT INTO polygon_bind VALUES(1,$1,'0,0')", 1, buf)))
		copy(buf, "9,9,9,9")
		run(fixture.Exec("INSERT INTO polygon_bind VALUES(2,NULL,'0,0')", 1))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q              string
				columns, types []string
				rows           [][]any
				declared       []bool
			}{
				{"SELECT id,v,n FROM polygon_bind ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "POLYGON", "POLYGON"}, [][]any{{int64(1), "((0,0),(1,1))", "((0,0))"}, {int64(2), nil, "((0,0))"}}, []bool{false, true, false}},
				{"SELECT v,n FROM polygon_bind WHERE id=0", []string{"v", "n"}, []string{"POLYGON", "POLYGON"}, [][]any{}, []bool{true, false}},
				{"SELECT NULL::polygon AS v", []string{"v"}, []string{"POLYGON"}, [][]any{{nil}}, nil},
				{"SELECT v FROM polygon_bind WHERE id=2", []string{"v"}, []string{"POLYGON"}, [][]any{{nil}}, []bool{true}},
			} {
				s := polygonSelect(b, tc.q, tc.columns, tc.types, tc.rows)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { polygonMetadata(t, b.PG, rows, tc.types, tc.declared) }
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
			}
			s := fixture.Select("SELECT oid::integer AS oid,typname,typlen,typbyval,typalign::text AS align,typstorage::text AS storage,typtype::text AS kind,typcategory::text AS category,typispreferred,typcollation::integer AS coll,typelem::integer AS elem,typarray::integer AS arr,typsubscript::oid::integer AS subscript,typinput::oid::integer AS input,typoutput::oid::integer AS output,typreceive::oid::integer AS receive,typsend::oid::integer AS send,typdelim::text AS delim,typtypmod FROM pg_type WHERE oid=604", []string{"oid", "typname", "typlen", "typbyval", "align", "storage", "kind", "category", "typispreferred", "coll", "elem", "arr", "subscript", "input", "output", "receive", "send", "delim", "typtypmod"}, []string{"INT4", "NAME", "INT2", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT", "INT4"}, [][]any{{int64(604), "polygon", int64(-1), false, "d", "x", "b", "G", false, int64(0), int64(0), int64(1027), int64(0), int64(347), int64(348), int64(2486), int64(2487), ",", int64(-1)}})
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
			a := fixture.Select("SELECT attname,atttypid::integer AS oid,atttypmod,attcollation::integer AS coll,attstorage::text AS storage FROM pg_attribute WHERE attrelid='polygon_bind'::regclass AND attnum>1 ORDER BY attnum", []string{"attname", "oid", "atttypmod", "coll", "storage"}, []string{"NAME", "INT4", "INT4", "INT4", "TEXT"}, [][]any{{"v", int64(604), int64(-1), int64(0), "x"}, {"n", int64(604), int64(-1), int64(0), "x"}})
			if prepared {
				a = fixture.Prepared(a)
			}
			run(a)
		}
		run(fixture.Select("SELECT 'path'::regtype::oid::integer AS path,'polygon'::regtype::oid::integer AS polygon", []string{"path", "polygon"}, []string{"INT4", "INT4"}, [][]any{{int64(602), int64(604)}}))
	})
}
