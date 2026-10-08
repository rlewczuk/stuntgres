//go:build reference

package reference_test

import "testing"

func TestReferenceBinaryBasicCheckDeparseGoldens(t *testing.T) {
	p := newReferencePair(t)
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
		p.step(t, referenceExec("CREATE TABLE binary_"+tc.name+"(v BYTEA,s BYTEA,e BYTEA,CONSTRAINT binary_ck_"+tc.name+" CHECK("+tc.expr+"))", 0))
		for _, mode := range []struct{ flag, want string }{{"false", tc.compact}, {"true", tc.pretty}} {
			for _, prepared := range []bool{false, true} {
				expr := referenceSelect("SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='binary_ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}})
				def := referenceSelect("SELECT pg_get_constraintdef(oid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='binary_ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + mode.want + ")"}})
				if prepared {
					expr = referencePrepared(expr)
					def = referencePrepared(def)
				}
				p.step(t, expr)
				p.step(t, def)
			}
		}
	}
	p.step(t, referenceExec(`INSERT INTO binary_bt VALUES('\x00ff8041008042ff00','\xff8000','\x41008042')`, 1))
	p.step(t, referenceSelect("SELECT v FROM binary_bt", []string{"v"}, []string{"BYTEA"}, [][]any{{"\x00\xff\x80A\x00\x80B\xff\x00"}}))
}
