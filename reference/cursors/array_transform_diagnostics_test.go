package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayTransformDiagnostics(t *testing.T) {
	arrayTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// REL_18_0 array_userfuncs.c, arrayfuncs.c: exact independent primary
		// and detail strings. Every unspecified diagnostic field must be absent.
		for _, tc := range []struct{ name, expr, code, message, detail string }{
			{"append_rank", "array_append('{{1,2}}'::int[],3)", "22000", "argument must be empty or one-dimensional array", ""},
			{"prepend_rank", "array_prepend(3,'{{1,2}}'::int[])", "22000", "argument must be empty or one-dimensional array", ""},
			{"prepend_overflow", "array_prepend(1,'[-2147483648:-2147483648]={2}'::int[])", "22003", "integer out of range", ""},
			{"cat_rank", "array_cat('{1}'::int[],'{{{2}}}'::int[])", "2202E", "cannot concatenate incompatible arrays", "Arrays of 1 and 3 dimensions are not compatible for concatenation."},
			{"cat_trailing_length", "array_cat('{{1,2}}'::int[],'{{3}}'::int[])", "2202E", "cannot concatenate incompatible arrays", "Arrays with differing element dimensions are not compatible for concatenation."},
			{"cat_trailing_bounds", "array_cat('[1:1][0:1]={{1,2}}'::int[],'{{3,4}}'::int[])", "2202E", "cannot concatenate incompatible arrays", "Arrays with differing element dimensions are not compatible for concatenation."},
			{"cat_rank_up_shape", "array_cat('{1}'::int[],'{{2,3}}'::int[])", "2202E", "cannot concatenate incompatible arrays", "Arrays with differing dimensions are not compatible for concatenation."},
			{"cat_rank_down_shape", "array_cat('{{2,3}}'::int[],'{1}'::int[])", "2202E", "cannot concatenate incompatible arrays", "Arrays with differing dimensions are not compatible for concatenation."},
			{"fill_null_dimensions", "array_fill(1,NULL::int[])", "22004", "dimension array or low bound array cannot be null", ""},
			{"fill_null_bounds", "array_fill(1,ARRAY[1],NULL::int[])", "22004", "dimension array or low bound array cannot be null", ""},
			{"fill_rank_dimensions", "array_fill(1,'{{1}}'::int[])", "2202E", "wrong number of array subscripts", "Dimension array must be one dimensional."},
			{"fill_rank_bounds", "array_fill(1,ARRAY[1],'{{1}}'::int[])", "2202E", "wrong number of array subscripts", "Dimension array must be one dimensional."},
			{"fill_null_dimension_member", "array_fill(1,ARRAY[NULL]::int[])", "22004", "dimension values cannot be null", ""},
			{"fill_null_bound_member", "array_fill(1,ARRAY[1],ARRAY[NULL]::int[])", "22004", "dimension values cannot be null", ""},
			{"fill_vector_length", "array_fill(1,ARRAY[1,2],ARRAY[1])", "2202E", "wrong number of array subscripts", "Low bound array has different size than dimensions array."},
			{"fill_seven", "array_fill(1,ARRAY[1,1,1,1,1,1,1])", "54000", "number of array dimensions (7) exceeds the maximum allowed (6)", ""},
			{"trim_negative", "trim_array(ARRAY[1,2],-1)", "2202E", "number of elements to trim must be between 0 and 2", ""},
			{"trim_excess", "trim_array('{{1,2},{3,4}}'::int[],3)", "2202E", "number of elements to trim must be between 0 and 2", ""},
			{"trim_empty_excess", "trim_array('{}'::int[],1)", "2202E", "number of elements to trim must be between 0 and 0", ""},
			{"json_provider", "array_sort(ARRAY['1'::json,'2'::json])", "0A000", "could not identify a comparison function for type json", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				arrayTransformDiagnostic(t, p, b, c, "SELECT "+tc.expr+" AS a FROM at_one", fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail}, false)
			})
		}
		// Known parameter types separate import from shape validation. Unknown
		// bind Context/location are observed independently, not stripped away.
		for _, tc := range []struct {
			q    string
			args []any
		}{
			{"SELECT array_append($1::int[],$2::int) AS a FROM at_one", []any{"{{1,2}}", int64(3)}},
			{"SELECT array_fill($1::int,$2::int[],$3::int[]) AS a FROM at_one", []any{int64(1), "{2}", "{NULL}"}},
			{"SELECT trim_array($1::int[],$2::int) AS a FROM at_one", []any{"{1,2}", int64(3)}},
			{"SELECT array_reverse($1::int[]) AS a FROM at_one", []any{"{oops}"}},
			{"SELECT ARRAY[1,2] || $1 AS a FROM at_one", []any{"7"}},
		} {
			arrayTransformAssertContract(t, p, b, c, tc.q, tc.q, tc.args...)
		}
		for _, expr := range []string{
			"array_append()", "array_append(ARRAY[1])", "array_append(ARRAY[true],1::numeric)",
			"array_cat(ARRAY[true],ARRAY[1::numeric])", "array_prepend(true,ARRAY[1])",
			"array_fill('x',ARRAY[1])", "array_fill(NULL,ARRAY[1])", "array_fill(ARRAY[1],ARRAY[2])",
			"array_reverse(NULL)", "array_sort(NULL)", "trim_array(NULL,1)", "array_to_string(NULL,',')",
			"array_fill(1,ARRAY[1::bigint])", "array_reverse(1)", "array_sort(ARRAY[1],1)",
			"trim_array(ARRAY[1],1::bigint)", "array_to_string(ARRAY[1],1)",
			"array_sort(array => ARRAY[2,1])", "array_sort(descending => true)",
			"array_sort(array => ARRAY[2,1],unknown => false)",
			"array_sort(array => ARRAY[2,1],descending => false,descending => true)",
			"array_sort(descending => false,ARRAY[2,1])",
			"array_append(array => ARRAY[1],element => 2)",
			"array_sort(VARIADIC ARRAY[1,2])", "array_append(VARIADIC ARRAY[1,2])",
			"array_sort(DISTINCT ARRAY[1,2])", "array_sort(ALL ARRAY[1,2])", "array_sort(*)",
			"array_sort(ARRAY[1,2] ORDER BY id)", "array_sort(ARRAY[1,2]) FILTER (WHERE true)",
			"array_sort(ARRAY[1,2]) OVER ()", "array_sort(ARRAY[1,2]) WITHIN GROUP (ORDER BY id)",
			"public.array_sort(ARRAY[1])", "\"Array_Sort\"(ARRAY[1])", "pg_catalog.array_sort(ARRAY[1])",
			"array_reverse(missing)", "array_sort(missing,unknown => false)",
			"array_sort(ARRAY[1]) FILTER (WHERE missing)",
			"ARRAY[1,2] || '7'", "ARRAY[true] || 1::numeric",
			"array_cat(ARRAY['a'] COLLATE \"C\",ARRAY['b'] COLLATE \"POSIX\")",
		} {
			arrayTransformAssertContract(t, p, b, c, expr, "SELECT "+expr+" AS a FROM at_one")
		}
		arrayTransformAssertContract(t, p, b, c, "utf8_function_position", "/*é*/ SELECT array_reverse(1) AS a FROM at_one")
		arrayTransformAssertContract(t, p, b, c, "utf8_literal_position", "/*é*/ SELECT ARRAY[1] || '7' AS a FROM at_one")
		arrayTransformAssertContract(t, p, b, c, "arity_101", "SELECT array_sort("+strings.Repeat("ARRAY[1],", 100)+"ARRAY[1]) AS a FROM at_one")
		// Pinned PG18.0: only the two/three-argument sort overloads name
		// their parameters; duplicate labels fail before signature lookup.
		for _, tc := range []struct {
			name, expr string
			diagnostic fixture.Diagnostic
		}{
			{"named_single", "array_sort(\"array\" => ARRAY[2,1])", fixture.Diagnostic{Code: "42883", Message: "function array_sort(array => integer[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}},
			{"named_unknown", "array_sort(\"array\" => ARRAY[2,1],unknown => false)", fixture.Diagnostic{Code: "42883", Message: "function array_sort(array => integer[], unknown => boolean) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}},
			{"named_duplicate", "array_sort(\"array\" => ARRAY[2,1],descending => false,descending => true)", fixture.Diagnostic{Code: "42601", Message: "argument name \"descending\" used more than once", Position: "61"}},
			{"named_append", "array_append(\"array\" => ARRAY[1],element => 2)", fixture.Diagnostic{Code: "42883", Message: "function array_append(array => integer[], element => integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				arrayTransformDiagnostic(t, p, b, c, "SELECT "+tc.expr+" AS a FROM at_one", tc.diagnostic, true)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=public", 0))
		// pg_catalog is implicitly searched unless explicitly positioned; this
		// must stay available rather than becoming a fabricated hidden routine.
		arrayTransformBoth(t, p, b, c, arrayTransformSelect(b, "SELECT pg_catalog.array_sort(ARRAY[2,1]) AS a FROM at_one", []string{"a"}, []string{"_INT4"}, [][]any{{"{1,2}"}}))
	})
}
