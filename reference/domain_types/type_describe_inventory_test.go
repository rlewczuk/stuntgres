package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeDescribeInventory(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		typeDescribeBoth(run, fixture.Select("SELECT oid::bigint,proname,proargtypes::text,prorettype::bigint,provolatile::text,proisstrict,proretset,pronargdefaults::integer,provariadic::bigint,proparallel::text,procost::text,prosrc FROM pg_proc WHERE pronamespace='pg_catalog'::regnamespace AND proname IN ('format_type','pg_basetype','pg_typeof') ORDER BY oid", []string{"oid", "proname", "proargtypes", "prorettype", "provolatile", "proisstrict", "proretset", "pronargdefaults", "provariadic", "proparallel", "procost", "prosrc"}, []string{"INT8", "NAME", "TEXT", "INT8", "TEXT", "BOOL", "BOOL", "INT4", "INT8", "TEXT", "TEXT", "TEXT"}, [][]any{
			{int64(1081), "format_type", "26 23", int64(25), "s", false, false, int64(0), int64(0), "s", "1", "format_type"},
			{int64(1619), "pg_typeof", "2276", int64(2206), "s", false, false, int64(0), int64(0), "s", "1", "pg_typeof"},
			{int64(6315), "pg_basetype", "2206", int64(2206), "s", true, false, int64(0), int64(0), "s", "1", "pg_basetype"},
		}))
		for _, tc := range []struct {
			signature string
			oid       int64
		}{{"pg_catalog.format_type(oid,integer)", 1081}, {"pg_catalog.pg_typeof(\"any\")", 1619}, {"pg_catalog.pg_basetype(regtype)", 6315}} {
			typeDescribeBoth(run, fixture.Select("SELECT $1::regprocedure::oid::bigint AS identity", []string{"identity"}, []string{"INT8"}, [][]any{{tc.oid}}, tc.signature))
		}
	})
}
