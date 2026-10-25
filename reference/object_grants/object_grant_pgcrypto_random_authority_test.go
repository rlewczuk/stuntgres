package reference_test

import (
	"crypto/fips140"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Fresh random values are not goldens. Lengths and RFC 9562 version/variant
// structure are independent contracts, not entropy tests or draw comparisons.
func TestReferenceObjectGrantPGCryptoRandomAuthority(t *testing.T) {
	const uuidPattern = "^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"
	for _, tc := range []struct {
		name, signature, typ, symbol string
		result, arity                int64
		strict                       bool
	}{
		{"gen_random_bytes", "integer", "BYTEA", "pg_random_bytes", 17, 1, true},
		{"gen_random_uuid", "", "UUID", "pg_random_uuid", 2950, 0, false},
		{"fips_mode", "", "BOOL", "pg_check_fipsmode", 16, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, []string{"og_random"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				if b.PG {
					clean := func() {
						for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS pgcrypto CASCADE"} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Error(q, err)
							}
						}
					}
					defer clean()
					clean()
				}
				run(fixture.Exec("CREATE SCHEMA og_random", 0))
				run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA og_random", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_random_source(id INT,n INT)", 0))
				run(fixture.Exec("INSERT INTO og_random_source VALUES(1,1),(2,NULL),(3,0),(4,-1),(5,1025),(6,16),(7,1023),(8,1024)", 8))
				run(fixture.Exec("GRANT SELECT ON og_random_source TO og_object_reader", 0))
				call := "og_random." + tc.name + "()"
				if tc.arity == 1 {
					call = "og_random.gen_random_bytes(n)"
				}
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RoleGrantPermissionDenied(run, "SELECT "+call+" FROM og_random_source WHERE id=1", "schema", "og_random", prepared)
				})
				run(fixture.Exec("GRANT USAGE ON SCHEMA og_random TO og_object_reader", 0))

				// Actual extension membership, not an assumed allocator ordering.
				seen := map[int64]bool{3432: true}
				for _, member := range []struct {
					key, name, symbol string
					result, arity     int64
					strict            bool
				}{
					{"gen_random_bytes(integer)", "gen_random_bytes", "pg_random_bytes", 17, 1, true},
					{"gen_random_uuid()", "gen_random_uuid", "pg_random_uuid", 2950, 0, false},
					{"fips_mode()", "fips_mode", "pg_check_fipsmode", 16, 0, true},
				} {
					var oid int64
					if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid", "og_random."+member.key).Scan(&oid); err != nil || oid <= 0 || seen[oid] {
						t.Fatal("installed random identity", member.key, oid, err)
					}
					seen[oid] = true
					full := fixture.Select("SELECT prorettype::BIGINT AS result,prokind,prolang::BIGINT AS language,proisstrict,provolatile,proparallel,proretset,pronargdefaults,provariadic::BIGINT AS variadic,proargnames IS NULL AS unnamed,prosrc FROM pg_proc WHERE oid=$1", []string{"result", "prokind", "language", "proisstrict", "provolatile", "proparallel", "proretset", "pronargdefaults", "variadic", "unnamed", "prosrc"}, []string{"INT8", "CHAR", "INT8", "BOOL", "CHAR", "CHAR", "BOOL", "INT2", "INT8", "BOOL", "TEXT"}, [][]any{{member.result, "f", int64(13), member.strict, "v", "s", false, int64(0), int64(0), true, member.symbol}}, oid)
					if b.PG {
						run(full)
					}
					run(fixture.Select("SELECT p.oid::regprocedure::TEXT AS identity,p.proname,p.pronargs,p.prorettype::BIGINT AS result,p.provolatile,p.proisstrict,p.proparallel,n.nspname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=$1", []string{"identity", "proname", "pronargs", "result", "provolatile", "proisstrict", "proparallel", "nspname"}, []string{"TEXT", "NAME", "INT2", "INT8", "CHAR", "BOOL", "CHAR", "NAME"}, [][]any{{"og_random." + member.key, member.name, member.arity, member.result, "v", member.strict, "s", "og_random"}}, oid))
					run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid JOIN pg_namespace n ON n.oid=e.extnamespace WHERE d.classid=1255 AND d.objid=$1 AND d.objsubid=0 AND d.refclassid=3079 AND d.refobjsubid=0 AND d.deptype='e' AND e.extname='pgcrypto' AND e.extversion='1.4' AND e.extrelocatable AND n.nspname='og_random'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, oid))
				}
				run(fixture.Select("SELECT oid::BIGINT AS identity,proisstrict,provolatile,proparallel,prorettype::BIGINT AS result FROM pg_proc WHERE oid=3432", []string{"identity", "proisstrict", "provolatile", "proparallel", "result"}, []string{"INT8", "BOOL", "CHAR", "CHAR", "INT8"}, [][]any{{int64(3432), true, "v", "s", int64(2950)}}))

				wantFIPS := false // Ordinary pinned OpenSSL mode; root confirms PG-first.
				if !b.PG {
					wantFIPS = fips140.Enabled()
				}
				// A volatile subquery supplies one actual datum to both its invariant
				// and typed NULL projection. Preserve the producer's result metadata
				// without making a fresh draw an expected row value.
				proof := func(expr, source string, size any, args ...any) fixture.Step {
					q := "SELECT octet_length(v) AS size,CASE WHEN v IS NULL THEN v ELSE NULL END AS typed_null FROM (SELECT " + expr + " AS v" + source + ") AS draws"
					if tc.typ == "UUID" {
						q = "SELECT regexp_like(concat(v),'" + uuidPattern + "') AS shape,CASE WHEN v IS NULL THEN v ELSE NULL END AS typed_null FROM (SELECT " + expr + " AS v" + source + ") AS draws"
						return fixture.Select(q, []string{"shape", "typed_null"}, []string{"BOOL", "UUID"}, [][]any{{true, nil}}, args...)
					}
					if tc.typ == "BOOL" {
						return fixture.Select("SELECT "+expr+" AS v"+source, []string{"v"}, []string{"BOOL"}, [][]any{{wantFIPS}}, args...)
					}
					return fixture.Select(q, []string{"size", "typed_null"}, []string{"INT4", "BYTEA"}, [][]any{{size, nil}}, args...)
				}
				from := " FROM og_random_source WHERE id=1"
				run(fixture.Exec("CREATE VIEW og_random_view AS SELECT "+call+" AS v"+from, 0))
				run(fixture.Exec("GRANT SELECT ON og_random_view TO og_object_reader", 0))
				parameter := "CASE WHEN id=$1::INTEGER THEN " + call + " ELSE NULL::" + tc.typ + " END"
				args := []any{int64(1)}
				if tc.arity == 1 {
					parameter = "og_random.gen_random_bytes(CASE WHEN id=1 THEN $1::INTEGER ELSE n END)"
					args[0] = int64(16)
				}
				cases := []objectGrantProviderCase{
					{proof(call, from, int64(1)), true, true},
					{proof(parameter, from, int64(16), args...), true, true},
					{proof("v", " FROM og_random_view", int64(1)), true, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_random_source WHERE id>99", []string{"v"}, []string{tc.typ}, nil), true, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_random_source WHERE FALSE", []string{"v"}, []string{tc.typ}, nil), true, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_random_source LIMIT 0", []string{"v"}, []string{tc.typ}, nil), true, true},
					{fixture.Select("SELECT CASE WHEN FALSE THEN "+call+" ELSE NULL::"+tc.typ+" END AS v"+from, []string{"v"}, []string{tc.typ}, [][]any{{nil}}), false, true},
				}
				if tc.arity == 1 {
					for _, bound := range []struct{ id, n int64 }{{6, 16}, {7, 1023}, {8, 1024}} {
						cases = append(cases, objectGrantProviderCase{proof(call, " FROM og_random_source WHERE id=$1::INTEGER", bound.n, bound.id), true, true})
					}
					cases = append(cases,
						objectGrantProviderCase{fixture.Select("SELECT "+call+" AS v FROM og_random_source WHERE id=2", []string{"v"}, []string{"BYTEA"}, [][]any{{nil}}), true, true},
						objectGrantProviderCase{fixture.Select("SELECT og_random.gen_random_bytes(NULL::INTEGER) AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{nil}}), false, true},
						objectGrantProviderCase{fixture.Select("SELECT og_random.gen_random_bytes($1::INTEGER) AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{nil}}, nil), false, false},
					)
					for _, id := range []int64{3, 4, 5} {
						s := fixture.ImplicitError("SELECT "+call+" AS v FROM og_random_source WHERE id=$1::INTEGER", fixture.Diagnostic{Code: "39000", Message: "Length not in range"}, id)
						s.SelectRows, s.ErrorStage = true, "execute"
						cases = append(cases, objectGrantProviderCase{s, true, true})
					}
				} else {
					// STRICT with no arguments has no NULL child to eliminate. Both
					// no-arg members still invoke on the row whose count is SQL NULL.
					cases = append(cases, objectGrantProviderCase{proof(call, " FROM og_random_source WHERE id=2", nil), true, true})
				}
				controls := []fixture.Step{
					fixture.Select("SELECT encode(og_random.digest('abc','sha256'),'hex') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}}),
					fixture.Select("SELECT regexp_like(concat(pg_catalog.gen_random_uuid()),'"+uuidPattern+"') AS shape", []string{"shape"}, []string{"BOOL"}, [][]any{{true}}),
				}
				if tc.name != "gen_random_bytes" {
					controls = append(controls, fixture.Select("SELECT octet_length(og_random.gen_random_bytes(n)) AS size"+from, []string{"size"}, []string{"INT4"}, [][]any{{int64(1)}}))
				}
				if tc.name != "gen_random_uuid" {
					controls = append(controls, fixture.Select("SELECT regexp_like(concat(og_random.gen_random_uuid()),'"+uuidPattern+"') AS shape", []string{"shape"}, []string{"BOOL"}, [][]any{{true}}))
				}
				if tc.name != "fips_mode" {
					controls = append(controls, fixture.Select("SELECT og_random.fips_mode() AS v", []string{"v"}, []string{"BOOL"}, [][]any{{wantFIPS}}))
				}
				target := "og_random." + tc.name + "(" + tc.signature + ")"
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, target, "function", tc.name, cases, controls)

				// Reverse the UUID isolation witness: denying core3432 must not
				// deny the installed wrapper, even though they share V4 packing.
				if tc.name == "gen_random_uuid" {
					core := proof("pg_catalog.gen_random_uuid()", from, nil)
					run(fixture.Exec("CREATE VIEW og_random_core_view AS SELECT pg_catalog.gen_random_uuid() AS v"+from, 0))
					run(fixture.Exec("GRANT SELECT ON og_random_core_view TO og_object_reader", 0))
					objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog.gen_random_uuid()", "function", "gen_random_uuid", []objectGrantProviderCase{
						{core, true, true},
						{proof("v", " FROM og_random_core_view", nil), true, true},
					}, []fixture.Step{proof(call, from, nil), proof("v", " FROM og_random_view", nil)})
				}

				// Runtime demand is required in INSERT SELECT. A denied statement
				// publishes no rows; granting permits the same two-row statement.
				run(fixture.Exec("CREATE TABLE og_random_written(id INT,v "+tc.typ+")", 0))
				run(fixture.Exec("GRANT INSERT,SELECT ON og_random_written TO og_object_reader", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
				write := "INSERT INTO og_random_written SELECT id," + call + " FROM og_random_source WHERE id IN (1,6)"
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					s := fixture.ImplicitError(write, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.name})
					s.ErrorStage = "execute"
					run(s)
					run(fixture.Select("SELECT count(*) FROM og_random_written", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec(write, 2)) })
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(proof("v", " FROM og_random_written WHERE id=1", int64(1)))
					run(proof("v", " FROM og_random_written WHERE id=6", int64(16)))
					if tc.arity == 1 {
						// Import occurs before admission even with selected EXECUTE denied.
						for _, input := range []struct{ value, code, message string }{
							{"oops", "22P02", `invalid input syntax for type integer: "oops"`},
							{"2147483648", "22003", `value "2147483648" is out of range for type integer`},
						} {
							s := fixture.ImplicitError("SELECT og_random.gen_random_bytes($1)", fixture.Diagnostic{Code: input.code, Message: input.message, Context: "unnamed portal parameter $1 = '...'"}, input.value)
							s.SelectRows, s.ErrorStage = true, "execute"
							run(s)
						}
					}
				})
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC", 0))
			})
		})
	}
}
