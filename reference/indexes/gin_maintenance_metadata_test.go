package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGINMaintenanceMetadata(t *testing.T) {
	t.Run("GinMaintCatalogMetadata", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, _ := classConn(t, p, b)
			// REL_18_6 pg_proc.dat OID 3789 plus pg_proc.h defaults. This is
			// an independent executable-declaration witness, not an identity-only
			// inventory or metadata learned from either backend.
			q := `SELECT oid::bigint AS oid,proname::text AS proname,
pronamespace::bigint AS pronamespace,proowner::bigint AS proowner,
prolang::bigint AS prolang,procost,prorows,
provariadic::bigint AS provariadic,prosupport::oid::bigint AS prosupport,
prokind::text AS prokind,prosecdef,proleakproof,proisstrict,proretset,
provolatile::text AS provolatile,proparallel::text AS proparallel,
pronargs::bigint AS pronargs,pronargdefaults::bigint AS pronargdefaults,
prorettype::bigint AS prorettype,proargtypes::text AS proargtypes,
proallargtypes::text AS proallargtypes,proargmodes::text AS proargmodes,
proargnames::text AS proargnames,proargdefaults IS NOT NULL AS has_defaults,
protrftypes::text AS protrftypes,prosrc,probin,
prosqlbody IS NOT NULL AS has_sqlbody,proconfig::text AS proconfig,
proacl::text AS proacl FROM pg_catalog.pg_proc WHERE oid=3789`
			s := fixture.Select(q,
				[]string{"oid", "proname", "pronamespace", "proowner", "prolang", "procost", "prorows", "provariadic", "prosupport", "prokind", "prosecdef", "proleakproof", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "has_defaults", "protrftypes", "prosrc", "probin", "has_sqlbody", "proconfig", "proacl"},
				[]string{"INT8", "TEXT", "INT8", "INT8", "INT8", "FLOAT4", "FLOAT4", "INT8", "INT8", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "INT8", "INT8", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT"},
				[][]any{{int64(3789), "gin_clean_pending_list", int64(11), int64(10), int64(12), float64(1), float64(0), int64(0), int64(0), "f", false, false, true, false, "v", "u", int64(1), int64(0), int64(20), "2205", nil, nil, nil, false, nil, "gin_clean_pending_list", nil, false, nil, nil}})
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, classHeld(t, p, conn, q), b.Recorder, s)
			classRoutes(t, p, b, conn, fixture.Select(`SELECT 'pg_catalog.gin_clean_pending_list(regclass)'::regprocedure::oid::bigint AS oid`, []string{"oid"}, []string{"INT8"}, [][]any{{int64(3789)}}))
		})
	})

	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run("GinMaintResultMetadata/"+route, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				run(fixture.Exec("CREATE TABLE gin_maint_oracle(id integer,a integer[])", 0))
				run(fixture.Exec("CREATE INDEX gin_maint_oracle_i ON gin_maint_oracle USING gin(a)", 0))
				q := "SELECT pg_catalog.gin_clean_pending_list($1::regclass)"
				positive := q + " > 0 AS cleaned"
				var execute func(fixture.Step)
				switch route {
				case "direct":
					execute = run
				case "fresh":
					execute = func(s fixture.Step) { run(fixture.Prepared(s)) }
				case "held":
					// Prepare against the empty index, then keep both statements
					// through multiple new pending batches. Preparation must not
					// execute, fold the volatile call, or capture an obsolete root.
					raw := classHeld(t, p, conn, q)
					clean := classHeld(t, p, conn, positive)
					execute = func(s fixture.Step) {
						if s.Query == q {
							fixture.RunStep(t, p.Ctx, raw, b.Recorder, s)
						} else {
							fixture.RunStep(t, p.Ctx, clean, b.Recorder, s)
						}
					}
				}
				execute(ginMaintResult(q, int64(0), "gin_maint_oracle_i"))
				for _, insert := range []string{
					"INSERT INTO gin_maint_oracle VALUES(1,'{1,2,2}')",
					"INSERT INTO gin_maint_oracle VALUES(2,'{2}')",
				} {
					run(fixture.Exec(insert, 1))
					execute(ginMaintPositive(positive, "gin_maint_oracle_i"))
					execute(ginMaintResult(q, int64(0), "gin_maint_oracle_i"))
				}
				run(ginIDs(ginMaintIDsSQL, []int64{1, 2}))
			})
		})
	}

	for _, q := range []string{
		"SELECT gin_clean_pending_list(NULL)",
		"SELECT pg_catalog.gin_clean_pending_list(NULL::regclass)",
		"SELECT gin_clean_pending_list($1::regclass)",
	} {
		t.Run("GinMaintNullMetadata/"+q, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				ginMaintSeed(run, false)
				s := ginMaintResult(q, nil)
				if q == "SELECT gin_clean_pending_list($1::regclass)" {
					s.Args = []any{nil}
				}
				classRoutes(t, p, b, conn, s)
				fixture.RunStep(t, p.Ctx, classHeld(t, p, conn, q), b.Recorder, s)
				// Strict NULL must not run the body or drain unrelated pending work.
				run(ginMaintPositive(ginMaintPositiveSQL))
				run(ginMaintResult(ginMaintCleanSQL, int64(0)))
			})
		})
	}
}
