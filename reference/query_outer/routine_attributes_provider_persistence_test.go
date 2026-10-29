package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/lib/pq"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Every case uses a genuine seeded or SQL-installed provider. In particular the
// first scalar case has no ROWS override: a projection failure in a different
// case cannot hide loss of already-visible COST/PARALLEL during logical replay.
func TestReferenceRoutineAttributesProviderPersistence(t *testing.T) {
	for _, tc := range []struct {
		name, signature, extension, declaration string
		inputs, source                          string
		result                                  int64
		sets, moved                             bool
	}{
		{name: "builtin_scalar", signature: "pg_catalog.abs(integer)"},
		{name: "builtin_set", signature: "pg_catalog.generate_series(integer,integer)", sets: true},
		{name: "installed_scalar", signature: "public.soundex(text)", extension: "fuzzystrmatch", moved: true, inputs: "25", source: "soundex", result: 25},
		{name: "installed_intagg", signature: "public.int_array_enum(integer[])", extension: "intagg", sets: true, inputs: "1007", source: "array_unnest", result: 23},
		{name: "installed_pgrowlocks", signature: "public.pgrowlocks(text)", extension: "pgrowlocks", sets: true, inputs: "25", source: "pgrowlocks", result: 2249},
		{name: "installed_amcheck", signature: "public.verify_heapam(regclass,boolean,boolean,text,bigint,bigint)", extension: "amcheck", sets: true, inputs: "2205 16 16 25 20 20", source: "verify_heapam", result: 2249},
		{name: "range_two_inputs", signature: "public.ra_provider_range(integer,integer)", declaration: "CREATE TYPE ra_provider_range AS RANGE(subtype=integer)"},
		{name: "range_three_inputs", signature: "public.ra_provider_range(integer,integer,text)", declaration: "CREATE TYPE ra_provider_range AS RANGE(subtype=integer)"},
		{name: "multirange_empty", signature: "public.ra_provider_multi()", declaration: "CREATE TYPE ra_provider_range AS RANGE(subtype=integer,multirange_type_name=ra_provider_multi)"},
		{name: "multirange_one_input", signature: "public.ra_provider_multi(ra_provider_range)", declaration: "CREATE TYPE ra_provider_range AS RANGE(subtype=integer,multirange_type_name=ra_provider_multi)"},
		{name: "multirange_variadic", signature: "public.ra_provider_multi(ra_provider_range[])", declaration: "CREATE TYPE ra_provider_range AS RANGE(subtype=integer,multirange_type_name=ra_provider_multi)"},
		{name: "pl_call_handler", signature: "pg_catalog.plpgsql_call_handler()"},
		{name: "pl_inline_handler", signature: "pg_catalog.plpgsql_inline_handler(internal)"},
		{name: "pl_validator", signature: "pg_catalog.plpgsql_validator(oid)"},
		{name: "access_method", signature: "pg_catalog.heap_tableam_handler(internal)"},
		{name: "brin_support", signature: "pg_catalog.brin_minmax_opcinfo(internal)"},
		{name: "conversion", signature: "pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"ra_provider_owner", "ra_provider_reader", "ra_provider_denied"})
			fixture.SchemaReferenceCleanup(t, "ra_provider_moved")
			p := fixture.NewScenario(t)
			backends := p.Backends()
			// The PostgreSQL declaration/catalog oracle runs before native controls.
			for i := len(backends) - 1; i >= 0; i-- {
				b := backends[i]
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					resultsExec(t, p, b, c, "CREATE ROLE ra_provider_owner", "CREATE ROLE ra_provider_reader", "CREATE ROLE ra_provider_denied", "GRANT USAGE ON SCHEMA public TO PUBLIC",
						"CREATE TABLE ra_provider_data(v integer,note text)", "ALTER TABLE ra_provider_data OWNER TO ra_provider_owner", "GRANT SELECT ON ra_provider_data TO ra_provider_reader")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_provider_data VALUES(-42,'Ashcraft')", 1))
					if tc.extension != "" {
						resultsExec(t, p, b, c, "CREATE EXTENSION "+tc.extension+" SCHEMA public")
					}
					if tc.declaration != "" {
						resultsExec(t, p, b, c, tc.declaration)
					}
					// Capture PostgreSQL's genuine seed at cleanup registration, not
					// native defaults (handler defaults can legitimately differ).
					// Patched oracle expectations never consume these values.
					cleanupSeed := func(pg *sql.Conn) func() {
						t.Helper()
						if !strings.HasPrefix(tc.signature, "pg_catalog.") {
							return func() {}
						}
						var parallel string
						var cost, rows float64
						if err := pg.QueryRowContext(p.Ctx, "SELECT proparallel::text,procost,prorows FROM pg_proc WHERE oid=$1::regprocedure", tc.signature).Scan(&parallel, &cost, &rows); err != nil {
							t.Fatal("real seeded signature", tc.signature, err)
						}
						// Seed extension replay can name every real handler owner,
						// not just the routine whose estimates this case changes.
						owners, err := pg.QueryContext(p.Ctx, "SELECT p.oid::regprocedure::text,p.proowner::regrole::text FROM pg_proc p LEFT JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid WHERE p.oid=$1::regprocedure OR e.extname='plpgsql'", tc.signature)
						if err != nil {
							t.Fatal("capture genuine seed owners", err)
						}
						var restoreOwners []string
						for owners.Next() {
							var signature, owner string
							if err := owners.Scan(&signature, &owner); err != nil {
								owners.Close()
								t.Fatal(err)
							}
							restoreOwners = append(restoreOwners, "ALTER FUNCTION "+signature+" OWNER TO "+pq.QuoteIdentifier(owner))
						}
						err = owners.Err()
						owners.Close()
						if err != nil {
							t.Fatal("capture genuine seed owners", err)
						}
						q := "ALTER FUNCTION " + tc.signature + " PARALLEL " + map[string]string{"s": "SAFE", "r": "RESTRICTED", "u": "UNSAFE"}[parallel] + " COST " + attributesProviderNumber(cost)
						if tc.sets {
							q += " ROWS " + attributesProviderNumber(rows)
						}
						return func() {
							ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							defer cancel()
							for _, ownerSQL := range restoreOwners {
								if _, err := pg.ExecContext(ctx, ownerSQL); err != nil {
									t.Error("restore seeded owner", ownerSQL, err)
								}
							}
							if _, err := pg.ExecContext(ctx, q); err != nil {
								t.Error("restore seeded metadata", q, err)
							}
						}
					}
					if b.PG {
						defer cleanupSeed(c)()
					}
					signature := tc.signature
					if tc.moved {
						resultsExec(t, p, b, c, "CREATE SCHEMA ra_provider_moved", "GRANT USAGE ON SCHEMA ra_provider_moved TO PUBLIC",
							"ALTER FUNCTION public.soundex(text) RENAME TO ra_provider_soundex", "ALTER FUNCTION public.ra_provider_soundex(text) SET SCHEMA ra_provider_moved",
							"ALTER FUNCTION ra_provider_moved.ra_provider_soundex(text) OWNER TO ra_provider_owner",
							"REVOKE EXECUTE ON FUNCTION ra_provider_moved.ra_provider_soundex(text) FROM PUBLIC",
							"GRANT EXECUTE ON FUNCTION ra_provider_moved.ra_provider_soundex(text) TO ra_provider_reader")
						signature = "ra_provider_moved.ra_provider_soundex(text)"
					}
					identity := attributesProviderIdentity(t, p, c, signature)
					q, resultType, resultValue := "SELECT pg_catalog.abs(v) AS v FROM ra_provider_data", "INT4", any(int64(42))
					switch tc.name {
					case "builtin_set":
						q = "SELECT pg_catalog.generate_series(abs(v),abs(v)) AS v FROM ra_provider_data"
					case "installed_scalar":
						q, resultType, resultValue = "SELECT ra_provider_moved.ra_provider_soundex(note) AS v FROM ra_provider_data", "TEXT", "A226"
					case "installed_intagg":
						q = "SELECT public.int_array_enum(ARRAY[abs(v)]) AS v FROM ra_provider_data"
					}
					// Direct and freshly prepared ALTER exercise the accepted publication
					// paths. Only estimates change, never intrinsic return-set capability.
					for _, prepared := range []bool{false, true} {
						q := "ALTER FUNCTION " + signature + " PARALLEL RESTRICTED COST 9"
						if tc.sets {
							q += " ROWS 23"
						}
						s := fixture.Exec(q, 0)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						attributesProviderIdentityBoth(t, p, b, c, signature, identity)
					}
					checkPhase := func(c *sql.Conn, b fixture.Backend, emittedSQL bool) {
						t.Helper()
						rows := float64(0)
						if tc.sets {
							rows = 23
						}
						attributesBoth(t, p, b, c, "SELECT proparallel::text AS parallel,procost+0::real AS cost,prorows+0::real AS rows,proretset OR false AS sets FROM pg_proc WHERE oid=$1::regprocedure",
							[]string{"parallel", "cost", "rows", "sets"}, []string{"TEXT", "FLOAT4", "FLOAT4", "BOOL"}, [][]any{{"r", float64(9), rows, tc.sets}}, signature)
						if !tc.moved {
							// Installed declarations, including stock PL handlers, carry
							// their native named owner in ordinary replay SQL. Other
							// seeded/generated PG providers retain the oracle owner.
							owner := "postgres"
							if b.PG && (!emittedSQL || tc.extension == "" && !strings.HasPrefix(tc.name, "pl_")) {
								owner = "stuntgres"
							}
							reader := tc.extension != "amcheck"
							attributesBoth(t, p, b, c, "SELECT proowner::regrole::text AS owner,has_function_privilege('ra_provider_reader',$1::text,'EXECUTE') OR false AS reader,has_function_privilege('ra_provider_denied',$1::text,'EXECUTE') OR false AS denied FROM pg_proc WHERE oid=$1::regprocedure",
								[]string{"owner", "reader", "denied"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{owner, reader, reader}}, signature)
						}
						if strings.HasPrefix(tc.signature, "pg_catalog.") && !strings.HasPrefix(tc.name, "pl_") {
							attributesBoth(t, p, b, c, "SELECT oid::bigint AS id FROM pg_proc WHERE oid=$1::regprocedure", []string{"id"}, []string{"INT8"}, [][]any{{identity[0]}}, signature)
						}
						attributesBoth(t, p, b, c, "SELECT relowner='ra_provider_owner'::regrole AS owner,has_table_privilege('ra_provider_reader','ra_provider_data','SELECT') OR false AS reader,has_table_privilege('ra_provider_denied','ra_provider_data','SELECT') OR false AS denied_read FROM pg_class WHERE oid='ra_provider_data'::regclass",
							[]string{"owner", "reader", "denied_read"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}})
						attributesIntBoth(t, p, b, c, "SELECT pg_catalog.abs(v) AS v FROM ra_provider_data", 42)
						attributesBoth(t, p, b, c, q, []string{"v"}, []string{resultType}, [][]any{{resultValue}})
						if tc.extension != "" {
							sourceMetadata := resultsTextMetadata
							sourceMetadata.NullableKnown = true
							attributesBothMetadata(t, p, b, c, "SELECT p.oid=$1::regprocedure AS identity,e.extnamespace='public'::regnamespace AS installation,p.prorettype=$3::oid AS result,p.proargtypes::text AS inputs,p.prosrc AS source FROM pg_proc p JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE p.oid=$1::regprocedure AND d.classid='pg_proc'::regclass AND d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname=$2::name",
								[]string{"identity", "installation", "result", "inputs", "source"}, []string{"BOOL", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{true, true, true, tc.inputs, tc.source}},
								[]resultsNativeColumn{{}, {}, {}, resultsTextMetadata, sourceMetadata}, signature, tc.extension, tc.result)
						}
						if tc.moved {
							denied := false
							var acl any = "{ra_provider_owner=X/ra_provider_owner,ra_provider_reader=X/ra_provider_owner}"
							attributesBoth(t, p, b, c, "SELECT proowner::regrole::text AS owner,has_function_privilege('ra_provider_reader',$1,'EXECUTE') OR false AS reader,has_function_privilege('ra_provider_denied',$1,'EXECUTE') OR false AS denied_execute,proacl::text AS acl FROM pg_proc WHERE oid=$1::regprocedure",
								[]string{"owner", "reader", "denied_execute", "acl"}, []string{"TEXT", "BOOL", "BOOL", "TEXT"}, [][]any{{"ra_provider_owner", true, denied, acl}}, signature)
							attributesTextBoth(t, p, b, c, "SELECT ra_provider_moved.ra_provider_soundex('Ashcraft') AS v", "A226")
						}
						if tc.extension == "intagg" {
							attributesBoth(t, p, b, c, "SELECT public.int_array_enum('{2,NULL,2}'::integer[]) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {nil}, {int64(2)}})
						}
						if tc.declaration != "" {
							attributesTextBoth(t, p, b, c, "SELECT public.ra_provider_range(1,4)::text AS v", "[1,4)")
							resultType := "ra_provider_range"
							if strings.HasPrefix(tc.name, "multirange_") {
								resultType = "ra_provider_multi"
								attributesTextBoth(t, p, b, c, "SELECT public.ra_provider_multi(public.ra_provider_range(1,4))::text AS v", "{[1,4)}")
							}
							attributesBoth(t, p, b, c, "SELECT prorettype=$2::regtype AS result FROM pg_proc WHERE oid=$1::regprocedure", []string{"result"}, []string{"BOOL"}, [][]any{{true}}, signature, resultType)
						}
						// A real PL invocation keeps handler routing meaningful without
						// pretending handler pseudotypes are ordinary callable values.
						if strings.HasPrefix(tc.name, "pl_") {
							attributesIntBoth(t, p, b, c, "SELECT ra_provider_pl() AS v", 42)
						}
					}
					check := func(c *sql.Conn, b fixture.Backend) {
						t.Helper()
						checkPhase(c, b, false)
					}
					if strings.HasPrefix(tc.name, "pl_") {
						resultsExec(t, p, b, c, "CREATE FUNCTION ra_provider_pl() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 42; END$$", "DO $$BEGIN PERFORM ra_provider_pl(); END$$")
					}
					check(c, b)
					if b.PG {
						return
					} // Explicit native-only replacement phases; shared SQL assertions above.
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := func() {
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, attributesStep(b, q, []string{"v"}, []string{resultType}, [][]any{{resultValue}}))
					}
					held()
					retained, err := c.QueryContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					var emitted []byte
					for range 2 {
						emitted = resultsNativeReplayRound(t, p, b, c, check)
						held()
						if p.Postgres != nil {
							pg, err := p.Postgres.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							func() {
								defer pg.Close()
								defer cleanupSeed(pg)()
								pgb := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
								resultsExec(t, p, pgb, pg, "RESET SESSION AUTHORIZATION", "DROP SCHEMA public CASCADE", "DROP SCHEMA IF EXISTS ra_provider_moved CASCADE", "CREATE SCHEMA public", "GRANT USAGE ON SCHEMA public TO PUBLIC")
								for _, role := range []string{"ra_provider_owner", "ra_provider_reader", "ra_provider_denied"} {
									var n int
									if err := pg.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname=$1", role).Scan(&n); err != nil {
										t.Fatal(err)
									}
									if n == 0 {
										resultsExec(t, p, pgb, pg, "CREATE ROLE "+role)
									}
								}
								if _, err := pg.ExecContext(p.Ctx, string(emitted)); err != nil {
									t.Fatal("unchanged provider dump SQL on PostgreSQL", err)
								}
								resultsExec(t, p, pgb, pg, "RESET SESSION AUTHORIZATION", "SET check_function_bodies=true")
								checkPhase(pg, pgb, true)
							}()
						}
					}
					attributesMetadata(b, []string{"v"}, []string{resultType})(t, retained)
					if !retained.Next() {
						t.Fatal("held backing lost after two replacements", retained.Err())
					}
					var v any
					if err := retained.Scan(&v); err != nil || fixture.Normalize(v) != resultValue {
						t.Fatal("held backing", v, err)
					}
					if retained.Next() || retained.Err() != nil {
						t.Fatal("held backing extra row/error", retained.Err())
					}
					retained.Close()
					connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh-provider")
					if err != nil {
						t.Fatal(err)
					}
					raw := sql.OpenDB(connector)
					defer raw.Close()
					recorded, rec := fixture.RecordedNativePool(t, connector)
					for _, sql := range []string{"CREATE ROLE ra_provider_pressure", "CREATE ROLE ra_provider_owner", "CREATE ROLE ra_provider_reader", "CREATE ROLE ra_provider_denied", "CREATE TYPE ra_provider_pressure AS(v integer)"} {
						fixture.RunStep(t, p.Ctx, recorded, rec, fixture.Exec(sql, 0))
					}
					if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted)); err != nil {
						t.Fatal("fresh driver provider restore", err)
					}
					clone, err := recorded.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer clone.Close()
					check(clone, fixture.Backend{Recorder: rec})
					fixture.RunStep(t, p.Ctx, clone, rec, fixture.Exec("UPDATE ra_provider_data SET v=-99", 1))
					check(c, b)
					held()
					incumbent := attributesProviderIdentity(t, p, c, signature)
					var before bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
						t.Fatal(err)
					}
					want := proceduresDiagnostic("22023", "COST must be positive", 0)
					bad := before.String() + ";CREATE TABLE ra_provider_prefix(v integer);ALTER FUNCTION " + signature + " COST 0;"
					fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
					atomic := func() {
						check(c, b)
						attributesProviderIdentityBoth(t, p, b, c, signature, incumbent)
						proceduresCount(t, p, b, c, "pg_class WHERE relname='ra_provider_prefix'", 0)
						held()
						var after bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(before.Bytes(), after.Bytes()) {
							t.Fatal("failed Restore changed provider identity, metadata, authority or backing")
						}
					}
					atomic()
					failure := errors.New("provider metadata restore read failure")
					if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(bytes.NewReader(before.Bytes()), strings.NewReader(";CREATE TABLE ra_provider_prefix(v integer);"), resultsFailReader{failure})); !errors.Is(err, failure) {
						t.Fatal("late reader failure", err)
					}
					atomic()
				})
			}
		})
	}
}

