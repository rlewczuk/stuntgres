package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBrinIndexDiagnostics(t *testing.T) {
	brinIndexDiagnosticAdmission(t)
	for _, tc := range []struct{ name, prefix, tail, code, message, hint string }{
		{"unique", "UNIQUE ", "(n)", "0A000", `access method "brin" does not support unique indexes`, ""},
		{"include", "", "(n) INCLUDE(s)", "0A000", `access method "brin" does not support included columns`, ""},
		{"asc", "", "(n ASC)", "0A000", `access method "brin" does not support ASC/DESC options`, ""},
		{"desc", "", "(n DESC)", "0A000", `access method "brin" does not support ASC/DESC options`, ""},
		{"first", "", "(n NULLS FIRST)", "0A000", `access method "brin" does not support NULLS FIRST/LAST options`, ""},
		{"last", "", "(n NULLS LAST)", "0A000", `access method "brin" does not support NULLS FIRST/LAST options`, ""},
		{"wrong-method", "", "(n int4_ops)", "42704", `operator class "int4_ops" does not exist for access method "brin"`, ""},
		{"missing", "", "(n public.missing_ops)", "42704", `operator class "public.missing_ops" does not exist for access method "brin"`, ""},
		{"namespace", "", "(n absent_brin_schema.int4_minmax_ops)", "3F000", `schema "absent_brin_schema" does not exist`, ""},
		{"incompatible", "", "(n text_minmax_ops)", "42804", `operator class "text_minmax_ops" does not accept data type integer`, ""},
		{"class-before-direction", "", "(n missing_ops DESC)", "42704", `operator class "missing_ops" does not exist for access method "brin"`, ""},
		{"type-before-direction", "", "(n text_minmax_ops DESC)", "42804", `operator class "text_minmax_ops" does not accept data type integer`, ""},
		{"options", "", "(n int4_minmax_ops(foo=1))", "22023", "operator class int4_minmax_ops has no options", ""},
		{"missing-key", "", "(missing)", "42703", `column "missing" does not exist`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				brinIndexSeed(run)
				hashIndexSemanticFailure(t, p, b, run, "CREATE "+tc.prefix+"INDEX retry ON t USING brin"+tc.tail, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint})
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
				run(brinIndexIDs("n=4", 3, 6))
				run(fixture.Exec("CREATE INDEX retry ON t USING brin(n)", 0))
			})
		})
	}
	t.Run("exclusion", func(t *testing.T) {
		brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			hashIndexSemanticFailure(t, p, b, run, "CREATE TABLE excluded_brin(n INT, EXCLUDE USING brin(n WITH =))", fixture.Diagnostic{Code: "0A000", Message: `access method "brin" does not support exclusion constraints`})
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='excluded_brin'", []string{"relname"}, []string{"NAME"}, nil))
		})
	})
}

func TestReferenceBrinIndexEligibility(t *testing.T) {
	brinIndexEnumEligibility(t)
	for _, tc := range []struct{ typ, display string }{
		{"BOOL", "boolean"}, {"JSON", "json"}, {"JSONB", "jsonb"}, {"MONEY", "money"}, {"INT[]", "integer[]"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(v "+tc.typ+")", 0))
				hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX retry ON t USING brin(v)", fixture.Diagnostic{Code: "42704", Message: `data type ` + tc.display + ` has no default operator class for access method "brin"`, Hint: "You must specify an operator class for the index or define a default operator class for the data type."})
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			})
		})
	}
	for _, tc := range []struct{ typ, key string }{
		{"INET", "v"}, {"INET", "v inet_inclusion_ops"}, {"INT", "v int4_bloom_ops"}, {"INT", "v int4_minmax_multi_ops"},
	} {
		t.Run(tc.typ+tc.key, func(t *testing.T) {
			brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(v "+tc.typ+")", 0))
				q := "CREATE INDEX retry ON t USING brin(" + tc.key + ")"
				if b.PG {
					run(fixture.Exec(q, 0))
				} else {
					hashIndexSemanticFailure(t, p, b, run, q, fixture.Diagnostic{Code: "0A000", Message: "index operator class is unsupported"})
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
				}
			})
		})
	}
}
