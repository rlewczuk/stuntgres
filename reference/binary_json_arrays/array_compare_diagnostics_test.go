package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayCompareDiagnostics(t *testing.T) {
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	arrayCompareScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ac_diag(i INT,j BIGINT)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ac_diag_empty(i INT,j BIGINT)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ac_diag VALUES(1,2147483648)", 1))
		for _, tc := range []struct {
			name, q string
			d       fixture.Diagnostic
			args    []any
		}{
			{"scalar_any", "SELECT 1 = ANY(1)", fixture.Diagnostic{Code: "42809", Message: "op ANY/ALL (array) requires array on right side", Position: "10"}, nil},
			{"scalar_null_all", "SELECT 1 = ALL(NULL::integer)", fixture.Diagnostic{Code: "42809", Message: "op ANY/ALL (array) requires array on right side", Position: "10"}, nil},
			{"element_operator", "SELECT 1 = ANY(ARRAY[TRUE])", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "10", Hint: hint}, nil},
			{"some_alias", "SELECT 1 != SOME(ARRAY[TRUE])", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer <> boolean", Position: "10", Hint: hint}, nil},
			{"decisive_in", "SELECT 1 IN (1,TRUE)", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "10", Hint: hint}, nil},
			{"decisive_not_in", "SELECT 1 NOT IN (1,TRUE)", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer <> boolean", Position: "10", Hint: hint}, nil},
			// Competing conversion failures: PostgreSQL batches and coerces all
			// non-Var RHS inputs in source order before finalizing the lhs.
			{"batch_rhs_before_lhs_in", "SELECT 'badleft' IN (1,'badright')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "badright"`, Position: "24"}, nil},
			{"batch_rhs_before_lhs_not_in", "SELECT 'badleft' NOT IN (1,'badright')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "badright"`, Position: "28"}, nil},
			{"batch_first_rhs_in", "SELECT 'badleft' IN ('badfirst',1,'badlast')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "badfirst"`, Position: "22"}, nil},
			{"batch_first_rhs_not_in", "SELECT 'badleft' NOT IN ('badfirst',1,'badlast')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "badfirst"`, Position: "26"}, nil},
			{"empty_list", "SELECT 1 IN ()", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "14"}, nil},
			{"empty_not_list", "SELECT 1 NOT IN ()", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "18"}, nil},
			{"left_input", "SELECT 'oops' = ANY('{1}'::integer[])", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "8"}, nil},
			{"array_input", "SELECT 1 = ANY('oops')", fixture.Diagnostic{Code: "22P02", Message: `malformed array literal: "oops"`, Position: "16", Detail: `Array value must start with "{" or dimension information.`}, nil},
			{"unicode_position", "SELECT /*é*/1 = ANY(1)", fixture.Diagnostic{Code: "42809", Message: "op ANY/ALL (array) requires array on right side", Position: "15"}, nil},
			{"var_not_batch", "SELECT '2147483648' IN(i,j)", fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "8"}, nil},
			// Batch conversion must not replace the original unknown left input
			// used independently by the remaining Var comparisons.
			{"mixed_batch_var", "SELECT '2147483648' IN(1,2147483648,i,j)", fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "8"}, nil},
			{"correlated_child_var", "SELECT '2147483648' IN((SELECT i),2147483648)", fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "8"}, nil},
			// Full occurrence diagnostics observed on pinned PostgreSQL 18.0.
			{"var_param_conflict", "SELECT $1 IN(i,j)", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Position: "8", Detail: "integer versus bigint"}, []any{"1"}},
			{"bare_output_conflict", "SELECT $1,$1 IN(1,2147483648)", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Position: "8", Detail: "bigint versus text"}, []any{"1"}},
			{"already_scalar_array", "SELECT $1::INT,$1=ANY($1)", fixture.Diagnostic{Code: "42809", Message: "op ANY/ALL (array) requires array on right side", Position: "18"}, []any{"1"}},
			{"repeated_text_first", "SELECT $1 IN($1),i=$1", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer = text", Position: "19", Hint: hint}, []any{"1"}},
			{"non_boolean_operator", "SELECT 1 OPERATOR(pg_catalog.+) ANY(ARRAY[1])", fixture.Diagnostic{Code: "42809", Message: "op ANY/ALL (array) requires operator to yield boolean", Position: "10"}, nil},
			{"unsupported_json", "SELECT '{}'::JSON = ANY(ARRAY['{}'::JSON])", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: json = json", Position: "19", Hint: hint}, nil},
			// Operator lookup precedes unknown-array input. These source-derived
			// complete records must be confirmed on the pinned oracle.
			{"json_operator_before_array_any", "SELECT '{}'::JSON = ANY('oops')", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: json = unknown", Position: "19", Hint: hint}, nil},
			{"json_operator_before_array_some", "SELECT '{}'::JSON = SOME('oops')", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: json = unknown", Position: "19", Hint: hint}, nil},
			{"json_operator_before_array_all", "SELECT '{}'::JSON = ALL('oops')", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: json = unknown", Position: "19", Hint: hint}, nil},
			{"json_operator_before_array_alias", "SELECT '{}'::JSON != SOME('oops')", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: json <> unknown", Position: "19", Hint: hint}, nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, source := range []string{"ac_diag", "ac_diag_empty"} {
					t.Run(source, func(t *testing.T) {
						for _, prepared := range []bool{false, true} {
							t.Run(map[bool]string{false: "query", true: "prepare"}[prepared], func(t *testing.T) {
								s := fixture.ImplicitError(tc.q+" FROM "+source, tc.d, tc.args...)
								s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
								if prepared {
									s.ErrorStage = "prepare"
								}
								fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
								recovery := arrayCompareBool("SELECT 1 IN(1,2),1=ANY(ARRAY[1])", []any{true, true})
								recovery.Prepared = prepared
								fixture.RunStep(t, p.Ctx, c, b.Recorder, recovery)
							})
						}
					})
				}
			})
		}
		t.Run("malformed_parameter_and_held_recovery", func(t *testing.T) {
			const q = "SELECT 1=ANY($1)"
			bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `malformed array literal: "oops"`, Detail: `Array value must start with "{" or dimension information.`}, "oops")
			bad.SelectRows, bad.ErrorStage = true, "execute"
			bad.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
			arrayCompareBoth(t, p, b, c, bad)
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			bad.Prepared = false
			fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, arrayCompareBool(q, []any{true}, "{1,NULL}"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, arrayCompareBool(q, []any{nil}, "{2,NULL}"))
		})
		arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT 1 IN(1,2),1=ANY(ARRAY[1])", []any{true, true}))
	})
}
