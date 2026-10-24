package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func stringPadScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func stringPadBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func stringPadValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, expr, typ string, want any) {
	t.Helper()
	stringPadBoth(t, p, b, c, fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{typ}, [][]any{{want}}))
}

func TestReferenceStringPadPadding(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, left, right string }{
			{"'hi',5,'xy'", "xyxhi", "hixyx"}, {"'hi',5", "   hi", "hi   "},
			{"'abcdef',3,'xy'", "abc", "abc"}, {"'abc',3,'xy'", "abc", "abc"},
			{"'',3,'xy'", "xyx", "xyx"}, {"'abc',5,''", "abc", "abc"}, {"'abc',2,''", "ab", "ab"},
			{"'a',5,'x'::char(3)", "xxxxa", "axxxx"}, {"'a',5,'x  '::varchar(3)", "x  xa", "ax  x"},
			{"'ab  '::char(4),3,'x'", "xab", "abx"}, {"'ab  '::varchar(4),3,'x'", "ab ", "ab "},
			{"'a',2::smallint,'x'", "xa", "ax"}, {"'a','2','x'", "xa", "ax"},
			{"'a',2::bigint::int,'x'", "xa", "ax"}, {"'a'::name,2,'x'::name", "xa", "ax"},
			{"'abc',0,'x'", "", ""}, {"'abc',-1,'x'", "", ""}, {"'abc',-2147483648,'x'", "", ""},
			{"'abc',2147483647,''", "abc", "abc"},
		} {
			for _, f := range []struct{ name, want string }{{"lpad", tc.left}, {"rpad", tc.right}} {
				t.Run(f.name+tc.args, func(t *testing.T) { stringPadValue(t, p, b, c, f.name+"("+tc.args+")", "TEXT", f.want) })
			}
		}
	})
}

func TestReferenceStringPadUnicode(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ expr, want string }{
			{"lpad('é🙂',5,'界x')", "界x界é🙂"}, {"rpad('é🙂',5,'界x')", "é🙂界x界"},
			{"lpad('é🙂',1,'界x')", "é"}, {"rpad('é🙂',1,'界x')", "é"},
			{`lpad(U&'e\0301',1,'x')`, "e"}, {`rpad(U&'e\0301',1,'x')`, "e"},
			{"repeat('é🙂',2)", "é🙂é🙂"}, {"reverse('Aé🙂Z')", "Z🙂éA"},
			{`reverse(U&'a\0301b')`, "b\u0301a"}, {`reverse(U&'a\200Db')`, "b\u200da"},
			{`reverse(reverse(U&'a\0301🙂'))`, "a\u0301🙂"},
		} {
			t.Run(tc.expr, func(t *testing.T) { stringPadValue(t, p, b, c, tc.expr, "TEXT", tc.want) })
		}
	})
}

func TestReferenceStringPadRepeat(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, want string }{
			{"'Pg',4", "PgPgPgPg"}, {"' x ',2", " x  x "}, {"'',3", ""}, {"'abc',1", "abc"},
			{"'abc',0", ""}, {"'abc',-1", ""}, {"'abc',-2147483648", ""},
			{"'ab'::char(4),2", "abab"}, {"'ab  '::varchar(4),2", "ab  ab  "}, {"'a',2::smallint", "aa"}, {"'a','2'", "aa"},
		} {
			t.Run(tc.args, func(t *testing.T) { stringPadValue(t, p, b, c, "repeat("+tc.args+")", "TEXT", tc.want) })
		}
	})
}

func TestReferenceStringPadReverse(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ expr, want string }{
			{"reverse('')", ""}, {"reverse('🙂')", "🙂"}, {"reverse('abc')", "cba"},
			{"reverse('ab'::char(4))", "ba"}, {"reverse('ab  '::varchar(4))", "  ba"}, {"reverse('abc'::name)", "cba"},
		} {
			t.Run(tc.expr, func(t *testing.T) { stringPadValue(t, p, b, c, tc.expr, "TEXT", tc.want) })
		}
	})
}

