package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_0 declares three callable ACL value functions and aclcontains, the
// backing procedure of array/item @>. Native does not expose aclcontains as a
// named call: the operator is the genuine positive spelling here. Expected raw
// masks/defaults and OUT fields are independent of either backend's output.
// Demand/precedence hypotheses require the root's whole-definition PG-first run.
func TestReferenceObjectGrantACLValueAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, signature, typ string
		oid, result, arity   int64
		volatility           string
	}{
		{"acldefault", `"char",oid`, "_ACLITEM", 3943, 1034, 2, "i"},
		{"makeaclitem", "oid,oid,text,boolean", "ACLITEM", 1365, 1033, 4, "i"},
		{"aclexplode", "aclitem[]", "RECORD", 1689, 2249, 1, "s"},
		{"aclcontains", "aclitem[],aclitem", "BOOL", 1037, 16, 2, "i"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, []string{"og_acl_values"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				target := "pg_catalog." + tc.name + "(" + tc.signature + ")"
				run(fixture.Select("SELECT proname,pronamespace::BIGINT AS namespace,pronargs,prorettype::BIGINT AS result,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid=$1",
					[]string{"proname", "namespace", "pronargs", "result", "proisstrict", "provolatile", "proparallel"},
					[]string{"NAME", "INT8", "INT2", "INT8", "BOOL", "CHAR", "CHAR"},
					[][]any{{tc.name, int64(11), tc.arity, tc.result, true, tc.volatility, "s"}}, tc.oid))
				// Do not expand native's existing pg_proc projection for this test.
				q := "SELECT p.proargtypes::TEXT AS inputs,p.prokind,p.prolang::BIGINT AS language,p.proretset FROM pg_proc p WHERE p.oid=$1"
				inputs := map[string]string{"acldefault": "18 26", "makeaclitem": "26 26 25 16", "aclexplode": "1034", "aclcontains": "1034 1033"}[tc.name]
				metadata := fixture.Select(q, []string{"inputs", "prokind", "language", "proretset"}, []string{"TEXT", "CHAR", "INT8", "BOOL"}, [][]any{{inputs, "f", int64(12), tc.name == "aclexplode"}}, tc.oid)
				// PostgreSQL-only full metadata; shared identity/ACL checks above
				// do not depend on the first unsupported native catalog column.
				if b.PG {
					run(metadata)
				}
				if b.PG && tc.name == "aclcontains" {
					run(fixture.Select("SELECT oprname,oprleft::BIGINT AS l,oprright::BIGINT AS r,oprcode::oid::BIGINT AS routine FROM pg_operator WHERE oid=968", []string{"oprname", "l", "r", "routine"}, []string{"NAME", "INT8", "INT8", "INT8"}, [][]any{{"@>", int64(1034), int64(1033), int64(1037)}}))
				}
				var reader, owner int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid::BIGINT FROM pg_roles WHERE rolname='og_object_reader'").Scan(&reader); err != nil {
					t.Fatal(err)
				}
				if err := c.QueryRowContext(p.Ctx, "SELECT oid::BIGINT FROM pg_roles WHERE rolname='og_object_other'").Scan(&owner); err != nil {
					t.Fatal(err)
				}
				if reader <= 0 || owner <= 0 || reader == owner {
					t.Fatalf("invalid real role identities reader=%d owner=%d", reader, owner)
				}
				r, o := strconv.FormatInt(reader, 10), strconv.FormatInt(owner, 10)
				item := "og_object_reader=r*w*/og_object_other"
				array := "{" + item + "}"
				defaults := "{og_object_other=arwdDxtm/og_object_other}"
				makeCall := "makeaclitem(" + r + "::oid," + o + "::oid,'SELECT,UPDATE',true)"
				defaultCall := "acldefault('r'," + o + "::oid)"
				run(fixture.Exec("CREATE SCHEMA og_acl_values AUTHORIZATION og_object_other", 0))
				fixture.RoleGrantActor(run, "og_object_other", func() {
					run(fixture.Exec("CREATE TABLE og_acl_values.source(id INTEGER,g OID,o OID,p TEXT,flag BOOLEAN,aa ACLITEM[],a ACLITEM)", 0))
					// Build actual values while every producer is allowed. Later
					// selected denials cannot be masked by a denied producer child.
					run(fixture.Exec("INSERT INTO og_acl_values.source VALUES(1,"+r+","+o+",'SELECT,UPDATE',true,ARRAY["+makeCall+"],"+makeCall+"),(2,NULL,NULL,NULL,NULL,NULL,NULL),(3,"+r+","+o+",'SELECT,UPDATE',true,ARRAY[makeaclitem("+r+","+o+",'SELECT',false),makeaclitem("+r+","+o+",'UPDATE',false)],"+makeCall+")", 3))
				})
				run(fixture.Exec("GRANT USAGE ON SCHEMA og_acl_values TO og_object_reader", 0))
				run(fixture.Exec("GRANT SELECT ON og_acl_values.source TO og_object_reader", 0))
				value := func(expr, tail, typ string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_acl_values.source"+tail, []string{"v"}, []string{typ}, [][]any{{want}}, args...)
				}
				literal := func(expr, typ string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{typ}, [][]any{{want}}, args...)
				}
				fullDiagnostic := func(query, code, message string, args ...any) fixture.Step {
					s := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message}, args...)
					s.SelectRows, s.ErrorStage = true, "execute"
					return s
				}
				var cases []objectGrantProviderCase
				var controls []fixture.Step
				var call, constant string
				var want any
				switch tc.name {
				case "acldefault":
					call, constant, want = "acldefault('r',o)", defaultCall, defaults
					cases = append(cases,
						objectGrantProviderCase{value("pg_catalog.acldefault('r',o)", " WHERE id=1", tc.typ, defaults), true, true},
						objectGrantProviderCase{fullDiagnostic("SELECT acldefault('z',"+o+"::oid)", "XX000", "unrecognized object type abbreviation: z"), true, false},
						objectGrantProviderCase{value("acldefault($1::\"char\",CASE WHEN id=1 THEN $2::oid ELSE o END)", " WHERE id=1", tc.typ, defaults, "r", owner), true, true},
						objectGrantProviderCase{literal("acldefault(NULL,"+o+"::oid)", tc.typ, nil), false, false},
						objectGrantProviderCase{literal("acldefault('z',NULL::oid)", tc.typ, nil), false, false},
						objectGrantProviderCase{value("acldefault('r',$1::oid)", " WHERE id=1", tc.typ, nil, nil), false, false},
						objectGrantProviderCase{fullDiagnostic("SELECT acldefault('z',o) FROM og_acl_values.source WHERE id=1", "XX000", "unrecognized object type abbreviation: z"), true, true})
					controls = append(controls, literal(makeCall, "ACLITEM", item))
				case "makeaclitem":
					call, constant, want = "makeaclitem(g,o,p,flag)", makeCall, item
					cases = append(cases,
						objectGrantProviderCase{value("pg_catalog.makeaclitem(g,o,p,flag)", " WHERE id=1", tc.typ, item), true, true},
						objectGrantProviderCase{fullDiagnostic("SELECT makeaclitem("+r+"::oid,"+o+"::oid,'bad',false)", "22023", `unrecognized privilege type: "bad"`), true, false},
						// A demanded immutable child is a planning error even
						// when another strict scalar formal is known NULL.
						objectGrantProviderCase{fullDiagnostic("SELECT makeaclitem(NULL::oid,"+o+"::oid,(1/0)::text,false)", "22012", "division by zero"), false, false},
						objectGrantProviderCase{value("makeaclitem(g,o,CASE WHEN id=1 THEN $1::text ELSE p END,$2::bool)", " WHERE id=1", tc.typ, item, "SELECT,UPDATE", true), true, true},
						objectGrantProviderCase{fullDiagnostic("SELECT makeaclitem(g,o,'bad',flag) FROM og_acl_values.source WHERE id=1", "22023", `unrecognized privilege type: "bad"`), true, true})
					for _, args := range []string{"NULL," + o + ",'bad',false", r + ",NULL,'bad',false", r + "," + o + ",NULL,false", r + "," + o + ",'bad',NULL"} {
						cases = append(cases, objectGrantProviderCase{literal("makeaclitem("+args+")", tc.typ, nil), false, false})
					}
					cases = append(cases, objectGrantProviderCase{value("makeaclitem(g,o,$1::text,flag)", " WHERE id=1", tc.typ, nil, nil), false, false})
					controls = append(controls, literal(defaultCall, "_ACLITEM", defaults))
				case "aclcontains":
					call, constant, want = "aa @> a", "'"+array+"'::aclitem[] @> '"+item+"'::aclitem", true
					cases = append(cases,
						objectGrantProviderCase{value("aa OPERATOR(pg_catalog.@>) a", " WHERE id=1", tc.typ, true), true, true},
						objectGrantProviderCase{value("aa @> 'og_object_reader=r/og_object_other'::aclitem", " WHERE id=1", tc.typ, true), true, true},
						objectGrantProviderCase{value("aa @> 'og_object_other=r/og_object_reader'::aclitem", " WHERE id=1", tc.typ, false), true, true},
						objectGrantProviderCase{value("aa @> 'og_object_reader=r*/og_object_other'::aclitem", " WHERE id=3", tc.typ, false), true, true},
						objectGrantProviderCase{value("aa @> 'og_object_reader=rw/og_object_other'::aclitem", " WHERE id=3", tc.typ, false), true, true},
						objectGrantProviderCase{value(call, " WHERE id=3", tc.typ, false), true, true},
						objectGrantProviderCase{value("aa @> CASE WHEN id=1 THEN $1::aclitem ELSE a END", " WHERE id=1", tc.typ, true, item), true, true},
						objectGrantProviderCase{literal("NULL::aclitem[] @> '"+item+"'::aclitem", tc.typ, nil), false, false},
						objectGrantProviderCase{literal("'"+array+"'::aclitem[] @> NULL::aclitem", tc.typ, nil), false, false},
						objectGrantProviderCase{value("aa @> $1::aclitem", " WHERE id=1", tc.typ, nil, nil), false, false},
						objectGrantProviderCase{fullDiagnostic("SELECT '{NULL}'::aclitem[] @> a FROM og_acl_values.source WHERE id=1", "22004", "ACL arrays must not contain null values"), true, true})
					controls = append(controls, literal(makeCall, "ACLITEM", item), literal(defaultCall, "_ACLITEM", defaults))
				case "aclexplode":
					// OID values have an existing backend adapter distinction, not
					// allocator goldens. All four named OUT fields are asserted.
					oidValue := func(id int64) any {
						if b.PG {
							return strconv.FormatInt(id, 10)
						}
						return id
					}
					expanded := [][]any{{oidValue(owner), oidValue(reader), "SELECT", true}, {oidValue(owner), oidValue(reader), "UPDATE", true}}
					explode := func(query string, rows [][]any, args ...any) fixture.Step {
						s := fixture.Select(query, []string{"grantor", "grantee", "privilege_type", "is_grantable"}, []string{"OID", "OID", "TEXT", "BOOL"}, rows, args...)
						s.Bag = true
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
							cols, err := rows.ColumnTypes()
							if err != nil {
								t.Fatal(err)
							}
							fixture.ScalarAssertDescription(t, cols, s.Columns, s.Types)
						}
						return s
					}
					for _, expr := range []string{"aa", "CASE WHEN id=1 THEN $1::aclitem[] ELSE aa END"} {
						s := explode("SELECT x.* FROM og_acl_values.source s,LATERAL pg_catalog.aclexplode("+expr+") x WHERE id=1", expanded)
						if strings.Contains(expr, "$1") {
							s.Args = []any{array}
						}
						cases = append(cases, objectGrantProviderCase{s, true, true})
					}
					// Strict SETOF NULL is zero rows, not scalar NULL folding.
					for _, s := range []fixture.Step{
						explode("SELECT * FROM aclexplode(NULL::aclitem[])", nil),
						explode("SELECT * FROM aclexplode($1::aclitem[])", nil, nil),
						explode("SELECT x.* FROM og_acl_values.source s,LATERAL aclexplode(aa) x WHERE id=2", nil),
						explode("SELECT * FROM aclexplode('"+array+"'::aclitem[]) LIMIT 0", nil),
					} {
						cases = append(cases, objectGrantProviderCase{s, true, true})
					}
					cases = append(cases,
						objectGrantProviderCase{explode("SELECT * FROM aclexplode('"+array+"'::aclitem[]) WHERE FALSE", nil), false, true},
						objectGrantProviderCase{explode("SELECT x.* FROM og_acl_values.source s,LATERAL aclexplode(aa) x WHERE FALSE", nil), false, true},
						objectGrantProviderCase{fullDiagnostic("SELECT * FROM aclexplode('{NULL}'::aclitem[])", "22004", "ACL arrays must not contain null values"), true, true})
					// A target SRF still has RECORD2249; it is not a scalar
					// four-column return or an ACLITEM array disguised as text.
					records := [][]any{{"(" + o + "," + r + ",SELECT,t)"}, {"(" + o + "," + r + ",UPDATE,t)"}}
					s := fixture.Select("SELECT aclexplode(aa) AS v FROM og_acl_values.source WHERE id=1", []string{"v"}, []string{"RECORD"}, records)
					s.Bag = true
					cases = append(cases, objectGrantProviderCase{s, true, true})
					cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT aclexplode(NULL::aclitem[]) AS v", []string{"v"}, []string{"RECORD"}, nil), true, true})
					for _, tail := range []string{" WHERE FALSE", " WHERE id>99", " LIMIT 0"} {
						cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT aclexplode(aa) AS v FROM og_acl_values.source"+tail, []string{"v"}, []string{"RECORD"}, nil), true, true})
					}
					fixture.RoleGrantActor(run, "og_object_other", func() {
						run(fixture.Exec("CREATE VIEW og_acl_values.saved AS SELECT x.* FROM og_acl_values.source s,LATERAL aclexplode(aa) x WHERE id=1", 0))
						run(fixture.Exec("GRANT SELECT ON og_acl_values.saved TO og_object_reader", 0))
					})
					cases = append(cases, objectGrantProviderCase{explode("SELECT * FROM og_acl_values.saved", expanded), true, true})
					controls = append(controls, literal(makeCall, "ACLITEM", item), literal(defaultCall, "_ACLITEM", defaults), value("aa @> a", " WHERE id=1", "BOOL", true))
				}
				if tc.name != "aclexplode" {
					cases = append(cases,
						objectGrantProviderCase{value(call, " WHERE id=1", tc.typ, want), true, true},
						objectGrantProviderCase{value(call, " WHERE id=2", tc.typ, nil), true, true},
						objectGrantProviderCase{literal(constant, tc.typ, want), true, false})
					for _, tail := range []string{" WHERE FALSE", " WHERE id>99", " LIMIT 0"} {
						cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT "+call+" AS v FROM og_acl_values.source"+tail, []string{"v"}, []string{tc.typ}, nil), true, true})
					}
					cases = append(cases, objectGrantProviderCase{value("CASE WHEN FALSE THEN "+call+" ELSE NULL END", " WHERE id=1", tc.typ, nil), false, true})
					fixture.RoleGrantActor(run, "og_object_other", func() {
						run(fixture.Exec("CREATE VIEW og_acl_values.saved AS SELECT "+call+" AS v FROM og_acl_values.source WHERE id=1", 0))
						run(fixture.Exec("GRANT SELECT ON og_acl_values.saved TO og_object_reader", 0))
					})
					cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT v FROM og_acl_values.saved", []string{"v"}, []string{tc.typ}, [][]any{{want}}), true, true})
				}
				// Reading captured ACL data/codecs/role-name output is not a new
				// makeaclitem/acldefault invocation or a grant of actual rights.
				controls = append(controls, fixture.Select("SELECT aa,a FROM og_acl_values.source WHERE id=1", []string{"aa", "a"}, []string{"_ACLITEM", "ACLITEM"}, [][]any{{array, item}}))
				objectGrantProviderLifecycle(t, p, b, c, run, target, "function", tc.name, cases, controls)

				defer objectGrantRoutineCleanup(t, p, c, []string{target})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
				// Import occurs before selected execution; use real bound input,
				// not an ACL producer that could itself fail EXECUTE first.
				importQuery := "SELECT acldefault('r',$1::oid)"
				if tc.name == "makeaclitem" {
					importQuery = "SELECT makeaclitem($1::oid," + o + ",p,flag) FROM og_acl_values.source WHERE id=1"
				} else if tc.name == "aclexplode" {
					importQuery = "SELECT * FROM aclexplode($1::aclitem[])"
				} else if tc.name == "aclcontains" {
					importQuery = "SELECT $1::aclitem[] @> a FROM og_acl_values.source WHERE id=1"
				}
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					s := fullDiagnostic(importQuery, "22P02", `invalid input syntax for type oid: "bad"`, "bad")
					if tc.name == "aclexplode" || tc.name == "aclcontains" {
						s.Diagnostic.Message = `malformed array literal: "bad"`
						s.Diagnostic.Detail = `Array value must start with "{" or dimension information.`
					}
					s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
					run(s)
				})
				// Release the selected target before the explicit-child proof.
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC", 0))
				if tc.name == "aclexplode" || tc.name == "aclcontains" {
					child := "pg_catalog.makeaclitem(oid,oid,text,boolean)"
					defer objectGrantRoutineCleanup(t, p, c, []string{child})
					query := "SELECT ARRAY[makeaclitem(g,o,p,flag)] @> a AS v FROM og_acl_values.source WHERE id=1"
					allowed := fixture.Select(query, []string{"v"}, []string{"BOOL"}, [][]any{{true}})
					if tc.name == "aclexplode" {
						query = "SELECT aclexplode(ARRAY[makeaclitem(g,o,p,flag)]) AS v FROM og_acl_values.source WHERE id=1"
						allowed = fixture.Select(query, []string{"v"}, []string{"RECORD"}, [][]any{{fmt.Sprintf("(%d,%d,SELECT,t)", owner, reader)}, {fmt.Sprintf("(%d,%d,UPDATE,t)", owner, reader)}})
						allowed.Bag = true
					}
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					for _, granted := range []bool{true, false, true, false} {
						verb, preposition := "GRANT", "TO"
						if !granted {
							verb, preposition = "REVOKE", "FROM"
						}
						run(fixture.Exec(verb+" EXECUTE ON FUNCTION "+child+" "+preposition+" PUBLIC", 0))
						fixture.RoleGrantActor(run, "og_object_reader", func() {
							s := allowed
							if !granted {
								s = fullDiagnostic(query, "42501", "permission denied for function makeaclitem")
							}
							run(s)
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						})
					}
				}
			})
		})
	}
}
