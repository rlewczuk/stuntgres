package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations are independently derived from PostgreSQL 18's documented
// identifier rules; the initial oracle gate precedes native implementation.
func stringIdentScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, backend, c)
		})
	}
}

func stringIdentSelect(q string, b fixture.Backend, want any, args ...any) fixture.Step {
	s := fixture.ArrayReferenceSelect(q, []string{"ids"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{want}}, b.PG, args...)
	metadata := s.CheckMetadata
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		metadata(t, rows)
		ct, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if n, known := ct[0].Nullable(); n || known {
			t.Fatalf("computed nullable %v/%v", n, known)
		}
		if _, _, known := ct[0].DecimalSize(); known {
			t.Fatal("array decimal size is known")
		}
	}
	return s
}

func TestReferenceStringIdentValues(t *testing.T) {
	stringIdentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ input, want string }{
			{`"SomeSchema".someTable`, `{SomeSchema,sometable}`},
			{` A_1$ . "B.C" `, `{a_1$,B.C}`},
			{`"a""b".""""`, `{"a\"b","\""}`},
			{`SELECT._X9$.a.b.c`, `{select,_x9$,a,b,c}`},
			{`ÄBC.İΣ.🙂X`, `{Äbc,İΣ,🙂x}`},
			{"\u00a0FOO", "{\u00a0foo}"},
			{" \t\n\r\v\f A \t.\n B \r\v\f", `{a,b}`},
			{`"NULL"."a,b"."{x}"."e\f"."A "`, `{"NULL","a,b","{x}","e\\f","A "}`},
			{strings.Repeat("A", 64), "{" + strings.Repeat("a", 64) + "}"},
			{strings.Repeat("Z", 256), "{" + strings.Repeat("z", 256) + "}"},
			{`"` + strings.Repeat("🙂", 128) + `"`, "{" + strings.Repeat("🙂", 128) + "}"},
		} {
			t.Run(tc.input, func(t *testing.T) {
				stringReplaceBoth(t, p, b, c, stringIdentSelect("SELECT parse_ident($1) AS ids", b, tc.want, tc.input))
			})
		}
		for _, input := range []string{`foo.boo[]`, `foo(integer)`, `foo bar`, `foo/*x*/`, `foo"unterminated`, `U&"x"`} {
			want := `{foo}`
			if input == `foo.boo[]` {
				want = `{foo,boo}`
			}
			if input == `U&"x"` {
				want = `{u}`
			}
			stringReplaceBoth(t, p, b, c, stringIdentSelect("SELECT parse_ident($1,false) AS ids", b, want, input))
		}
		for _, call := range []string{`pg_catalog.parse_ident('A.B')`, `"parse_ident"('A.B')`} {
			stringReplaceBoth(t, p, b, c, stringIdentSelect("SELECT "+call+" AS ids", b, `{a,b}`))
		}
		stringReplaceBoth(t, p, b, c, fixture.Select(`SELECT array_dims(parse_ident('A.B')) AS d,array_lower(parse_ident('A.B'),1) AS l,array_upper(parse_ident('A.B'),1) AS u,cardinality(parse_ident('A.B')) AS n,(parse_ident('A.B'))[1] AS first`, []string{"d", "l", "u", "n", "first"}, []string{"TEXT", "INT4", "INT4", "INT4", "TEXT"}, [][]any{{"[1:2]", int64(1), int64(2), int64(2), "a"}}))
	})
}

func TestReferenceStringIdentNullAndCoercion(t *testing.T) {
	stringIdentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, args := range []string{`NULL`, `NULL,true`, `NULL,false`, `'"',NULL`, `NULL::text,NULL::boolean`} {
			stringReplaceBoth(t, p, b, c, stringIdentSelect("SELECT parse_ident("+args+") AS ids", b, nil))
		}
		for _, args := range []string{`'A.B'::text`, `'A.B'::name`, `'A.B'::varchar(8)`, `'A.B'::char(8)`, `'A.B','true'`, `'A.B[]','false'`, `'A.B[]','0'`, `'A.B','1'`} {
			stringReplaceBoth(t, p, b, c, stringIdentSelect("SELECT parse_ident("+args+") AS ids", b, `{a,b}`))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN ident_bool AS boolean", 0))
		stringReplaceBoth(t, p, b, c, stringIdentSelect("SELECT parse_ident('A.B[]',false::ident_bool) AS ids", b, `{a,b}`))
	})
}

func TestReferenceStringIdentNamedArguments(t *testing.T) {
	stringIdentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, args := range []string{`str=>'A.B'`, `'A.B[]',strict=>false`, `str=>'A.B[]',strict=>false`, `strict=>false,str=>'A.B[]'`, `STRICT=>false,STR=>'A.B[]'`, `"str"=>'A.B'`} {
			stringReplaceBoth(t, p, b, c, stringIdentSelect("SELECT parse_ident("+args+") AS ids", b, `{a,b}`))
		}
	})
}

func TestReferenceStringIdentFramesAndMetadata(t *testing.T) {
	stringIdentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT parse_ident($1,$2) AS ids", "SELECT parse_ident(strict=>$2,str=>$1) AS ids"} {
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer st.Close()
				held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
				for _, tc := range []struct{ source, strict, want any }{
					{"A.B", true, `{a,b}`}, {`"X.Y".Z[]`, false, `{X.Y,z}`}, {nil, true, nil}, {`"`, nil, nil}, {"A.B", true, `{a,b}`},
				} {
					s := stringIdentSelect(q, b, tc.want, tc.source, tc.strict)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				d := stringIdentDiagnostic("a.", `No valid identifier after ".".`)
				s := fixture.ErrorDiagnostic(q, d, "a.", true)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, stringIdentSelect(q, b, `{a,b}`, "A.B", true))
			}()
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ident_frames(id int,s text,b boolean)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO ident_frames VALUES(1,'A.B',true),(2,'"X.Y".Z[]',false),(3,NULL,true),(4,'"',NULL)`, 4))
		stringReplaceBoth(t, p, b, c, fixture.ArrayReferenceSelect("SELECT id,parse_ident(s,b) AS ids FROM ident_frames ORDER BY id", []string{"id", "ids"}, []string{"INT4", "_TEXT"}, []string{"INT4", "_TEXT"}, [][]any{{int64(1), `{a,b}`}, {int64(2), `{X.Y,z}`}, {int64(3), nil}, {int64(4), nil}}, b.PG))
		// Keep a detached metadata slice after the cursor has been consumed.
		rows, err := c.QueryContext(p.Ctx, "SELECT parse_ident('A.B') AS ids")
		if err != nil {
			t.Fatal(err)
		}
		ct, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		wantScan := reflect.TypeFor[string]()
		if b.PG {
			wantScan = reflect.TypeFor[any]()
		}
		if ct[0].Name() != "ids" || ct[0].DatabaseTypeName() != "_TEXT" || ct[0].ScanType() != wantScan {
			t.Fatal(ct)
		}
	})
}
