package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_0 contrib/intagg/intagg--1.1.sql declares two distinct public
// identities. Sharing array_agg/unnest implementation code creates no SQL ACL
// dependency on those core declarations or on the other installed member.
func TestReferenceObjectGrantIntaggAuthority(t *testing.T) {
	for _, aggregate := range []bool{true, false} {
		name, signature, kind := "int_array_enum", "integer[]", "function"
		if aggregate {
			name, signature, kind = "int_array_aggregate", "integer", "aggregate"
		}
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, []string{"og_intagg"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				if b.PG {
					clean := func() {
						for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS intagg CASCADE"} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Error(q, err)
							}
						}
					}
					clean()
					defer clean()
				}
				run(fixture.Exec("CREATE SCHEMA og_intagg", 0))
				run(fixture.Exec("CREATE EXTENSION intagg SCHEMA og_intagg VERSION '1.1'", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_intagg_input(id INT,v INT,a INT[])", 0))
				run(fixture.Exec("INSERT INTO og_intagg_input VALUES(1,1,'{1,NULL,2}'),(2,NULL,'{}'),(3,2,NULL)", 3))
				run(fixture.Exec("GRANT SELECT ON og_intagg_input TO og_object_reader,og_object_other", 0))

				// Dynamic installed identities and actual extension membership, never
				// allocator numbers or aliases for the fixed core declarations.
				seen := make(map[int64]bool)
				for _, member := range []struct {
					name, signature, in, kind, symbol string
					result                            int64
					strict, set                       bool
				}{
					{"int_array_aggregate", "integer", "23", "a", "aggregate_dummy", 1007, false, false},
					{"int_array_enum", "integer[]", "1007", "f", "array_unnest", 23, true, true},
				} {
					var oid int64
					if err := c.QueryRowContext(p.Ctx, "SELECT p.oid::BIGINT FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='og_intagg' AND p.proname=$1", member.name).Scan(&oid); err != nil || oid <= 0 || seen[oid] {
						t.Fatal("distinct installed intagg identity", member.name, oid, err)
					}
					seen[oid] = true
					run(fixture.Select("SELECT p.oid::regprocedure::TEXT AS identity,p.proname,p.pronargs,p.prorettype::BIGINT AS result,p.proisstrict,p.provolatile,p.proparallel,n.nspname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=$1", []string{"identity", "proname", "pronargs", "result", "proisstrict", "provolatile", "proparallel", "nspname"}, []string{"TEXT", "NAME", "INT2", "INT8", "BOOL", "CHAR", "CHAR", "NAME"}, [][]any{{"og_intagg." + member.name + "(" + member.signature + ")", member.name, int64(1), member.result, member.strict, "i", "s", "og_intagg"}}, oid))
					// Native intentionally lacks these pg_proc columns. The shared
					// projection above and typed executions below retain its boundary.
					if b.PG {
						run(fixture.Select("SELECT proargtypes::TEXT AS full_in,prokind,prolang::BIGINT AS language,proretset,pronargdefaults,provariadic::BIGINT AS variadic,proargnames IS NULL AS unnamed,prosrc FROM pg_proc WHERE oid=$1", []string{"full_in", "prokind", "language", "proretset", "pronargdefaults", "variadic", "unnamed", "prosrc"}, []string{"TEXT", "CHAR", "INT8", "BOOL", "INT2", "INT8", "BOOL", "TEXT"}, [][]any{{member.in, member.kind, int64(12), member.set, int64(0), int64(0), true, member.symbol}}, oid))
					}
					run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid JOIN pg_namespace n ON n.oid=e.extnamespace WHERE d.classid=1255 AND d.objid=$1 AND d.objsubid=0 AND d.refclassid=3079 AND d.refobjsubid=0 AND d.deptype='e' AND e.extname='intagg' AND e.extversion='1.1' AND e.extrelocatable AND n.nspname='og_intagg'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, oid))
				}

				array := func(q string, rows [][]any, args ...any) fixture.Step {
					return fixture.ArrayReferenceSelect(q, []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, rows, b.PG, args...)
				}
				enum := func(q string, rows [][]any, args ...any) fixture.Step {
					s := fixture.Select(q, []string{"v"}, []string{"INT4"}, rows, args...)
					s.CheckMetadata = fixture.StringEncodingMetadata(b.PG, s.Columns, s.Types)
					return s
				}
				arrayRows := [][]any{{"{1,NULL,2}"}}
				// ORDER BY ordinality, not value, proves the actual array traversal
				// including the middle NULL. Target form orders its output explicitly.
				enumRows := [][]any{{int64(1)}, {nil}, {int64(2)}}
				enumSorted := [][]any{{int64(1)}, {int64(2)}, {nil}}
				agg := array("SELECT og_intagg.int_array_aggregate(v ORDER BY id) AS v FROM og_intagg_input", arrayRows)
				enumeration := enum("SELECT v FROM og_intagg.int_array_enum('{1,NULL,2}'::INT[]) WITH ORDINALITY AS u(v,n) ORDER BY n", enumRows)
				selected := enumeration
				if aggregate {
					selected = agg
				}
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RoleGrantPermissionDenied(run, selected.Query, "schema", "og_intagg", prepared)
				})
				run(fixture.Exec("GRANT USAGE ON SCHEMA og_intagg TO og_object_reader,og_object_other", 0))
				target := "og_intagg." + name + "(" + signature + ")"
				// Existing FUNCTION targeting includes aggregates; PROCEDURE must
				// reject the same full identity without a new parser feature.
				fixture.RoleGrantError(run, "GRANT EXECUTE ON PROCEDURE "+target+" TO og_object_reader", "42809", target+" is not a procedure")
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_other", 0))
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO og_object_other", 0))
				for _, view := range []struct{ name, options, body string }{
					{"og_intagg_view", "", selected.Query},
					{"og_intagg_invoker_view", " WITH(security_invoker=true)", selected.Query},
				} {
					run(fixture.Exec("CREATE VIEW "+view.name+view.options+" AS "+view.body, 0))
					run(fixture.Exec("ALTER VIEW "+view.name+" OWNER TO og_object_other", 0))
					run(fixture.Exec("GRANT SELECT ON "+view.name+" TO og_object_reader", 0))
				}
				cases := []objectGrantProviderCase{{selected, true, true}}
				for _, view := range []string{"og_intagg_view", "og_intagg_invoker_view"} {
					s := array("SELECT v FROM "+view, arrayRows)
					if !aggregate {
						s = enum("SELECT v FROM "+view+" ORDER BY v NULLS LAST", enumSorted)
						s.CheckMetadata = fixture.StringEncodingViewMetadata(b.PG, s.Columns, s.Types)
					}
					cases = append(cases, objectGrantProviderCase{s, true, true})
				}
				controls := []fixture.Step{
					array("SELECT pg_catalog.array_agg(v ORDER BY id) AS v FROM og_intagg_input", arrayRows),
					enum("SELECT v FROM pg_catalog.unnest('{1,NULL,2}'::INT[]) WITH ORDINALITY AS u(v,n) ORDER BY n", enumRows),
				}
				if aggregate {
					controls = append(controls, enumeration)
					cases = append(cases,
						objectGrantProviderCase{array("SELECT og_intagg.int_array_aggregate(CASE WHEN id=1 THEN $1::INT ELSE v END ORDER BY id) AS v FROM og_intagg_input", arrayRows, int64(1)), true, true},
						objectGrantProviderCase{array("SELECT og_intagg.int_array_aggregate(v ORDER BY id) AS v FROM og_intagg_input WHERE FALSE", [][]any{{nil}}), true, true},
						objectGrantProviderCase{array("SELECT og_intagg.int_array_aggregate(v ORDER BY id) FILTER(WHERE FALSE) AS v FROM og_intagg_input", [][]any{{nil}}), true, true},
						objectGrantProviderCase{array("SELECT og_intagg.int_array_aggregate(NULL::INT) AS v", [][]any{{"{NULL}"}}), true, true},
						objectGrantProviderCase{array(agg.Query+" LIMIT 0", nil), true, true},
					)
				} else {
					controls = append(controls, agg)
					cases = append(cases,
						objectGrantProviderCase{enum("SELECT og_intagg.int_array_enum(a) AS v FROM og_intagg_input WHERE id=1 ORDER BY v NULLS LAST", enumSorted), true, true},
						objectGrantProviderCase{enum("SELECT u.v FROM og_intagg_input s CROSS JOIN LATERAL og_intagg.int_array_enum(s.a) WITH ORDINALITY AS u(v,n) WHERE s.id=1 ORDER BY n", enumRows), true, true},
						objectGrantProviderCase{enum("SELECT og_intagg.int_array_enum($1::INT[]) AS v ORDER BY v NULLS LAST", enumSorted, "{1,NULL,2}"), true, true},
					)
					// STRICT SRF NULL/empty output is not a scalar NULL ACL waiver.
					for _, arg := range []string{"'{}'::INT[]", "NULL::INT[]"} {
						cases = append(cases,
							objectGrantProviderCase{enum("SELECT og_intagg.int_array_enum("+arg+") AS v", nil), true, true},
							objectGrantProviderCase{enum("SELECT v FROM og_intagg.int_array_enum("+arg+") AS u(v)", nil), true, true},
						)
					}
					for _, arg := range []any{"{}", nil} {
						cases = append(cases,
							objectGrantProviderCase{enum("SELECT og_intagg.int_array_enum($1::INT[]) AS v", nil, arg), true, true},
							objectGrantProviderCase{enum("SELECT v FROM og_intagg.int_array_enum($1::INT[]) AS u(v)", nil, arg), true, true},
						)
					}
					for _, id := range []string{"2", "3", "99"} {
						cases = append(cases, objectGrantProviderCase{enum("SELECT og_intagg.int_array_enum(a) AS v FROM og_intagg_input WHERE id="+id, nil), true, true})
					}
					// Independently reasoned PG-first initialization hypotheses: dead
					// FROM may disappear, surviving target/LIMIT initializes its SRF.
					cases = append(cases,
						objectGrantProviderCase{enum("SELECT og_intagg.int_array_enum(a) AS v FROM og_intagg_input WHERE FALSE", nil), true, true},
						objectGrantProviderCase{enum("SELECT v FROM og_intagg.int_array_enum('{1,NULL,2}'::INT[]) u(v) WHERE FALSE", nil), false, true},
						objectGrantProviderCase{enum("SELECT u.v FROM og_intagg_input s CROSS JOIN LATERAL og_intagg.int_array_enum(s.a) u(v) WHERE FALSE", nil), false, true},
						objectGrantProviderCase{enum("SELECT og_intagg.int_array_enum(a) AS v FROM og_intagg_input LIMIT 0", nil), true, true},
						objectGrantProviderCase{enum("SELECT v FROM og_intagg.int_array_enum('{1,NULL,2}'::INT[]) u(v) LIMIT 0", nil), true, true},
					)
				}
				objectGrantProviderLifecycle(t, p, b, c, run, target, kind, name, cases, controls)

				// Schema USAGE remains independently required even with explicit X.
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				run(fixture.Exec("REVOKE USAGE ON SCHEMA og_intagg FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RoleGrantPermissionDenied(run, selected.Query, "schema", "og_intagg", prepared)
				})
				run(fixture.Exec("GRANT USAGE ON SCHEMA og_intagg TO og_object_reader", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))

				// Warm the SAME physical INSERT SELECT before revocation. Neither
				// direct/per-step prepared nor retained denial may publish a prefix.
				defer objectGrantRoutineCleanup(t, p, c, []string{target})
				sinkType, sentinel := "INT", any(int64(99))
				if aggregate {
					sinkType, sentinel = "INT[]", "{99}"
				}
				run(fixture.Exec("CREATE TABLE og_intagg_sink(v "+sinkType+")", 0))
				run(fixture.Exec("INSERT INTO og_intagg_sink VALUES($1)", 1, sentinel))
				run(fixture.Exec("GRANT SELECT,INSERT,DELETE ON og_intagg_sink TO og_object_reader", 0))
				insert := "INSERT INTO og_intagg_sink " + selected.Query
				stmt, err := c.PrepareContext(p.Ctx, insert)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: insert}
				for phase, allowed := range []bool{true, false, true, false} {
					switch phase {
					case 1:
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
					case 2:
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
					case 3:
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
					}
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						for _, retained := range []bool{false, true} {
							count := int64(3)
							if aggregate {
								count = 1
							}
							s := fixture.Exec(insert, count)
							if !allowed {
								s = fixture.ImplicitError(insert, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + kind + " " + name})
								s.ErrorStage = "execute"
							}
							if retained {
								fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
							} else {
								run(s)
							}
							want := [][]any{{sentinel}}
							if allowed {
								if aggregate {
									want = [][]any{{"{1,NULL,2}"}, {sentinel}}
								} else {
									want = [][]any{{int64(1)}, {int64(2)}, {sentinel}, {nil}}
								}
							}
							q := "SELECT v FROM og_intagg_sink ORDER BY v NULLS LAST"
							if aggregate {
								// The atomicity witness needs a stable row order, not
								// an unavailable native ARRAY ordering operator.
								q = "SELECT v FROM og_intagg_sink ORDER BY concat(v)"
							}
							check := enum(q, want)
							check.CheckMetadata = fixture.StringEncodingViewMetadata(b.PG, check.Columns, check.Types)
							if aggregate {
								check = array(q, want)
							}
							run(check)
							if allowed {
								if aggregate {
									run(fixture.Exec("DELETE FROM og_intagg_sink WHERE v<>'{99}'::INT[]", count))
								} else {
									run(fixture.Exec("DELETE FROM og_intagg_sink WHERE v<>99 OR v IS NULL", count))
								}
							}
						}
					})
				}
			})
		})
	}
}