func TestReferenceStringPadReverseBinary(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ expr, want string }{
			{`reverse('\x00ff8041'::bytea)`, string([]byte{0x41, 0x80, 0xff, 0})},
			{`reverse('\xc3a9'::bytea)`, string([]byte{0xa9, 0xc3})}, {`reverse('\x'::bytea)`, ""},
			{`reverse(reverse('\x00ff8041'::bytea))`, string([]byte{0, 0xff, 0x80, 0x41})},
		} {
			t.Run(tc.expr, func(t *testing.T) { stringPadValue(t, p, b, c, tc.expr, "BYTEA", tc.want) })
		}
		stringPadValue(t, p, b, c, "reverse(NULL::bytea)", "BYTEA", nil)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN sp_binary AS bytea", 0))
		stringPadValue(t, p, b, c, `reverse('\x00ff'::sp_binary)`, "BYTEA", string([]byte{0xff, 0}))
	})
}

func TestReferenceStringPadNull(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []struct {
			name string
			args []string
		}{
			{"lpad", []string{"'a'", "3"}}, {"lpad", []string{"'a'", "3", "'x'"}},
			{"rpad", []string{"'a'", "3"}}, {"rpad", []string{"'a'", "3", "'x'"}},
			{"repeat", []string{"'a'", "2"}}, {"reverse", []string{"'a'"}},
		} {
			for slot := range f.args {
				for _, typed := range []bool{false, true} {
					args := append([]string(nil), f.args...)
					args[slot] = "NULL"
					if typed {
						typ := "text"
						if slot == 1 {
							typ = "integer"
						}
						args[slot] += "::" + typ
					}
					expr := f.name + "(" + strings.Join(args, ",") + ")"
					t.Run(expr, func(t *testing.T) { stringPadValue(t, p, b, c, expr, "TEXT", nil) })
				}
			}
		}
		for _, expr := range []string{"lpad(NULL,2147483647,'x')", "rpad(NULL,2147483647,'x')", "repeat(NULL,2147483647)"} {
			stringPadValue(t, p, b, c, expr, "TEXT", nil)
		}
	})
}

func TestReferenceStringPadSignatures(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ expr, want string }{
			{`"lpad"('a',3,'x')`, "xxa"}, {`LPAD('a',3,'x')`, "xxa"}, {"pg_catalog.rpad('a',3,'x')", "axx"},
			{"pg_catalog.repeat('a',2)", "aa"}, {`"reverse"('ab')`, "ba"},
			{"lpad('a',3,VARIADIC 'x')", "xxa"}, {"rpad('a',VARIADIC 3)", "a  "},
			{"repeat('a',VARIADIC 2)", "aa"}, {"reverse(VARIADIC 'ab')", "ba"},
		} {
			t.Run(tc.expr, func(t *testing.T) { stringPadValue(t, p, b, c, tc.expr, "TEXT", tc.want) })
		}
		stringPadValue(t, p, b, c, `reverse(VARIADIC '\x00ff'::bytea)`, "BYTEA", string([]byte{0xff, 0}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN sp_char AS char(4)", 0))
		stringPadValue(t, p, b, c, "reverse('ab'::sp_char)", "TEXT", "ba")
		stringPadBoth(t, p, b, c, fixture.Select("SELECT reverse($1),reverse($2::bytea)", []string{"reverse", "reverse"}, []string{"TEXT", "BYTEA"}, [][]any{{"ba", "ba"}}, "ab", []byte("ab")))
		q := "SELECT reverse($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ arg, want any }{{"ab", "ba"}, {"é🙂", "🙂é"}, {nil, nil}, {"ab", "ba"}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"reverse"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.arg))
		}
		mixed := "SELECT reverse($1),reverse($1::bytea)"
		stringPadBoth(t, p, b, c, fixture.Select(mixed, []string{"reverse", "reverse"}, []string{"TEXT", "BYTEA"}, [][]any{{"ba", "ba"}}, "ab"))
	})
}
