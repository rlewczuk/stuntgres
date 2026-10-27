package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Original-OID PostgreSQL builtins take fmgr_isbuiltin's fast path before the
// pg_proc security/configuration lookup (REL_18_6 fmgr.c:167–180). Installed C
// functions reach that lookup. Native explicitly rejects these execution
// declarations before publication rather than claiming to implement them.
// record_plan_function_dependency (REL_18_6 optimizer/plan/setrefs.c) also omits
// pinned builtin OIDs from invalidation dependencies. Neither that held-plan
// exemption nor fresh preparation makes builtin fmgr consult these attributes.
func TestReferenceRoutineAttributesNativeProviderBoundary(t *testing.T) {
	for _, provider := range []string{"builtin", "builtin_actor", "installed"} {
		t.Run(provider, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"ra_np_caller"})
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				signature, name, arguments, volatility := "pg_catalog.current_setting(text)", "current_setting", "(text)", "s"
				if provider == "builtin_actor" {
					signature, name, arguments = "pg_catalog.has_table_privilege(text,text)", "has_table_privilege", "(text, text)"
					resultsExec(t, p, b, c, "CREATE TABLE ra_np_secret(v integer)", "REVOKE ALL ON TABLE ra_np_secret FROM PUBLIC")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_np_secret VALUES (42)", 1))
				}
				if provider == "installed" {
					resultsExec(t, p, b, c, "CREATE EXTENSION fuzzystrmatch SCHEMA public")
					signature, name, volatility = "public.soundex(text)", "soundex", "i"
				}
				originalVolatility := "STABLE"
				if volatility == "i" {
					originalVolatility = "IMMUTABLE"
				}
				restore := "ALTER FUNCTION " + signature + " " + originalVolatility + " STRICT SECURITY INVOKER RESET ALL"
				// public cleanup does not restore pg_catalog. Register independent,
				// bounded cleanup before the first ALTER, including fatal-test paths.
				// The baseline assertion below requires these original attributes.
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil {
						t.Error("provider cleanup role", err)
					}
					if b.PG {
						if _, err := c.ExecContext(ctx, restore); err != nil {
							t.Error("provider cleanup attributes", err)
						}
					}
				}()
				resultsExec(t, p, b, c, "CREATE ROLE ra_np_caller", "GRANT USAGE ON SCHEMA public TO ra_np_caller", "SET application_name='outside'")
				var administrator, session string
				if err := c.QueryRowContext(p.Ctx, "SELECT current_user::text,session_user::text").Scan(&administrator, &session); err != nil {
					t.Fatal(err)
				}
				var oid, owner int64
				var acl sql.NullString
				if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,proowner::bigint,proacl::text FROM pg_proc WHERE oid=$1::regprocedure", signature).Scan(&oid, &owner, &acl); err != nil || oid <= 0 || owner <= 0 {
					t.Fatal("provider identity", oid, owner, acl, err)
				}
				var aclValue any
				if acl.Valid {
					aclValue = acl.String
				}
				catalog := func(wantVolatility string, strict, definer bool, config any) {
					t.Helper()
					attributesDeclarationCatalogBoth(t, p, b, c,
						"SELECT provolatile,proisstrict,prosecdef,proparallel,procost,prorows,proconfig FROM pg_proc WHERE oid='"+signature+"'::regprocedure",
						[]string{"provolatile", "proisstrict", "prosecdef", "proparallel", "procost", "prorows", "proconfig"}, []string{"CHAR", "BOOL", "BOOL", "CHAR", "FLOAT4", "FLOAT4", "_TEXT"},
						[][]any{{wantVolatility, strict, definer, "s", float64(1), float64(0), config}})
					attributesBoth(t, p, b, c,
						"SELECT oid::bigint AS id,proowner::bigint AS owner,proacl::text AS acl FROM pg_proc WHERE oid='"+signature+"'::regprocedure",
						[]string{"id", "owner", "acl"}, []string{"INT8", "INT8", "TEXT"}, [][]any{{oid, owner, aclValue}})
				}
				catalog(volatility, true, false, nil)
				q := "SELECT pg_catalog.current_setting($1::text) AS v"
				if provider == "installed" {
					q = "SELECT public.soundex($1::text) AS v"
				}
				if provider == "builtin_actor" {
					q = "SELECT pg_catalog.has_table_privilege($1::text,$2::text) AS v"
					s := attributesStep(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, "public.ra_np_secret", "SELECT")
					s.CheckMetadata = attributesMetadata(b, []string{"v"}, []string{"BOOL"}, resultsNullableMetadata)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				}
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				invoke := func(phase string) {
					t.Helper()
					runRoutes := func(observation, typ string, profile resultsNativeColumn, directValue, freshValue, heldValue any, args ...any) {
						t.Helper()
						for _, route := range []struct {
							name     string
							executor fixture.Executor
							prepared bool
							value    any
						}{
							{"direct", c, false, directValue},
							{"fresh_prepared", c, true, freshValue},
							{"held_pre_alter", held, false, heldValue},
						} {
							t.Run(phase+"/"+observation+"/"+route.name, func(t *testing.T) {
								s := attributesStep(b, q, []string{"v"}, []string{typ}, [][]any{{route.value}}, args...)
								s.CheckMetadata = attributesMetadata(b, []string{"v"}, []string{typ}, profile)
								s.Prepared = route.prepared
								fixture.RunStep(t, p.Ctx, route.executor, b.Recorder, s)
							})
						}
					}
					switch provider {
					case "builtin":
						profile := resultsTextMetadata
						profile.Nullable, profile.NullableKnown = true, true
						// These explicit per-route expectations are equal because the
						// builtin fast path bypasses pg_proc even for a new FmgrInfo.
						// This is not stale held-plan/fmgr state. The is_superuser
						// GUC is retained as a setting assertion, not actor evidence.
						runRoutes("application_name", "TEXT", profile, "outside", "outside", "outside", "application_name")
						runRoutes("is_superuser", "TEXT", profile, "off", "off", "off", "is_superuser")
					case "builtin_actor":
						// This builtin actually consults GetUserId (acl.c,
						// has_table_privilege_name). A caller denied SELECT remains
						// denied on all routes despite a published prosecdef flag.
						runRoutes("protected_table_actor", "BOOL", resultsNullableMetadata, false, false, false, "public.ra_np_secret", "SELECT")
					case "installed":
						// C soundex has no setting/actor observation. It witnesses
						// installed-family admission and retained implementation,
						// not invented semantic parity for its return value.
						runRoutes("soundex", "TEXT", resultsTextMetadata, "A226", "A226", "A226", "Ashcraft")
					}
				}
				outside := func(actor string) {
					t.Helper()
					attributesBoth(t, p, b, c, "SELECT current_user::text AS actor,session_user::text AS session", []string{"actor", "session"}, []string{"TEXT", "TEXT"}, [][]any{{actor, session}})
					resultsBothMetadata(t, p, b, c, "SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"outside"}}, []resultsNativeColumn{{Length: 1<<63 - 1, LengthKnown: true, NullableKnown: true}})
					resultsBothMetadata(t, p, b, c, "SHOW is_superuser", []string{"is_superuser"}, []string{"TEXT"}, [][]any{{"off"}}, []resultsNativeColumn{{Length: 1<<63 - 1, LengthKnown: true, NullableKnown: true}})
				}
				resultsExec(t, p, b, c, "SET ROLE ra_np_caller")
				invoke("baseline")
				outside("ra_np_caller")
				// Owner authority wins over the native unsupported-feature gate.
				// Use the unqualified name so both English diagnostics name the
				// same visible routine, not different qualification spellings.
				for _, prepared := range []bool{false, true} {
					missing := fixture.ErrorDiagnostic("ALTER FUNCTION ra_np_missing"+arguments+" SECURITY DEFINER SET application_name='inside'", proceduresDiagnostic("42883", "function ra_np_missing"+arguments+" does not exist", 0))
					missing.Prepared, missing.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, missing)
					s := fixture.ErrorDiagnostic("ALTER FUNCTION "+name+arguments+" SECURITY DEFINER SET application_name='inside'", proceduresDiagnostic("42501", "must be owner of function "+name, 0))
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					outside("ra_np_caller")
					invoke(fmt.Sprintf("ownership_rejected/prepared=%t", prepared))
				}
				resultsExec(t, p, b, c, "RESET ROLE")
				catalog(volatility, true, false, nil)
				for _, action := range []struct {
					clause     string
					volatility string
					strict     bool
					definer    bool
					config     any
				}{
					{"SECURITY DEFINER", volatility, true, true, nil},
					{"SECURITY INVOKER", volatility, true, false, nil},
					{"SET application_name='inside'", volatility, true, false, "{application_name=inside}"},
					{"SECURITY DEFINER SET application_name='inside'", volatility, true, true, "{application_name=inside}"},
					{"RESET application_name", volatility, true, false, nil},
					{"RESET ALL", volatility, true, false, nil},
					{"STRICT", volatility, true, false, nil},
					{"RETURNS NULL ON NULL INPUT", volatility, true, false, nil},
					{"CALLED ON NULL INPUT", volatility, false, false, nil},
					{"VOLATILE", "v", true, false, nil},
					{"STABLE", "s", true, false, nil},
					{"IMMUTABLE", "i", true, false, nil},
				} {
					for _, prepared := range []bool{false, true} {
						alter := "ALTER FUNCTION " + signature + " " + action.clause
						// This is an asserted supported-feature boundary, not a
						// backend skip: every route executes and checks a full result.
						step := fixture.Exec(alter, 0)
						if !b.PG {
							step = fixture.ErrorDiagnostic(alter, proceduresDiagnostic("0A000", "execution attributes are unsupported for native routine providers", 0))
							step.ErrorStage = "execute"
						}
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
						definer, config := false, any(nil)
						wantVolatility, strict := volatility, true
						if b.PG {
							definer, config = action.definer, action.config
							wantVolatility, strict = action.volatility, action.strict
						}
						catalog(wantVolatility, strict, definer, config)
						resultsExec(t, p, b, c, "SET ROLE ra_np_caller")
						invoke(fmt.Sprintf("%s/prepared=%t", action.clause, prepared))
						outside("ra_np_caller")
						resultsExec(t, p, b, c, "RESET ROLE")
						attributesTextBoth(t, p, b, c, "SELECT current_user::text AS v", administrator)
						if b.PG {
							resultsExec(t, p, b, c, restore)
						}
						catalog(volatility, true, false, nil)
					}
				}
			})
		})
	}
}

