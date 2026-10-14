package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRegTSPartitionDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, columnType, key, label, valueCast string
	}{
		{"column", "regdictionary", "k", "k", "::oid::regdictionary"},
		{"derived", "oid", "(k::regdictionary)", "(k::regdictionary)", "::oid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			regtsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsInstall(t, p, b, c)
				// Keep the real dictionary off path so error output must retain
				// its quoted, qualified name rather than its allocator identity.
				regtsExec(t, p, b, c, "SET search_path TO public", 0)
				oid := regtsDictionaryOID(t, p, c)
				regtsExec(t, p, b, c, fmt.Sprintf("CREATE TABLE public.reg_ts_diagnostic_parent(id integer,k %s) PARTITION BY RANGE(%s)", tc.columnType, tc.key), 0)
				regtsExec(t, p, b, c, `CREATE TABLE public.reg_ts_diagnostic_child PARTITION OF public.reg_ts_diagnostic_parent FOR VALUES FROM(MINVALUE) TO('"Reg.TS".unaccent')`, 0)
				regtsExec(t, p, b, c, fmt.Sprintf("INSERT INTO public.reg_ts_diagnostic_parent VALUES(0,'%d'%s)", oid-1, tc.valueCast), 1)
				// Both cases expose a RegTS key. In the derived case every
				// physical column is primitive, so column-only output demand
				// cannot supply the diagnostic's dictionary formatting authority.
				regtsSelect(t, p, b, c, "SELECT k::regdictionary AS key FROM public.reg_ts_diagnostic_parent", []string{"key"}, []string{"REGDICTIONARY"}, [][]any{{fmt.Sprint(oid - 1)}})
				check := func() {
					regtsSelect(t, p, b, c, "SELECT id,k::oid::bigint AS k FROM public.reg_ts_diagnostic_child ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT8"}, [][]any{{int64(0), oid - 1}})
					regtsSelect(t, p, b, c, "SELECT id,k::oid::bigint AS k FROM public.reg_ts_diagnostic_parent ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT8"}, [][]any{{int64(0), oid - 1}})
				}
				for _, prepared := range []bool{false, true} {
					t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
						endpoint := fmt.Sprintf("'%d'", oid)
						var args []any
						if prepared {
							endpoint = "$1"
							args = []any{fmt.Sprint(oid)}
						}
						q := fmt.Sprintf("INSERT INTO public.reg_ts_diagnostic_parent VALUES(1,'0'%s),(2,%s%s)", tc.valueCast, endpoint, tc.valueCast)
						miss := fixture.ImplicitError(q, fixture.Diagnostic{
							Code: "23514", Message: `no partition of relation "reg_ts_diagnostic_parent" found for row`,
							Schema: "public", Table: "reg_ts_diagnostic_parent",
							Detail: "Partition key of the failing row contains (" + tc.label + `) = ("Reg.TS".unaccent).`,
						}, args...)
						miss.ErrorStage = "execute"
						if prepared {
							miss = fixture.Prepared(miss)
						}
						// ImplicitError asserts both ERROR severities; all omitted
						// logical fields, including Position and Context, are absent.
						fixture.RunStep(t, p.Ctx, c, b.Recorder, miss)
						check() // The preceding valid row was not published.
					})
				}
				regtsExec(t, p, b, c, fmt.Sprintf("INSERT INTO public.reg_ts_diagnostic_parent VALUES(3,'0'%s)", tc.valueCast), 1)
				regtsSelect(t, p, b, c, "SELECT id,k::oid::bigint AS k FROM public.reg_ts_diagnostic_child ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT8"}, [][]any{{int64(0), oid - 1}, {int64(3), int64(0)}})
			})
		})
	}
}
