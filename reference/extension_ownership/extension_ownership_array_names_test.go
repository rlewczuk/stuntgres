package reference_test

import (
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

// These names follow REL_18_6 makeArrayTypeName/moveArrayTypeName. In particular
// the UTF-8 numeric candidate is 62 bytes: its next rune cannot fit the spare byte.
func ownershipArrayNameControls(t *testing.T) {
	long := strings.Repeat("é", 30) + "ab"
	for _, installed := range []bool{true, false} {
		for _, shape := range []string{"triple", "numeric", "mixed", "utf8_numeric"} {
			for _, retained := range []bool{false, true} {
				prefix := "installed_"
				if !installed {
					prefix = "ordinary_"
				}
				name := prefix + shape + "_history_removed"
				if retained {
					name = prefix + shape + "_blockers_retained"
				}
				t.Run(name, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"eo_array_owner", "eo_array_reader"})
					ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := ownershipRun(t, p, b, c)
						ownershipSetup(run)
						sourceExtension := "lo"
						if installed {
							run(fixture.Exec(`CREATE EXTENSION lo SCHEMA eo_old`, 0))
						} else {
							sourceExtension = "unaccent"
							run(fixture.Exec(`CREATE DOMAIN eo_old.lo AS oid`, 0))
							run(fixture.Exec(`ALTER EXTENSION unaccent ADD DOMAIN eo_old.lo`, 0))
						}
						parent := "lo"
						array := "_lo"
						baseID := ownershipID(t, p, c, `SELECT 'eo_old.lo'::regtype::bigint`)
						arrayID := ownershipID(t, p, c, `SELECT typarray::bigint FROM pg_type WHERE oid='eo_old.lo'::regtype`)
						qname := func(n string) string { return "eo_old." + pq.QuoteIdentifier(n) }
						create := func(n string) { run(fixture.Exec("CREATE DOMAIN "+qname(n)+" AS integer", 0)) }
						drop := func(n string) { run(fixture.Exec("DROP DOMAIN "+qname(n), 0)) }
						var blockers, blockerArrays []string
						switch shape {
						case "triple":
							create("_lo")
							drop("_lo")
							create("__lo")
							array = "___lo"
							blockers = []string{"__lo"}
							blockerArrays = []string{"___lo_1"}
						case "numeric":
							run(fixture.Exec(`ALTER DOMAIN eo_old.lo RENAME TO eo_array_parent`, 0))
							create("_lo")
							run(fixture.Exec(`ALTER DOMAIN eo_old.eo_array_parent RENAME TO lo`, 0))
							array = "_lo_1"
							blockers = []string{"_lo"}
							blockerArrays = []string{"__lo"}
						case "mixed":
							create("__lo")
							create("_lo")
							array = "__lo_1"
							blockers = []string{"_lo", "__lo"}
							blockerArrays = []string{"__lo_2", "___lo"}
						case "utf8_numeric":
							parent = long
							run(fixture.Exec(`ALTER DOMAIN eo_old.lo RENAME TO `+pq.QuoteIdentifier(parent), 0))
							block := "__" + strings.Repeat("é", 30) + "a"
							trigger := "_" + parent
							create(block)
							create(trigger)
							array = "__" + strings.Repeat("é", 29) + "_1"
							blockers = []string{trigger, block}
							blockerArrays = []string{"__" + strings.Repeat("é", 29) + "_2", "___" + strings.Repeat("é", 30)}
						}
						if !retained {
							for _, n := range blockers {
								drop(n)
							}
							blockers = nil
							blockerArrays = nil
						}
						member := sourceExtension
						if shape == "triple" {
							run(fixture.Exec("ALTER EXTENSION "+sourceExtension+" DROP TYPE "+qname(array), 0))
							member = ""
						}
						if shape == "numeric" || shape == "utf8_numeric" {
							run(fixture.Exec(`CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`, 0))
							run(fixture.Exec("ALTER EXTENSION "+sourceExtension+" DROP TYPE "+qname(array), 0))
							run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD TYPE `+qname(array), 0))
							member = "uuid-ossp"
						}
						for _, q := range []string{`CREATE ROLE eo_array_owner`, `CREATE ROLE eo_array_reader`, `GRANT USAGE,CREATE ON SCHEMA eo_old TO eo_array_owner`, `GRANT USAGE ON SCHEMA eo_old TO eo_array_reader`, "ALTER DOMAIN " + qname(parent) + " OWNER TO eo_array_owner", "REVOKE ALL ON TYPE " + qname(parent) + " FROM PUBLIC", "GRANT USAGE ON TYPE " + qname(parent) + " TO eo_array_reader"} {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Select(`SELECT oid::bigint,typarray::bigint FROM pg_type WHERE oid=`+pq.QuoteLiteral(qname(parent))+`::regtype`, []string{"oid", "typarray"}, []string{"INT8", "INT8"}, [][]any{{baseID, arrayID}}))
						expectedNames := append([]string{parent, array}, blockers...)
						expectedNames = append(expectedNames, blockerArrays...)
						slices.Sort(expectedNames)
						check := func(conn *sql.Conn, recorder *fixture.Recorder) {
							r := ownershipRunWithRecorder(t, p, conn, recorder)
							rows := make([][]any, len(expectedNames))
							for i, n := range expectedNames {
								rows[i] = []any{n}
							}
							r(fixture.Select(`SELECT typname FROM pg_type WHERE typnamespace='eo_old'::regnamespace ORDER BY typname`, []string{"typname"}, []string{"NAME"}, rows))
							r(ownershipCount(`SELECT count(*) FROM pg_type p JOIN pg_type a ON a.oid=p.typarray AND a.typelem=p.oid WHERE p.oid=`+pq.QuoteLiteral(qname(parent))+`::regtype AND a.oid=`+pq.QuoteLiteral(qname(array))+`::regtype AND p.typnamespace=a.typnamespace AND p.typowner=a.typowner`, 1))
							r(fixture.Select(`SELECT o.rolname,p.typacl IS NOT NULL AS explicit_acl,has_type_privilege('eo_array_reader',p.oid,'USAGE') AS allowed FROM pg_type p JOIN pg_roles o ON o.oid=p.typowner WHERE p.oid=`+pq.QuoteLiteral(qname(parent))+`::regtype`, []string{"rolname", "explicit_acl", "allowed"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"eo_array_owner", true, true}}))
							r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_type'::regclass AND objid=`+pq.QuoteLiteral(qname(array))+`::regtype AND refclassid='pg_type'::regclass AND refobjid=`+pq.QuoteLiteral(qname(parent))+`::regtype AND deptype='i'`, 1))
							r(fixture.Select(`SELECT COALESCE(e.extname::text,'') AS member_of FROM pg_type a LEFT JOIN pg_depend d ON d.classid='pg_type'::regclass AND d.objid=a.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid WHERE a.oid=`+pq.QuoteLiteral(qname(array))+`::regtype`, []string{"member_of"}, []string{"TEXT"}, [][]any{{member}}))
							// Lo's represented recipe is its domain/array, not upstream's unrelated trigger routines.
							sourceMembers := int64(1)
							class := " AND classid='pg_type'::regclass"
							if !installed {
								sourceMembers = 7
								class = ""
							}
							if member == sourceExtension {
								sourceMembers++
							}
							r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname=`+pq.QuoteLiteral(sourceExtension)+`) AND deptype='e'`+class, sourceMembers))
							s := fixture.Select("SELECT 42::"+qname(parent)+"::oid::bigint AS scalar,NULL::"+qname(parent)+"::oid::bigint AS absent", []string{"scalar", "absent"}, []string{"INT8", "INT8"}, [][]any{{int64(42), nil}})
							if !installed {
								s = fixture.Select("SELECT 42::"+qname(parent)+"::oid::bigint AS scalar,'{1,NULL,2}'::"+qname(array)+"::text AS array_value,NULL::"+qname(array)+"::text AS absent", []string{"scalar", "array_value", "absent"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{int64(42), "{1,NULL,2}", nil}})
							}
							r(s)
							r(fixture.Prepared(s))
							r(ownershipCount(`SELECT count(*) FROM pg_namespace WHERE nspname NOT IN ('public','eo_old','eo_new','eo_other','information_schema') AND nspname NOT LIKE 'pg_%'`, 0))
						}
						check(c, b.Recorder)
						if parent == "lo" {
							s := fixture.ImplicitError("ALTER TYPE "+qname(array)+" RENAME TO forbidden", fixture.Diagnostic{Code: "42809", Message: "cannot alter array type eo_old.lo[]", Hint: "You can alter type eo_old.lo, which will alter the array type as well."})
							s.ErrorStage = "execute"
							run(s)
							check(c, b.Recorder)
						}
						if b.PG {
							return
						}
						saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
						connector, err := stuntgres.NewDriver().OpenConnector("eo_names_" + name)
						if err != nil {
							t.Fatal(err)
						}
						recorded, recorder := fixture.RecordedNativePool(t, connector)
						other := sql.OpenDB(connector)
						defer other.Close()
						for _, destination := range []struct {
							control, recorded *sql.DB
							recorder          *fixture.Recorder
						}{
							{p.Native, b.DB, b.Recorder},
							{other, recorded, recorder},
						} {
							db := destination.control
							if err := stuntgres.Reset(p.Ctx, db); err != nil {
								t.Fatal(err)
							}
							if db != p.Native {
								for _, q := range []string{`CREATE ROLE eo_array_owner`, `CREATE ROLE eo_array_reader`} {
									if _, err := db.ExecContext(p.Ctx, q); err != nil {
										t.Fatal(err)
									}
								}
							}
							if err := stuntgres.Restore(p.Ctx, db, strings.NewReader(saved)); err != nil {
								t.Fatal(err)
							}
							conn, err := destination.recorded.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							check(conn, destination.recorder)
							conn.Close()
						}
					})
				})
			}
		}
	}
}
