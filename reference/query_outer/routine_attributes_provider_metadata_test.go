package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Independent release declarations:
// https://github.com/postgres/postgres/blob/REL_18_6/src/include/catalog/pg_proc.dat
// (OID 1067, generate_series_int4, int4/int4 -> SETOF int4, ROWS 1000),
// https://github.com/postgres/postgres/blob/REL_18_6/contrib/intagg/intagg--1.1.sql
// (int_array_enum(int4[]) -> SETOF int4, array_unnest, INTERNAL IMMUTABLE
// STRICT PARALLEL SAFE). INTERNAL's default COST is 1 and SETOF's default
// ROWS is 1000 (functioncmds.c). These estimates do not alter set capability
// or the number of rows actually returned. This checks published pg_proc,
// never an unpublished candidate or an implementation-private descriptor.
func TestReferenceRoutineAttributesProviderMetadata(t *testing.T) {
	for _, provider := range []string{"builtin", "installed_intagg"} {
		t.Run(provider, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"ra_pm_caller"})
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				signature, name, arguments := "pg_catalog.generate_series(integer,integer)", "generate_series", "(integer, integer)"
				argtypes, source, namespace := "23 23", "generate_series_int4", int64(11)
				q := "SELECT pg_catalog.generate_series($1::integer,$2::integer) AS v ORDER BY v"
				callArgs := []any{2, 4}
				callRows := [][]any{{int64(2)}, {int64(3)}, {int64(4)}}
				if provider == "installed_intagg" {
					resultsExec(t, p, b, c, "CREATE EXTENSION intagg SCHEMA public")
					signature, name, arguments = "public.int_array_enum(integer[])", "int_array_enum", "(integer[])"
					argtypes, source = "1007", "array_unnest"
					if err := c.QueryRowContext(p.Ctx, "SELECT 'public'::regnamespace::bigint").Scan(&namespace); err != nil || namespace <= 0 {
						t.Fatal("actual installation namespace", namespace, err)
					}
					q = "SELECT public.int_array_enum($1::integer[]) AS v ORDER BY v NULLS LAST"
					callArgs = []any{"{2,2,NULL}"}
					callRows = [][]any{{int64(2)}, {int64(2)}, {nil}}
				}
				// pg_catalog survives public-schema fixture cleanup. Restore the
				// released builtin defaults even when a later assertion is fatal.
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil {
						t.Error("provider metadata cleanup role", err)
					}
					if provider == "builtin" {
						if _, err := c.ExecContext(ctx, "ALTER FUNCTION "+signature+" PARALLEL SAFE COST 1 ROWS 1000"); err != nil {
							t.Error("provider metadata cleanup defaults", err)
						}
					}
				}()
				resultsExec(t, p, b, c, "CREATE ROLE ra_pm_caller", "GRANT USAGE ON SCHEMA public TO ra_pm_caller")
				var oid, owner int64
				var acl sql.NullString
				if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,proowner::bigint,proacl::text FROM pg_proc WHERE oid=$1::regprocedure", signature).Scan(&oid, &owner, &acl); err != nil || oid <= 0 || owner <= 0 {
					t.Fatal("provider identity", oid, owner, acl, err)
				}
				if provider == "builtin" && oid != 1067 {
					t.Fatalf("generate_series(integer,integer) OID = %d, want released OID 1067", oid)
				}
				var aclValue any
				if acl.Valid {
					aclValue = acl.String
				}
				where := " FROM pg_proc WHERE oid='" + signature + "'::regprocedure"
				catalogQ := "SELECT prokind,provolatile,proisstrict,prosecdef,proparallel,procost,prorows,proretset,proconfig" + where
				catalogNames := []string{"prokind", "provolatile", "proisstrict", "prosecdef", "proparallel", "procost", "prorows", "proretset", "proconfig"}
				catalogTypes := []string{"CHAR", "CHAR", "BOOL", "BOOL", "CHAR", "FLOAT4", "FLOAT4", "BOOL", "_TEXT"}
				catalogProfile := []resultsNativeColumn{resultsNonNullMetadata, resultsNonNullMetadata, resultsNonNullMetadata, resultsNonNullMetadata, resultsNonNullMetadata, resultsNonNullMetadata, resultsNonNullMetadata, resultsNonNullMetadata, resultsNullableMetadata}
				identityQ := "SELECT oid::bigint AS id,proowner::bigint AS owner,proacl::text AS acl,proname::text AS name,pronamespace::bigint AS namespace,proargtypes::text AS arguments,prorettype::bigint AS result,prolang::bigint AS language,prosrc::text AS source,pronargs::integer AS inputs,pronargdefaults::integer AS defaults" + where
				identityNames := []string{"id", "owner", "acl", "name", "namespace", "arguments", "result", "language", "source", "inputs", "defaults"}
				identityTypes := []string{"INT8", "INT8", "TEXT", "TEXT", "INT8", "TEXT", "INT8", "INT8", "TEXT", "INT4", "INT4"}
				identityProfile := []resultsNativeColumn{{}, {}, resultsTextMetadata, resultsTextMetadata, {}, resultsTextMetadata, {}, {}, {Length: 1<<63 - 1, LengthKnown: true, NullableKnown: true}, {}, {}}
				inputs := int64(2)
				if provider == "installed_intagg" {
					inputs = 1
				}
				identityRows := [][]any{{oid, owner, aclValue, name, namespace, argtypes, int64(23), int64(12), source, inputs, int64(0)}}
				prepare := func(query string) fixture.HeldStatement {
					t.Helper()
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal("provider metadata Prepare", err)
					}
					t.Cleanup(func() { stmt.Close() })
					return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				}
				// All three held statements predate every metadata mutation.
				catalogHeld, identityHeld, callHeld := prepare(catalogQ), prepare(identityQ), prepare(q)
				check := func(phase, parallel string, cost, rows float64) {
					t.Helper()
					for _, route := range []struct {
						name     string
						prepared bool
						held     bool
					}{{"direct", false, false}, {"fresh_prepared", true, false}, {"held_pre_alter", false, true}} {
						t.Run(phase+"/"+route.name, func(t *testing.T) {
							cat := attributesStep(b, catalogQ, catalogNames, catalogTypes, [][]any{{"f", "i", true, false, parallel, cost, rows, true, nil}})
							cat.CheckMetadata = attributesMetadata(b, catalogNames, catalogTypes, catalogProfile...)
							identity := attributesStep(b, identityQ, identityNames, identityTypes, identityRows)
							identity.CheckMetadata = attributesMetadata(b, identityNames, identityTypes, identityProfile...)
							call := attributesStep(b, q, []string{"v"}, []string{"INT4"}, callRows, callArgs...)
							cat.Prepared, identity.Prepared, call.Prepared = route.prepared, route.prepared, route.prepared
							var catalogExecutor, identityExecutor, callExecutor fixture.Executor = c, c, c
							if route.held {
								catalogExecutor, identityExecutor, callExecutor = catalogHeld, identityHeld, callHeld
							}
							fixture.RunStep(t, p.Ctx, catalogExecutor, b.Recorder, cat)
							fixture.RunStep(t, p.Ctx, identityExecutor, b.Recorder, identity)
							fixture.RunStep(t, p.Ctx, callExecutor, b.Recorder, call)
						})
					}
				}
				check("baseline", "s", 1, 1000)
				parallel, cost, estimate := "s", float64(1), float64(1000)
				// Direct and held-execute ALTER each publish 23 then 31. Preparing
				// the ALTER must not itself publish a metadata patch.
				for _, prepared := range []bool{false, true} {
					for _, rows := range []int{23, 31} {
						phase := fmt.Sprintf("rows_%d/prepared_%t", rows, prepared)
						alter := fmt.Sprintf("ALTER FUNCTION %s PARALLEL RESTRICTED COST 9 ROWS %d", signature, rows)
						var executor fixture.Executor = c
						if prepared {
							executor = prepare(alter)
							check("after_prepare/"+phase, parallel, cost, estimate)
						}
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(alter, 0))
						parallel, cost, estimate = "r", 9, float64(rows)
						check(phase, parallel, cost, estimate)
					}
				}
				// Owner admission precedes option validation; neither the valid
				// patch nor the invalid ROWS patch may alter any preceding field.
				resultsExec(t, p, b, c, "SET ROLE ra_pm_caller")
				for _, suffix := range []string{"PARALLEL SAFE COST 7 ROWS 23", "PARALLEL SAFE COST 7 ROWS -1"} {
					for _, prepared := range []bool{false, true} {
						alter := "ALTER FUNCTION " + name + arguments + " " + suffix
						var executor fixture.Executor = c
						if prepared {
							executor = prepare(alter)
							check("owner_after_prepare/"+suffix, parallel, cost, estimate)
						}
						step := fixture.ErrorDiagnostic(alter, proceduresDiagnostic("42501", "must be owner of function "+name, 0))
						step.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
						check(fmt.Sprintf("owner_rejected/%s/prepared_%t", suffix, prepared), parallel, cost, estimate)
					}
				}
				resultsExec(t, p, b, c, "RESET ROLE")
				for _, rows := range []string{"0", "-1"} {
					for _, prepared := range []bool{false, true} {
						alter := "ALTER FUNCTION " + signature + " PARALLEL SAFE COST 7 ROWS " + rows
						var executor fixture.Executor = c
						if prepared {
							executor = prepare(alter)
							check("invalid_after_prepare/"+rows, parallel, cost, estimate)
						}
						step := fixture.ErrorDiagnostic(alter, proceduresDiagnostic("22023", "ROWS must be positive", 0))
						step.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
						check(fmt.Sprintf("invalid_rejected/%s/prepared_%t", rows, prepared), parallel, cost, estimate)
					}
				}
				// Execution-clause PG-success/native-0A000 distinctions remain in
				// TestReferenceRoutineAttributesNativeProviderBoundary. Do not
				// confuse this admitted metadata patch with an execution promise.
			})
		})
	}
}