// SQL and PL/pgSQL are the actual supported execution families, not collateral
// casualties of a gate based on SECURITY/SET syntax alone.
func TestReferenceRoutineAttributesNativeProviderSupportedLanguages(t *testing.T) {
	for _, language := range []string{"sql", "plpgsql"} {
		t.Run(language, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				body := "AS $$SELECT current_setting('application_name')$$"
				if language == "plpgsql" {
					body = "AS $$BEGIN RETURN current_setting('application_name'); END$$"
				}
				resultsExec(t, p, b, c, "SET application_name='outside'", "CREATE FUNCTION ra_np_supported() RETURNS text LANGUAGE "+language+" "+body)
				q := "SELECT ra_np_supported() AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec("ALTER FUNCTION ra_np_supported() SECURITY DEFINER SET application_name='inside'", 0)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					attributesTextBoth(t, p, b, c, q, "inside")
					fixture.RunStep(t, p.Ctx, held, b.Recorder, attributesStep(b, q, []string{"v"}, []string{"TEXT"}, [][]any{{"inside"}}))
					resultsBothMetadata(t, p, b, c, "SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"outside"}}, []resultsNativeColumn{{Length: 1<<63 - 1, LengthKnown: true, NullableKnown: true}})
					resultsExec(t, p, b, c, "ALTER FUNCTION ra_np_supported() SECURITY INVOKER RESET ALL")
				}
			})
		})
	}
}
