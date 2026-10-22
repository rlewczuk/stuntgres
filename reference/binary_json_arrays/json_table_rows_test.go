package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONTableColumns(t *testing.T) {
	jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(r FOR ORDINALITY,i int PATH '$.s',n int PATH '$.n',j jsonb PATH '$.n',e boolean EXISTS PATH '$.n',obj jsonb PATH '$.o',qs text FORMAT JSON PATH '$.s',raw text PATH '$.s',arr text FORMAT JSON PATH '$.a[*]' WITH WRAPPER))`
		s := fixture.Select(q, []string{"r", "i", "n", "j", "e", "obj", "qs", "raw", "arr"}, []string{"INT4", "INT4", "INT4", "JSONB", "BOOL", "JSONB", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), int64(123), nil, `null`, true, `{"x": 1}`, `"123"`, `123`, `[1, 2]`}}, `{"s":"123","n":null,"o":{"x":1},"a":[1,2]}`)
		sqljsonBoth(t, p, b, c, jsonTableMetadata(s, b.PG))
		for _, doc := range []any{nil, `[]`} {
			empty := s
			empty.Query = `SELECT * FROM JSON_TABLE($1::jsonb,'$[*]' COLUMNS(r FOR ORDINALITY,i int PATH '$.s',n int PATH '$.n',j jsonb PATH '$.n',e boolean EXISTS PATH '$.n',obj jsonb PATH '$.o',qs text FORMAT JSON PATH '$.s',raw text PATH '$.s',arr text FORMAT JSON PATH '$.a[*]' WITH WRAPPER))`
			empty.Args = []any{doc}
			empty.Rows = nil
			sqljsonBoth(t, p, b, c, jsonTableMetadata(empty, b.PG))
		}
		sqljsonBoth(t, p, b, c, jsonTableMetadata(fixture.Select(`SELECT * FROM JSON_TABLE('null','$' COLUMNS(r FOR ORDINALITY,n int PATH '$',j jsonb PATH '$',e boolean EXISTS PATH '$'))`, []string{"r", "n", "j", "e"}, []string{"INT4", "INT4", "JSONB", "BOOL"}, [][]any{{int64(1), nil, `null`, true}}), b.PG))
		sqljsonBoth(t, p, b, c, fixture.Select(`SELECT * FROM JSON_TABLE('{"Mixed":7,"a\"b":8,"x":[1,2],"s":"hi"}','$' COLUMNS("Mixed" int,"a""b" int,a int[] PATH '$.x',q text PATH '$.s' KEEP QUOTES,w text PATH '$.s' WITHOUT WRAPPER,e int EXISTS PATH '$.x',et text EXISTS PATH '$.missing'))`, []string{"Mixed", "a\"b", "a", "q", "w", "e", "et"}, []string{"INT4", "INT4", "_INT4", "TEXT", "TEXT", "INT4", "TEXT"}, [][]any{{int64(7), int64(8), `{1,2}`, `"hi"`, `"hi"`, int64(1), `false`}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TYPE jt_pair AS (x int)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jt_positive AS int CHECK(VALUE>0)`, 0))
		sqljsonBoth(t, p, b, c, fixture.Select(`SELECT a::text AS a,d,e FROM JSON_TABLE('{"o":{"x":3},"v":"abc"}','$' COLUMNS(a jt_pair PATH '$.o',d varchar(3) PATH '$.v',e jt_positive EXISTS PATH '$.o'))`, []string{"a", "d", "e"}, []string{"TEXT", "VARCHAR", "INT4"}, [][]any{{`(3)`, `abc`, int64(1)}}))
	})
}

