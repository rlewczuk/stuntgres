package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePathTransport(t *testing.T) {
	pathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE path_transport(id integer,p path)", 0))
		q := "INSERT INTO path_transport(id,p) VALUES($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		cases := []struct{ arg, want any }{
			{"[(1,2),(3,4)]", "[(1,2),(3,4)]"},
			{[]byte("((1,2),(3,4))"), "((1,2),(3,4))"},
			{"[5,6]", "[(5,6)]"},
			{"[(1,2),(1,2),(3,4)]", "[(1,2),(1,2),(3,4)]"},
			{nil, nil}, {[]byte(nil), nil},
			{boxValuer{value: "(7,8)"}, "((7,8))"},
			{boxValuer{value: []byte("[9,10]")}, "[(9,10)]"},
			{boxValuer{value: nil}, nil},
		}
		var want [][]any
		for i, tc := range cases {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, i+1, tc.arg))
			want = append(want, []any{int64(i + 1), tc.want})
		}
		for _, prepared := range []bool{false, true} {
			for _, tc := range cases {
				s := pathSelect(b, "SELECT $1::path AS p", []string{"p"}, []string{"PATH"}, [][]any{{tc.want}}, tc.arg)
				s.Prepared = prepared
				run(s)
			}
		}
		run(pathSelect(b, "SELECT id,p FROM path_transport ORDER BY id", []string{"id", "p"}, []string{"INT4", "PATH"}, want))
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type path: \"\"")
		d.Context = "unnamed portal parameter $2 = ''"
		s := fixture.ErrorDiagnostic(q, d, 10, []byte{})
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 10, "(11,12)"))
		buf := []byte("[(13,14)]")
		rows, err := c.QueryContext(p.Ctx, "SELECT $1::path AS p", buf)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		copy(buf, "[(99,99)]")
		if !rows.Next() {
			t.Fatalf("retained row %v", rows.Err())
		}
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != "[(13,14)]" {
			t.Fatalf("caller mutation changed PATH %q", value)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatalf("retained rows %v", rows.Err())
		}
		rows.Close()
		sentinel := errors.New("path Valuer failure")
		_, err = stmt.ExecContext(p.Ctx, 11, boxValuer{err: sentinel})
		if err == nil || !strings.Contains(err.Error(), sentinel.Error()) {
			t.Fatalf("Valuer error %v", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatalf("Valuer acquired SQLSTATE %s", state.SQLState())
		}
		if !b.PG {
			cancelled, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err = stmt.ExecContext(cancelled, 11, "[1,2]")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-cancel %v", err)
			}
		}
		run(fixture.Select("SELECT count(*) AS n FROM path_transport", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
	})
}

func TestReferencePathMetadata(t *testing.T) {
	pathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE path_meta(p path NOT NULL,n path)", 0))
		run(fixture.Exec("INSERT INTO path_meta VALUES('[1,2]',NULL)", 1))
		for _, prepared := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				q, want := "SELECT p,n FROM path_meta", [][]any{{"[(1,2)]", nil}}
				if empty {
					q += " WHERE FALSE"
					want = nil
				}
				s := pathSelect(b, q, []string{"p", "n"}, []string{"PATH", "PATH"}, want)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					pathMetadata(t, b.PG, rows, []string{"PATH", "PATH"}, []bool{false, true})
				}
				run(s)
			}
			s := pathSelect(b, "SELECT NULL::path AS p", []string{"p"}, []string{"PATH"}, [][]any{{nil}})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Select("SELECT oid::bigint,typname,typlen,typbyval,typalign,typstorage,typtype,typcategory,typispreferred,typcollation::bigint,typelem::bigint,typarray::bigint,typsubscript::oid::bigint,typinput::oid::bigint,typoutput::oid::bigint,typreceive::oid::bigint,typsend::oid::bigint,typdelim FROM pg_type WHERE oid=602", []string{"oid", "typname", "typlen", "typbyval", "typalign", "typstorage", "typtype", "typcategory", "typispreferred", "typcollation", "typelem", "typarray", "typsubscript", "typinput", "typoutput", "typreceive", "typsend", "typdelim"}, []string{"INT8", "NAME", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "CHAR", "BOOL", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "CHAR"}, [][]any{{int64(602), "path", int64(-1), false, "d", "x", "b", "G", false, int64(0), int64(0), int64(1019), int64(0), int64(121), int64(122), int64(2482), int64(2483), ","}}))
		run(fixture.Select("SELECT oid::bigint,typname,typelem::bigint FROM pg_type WHERE oid=1019", []string{"oid", "typname", "typelem"}, []string{"INT8", "NAME", "INT8"}, [][]any{{int64(1019), "_path", int64(602)}}))
		run(fixture.Select("SELECT attname,atttypid::bigint,atttypmod,attcollation::bigint,attstorage FROM pg_attribute WHERE attrelid='path_meta'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "atttypmod", "attcollation", "attstorage"}, []string{"NAME", "INT8", "INT4", "INT8", "CHAR"}, [][]any{{"p", int64(602), int64(-1), int64(0), "x"}, {"n", int64(602), int64(-1), int64(0), "x"}}))
	})
}