// Numeric spelling is cleanup-only; it never supplies expected oracle values.
func attributesProviderNumber(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func attributesProviderIdentity(t *testing.T, p *fixture.Pair, c *sql.Conn, signature string) []any {
	t.Helper()
	var oid, owner, result int64
	var acl sql.NullString
	var inputs, source string
	if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,proowner::bigint,proacl::text,prorettype::bigint,proargtypes::text,prosrc FROM pg_proc WHERE oid=$1::regprocedure", signature).Scan(&oid, &owner, &acl, &result, &inputs, &source); err != nil || oid <= 0 || owner <= 0 || result <= 0 {
		t.Fatal("provider identity", signature, oid, owner, result, err)
	}
	var aclValue any
	if acl.Valid {
		aclValue = acl.String
	}
	return []any{oid, owner, aclValue, result, inputs, source}
}

func attributesProviderIdentityBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, signature string, identity []any) {
	t.Helper()
	attributesBothMetadata(t, p, b, c, "SELECT oid::bigint AS id,proowner::bigint AS owner,proacl::text AS acl,prorettype::bigint AS result,proargtypes::text AS inputs,prosrc AS source FROM pg_proc WHERE oid=$1::regprocedure",
		[]string{"id", "owner", "acl", "result", "inputs", "source"}, []string{"INT8", "INT8", "TEXT", "INT8", "TEXT", "TEXT"}, [][]any{identity},
		[]resultsNativeColumn{{}, {}, resultsTextMetadata, {}, resultsTextMetadata, {Length: 1<<63 - 1, LengthKnown: true, NullableKnown: true}}, signature)
}
