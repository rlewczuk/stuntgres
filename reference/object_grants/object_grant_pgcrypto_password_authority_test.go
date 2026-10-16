package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// These are the three installed C declarations, not fixed pg_catalog OIDs or
// one defaulted gen_salt signature. Random salt bytes are never expected values.
func TestReferenceObjectGrantPGCryptoPasswordAuthority(t *testing.T) {
	const salt = "$2a$06$RQiOJ.3ELirrXwxIZY8q0O"
	const hash = "$2a$06$RQiOJ.3ELirrXwxIZY8q0OR3CVJrAfda1z26CCHPnB6mmVZD8p0/C"
	for _, tc := range []struct {
		label, name, signature, call, parameter, pattern, kernel string
		args                                                     []any
	}{
		{"crypt", "crypt", "text,text", "og_password.crypt(p,s)", "og_password.crypt(CASE WHEN id=1 THEN $1::TEXT ELSE p END,$2::TEXT)", "", "invalid salt", []any{"foox", salt}},
		{"salt_short", "gen_salt", "text", "og_password.gen_salt(a)", "og_password.gen_salt(CASE WHEN id=1 THEN $1::TEXT ELSE a END)", `^\$2a\$06\$[./A-Za-z0-9]{21}[.Oeu]$`, "gen_salt: Unknown salt algorithm", []any{"bf"}},
		{"salt_rounds", "gen_salt", "text,integer", "og_password.gen_salt(a,n)", "og_password.gen_salt(CASE WHEN id=1 THEN $1::TEXT ELSE a END,$2::INTEGER)", `^\$2a\$04\$[./A-Za-z0-9]{21}[.Oeu]$`, "gen_salt: Incorrect number of rounds", []any{"bf", int64(4)}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			objectGrantScenario(t, []string{"og_password"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				// PostgreSQL's extension is database-wide; native has a fresh engine.
				// No fictitious native DROP EXTENSION or second installation is needed.
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
				run(fixture.Exec("CREATE SCHEMA og_password", 0))
				run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA og_password", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_password_source(id INT,p TEXT,s TEXT,a TEXT,n INT,z INT)", 0))
				run(fixture.Exec("INSERT INTO og_password_source VALUES(1,'foox',$1,'bf',4,0),(2,NULL,NULL,NULL,NULL,0),(3,'foox','$2a$03$......................','unknown',3,0)", 3, salt))
				run(fixture.Exec("GRANT SELECT ON og_password_source TO og_object_reader", 0))
				// Schema USAGE is independent of the member's default PUBLIC EXECUTE.
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RoleGrantPermissionDenied(run, "SELECT "+tc.call+" FROM og_password_source WHERE id=1", "schema", "og_password", prepared)
				})
				run(fixture.Exec("GRANT USAGE ON SCHEMA og_password TO og_object_reader", 0))

				// Discover actual allocated IDs through regprocedure. Assert released
				// metadata and exact membership, never an allocator offset or seed.
				seen := make(map[int64]bool)
				for _, member := range []struct {
					key, name, vol, symbol string
					arity                  int64
				}{
					{"crypt(text,text)", "crypt", "i", "pg_crypt", 2},
					{"gen_salt(text)", "gen_salt", "v", "pg_gen_salt", 1},
					{"gen_salt(text,integer)", "gen_salt", "v", "pg_gen_salt_rounds", 2},
				} {
					var oid int64
					if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid", "og_password."+member.key).Scan(&oid); err != nil || oid <= 0 || seen[oid] {
						t.Fatal("installed declaration identity", member.key, oid, err)
					}
					seen[oid] = true
					full := fixture.Select("SELECT prorettype::BIGINT AS result,prokind,prolang::BIGINT AS language,proisstrict,provolatile,proparallel,proretset,pronargdefaults,provariadic::BIGINT AS variadic,proargnames IS NULL AS unnamed,prosrc FROM pg_proc WHERE oid=$1", []string{"result", "prokind", "language", "proisstrict", "provolatile", "proparallel", "proretset", "pronargdefaults", "variadic", "unnamed", "prosrc"}, []string{"INT8", "CHAR", "INT8", "BOOL", "CHAR", "CHAR", "BOOL", "INT2", "INT8", "BOOL", "TEXT"}, [][]any{{int64(25), "f", int64(13), true, member.vol, "s", false, int64(0), int64(0), true, member.symbol}}, oid)
					if !b.PG {
						// Native exposes the established nine-column pg_proc
						// projection, not the complete PostgreSQL catalog shape.
						// Keep the SAME full query as an explicit capability
						// boundary; do not discard its real PostgreSQL contract.
						full = fixture.ImplicitError(full.Query, fixture.Diagnostic{Code: "42703", Message: `column "prokind" does not exist`, Position: "37"}, oid)
						full.SelectRows, full.ErrorStage = true, "execute"
						if prepared {
							full.ErrorStage = "prepare"
						}
					}
					run(full)
					owner := "postgres"
					if b.PG {
						owner = "stuntgres"
					}
					run(fixture.Select("SELECT p.oid::regprocedure::TEXT AS identity,p.proname,p.pronargs,p.prorettype::BIGINT AS result,p.provolatile,p.proisstrict,p.proparallel,n.nspname,r.rolname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid=$1", []string{"identity", "proname", "pronargs", "result", "provolatile", "proisstrict", "proparallel", "nspname", "rolname"}, []string{"TEXT", "NAME", "INT2", "INT8", "CHAR", "BOOL", "CHAR", "NAME", "NAME"}, [][]any{{"og_password." + member.key, member.name, member.arity, int64(25), member.vol, true, "s", "og_password", owner}}, oid))
					run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid JOIN pg_namespace n ON n.oid=e.extnamespace WHERE d.classid=1255 AND d.objid=$1 AND d.objsubid=0 AND d.refclassid=3079 AND d.refobjsubid=0 AND d.deptype='e' AND e.extname='pgcrypto' AND e.extversion='1.4' AND e.extrelocatable AND n.nspname='og_password'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, oid))
				}
				run(fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='og_password' AND p.proname IN ('digest','hmac','crypt','gen_salt')", []string{"count"}, []string{"INT8"}, [][]any{{int64(7)}}))

				textValue := func(expr, tail string, value any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_password_source"+tail, []string{"v"}, []string{"TEXT"}, [][]any{{value}}, args...)
				}
				proof := func(expr, tail string, args ...any) fixture.Step {
					if tc.name == "crypt" {
						return textValue(expr, tail, hash, args...)
					}
					// The volatile subquery produces ONE salt datum. Every check uses
					// that datum, with full bcrypt structure and real authentication.
					// Preserve the selected salt's TEXT identity in actual cursor
					// metadata, without requiring the unsupported pg_typeof helper.
					q := "SELECT regexp_like(v,'" + tc.pattern + "') AS shape,length(og_password.crypt('foox',v)) AS size,og_password.crypt('foox',og_password.crypt('foox',v))=og_password.crypt('foox',v) AS verified,CASE WHEN v IS NULL THEN v ELSE NULL END AS typed_null FROM (SELECT " + expr + " AS v FROM og_password_source" + tail + ") AS salts"
					return fixture.Select(q, []string{"shape", "size", "verified", "typed_null"}, []string{"BOOL", "INT4", "BOOL", "TEXT"}, [][]any{{true, int64(60), true, nil}}, args...)
				}
				run(fixture.Exec("CREATE VIEW og_password_view AS SELECT "+tc.call+" AS v FROM og_password_source WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_password_view TO og_object_reader", 0))
				stored := fixture.Select("SELECT v FROM og_password_view", []string{"v"}, []string{"TEXT"}, [][]any{{hash}})
				if tc.name != "crypt" {
					stored = fixture.Select("SELECT regexp_like(v,'"+tc.pattern+"') AS shape,length(og_password.crypt('foox',v)) AS size,og_password.crypt('foox',og_password.crypt('foox',v))=og_password.crypt('foox',v) AS verified,CASE WHEN v IS NULL THEN v ELSE NULL END AS typed_null FROM og_password_view", []string{"shape", "size", "verified", "typed_null"}, []string{"BOOL", "INT4", "BOOL", "TEXT"}, [][]any{{true, int64(60), true, nil}})
				}
				badCall := tc.call
				if tc.label == "salt_rounds" {
					badCall = "og_password.gen_salt('bf',n)"
				}
				kernel := fixture.ImplicitError("SELECT "+badCall+" AS v FROM og_password_source WHERE id=3", fixture.Diagnostic{Code: "22023", Message: tc.kernel})
				kernel.SelectRows, kernel.ErrorStage = true, "execute"
				cases := []objectGrantProviderCase{
					{proof(tc.call, " WHERE id=1"), true, true},
					{proof(tc.parameter, " WHERE id=1", tc.args...), true, true},
					{stored, true, true},
					{textValue(tc.call, " WHERE id=2", nil), true, true},
					{fixture.Select("SELECT "+tc.call+" AS v FROM og_password_source WHERE id>99", []string{"v"}, []string{"TEXT"}, nil), true, true},
					{fixture.Select("SELECT "+tc.call+" AS v FROM og_password_source WHERE FALSE", []string{"v"}, []string{"TEXT"}, nil), true, true},
					{textValue("CASE WHEN FALSE THEN "+tc.call+" ELSE NULL::TEXT END", " WHERE id=1", nil), false, true},
					{kernel, true, true},
				}
				// Fresh constant NULL and freshly imported NULL differ from a runtime
				// NULL column. Do not ask a generic held plan to specialize a bind.
				switch tc.label {
				case "crypt":
					cryptNull := fixture.ImplicitError("SELECT og_password.crypt(p,'$2$') AS v FROM og_password_source WHERE id=1", fixture.Diagnostic{Code: "39000", Message: "crypt(3) returned NULL"})
					cryptNull.SelectRows, cryptNull.ErrorStage = true, "execute"
					cases = append(cases, objectGrantProviderCase{cryptNull, true, true})
					for _, expr := range []string{"og_password.crypt(NULL::TEXT,'invalid')", "og_password.crypt('foox',NULL::TEXT)"} {
						cases = append(cases, objectGrantProviderCase{textValue(expr, " WHERE id=1", nil), false, true})
					}
					for _, args := range [][]any{{nil, salt}, {"foox", nil}} {
						cases = append(cases, objectGrantProviderCase{textValue("og_password.crypt($1::TEXT,$2::TEXT)", " WHERE id=1", nil, args...), false, false})
					}
					child := fixture.ImplicitError("SELECT og_password.crypt(p,(1/z)::TEXT) AS v FROM og_password_source WHERE id=2", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					child.SelectRows, child.ErrorStage = true, "execute"
					cases = append(cases, objectGrantProviderCase{child, true, true})
				case "salt_short":
					cases = append(cases,
						objectGrantProviderCase{textValue("og_password.gen_salt(NULL::TEXT)", " WHERE id=1", nil), false, true},
						objectGrantProviderCase{textValue("og_password.gen_salt($1::TEXT)", " WHERE id=1", nil, nil), false, false},
					)
				case "salt_rounds":
					for _, expr := range []string{"og_password.gen_salt(NULL::TEXT,3)", "og_password.gen_salt('unknown',NULL::INTEGER)"} {
						cases = append(cases, objectGrantProviderCase{textValue(expr, " WHERE id=1", nil), false, true})
					}
					for _, args := range [][]any{{nil, int64(3)}, {"unknown", nil}} {
						cases = append(cases, objectGrantProviderCase{textValue("og_password.gen_salt($1::TEXT,$2::INTEGER)", " WHERE id=1", nil, args...), false, false})
					}
					// SMALLINT actual still selects the full TEXT,INT4 declaration.
					cases = append(cases, objectGrantProviderCase{proof("og_password.gen_salt(a,n::SMALLINT)", " WHERE id=1"), true, true})
				}
				// This expression contains TWO actual SQL calls, unlike the C
				// helper relationship. Each selected target independently gates
				// the nested consumer while the sibling remains PUBLIC-callable.
				saltCall, hashCost := "og_password.gen_salt(a,n)", "04"
				if tc.label == "salt_short" {
					saltCall, hashCost = "og_password.gen_salt(a)", "06"
				}
				nested := "SELECT regexp_like(h,'^\\$2a\\$" + hashCost + "\\$[./A-Za-z0-9]{53}$') AS shape,og_password.crypt('foox',h)=h AS verified FROM (SELECT og_password.crypt(p," + saltCall + ") AS h FROM og_password_source WHERE id=1) AS hashes"
				cases = append(cases, objectGrantProviderCase{fixture.Select(nested, []string{"shape", "verified"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}), true, true})
				controls := []fixture.Step{
					fixture.Select("SELECT encode(og_password.digest('abc','sha256'),'hex') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}}),
				}
				// Separate overloads have independent ACLs; crypt has no SQL body
				// dependency on gen_salt, and neither gen_salt calls its sibling.
				if tc.label != "crypt" {
					controls = append(controls, textValue("og_password.crypt(p,s)", " WHERE id=1", hash))
				}
				if tc.label != "salt_short" {
					controls = append(controls, fixture.Select(`SELECT regexp_like(og_password.gen_salt(a),'^\$2a\$06\$[./A-Za-z0-9]{21}[.Oeu]$') AS v FROM og_password_source WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
				}
				if tc.label != "salt_rounds" {
					controls = append(controls, fixture.Select(`SELECT regexp_like(og_password.gen_salt(a,n),'^\$2a\$04\$[./A-Za-z0-9]{21}[.Oeu]$') AS v FROM og_password_source WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
				}
				target := "og_password." + tc.name + "(" + tc.signature + ")"
				objectGrantProviderLifecycle(t, p, b, c, run, target, "function", tc.name, cases, controls)
				// The lifecycle helper restores PUBLIC as it returns.
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))

				// A row-backed write must fail without publishing any prefix;
				// existing stored data is independent of a fresh invocation.
				// Regrant permits the same write.
				run(fixture.Exec("CREATE TABLE og_password_written(v TEXT)", 0))
				run(fixture.Exec("GRANT INSERT,SELECT ON og_password_written TO og_object_reader", 0))
				write := "INSERT INTO og_password_written SELECT " + tc.call + " FROM og_password_source WHERE id=1"
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					s := fixture.ImplicitError(write, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.name})
					s.ErrorStage = "execute"
					run(s)
					run(fixture.Select("SELECT count(*) FROM og_password_written", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec(write, 1)) })
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					if tc.name == "crypt" {
						run(fixture.Select("SELECT v FROM og_password_written", []string{"v"}, []string{"TEXT"}, [][]any{{hash}}))
					} else {
						run(fixture.Select("SELECT regexp_like(v,'"+tc.pattern+"') AS shape FROM og_password_written", []string{"shape"}, []string{"BOOL"}, [][]any{{true}}))
					}
				})

				if tc.label == "salt_rounds" {
					// Import/bind errors precede admission of the revoked routine.
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						for _, input := range []struct{ value, code, message string }{
							{"oops", "22P02", `invalid input syntax for type integer: "oops"`},
							{"2147483648", "22003", `value "2147483648" is out of range for type integer`},
						} {
							s := fixture.ImplicitError("SELECT og_password.gen_salt('bf',$1)", fixture.Diagnostic{Code: input.code, Message: input.message, Context: "unnamed portal parameter $1 = '...'"}, input.value)
							s.SelectRows, s.ErrorStage = true, "execute"
							run(s)
							literal := fixture.ImplicitError("SELECT og_password.gen_salt('bf','"+input.value+"')", fixture.Diagnostic{Code: input.code, Message: input.message, Position: "34"})
							literal.SelectRows, literal.ErrorStage = true, "execute"
							if prepared {
								literal.ErrorStage = "prepare"
							}
							run(literal)
						}
					})
				}
				// Restore EXECUTE alone, then independently remove namespace
				// resolution rights. This is genuine schema USAGE, not ownership.
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				run(fixture.Exec("REVOKE USAGE ON SCHEMA og_password FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RoleGrantPermissionDenied(run, "SELECT "+tc.call+" FROM og_password_source WHERE id=1", "schema", "og_password", prepared)
				})
				run(fixture.Exec("GRANT USAGE ON SCHEMA og_password TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(proof(tc.call, " WHERE id=1")) })
			})
		})
	}
}
