package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRegexpSubstrAuthority(t *testing.T) {
	// Each arity is a separate released declaration, not an omitted-default
	// spelling of the six-formal overload. Matches are independently specified
	// in the owning regexp_substr selection/start/flags/capture goldens.
	for _, tc := range []struct {
		name   string
		values []string
		args   []any
		want   string
	}{
		{"text_text", []string{"'foobar'", "'o(.)b'"}, []any{"foobar", "o(.)b"}, "oob"},
		{"start", []string{"'a1b22c333'", "'[0-9]+'", "4"}, []any{"a1b22c333", "[0-9]+", int64(4)}, "22"},
		{"occurrence", []string{"'a1b22c333'", "'[0-9]+'", "1", "2"}, []any{"a1b22c333", "[0-9]+", int64(1), int64(2)}, "22"},
		{"flags", []string{"'ABCDEF'", "'c(.)(..)'", "1", "1", "'i'"}, []any{"ABCDEF", "c(.)(..)", int64(1), int64(1), "i"}, "CDEF"},
		{"subexpr", []string{"'ABCDEF'", "'c(.)(..)'", "1", "1", "'i'", "2"}, []any{"ABCDEF", "c(.)(..)", int64(1), int64(1), "i", int64(2)}, "EF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				arity := len(tc.values)
				columns := []string{"s", "p", "start_at", "occurrence", "flags", "subexpr"}[:arity]
				definitions := []string{"s TEXT", "p TEXT", "start_at INTEGER", "occurrence INTEGER", "flags TEXT", "subexpr INTEGER"}[:arity]
				types := []string{"text", "text", "integer", "integer", "text", "integer"}[:arity]
				labels := []string{"string", "pattern", "start", `"N"`, "flags", "subexpr"}[:arity]
				call := func(args []string) string { return "regexp_substr(" + strings.Join(args, ",") + ")" }
				rowCall := call(columns)
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_substr_authority(id INTEGER,"+strings.Join(definitions, ",")+")", 0))
				run(fixture.Exec("INSERT INTO og_substr_authority VALUES(1,"+strings.Join(tc.values, ",")+")", 1))
				// Each supplied formal is independently NULL only at row evaluation.
				for i := range columns {
					values := append([]string(nil), tc.values...)
					values[i] = "NULL::" + types[i]
					run(fixture.Exec("INSERT INTO og_substr_authority VALUES("+strconv.Itoa(i+2)+","+strings.Join(values, ",")+")", 1))
				}
				run(fixture.Exec("GRANT SELECT ON og_substr_authority TO og_object_reader", 0))
				run(fixture.Exec("CREATE VIEW og_substr_authority_view AS SELECT "+rowCall+" AS v FROM og_substr_authority WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_substr_authority_view TO og_object_reader", 0))
				value := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_substr_authority"+tail, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, args...)
				}
				cases := []objectGrantProviderCase{
					{value(rowCall, " WHERE id=1", tc.want), true, true},
					{value("pg_catalog."+rowCall, " WHERE id=1", tc.want), true, true},
					{fixture.Select("SELECT v FROM og_substr_authority_view", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}), true, true},
					// IMMUTABLE constant calls may remain folded in a PG held plan;
					// only the genuine row-backed calls above assert SAME-held X.
					{value(call(tc.values), " WHERE id=1", tc.want), true, false},
					{value("CASE WHEN FALSE THEN "+rowCall+" ELSE NULL::TEXT END", " WHERE id=1", nil), false, true},
					{fixture.Select("SELECT "+rowCall+" AS v FROM og_substr_authority WHERE id>99", []string{"v"}, []string{"TEXT"}, nil), true, true},
					{fixture.Select("SELECT "+rowCall+" AS v FROM og_substr_authority WHERE FALSE", []string{"v"}, []string{"TEXT"}, nil), true, true},
				}
				// Reversed named arguments must retain the canonical full-formal
				// declaration rather than the written argument order.
				named := make([]string, arity)
				for i := range columns {
					named[arity-1-i] = labels[i] + "=>" + columns[i]
				}
				cases = append(cases, objectGrantProviderCase{value(call(named), " WHERE id=1", tc.want), true, true})
				for i := range columns {
					cases = append(cases, objectGrantProviderCase{value(rowCall, " WHERE id="+strconv.Itoa(i+2), nil), true, true})
					args := append([]string(nil), columns...)
					args[i] = "NULL::" + types[i]
					cases = append(cases, objectGrantProviderCase{value(call(args), " WHERE id=1", nil), false, true})
					args[i] = "$1::" + types[i]
					cases = append(cases,
						objectGrantProviderCase{value(call(args), " WHERE id=1", tc.want, tc.args[i]), true, true},
						// Bound NULL may prune a strict call in a fresh plan. Do not
						// turn that into a claim about held generic parameter plans.
						objectGrantProviderCase{value(call(args), " WHERE id=1", nil, nil), false, false},
					)
				}
				// A no-match NULL is not a strict-input NULL: the row-backed call
				// still exists and must demand EXECUTE before producing its value.
				noMatch := append([]string(nil), columns...)
				noMatch[1] = "'z'"
				cases = append(cases, objectGrantProviderCase{value(call(noMatch), " WHERE id=1", nil), true, true})
				// An adjacent regexp provider and another regexp_substr arity are
				// separate authorities; revoking this exact overload must not leak.
				sibling := "regexp_substr(s,'o(.)b')"
				siblingWant := any(nil)
				if arity == 2 {
					sibling = "regexp_substr(s,p,1)"
					siblingWant = "oob"
				}
				controls := []fixture.Step{
					value("regexp_like(s,p)", " WHERE id=1", true),
					value(sibling, " WHERE id=1", siblingWant),
				}
				// regexp_like has BOOLEAN metadata, unlike the TEXT target.
				controls[0].Types = []string{"BOOL"}
				if arity >= 5 {
					controls[0].Rows = [][]any{{false}}
				}
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog.regexp_substr("+strings.Join(types, ",")+")", "function", "regexp_substr", cases, controls)
			})
		})
	}
}
