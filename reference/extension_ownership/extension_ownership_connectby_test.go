package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The released tree/layout expectations are shared with the connectby suite;
// current extension membership never substitutes for an installed declaration.
func ownershipConnectbySurvivors(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "detached"
		if foreign {
			name = "foreign"
		}
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"eo_cb_owner", "eo_cb_acl"})
			ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := ownershipRun(t, p, b, c)
				for _, q := range []string{
					`CREATE ROLE eo_cb_owner SUPERUSER`, `CREATE ROLE eo_cb_acl`,
					`CREATE SCHEMA eo_old AUTHORIZATION eo_cb_owner`, `CREATE SCHEMA eo_other`,
					`CREATE EXTENSION tablefunc SCHEMA eo_old`,
					`SET search_path TO eo_old,public`,
				} {
					run(fixture.Exec(q, 0))
				}
				fixture.ConnectbyTree(t, p, b, c)
				run(fixture.Exec(`RESET search_path`, 0))
				ids := make([]int64, len(fixture.ConnectbySignatures))
				before := make([][][]any, len(fixture.ConnectbySignatures))
				identity := func(id int64) string {
					return `SELECT p.oid::bigint,p.pronamespace::bigint,p.proowner::bigint,p.proacl::text,p.proargtypes::text,p.proallargtypes::text,p.proargmodes::text,p.proargnames::text,p.prorettype::bigint,p.prolang::bigint,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin,p.pronargdefaults,p.provariadic::bigint FROM pg_proc p WHERE p.oid=` + ownershipNumber(id)
				}
				query := func(v int, args, routine string) string {
					q := strings.Replace(fixture.ConnectbyQuery(v, args), "cb_part.connectby", routine, 1)
					return strings.ReplaceAll(q, "'cb_tree'", "'eo_old.cb_tree'")
				}
				for v, sig := range fixture.ConnectbySignatures {
					original := "eo_old.connectby(" + sig + ")"
					for _, q := range []string{
						"ALTER FUNCTION " + original + " OWNER TO eo_cb_owner",
						"REVOKE ALL ON FUNCTION " + original + " FROM PUBLIC",
						"GRANT EXECUTE ON FUNCTION " + original + " TO eo_cb_acl",
					} {
						run(fixture.Exec(q, 0))
					}
					ids[v] = ownershipID(t, p, c, "SELECT '"+original+"'::regprocedure::bigint")
					before[v] = ownershipSnapshot(t, p, c, identity(ids[v]))
					literal := strings.NewReplacer("$1::text", "'row2'::text", "$2::integer", "0::integer", "$3::text", "'~'::text").Replace(fixture.ConnectbyArgs[v])
					// Bind each genuine overload before either rename or retirement.
					run(fixture.Exec(fmt.Sprintf("CREATE VIEW eo_old.eo_walks_%d AS %s", v, query(v, literal, "eo_old.connectby")), 0))
					run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=`+ownershipNumber(ids[v])+` AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='tablefunc') AND deptype='e'`, 1))
					run(fixture.Exec("ALTER EXTENSION tablefunc DROP FUNCTION "+original, 0))
					run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=`+ownershipNumber(ids[v])+` AND refclassid='pg_extension'::regclass AND deptype='e'`, 0))
					run(fixture.Exec(fmt.Sprintf("ALTER FUNCTION %s RENAME TO eo_walk_%d", original, v), 0))
				}
				values := func(conn *sql.Conn, recorder *fixture.Recorder) {
					r := ownershipRunWithRecorder(t, p, conn, recorder)
					for v := range fixture.ConnectbySignatures {
						routine := fmt.Sprintf("eo_old.eo_walk_%d", v)
						args := []any{"row2", 0}
						if v == 1 || v == 3 {
							args = append(args, "~")
						}
						s := fixture.ConnectbySelect(v, query(v, fixture.ConnectbyArgs[v], routine), fixture.ConnectbyLayout(v, fixture.ConnectbyTreeRows), args...)
						r(s)
						r(fixture.Prepared(s))
						// Strict typed NULL must return the declared empty record shape
						// before trying to resolve this deliberately absent source.
						nullArgs := strings.Replace(fixture.ConnectbyArgs[v], "'cb_tree'", "'eo_old.cb_missing'", 1)
						nullArgs = strings.Replace(nullArgs, "$1::text", "NULL::text", 1)
						nullArgs = strings.NewReplacer("$2::integer", "0::integer", "$3::text", "'~'::text").Replace(nullArgs)
						s = fixture.ConnectbySelect(v, query(v, nullArgs, routine), nil)
						r(s)
						r(fixture.Prepared(s))
						q := fmt.Sprintf("SELECT * FROM eo_old.eo_walks_%d", v)
						if v >= 2 {
							q += " ORDER BY serial"
						}
						s = fixture.ConnectbySelect(v, q, fixture.ConnectbyLayout(v, fixture.ConnectbyTreeRows))
						r(s)
						r(fixture.Prepared(s))
					}
				}
				values(c, b.Recorder)
				run(fixture.Exec(`CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`, 0))
				for v, sig := range fixture.ConnectbySignatures {
					routine := fmt.Sprintf("eo_old.eo_walk_%d(%s)", v, sig)
					run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD FUNCTION `+routine, 0))
					for _, suffix := range []string{"", " CASCADE"} {
						s := ownershipError("DROP FUNCTION "+routine+suffix, "2BP01", "cannot drop function "+routine+" because extension uuid-ossp requires it")
						s.Diagnostic.Hint = "You can drop extension uuid-ossp instead."
						run(s)
					}
				}
				values(c, b.Recorder)
				run(fixture.Exec(`DROP EXTENSION tablefunc`, 0))
				if !foreign {
					for v, sig := range fixture.ConnectbySignatures {
						run(fixture.Exec(fmt.Sprintf(`ALTER EXTENSION "uuid-ossp" DROP FUNCTION eo_old.eo_walk_%d(%s)`, v, sig), 0))
					}
				}
				for v, id := range ids {
					ownershipUnchanged(t, p, c, identity(id), before[v])
				}
				check := func(conn *sql.Conn, recorder *fixture.Recorder) {
					r := ownershipRunWithRecorder(t, p, conn, recorder)
					values(conn, recorder)
					r(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='tablefunc'`, 0))
					r(ownershipCount(`SELECT count(*) FROM pg_proc WHERE pronamespace='eo_old'::regnamespace AND proname IN ('connectby','normal_rand')`, 0))
					member := ""
					if foreign {
						member = "uuid-ossp"
					}
					for v, sig := range fixture.ConnectbySignatures {
						routine := fmt.Sprintf("eo_old.eo_walk_%d(%s)", v, sig)
						where := " WHERE p.oid='" + routine + "'::regprocedure"
						r(fixture.Select(`SELECT COALESCE(e.extname::text,'') AS member_of FROM pg_proc p LEFT JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid`+where, []string{"member_of"}, []string{"TEXT"}, [][]any{{member}}))
						argtypes := []string{"25 25 25 25 23", "25 25 25 25 23 25", "25 25 25 25 25 23", "25 25 25 25 25 23 25"}
						symbol := "connectby_text"
						if v >= 2 {
							symbol = "connectby_text_serial"
						}
						r(fixture.Select(`SELECT r.rolname,p.proargtypes::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.prosrc,l.lanname,has_function_privilege('eo_cb_acl',p.oid,'EXECUTE') AS allowed,has_function_privilege('public',p.oid,'EXECUTE') AS public_allowed FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner JOIN pg_language l ON l.oid=p.prolang`+where, []string{"rolname", "proargtypes", "prorettype", "proisstrict", "provolatile", "proretset", "proparallel", "prosrc", "lanname", "allowed", "public_allowed"}, []string{"NAME", "TEXT", "INT8", "BOOL", "CHAR", "BOOL", "CHAR", "TEXT", "NAME", "BOOL", "BOOL"}, [][]any{{"eo_cb_owner", argtypes[v], int64(2249), true, "s", true, "u", symbol, "c", true, false}}))
						r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='`+routine+`'::regprocedure AND refclassid='pg_namespace'::regclass AND refobjid='eo_old'::regnamespace AND deptype='n'`, 1))
						if !foreign {
							s := ownershipError("DROP FUNCTION "+routine, "2BP01", "cannot drop function "+routine+" because other objects depend on it")
							s.Diagnostic.Detail = fmt.Sprintf("view eo_old.eo_walks_%d depends on function %s", v, routine)
							s.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
							r(s)
						}
					}
				}
				check(c, b.Recorder)
				logical := `SELECT p.proname,n.nspname,r.rolname,p.proacl::text,p.proargtypes::text,p.proallargtypes::text,p.proargmodes::text,p.proargnames::text,p.prorettype::bigint,l.lanname,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin,p.pronargdefaults,p.provariadic::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='eo_old' AND p.proname IN ('eo_walk_0','eo_walk_1','eo_walk_2','eo_walk_3') ORDER BY p.proname`
				current := ownershipSnapshot(t, p, c, logical)
				if !b.PG {
					saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
					connector, err := stuntgres.NewDriver().OpenConnector("eo_connectby_" + name)
					if err != nil {
						t.Fatal(err)
					}
					recorded, recorder := fixture.RecordedNativePool(t, connector)
					other := sql.OpenDB(connector)
					defer other.Close()
					for _, q := range []string{`CREATE ROLE eo_cb_owner SUPERUSER`, `CREATE ROLE eo_cb_acl`} {
						if _, err := other.ExecContext(p.Ctx, q); err != nil {
							t.Fatal(err)
						}
					}
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
						if err := stuntgres.Restore(p.Ctx, db, strings.NewReader(saved)); err != nil {
							t.Fatal(err)
						}
						conn, err := destination.recorded.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						check(conn, destination.recorder)
						ownershipUnchanged(t, p, conn, logical, current)
						ownershipConnectbyCleanup(ownershipRunWithRecorder(t, p, conn, destination.recorder), foreign)
						conn.Close()
					}
				} else {
					ownershipConnectbyCleanup(run, foreign)
				}
			})
		})
	}
}

func ownershipConnectbyCleanup(run func(fixture.Step), foreign bool) {
	for v, sig := range fixture.ConnectbySignatures {
		run(fixture.Exec(fmt.Sprintf("DROP VIEW eo_old.eo_walks_%d", v), 0))
		if foreign {
			run(fixture.Exec(fmt.Sprintf(`ALTER EXTENSION "uuid-ossp" DROP FUNCTION eo_old.eo_walk_%d(%s)`, v, sig), 0))
		}
		run(fixture.Exec(fmt.Sprintf("DROP FUNCTION eo_old.eo_walk_%d(%s)", v, sig), 0))
	}
	run(fixture.Exec(`DROP EXTENSION "uuid-ossp"`, 0))
}
