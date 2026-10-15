package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNamespaceIdentityDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	// Independent diagnostic expectations also hold for Prepare and direct Query.
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := namespaceConn(t, p, b)
			for _, tc := range []struct{ q, code, message, pos, hint string }{
				{"SELECT current_schema(1)", "42883", "function current_schema(integer) does not exist", "8", hint},
				{"SELECT current_schema(NULL)", "42883", "function current_schema(unknown) does not exist", "8", hint},
				{"SELECT current_schemas()", "42883", "function current_schemas() does not exist", "8", hint},
				{"SELECT current_schemas(false,true)", "42883", "function current_schemas(boolean, boolean) does not exist", "8", hint},
				{"SELECT current_schemas(1)", "42883", "function current_schemas(integer) does not exist", "8", hint},
				{"SELECT current_schemas('not_bool')", "22P02", `invalid input syntax for type boolean: "not_bool"`, "24", ""},
				{"SELECT pg_my_temp_schema(1)", "42883", "function pg_my_temp_schema(integer) does not exist", "8", hint},
				{"SELECT pg_my_temp_schema(NULL)", "42883", "function pg_my_temp_schema(unknown) does not exist", "8", hint},
				{"SELECT pg_is_other_temp_schema()", "42883", "function pg_is_other_temp_schema() does not exist", "8", hint},
				{"SELECT pg_is_other_temp_schema(1,2)", "42883", "function pg_is_other_temp_schema(integer, integer) does not exist", "8", hint},
				{"SELECT pg_is_other_temp_schema(true)", "42883", "function pg_is_other_temp_schema(boolean) does not exist", "8", hint},
				{"SELECT pg_is_other_temp_schema('1'::text)", "42883", "function pg_is_other_temp_schema(text) does not exist", "8", hint},
				{"SELECT pg_is_other_temp_schema('not_oid')", "22P02", `invalid input syntax for type oid: "not_oid"`, "32", ""},
				{"SELECT pg_is_other_temp_schema('4294967296')", "22003", `value "4294967296" is out of range for type oid`, "32", ""},
				{"SELECT public.pg_my_temp_schema()", "42883", "function public.pg_my_temp_schema() does not exist", "8", hint},
				{"SELECT public.pg_is_other_temp_schema(0)", "42883", "function public.pg_is_other_temp_schema(integer) does not exist", "8", hint},
				{"SELECT missing_namespace_identity.pg_my_temp_schema()", "3F000", `schema "missing_namespace_identity" does not exist`, "8", ""},
				{"SELECT missing_namespace_identity.pg_is_other_temp_schema(0)", "3F000", `schema "missing_namespace_identity" does not exist`, "8", ""},
				{`SELECT "PG_MY_TEMP_SCHEMA"()`, "42883", "function PG_MY_TEMP_SCHEMA() does not exist", "8", hint},
				{`SELECT "PG_IS_OTHER_TEMP_SCHEMA"(0)`, "42883", "function PG_IS_OTHER_TEMP_SCHEMA(integer) does not exist", "8", hint},
				{"SELECT format('%s',pg_catalog.pg_my_temp_schem())", "42883", "function pg_catalog.pg_my_temp_schem() does not exist", "20", hint},
				{"/* é */ SELECT pg_my_temp_schema(NULL)", "42883", "function pg_my_temp_schema(unknown) does not exist", "16", hint},
				{"SELECT public.pg_is_other_temp_schema(namespace_identity_absent)", "42703", `column "namespace_identity_absent" does not exist`, "39", ""},
			} {
				t.Run(tc.q, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						for _, query := range []bool{false, true} {
							s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.pos, Hint: tc.hint})
							s.Prepared, s.SelectRows = prepared, query
							if prepared {
								s.ErrorStage = "prepare"
							} else {
								s.ErrorStage = "execute"
							}
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						}
					}
				})
			}
		})
	}
}
