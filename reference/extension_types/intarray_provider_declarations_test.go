package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func intarrayDeclarationChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, owner string) [4]int64 {
	t.Helper()
	ids := intarrayIdentity(t, p, b, c, owner)
	intarrayBoth(t, p, b, c, fixture.Select("SELECT e.extname,e.extversion,e.extrelocatable,n.nspname,r.rolname FROM (SELECT * FROM pg_extension WHERE extname='intarray') e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner", []string{"extname", "extversion", "extrelocatable", "nspname", "rolname"}, []string{"NAME", "TEXT", "BOOL", "NAME", "NAME"}, [][]any{{"intarray", "1.5", true, "ia_provider", owner}}))
	for _, tc := range []struct {
		name, input, output, category, storage string
		element, array                         int64
	}{
		{"query_int", "bqarr_in", "bqarr_out", "U", "p", 0, ids[1]},
		{"intbig_gkey", "_intbig_in", "_intbig_out", "U", "p", 0, ids[3]},
		{"_query_int", "array_in", "array_out", "A", "x", ids[0], 0},
		{"_intbig_gkey", "array_in", "array_out", "A", "x", ids[2], 0},
	} {
		if tc.category == "A" {
			// Shared array I/O has genuine fixed regproc identities; it is
			// not part of the provider-owned materialized pg_proc subset.
			query := "SELECT t.typname,t.typlen,t.typbyval,t.typalign,t.typstorage,t.typcategory,t.typdelim,t.typelem::text AS element,t.typarray::text AS array,t.typinput::text AS input,t.typoutput::text AS output,t.typinput::oid::text AS input_oid,t.typoutput::oid::text AS output_oid,r.rolname FROM (SELECT * FROM pg_type WHERE typnamespace='ia_provider'::regnamespace AND typname=$1) t JOIN pg_roles r ON r.oid=t.typowner"
			intarrayBoth(t, p, b, c, fixture.Select(query, []string{"typname", "typlen", "typbyval", "typalign", "typstorage", "typcategory", "typdelim", "element", "array", "input", "output", "input_oid", "output_oid", "rolname"}, []string{"NAME", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "CHAR", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "NAME"}, [][]any{{tc.name, int64(-1), false, "i", tc.storage, tc.category, ",", fmt.Sprint(tc.element), fmt.Sprint(tc.array), tc.input, tc.output, "750", "751", owner}}, tc.name))
			continue
		}
		query := "SELECT t.typname,t.typlen,t.typbyval,t.typalign,t.typstorage,t.typcategory,t.typdelim,t.typelem::text AS element,t.typarray::text AS array,i.proname AS input,o.proname AS output,r.rolname FROM (SELECT * FROM pg_type WHERE typnamespace='ia_provider'::regnamespace AND typname=$1) t JOIN pg_proc i ON i.oid=t.typinput JOIN pg_proc o ON o.oid=t.typoutput JOIN pg_roles r ON r.oid=t.typowner"
		intarrayBoth(t, p, b, c, fixture.Select(query, []string{"typname", "typlen", "typbyval", "typalign", "typstorage", "typcategory", "typdelim", "element", "array", "input", "output", "rolname"}, []string{"NAME", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "CHAR", "TEXT", "TEXT", "NAME", "NAME", "NAME"}, [][]any{{tc.name, int64(-1), false, "i", tc.storage, tc.category, ",", fmt.Sprint(tc.element), fmt.Sprint(tc.array), tc.input, tc.output, owner}}, tc.name))
	}
	// Restrict the inventory to the genuine provider subset; upstream also owns
	// executable leaf functions, operators and index classes outside this part.
	intarrayBoth(t, p, b, c, fixture.Select("SELECT t.typname FROM (SELECT * FROM pg_type WHERE typnamespace='ia_provider'::regnamespace AND typname IN ('query_int','intbig_gkey')) t JOIN pg_depend d ON d.objid=t.oid AND d.classid='pg_type'::regclass JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname='intarray' ORDER BY t.typname", []string{"typname"}, []string{"NAME"}, [][]any{{"intbig_gkey"}, {"query_int"}}))
	intarrayBoth(t, p, b, c, fixture.Select("SELECT p.proname FROM (SELECT * FROM pg_proc WHERE pronamespace='ia_provider'::regnamespace AND proname IN ('bqarr_in','bqarr_out','_intbig_in','_intbig_out')) p JOIN pg_depend d ON d.objid=p.oid AND d.classid='pg_proc'::regclass JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname='intarray' ORDER BY p.proname", []string{"proname"}, []string{"NAME"}, [][]any{{"_intbig_in"}, {"_intbig_out"}, {"bqarr_in"}, {"bqarr_out"}}))
	intarrayBoth(t, p, b, c, fixture.Select("SELECT oid::text AS oid,typname FROM pg_type WHERE oid IN (23,1007) ORDER BY pg_type.oid", []string{"oid", "typname"}, []string{"TEXT", "NAME"}, [][]any{{"23", "int4"}, {"1007", "_int4"}}))
	intarrayBoth(t, p, b, c, fixture.Select("SELECT objid::text AS oid FROM pg_depend WHERE classid='pg_type'::regclass AND objid IN (23,1007) AND deptype='e'", []string{"oid"}, []string{"TEXT"}, [][]any{}))
	intarrayBoth(t, p, b, c, fixture.Select("SELECT typname FROM pg_type WHERE typname='intarray'", []string{"typname"}, []string{"NAME"}, [][]any{}))
	return ids
}

func TestReferenceIntarrayProviderDeclarations(t *testing.T) {
	intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		intarrayInstall(t, p, b, c)
		intarrayDeclarationChecks(t, p, b, c, intarrayFixtureOwner(b))
		intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT '1&(2|3)'::ia_provider.query_int AS v", []string{"v"}, []string{"QUERY_INT"}, nil, [][]any{{"1 & ( 2 | 3 )"}}))
		for _, value := range []any{"!!1", []byte("!!1"), nil} {
			var want any = "!( !1 )"
			if value == nil {
				want = nil
			}
			intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT $1::ia_provider.query_int AS v", []string{"v"}, []string{"QUERY_INT"}, nil, [][]any{{want}}, value))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ia_query(v ia_provider.query_int)", 0))
		intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT v FROM ia_query", []string{"v"}, []string{"QUERY_INT"}, []bool{true}, [][]any{}))
	})
}
