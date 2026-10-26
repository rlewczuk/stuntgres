package reference_test

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent declarations from PostgreSQL REL_18_6 pg_proc.dat, not from
// the native declaration inventory or the output of either backend.
type resultsFormalFact struct {
	oid                      int
	name, inputs, all, modes string
	variadic                 int64
	kind                     string
}

func resultsBuiltinFormalFacts() []resultsFormalFact {
	return []resultsFormalFact{
		{3713, "ts_token_type", "26", "{26,23,25,25}", "{i,o,o,o}", 0, "f"},
		{3714, "ts_token_type", "25", "{25,23,25,25}", "{i,o,o,o}", 0, "f"},
		{3715, "ts_parse", "26 25", "{26,25,23,25}", "{i,i,o,o}", 0, "f"},
		{3716, "ts_parse", "25 25", "{25,25,23,25}", "{i,i,o,o}", 0, "f"},
		{1689, "aclexplode", "1034", "{1034,26,26,25,16}", "{i,o,o,o,o}", 0, "f"},
		{3958, "json_each", "114", "{114,25,114}", "{i,o,o}", 0, "f"},
		{3959, "json_each_text", "114", "{114,25,25}", "{i,o,o}", 0, "f"},
		{3208, "jsonb_each", "3802", "{3802,25,3802}", "{i,o,o}", 0, "f"},
		{3932, "jsonb_each_text", "3802", "{3802,25,25}", "{i,o,o}", 0, "f"},
		{3955, "json_array_elements", "114", "{114,114}", "{i,o}", 0, "f"},
		{3969, "json_array_elements_text", "114", "{114,25}", "{i,o}", 0, "f"},
		{3219, "jsonb_array_elements", "3802", "{3802,3802}", "{i,o}", 0, "f"},
		{3465, "jsonb_array_elements_text", "3802", "{3802,25}", "{i,o}", 0, "f"},
		{3583, "pg_last_committed_xact", "", "{28,1184,26}", "{o,o,o}", 0, "f"},
		{6168, "pg_xact_commit_timestamp_origin", "28", "{28,1184,26}", "{i,o,o}", 0, "f"},
		{3819, "pg_get_multixact_members", "28", "{28,28,25}", "{i,o,o}", 0, "f"},
		{6159, "pg_get_catalog_foreign_keys", "", "{2205,1009,2205,1009,16,16}", "{o,o,o,o,o,o}", 0, "f"},
		{3423, "pg_partition_tree", "2205", "{2205,2205,2205,16,23}", "{i,o,o,o,o}", 0, "f"},
		{3425, "pg_partition_ancestors", "2205", "{2205,2205}", "{i,o}", 0, "f"},
		{3084, "pg_extension_update_paths", "19", "{19,25,25,25}", "{i,o,o,o}", 0, "f"},
		{3839, "pg_identify_object", "26 26 23", "{26,26,23,25,25,25,25}", "{i,i,i,o,o,o,o}", 0, "f"},
		{3382, "pg_identify_object_as_address", "26 26 23", "{26,26,23,25,1009,1009}", "{i,i,i,o,o,o}", 0, "f"},
		{3954, "pg_get_object_address", "25 1009 1009", "{25,1009,1009,26,26,23}", "{i,i,i,o,o,o}", 0, "f"},
		{438, "num_nulls", "2276", "{2276}", "{v}", 2276, "f"},
		{440, "num_nonnulls", "2276", "{2276}", "{v}", 2276, "f"},
		{3058, "concat", "2276", "{2276}", "{v}", 2276, "f"},
		{3059, "concat_ws", "25 2276", "{25,2276}", "{i,v}", 2276, "f"},
		{3539, "format", "25 2276", "{25,2276}", "{i,v}", 2276, "f"},
		{3198, "json_build_array", "2276", "{2276}", "{v}", 2276, "f"},
		{3200, "json_build_object", "2276", "{2276}", "{v}", 2276, "f"},
		{3271, "jsonb_build_array", "2276", "{2276}", "{v}", 2276, "f"},
		{3273, "jsonb_build_object", "2276", "{2276}", "{v}", 2276, "f"},
		{3951, "json_extract_path", "114 1009", "{114,1009}", "{i,v}", 25, "f"},
		{3953, "json_extract_path_text", "114 1009", "{114,1009}", "{i,v}", 25, "f"},
		{3217, "jsonb_extract_path", "3802 1009", "{3802,1009}", "{i,v}", 25, "f"},
		{3940, "jsonb_extract_path_text", "3802 1009", "{3802,1009}", "{i,v}", 25, "f"},
		{3971, "ordered_set_transition_multi", "2281 2276", "{2281,2276}", "{i,v}", 2276, "f"},
		{3987, "rank_final", "2281 2276", "{2281,2276}", "{i,v}", 2276, "f"},
		{3989, "percent_rank_final", "2281 2276", "{2281,2276}", "{i,v}", 2276, "f"},
		{3991, "cume_dist_final", "2281 2276", "{2281,2276}", "{i,v}", 2276, "f"},
		{3993, "dense_rank_final", "2281 2276", "{2281,2276}", "{i,v}", 2276, "f"},
		{3986, "rank", "2276", "{2276}", "{v}", 2276, "a"},
		{3988, "percent_rank", "2276", "{2276}", "{v}", 2276, "a"},
		{3990, "cume_dist", "2276", "{2276}", "{v}", 2276, "a"},
		{3992, "dense_rank", "2276", "{2276}", "{v}", 2276, "a"},
	}
}