func TestReferenceJSONTableNested(t *testing.T) {
	jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		s := fixture.Select(`SELECT * FROM JSON_TABLE('[{"kids":[{"v":[4,5]},{"v":[]}]},{"kids":[]}]','$[*]' COLUMNS(r FOR ORDINALITY,NESTED '$.kids[*]' COLUMNS(k FOR ORDINALITY,NESTED '$.v[*]' COLUMNS(n FOR ORDINALITY,v int PATH '$'))))`, []string{"r", "k", "n", "v"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1), int64(4)}, {int64(1), int64(1), int64(2), int64(5)}, {int64(1), int64(2), nil, nil}, {int64(2), nil, nil, nil}})
		s.Bag = true
		sqljsonBoth(t, p, b, c, jsonTableMetadata(s, b.PG))
		sqljsonBoth(t, p, b, c, jsonTableSiblingStep(`SELECT * FROM JSON_TABLE($1::jsonb,'$[*]'`+jsonTableSiblingColumns+`)`, jsonTableSiblingDocument))
		sqljsonBoth(t, p, b, c, fixture.Select(`SELECT count(*) AS v FROM JSON_TABLE('{"a":[1,2],"b":[3,4,5]}','$' COLUMNS(NESTED '$.a[*]' COLUMNS(a int PATH '$'),NESTED '$.b[*]' COLUMNS(b int PATH '$')))`, []string{"v"}, []string{"INT8"}, [][]any{{int64(5)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jt_required AS int NOT NULL`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE SEQUENCE jt_missing_default START 1`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, sqljsonQueryOne(`SELECT nextval('jt_missing_default') AS v`, "INT8", int64(1)))
		// Volatile DEFAULT cannot fold during preflight. Missing-child extension
		// must skip it, whereas an active child demands the same failing default.
		sqljsonBoth(t, p, b, c, jsonTableMetadata(fixture.Select(`SELECT * FROM JSON_TABLE('{}','$' COLUMNS(NESTED '$.missing[*]' COLUMNS(o FOR ORDINALITY,v jt_required PATH '$' ERROR ON ERROR,e boolean EXISTS PATH '$' TRUE ON ERROR,d int PATH '$.missing' DEFAULT (1/(nextval('jt_missing_default')-2)) ON EMPTY),parent int PATH '$.missing' DEFAULT 9 ON EMPTY))`, []string{"parent", "o", "v", "e", "d"}, []string{"INT4", "INT4", "INT4", "BOOL", "INT4"}, [][]any{{int64(9), nil, nil, nil, nil}}), b.PG))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, sqljsonQueryOne(`SELECT currval('jt_missing_default') AS v`, "INT8", int64(1)))
		q := `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(NESTED '$.missing[*]' COLUMNS(d int PATH '$.missing' DEFAULT (1/(nextval('jt_missing_default')-2)) ON EMPTY)))`
		for _, prepared := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER SEQUENCE jt_missing_default RESTART WITH 2`, 0))
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"), `{"missing":[{}]}`)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, sqljsonQueryOne(`SELECT currval('jt_missing_default') AS v`, "INT8", int64(2)))
		}
	})
}

func TestReferenceJSONTablePrepared(t *testing.T) {
	jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE SEQUENCE jt_prepared_default START 1`, 0))
		// Bare DEFAULT parameters are rejected (Diagnostics). Bound document and
		// PASSING remain reusable; a legal volatile DEFAULT is demanded per row.
		q := `SELECT * FROM JSON_TABLE($1::jsonb,'$[*]?(@ < $x)' PASSING $2::int AS x COLUMNS(r FOR ORDINALITY,v int PATH '$' ERROR ON ERROR,d int PATH '$.missing' DEFAULT nextval('jt_prepared_default') ON EMPTY))`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			doc  any
			x, d int
			rows [][]any
		}{{`[1,2,3]`, 3, 7, [][]any{{int64(1), int64(1), int64(7)}, {int64(2), int64(2), int64(8)}}}, {`[]`, 9, 8, nil}, {nil, 9, 8, nil}, {`[4]`, 9, 8, [][]any{{int64(1), int64(4), int64(8)}}}} {
			for route := range 3 {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(fmt.Sprintf(`ALTER SEQUENCE jt_prepared_default RESTART WITH %d`, tc.d), 0))
				s := jsonTableMetadata(fixture.Select(q, []string{"r", "v", "d"}, []string{"INT4", "INT4", "INT4"}, tc.rows, tc.doc, tc.x), b.PG)
				s.Prepared = route == 1
				if route == 2 {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				} else {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
		}
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
		d.Detail = `Token "bad" is invalid.`
		d.Context = "JSON data, line 1: bad\nunnamed portal parameter $1 = '...'"
		s := fixture.ErrorDiagnostic(q, d, `bad`, 9)
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER SEQUENCE jt_prepared_default RESTART WITH 10`, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"r", "v", "d"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(10)}}, `[2]`, 3))
	})
}
