package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinaryBasicCheckDeparseGoldens(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			// Independent SQL rendering expectations, asserted on pinned PostgreSQL
			// before parity. Structural conbin is intentionally native-only: PostgreSQL
			// nodeToString is not the native structural representation.
			for _, tc := range []struct{ name, expr, compact, pretty string }{
				{"len", "length(v)>0", "(length(v) > 0)", "length(v) > 0"},
				{"oct", "octet_length(v)>0", "(octet_length(v) > 0)", "octet_length(v) > 0"},
				{"bit", "bit_length(v)>0", "(bit_length(v) > 0)", "bit_length(v) > 0"},
				{"bt", "btrim(v,s)=e", "(btrim(v, s) = e)", "btrim(v, s) = e"},
				{"lt", "ltrim(v,s)=e", "(ltrim(v, s) = e)", "ltrim(v, s) = e"},
				{"rt", "rtrim(v,s)=e", "(rtrim(v, s) = e)", "rtrim(v, s) = e"},
				{"cat", "v||s=e", "((v || s) = e)", "(v || s) = e"},
				{"nested", "length(v||(s||v))>0", "(length((v || (s || v))) > 0)", "length(v || (s || v)) > 0"},
			} {
				apply(t, fixture.Exec("CREATE TABLE binary_"+tc.name+"(v BYTEA,s BYTEA,e BYTEA,CONSTRAINT binary_ck_"+tc.name+" CHECK("+tc.expr+"))", 0))
				for _, mode := range []struct{ flag, want string }{{"false", tc.compact}, {"true", tc.pretty}} {
					for _, prepared := range []bool{false, true} {
						expr := fixture.Select("SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='binary_ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}})
						def := fixture.Select("SELECT pg_get_constraintdef(oid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='binary_ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + mode.want + ")"}})
						if prepared {
							expr = fixture.Prepared(expr)
							def = fixture.Prepared(def)
						}
						apply(t, expr)
						apply(t, def)
					}
				}
			}
			apply(t, fixture.Exec(`INSERT INTO binary_bt VALUES('\x00ff8041008042ff00','\xff8000','\x41008042')`, 1))
			apply(t, fixture.Select("SELECT v FROM binary_bt", []string{"v"}, []string{"BYTEA"}, [][]any{{"\x00\xff\x80A\x00\x80B\xff\x00"}}))
		})
	}
}