func TestReferenceRoutineResultsFormalBuiltins(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		facts := resultsBuiltinFormalFacts()
		if len(facts) != 45 {
			t.Fatal("incomplete released affected roster", len(facts))
		}
		sort.Slice(facts, func(i, j int) bool { return facts[i].oid < facts[j].oid })
		var ids []string
		var want [][]any
		for i, f := range facts {
			if i > 0 && facts[i-1].oid == f.oid {
				t.Fatal("duplicate declaration", f.oid)
			}
			ids = append(ids, fmt.Sprint(f.oid))
			want = append(want, []any{int64(f.oid), f.name, int64(len(strings.Fields(f.inputs))), f.inputs, f.all, f.modes, f.variadic, f.kind})
		}
		q := "SELECT oid::bigint AS id,proname::text AS name,pronargs::integer AS inputs,proargtypes::text AS identity,proallargtypes::text AS alltypes,proargmodes::text AS modes,provariadic::bigint AS variadic,prokind::text AS kind FROM pg_proc WHERE oid IN (" + strings.Join(ids, ",") + ") ORDER BY oid"
		resultsFormalBoth(t, p, b, c, q, []string{"id", "name", "inputs", "identity", "alltypes", "modes", "variadic", "kind"}, []string{"INT8", "TEXT", "INT4", "TEXT", "TEXT", "TEXT", "INT8", "TEXT"}, want)
		resultsFormalBuiltinNames(t, p, b, c)
		resultsFormalRaw(t, p, b, c, 3713, "{26,23,25,25}", "{i,o,o,o}", "f")
	})
}

func TestReferenceRoutineResultsFormalAllInput(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Scalar, dynamic RECORD, polymorphic composite/set, all-IN set and
		// zero-input overloads must not acquire invented OUT or VARIADIC slots.
		ids := []int{40, 41, 54, 55, 938, 939, 1066, 1067, 1068, 1069, 1293, 1397, 2331, 2410, 2411, 2420, 2421, 3101, 3102, 3103, 3104, 3199, 3201, 3204, 3205, 3209, 3259, 3260, 3272, 3274, 3475, 3490, 3491, 3540, 3717, 3718, 3719, 3720, 3721, 3931, 3957, 3960, 3961, 6274}
		var list []string
		var want [][]any
		for _, id := range ids {
			list = append(list, fmt.Sprint(id))
			want = append(want, []any{int64(id), nil, nil})
		}
		resultsFormalBoth(t, p, b, c, "SELECT oid::bigint AS id,proallargtypes::text AS alltypes,proargmodes::text AS modes FROM pg_proc WHERE oid IN ("+strings.Join(list, ",")+") ORDER BY oid", []string{"id", "alltypes", "modes"}, []string{"INT8", "TEXT", "TEXT"}, want)
		resultsFormalRaw(t, p, b, c, 1397, "", "", "f")
		resultsFormalBoth(t, p, b, c, "SELECT proargtypes::text AS identity,pronargs::integer AS inputs FROM pg_proc WHERE oid=3199", []string{"identity", "inputs"}, []string{"TEXT", "INT4"}, [][]any{{"", int64(0)}})
	})
}

func resultsFormalBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, names, types []string, want [][]any, args ...any) {
	t.Helper()
	s := fixture.Select(q, names, types, want, args...)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}
