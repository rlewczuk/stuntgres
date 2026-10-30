package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The native raw tree is an immutable syntax owner, not PostgreSQL's bound
// nodeToString representation. PostgreSQL independently proves the live type,
// callee and consumer identities; only native controls compare captured syntax.
func ownershipEarthBodyCoordinates(t *testing.T) {
	for _, mode := range []string{"removed_history", "occupied_history", "pg_catalog_history", "occupied_provider_history", "occupied_provider_pg_catalog"} {
		t.Run(mode, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "EO Cube Past")
			fixture.SchemaReferenceCleanup(t, "eo_cube_new")
			fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := fixture.OwnershipRun(t, p, b, c)
				providerHistory := mode == "occupied_provider_history" || mode == "occupied_provider_pg_catalog"
				var pgCatalogFunctions []int64
				history := `"EO Cube Past"`
				for _, q := range []string{"CREATE SCHEMA eo_old", "CREATE SCHEMA eo_new", "CREATE SCHEMA eo_other"} {
					run(fixture.Exec(q, 0))
				}
				if mode == "pg_catalog_history" || mode == "occupied_provider_pg_catalog" {
					history = "pg_catalog"
					if providerHistory && b.PG {
						defer func() {
							ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							defer cancel()
							for _, q := range []string{"DROP EXTENSION IF EXISTS earthdistance CASCADE", "DROP EXTENSION IF EXISTS cube CASCADE", "DROP TYPE IF EXISTS pg_catalog.eo_old_cube CASCADE", "DROP TYPE IF EXISTS pg_catalog.cube CASCADE"} {
								if _, err := c.ExecContext(ctx, q); err != nil {
									t.Error("pg_catalog source cleanup", q, err)
								}
							}
							// Upstream GiST helpers with builtin-only signatures do
							// not depend on cube's nominal type. Type CASCADE cannot
							// retire them; remove only this fixture's captured OIDs
							// after every source/control witness has finished.
							for _, oid := range pgCatalogFunctions {
								var identity string
								err := c.QueryRowContext(ctx, `SELECT oid::regprocedure::text FROM pg_proc WHERE oid=$1::oid`, oid).Scan(&identity)
								if err == sql.ErrNoRows {
									continue // The real extension/type cleanup already removed it.
								}
								if err != nil {
									t.Error("resolve owned pg_catalog helper for cleanup", oid, err)
									continue
								}
								if _, err := c.ExecContext(ctx, "DROP FUNCTION "+identity); err != nil {
									t.Error("drop owned pg_catalog helper", oid, identity, err)
								}
							}
						}()
					}
				} else {
					run(fixture.Exec("CREATE SCHEMA "+history, 0))
				}
				run(fixture.Exec("CREATE EXTENSION cube SCHEMA "+history, 0))
				oldCube := fixture.OwnershipID(t, p, c, "SELECT '"+history+".cube'::regtype::bigint")
				oldArray := fixture.OwnershipID(t, p, c, "SELECT typarray::bigint FROM pg_type WHERE oid='"+history+".cube'::regtype")
				run(fixture.Exec("CREATE EXTENSION earthdistance SCHEMA eo_old", 0))
				run(fixture.Exec("CREATE VIEW eo_old.body_consumer AS SELECT eo_old.latitude(eo_old.ll_to_earth(0::float8,0::float8)) AS latitude", 0))
				// Preserve the first domain and all six SQL bodies while retiring its
				// real originating extension. No synthetic cohort is introduced.
				for _, member := range []string{
					"TYPE eo_old.earth", "FUNCTION eo_old.earth()",
					"FUNCTION eo_old.sec_to_gc(double precision)", "FUNCTION eo_old.gc_to_sec(double precision)",
					"FUNCTION eo_old.ll_to_earth(double precision,double precision)",
					"FUNCTION eo_old.latitude(eo_old.earth)", "FUNCTION eo_old.longitude(eo_old.earth)",
				} {
					run(fixture.Exec("ALTER EXTENSION earthdistance DROP "+member, 0))
				}
				run(fixture.Exec("DROP EXTENSION earthdistance", 0))
				if providerHistory {
					// Only setup enumerates actual callable subsets: PostgreSQL's
					// shipped cube module is larger than the represented recipe.
					type oldFunction struct {
						oid             int64
						name, signature string
					}
					rows, err := c.QueryContext(p.Ctx, `SELECT p.oid::bigint,p.proname,p.proargtypes::text FROM pg_proc p JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='cube' ORDER BY p.oid`)
					if err != nil {
						t.Fatal(err)
					}
					var oldFunctions []oldFunction
					var oldConstructor int64
					for rows.Next() {
						var f oldFunction
						if err := rows.Scan(&f.oid, &f.name, &f.signature); err != nil {
							rows.Close()
							t.Fatal(err)
						}
						oldFunctions = append(oldFunctions, f)
						if b.PG && mode == "occupied_provider_pg_catalog" {
							pgCatalogFunctions = append(pgCatalogFunctions, f.oid)
						}
						if f.name == "cube" && f.signature == "701" {
							oldConstructor = f.oid
						}
					}
					if err := rows.Err(); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					if oldConstructor == 0 {
						t.Fatal("missing released cube(double precision)")
					}
					identity := func(f oldFunction) string {
						var name string
						if err := c.QueryRowContext(p.Ctx, `SELECT $1::oid::regprocedure::text`, f.oid).Scan(&name); err != nil {
							t.Fatal(err)
						}
						return name
					}
					run(fixture.Exec("ALTER EXTENSION cube DROP TYPE "+history+".cube", 0))
					for _, f := range oldFunctions {
						run(fixture.Exec("ALTER EXTENSION cube DROP FUNCTION "+identity(f), 0))
					}
					oldEquality := fixture.OwnershipID(t, p, c, "SELECT '"+history+".=("+history+".cube,"+history+".cube)'::regoperator::bigint")
					operators, err := c.QueryContext(p.Ctx, `SELECT o.oid::regoperator::text FROM pg_operator o JOIN pg_depend d ON d.classid='pg_operator'::regclass AND d.objid=o.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='cube' ORDER BY o.oid`)
					if err != nil {
						t.Fatal(err)
					}
					var operatorNames []string
					for operators.Next() {
						var name string
						if err := operators.Scan(&name); err != nil {
							operators.Close()
							t.Fatal(err)
						}
						operatorNames = append(operatorNames, name)
					}
					if err := operators.Err(); err != nil {
						operators.Close()
						t.Fatal(err)
					}
					if err := operators.Close(); err != nil {
						t.Fatal(err)
					}
					for _, name := range operatorNames {
						run(fixture.Exec("ALTER EXTENSION cube DROP OPERATOR "+name, 0))
					}
					run(fixture.Exec("DROP EXTENSION cube", 0))
					oldArgument := "eo_other.cube"
					if mode == "occupied_provider_pg_catalog" {
						oldArgument = "pg_catalog.eo_old_cube"
						run(fixture.Exec("ALTER TYPE pg_catalog.cube RENAME TO eo_old_cube", 0))
						for i, f := range oldFunctions {
							if f.name != "cube_ll_coord" {
								run(fixture.Exec("ALTER FUNCTION "+identity(f)+" RENAME TO eo_old_cube_proc_"+strconv.Itoa(i), 0))
							}
						}
					} else {
						run(fixture.Exec("ALTER TYPE "+history+".cube SET SCHEMA eo_other", 0))
						for _, f := range oldFunctions {
							run(fixture.Exec("ALTER FUNCTION "+identity(f)+" SET SCHEMA eo_other", 0))
						}
					}
					run(fixture.Exec("CREATE EXTENSION cube SCHEMA "+history, 0))
					newCube := fixture.OwnershipID(t, p, c, "SELECT '"+history+".cube'::regtype::bigint")
					if newCube == oldCube {
						t.Fatal("distinct genuine cube installations reused predecessor identity")
					}
					run(fixture.Select(`SELECT $1::regprocedure::oid<>$2::oid AS new_constructor`, []string{"new_constructor"}, []string{"BOOL"}, [][]any{{true}}, history+".cube(double precision)", oldConstructor))
					run(fixture.Select(`SELECT $1::regoperator::oid<>$2::regoperator::oid AS nominal_operators`, []string{"nominal_operators"}, []string{"BOOL"}, [][]any{{true}}, history+".=("+oldArgument+","+oldArgument+")", history+".=("+history+".cube,"+history+".cube)"))
					if mode == "occupied_provider_pg_catalog" {
						run(fixture.Select(`SELECT $1::regprocedure::oid<>$2::regprocedure::oid AS nominal_overloads`, []string{"nominal_overloads"}, []string{"BOOL"}, [][]any{{true}}, "pg_catalog.cube_ll_coord(pg_catalog.eo_old_cube,integer)", "pg_catalog.cube_ll_coord(pg_catalog.cube,integer)"))
					}
					run(fixture.Exec("CREATE EXTENSION earthdistance SCHEMA eo_new", 0))
					run(fixture.Exec("CREATE SCHEMA eo_cube_new", 0))
					run(fixture.Exec("ALTER EXTENSION cube SET SCHEMA eo_cube_new", 0))
					if mode == "occupied_provider_pg_catalog" {
						run(fixture.Exec("ALTER TYPE pg_catalog.eo_old_cube RENAME TO cube", 0))
						for _, f := range oldFunctions {
							if f.name != "cube_ll_coord" {
								run(fixture.Exec("ALTER FUNCTION "+identity(f)+` RENAME TO "`+strings.ReplaceAll(f.name, `"`, `""`)+`"`, 0))
							}
						}
					} else {
						run(fixture.Exec("ALTER TYPE eo_other.cube SET SCHEMA "+history, 0))
						for _, f := range oldFunctions {
							run(fixture.Exec("ALTER FUNCTION "+identity(f)+" SET SCHEMA "+history, 0))
						}
					}
					if now := fixture.OwnershipID(t, p, c, "SELECT 'eo_cube_new.cube'::regtype::bigint"); now != newCube {
						t.Fatal("source choreography replaced current cube identity", newCube, now)
					}
					if now := fixture.OwnershipID(t, p, c, "SELECT '"+history+".cube(double precision)'::regprocedure::bigint"); now != oldConstructor {
						t.Fatal("source choreography replaced predecessor builtin-input constructor", oldConstructor, now)
					}
					if now := fixture.OwnershipID(t, p, c, "SELECT '"+history+".=("+history+".cube,"+history+".cube)'::regoperator::bigint"); now != oldEquality {
						t.Fatal("source choreography replaced predecessor nominal-input operator", oldEquality, now)
					}
				} else {
					run(fixture.Exec("ALTER EXTENSION cube SET SCHEMA eo_other", 0))
					if mode == "removed_history" {
						run(fixture.Exec("DROP SCHEMA "+history, 0))
					} else if mode == "occupied_history" {
						// This final ordinary relation owns the old canonical type key.
						// It must be created only after the prerequisite leaves that key.
						run(fixture.Exec("CREATE TABLE "+history+".cube(i integer)", 0))
						run(fixture.Exec("INSERT INTO "+history+".cube VALUES (17)", 1))
					}
					run(fixture.Exec("CREATE EXTENSION earthdistance SCHEMA eo_new", 0))
				}
				run(fixture.Exec("CREATE VIEW eo_new.body_consumer AS SELECT eo_new.latitude(eo_new.ll_to_earth(0::float8,0::float8)) AS latitude", 0))
				oldCubeSchema := "eo_other"
				if providerHistory {
					oldCubeSchema = history
				}
				if now := fixture.OwnershipID(t, p, c, "SELECT '"+oldCubeSchema+".cube'::regtype::bigint"); now != oldCube {
					t.Fatal("source choreography replaced predecessor cube identity", oldCube, now)
				}
				if now := fixture.OwnershipID(t, p, c, "SELECT typarray::bigint FROM pg_type WHERE oid='"+oldCubeSchema+".cube'::regtype"); now != oldArray {
					t.Fatal("source choreography replaced predecessor array identity", oldArray, now)
				}
				const bodies = `SELECT p.prosqlbody FROM pg_proc p WHERE p.pronamespace IN ('eo_old'::regnamespace,'eo_new'::regnamespace) AND p.proname IN ('ll_to_earth','latitude','longitude') ORDER BY p.pronamespace::regnamespace::text,p.proname`
				var captured [][]any
				if !b.PG {
					captured = fixture.OwnershipSnapshot(t, p, c, bodies)
					if len(captured) != 6 {
						t.Fatal("missing genuine captured bodies", captured)
					}
					for i, row := range captured {
						schema := "eo_other" // eo_new sorts before eo_old.
						if i >= 3 || providerHistory {
							schema = "EO Cube Past"
							if history == "pg_catalog" {
								schema = "pg_catalog"
							}
						}
						tree, ok := row[0].(string)
						if !ok || !strings.HasPrefix(tree, "{RoutineDeclarationBody {parser.Select") || !strings.Contains(tree, `Text "`+schema+`"`) {
							t.Fatal("lost independently expected captured dependency spelling", schema, row)
						}
					}
				}
				check := func(t *testing.T, conn *sql.Conn, recorder *fixture.Recorder) {
					r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_extension WHERE extname='earthdistance' AND extnamespace='eo_new'::regnamespace`, 1))
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_extension WHERE extname='cube'`, 1))
					if providerHistory {
						r(fixture.Select(`SELECT o.typbasetype=$1::regtype AS old_base,n.typbasetype='eo_cube_new.cube'::regtype AS new_base,o.typbasetype<>n.typbasetype AS distinct_bases FROM pg_type o,pg_type n WHERE o.oid='eo_old.earth'::regtype AND n.oid='eo_new.earth'::regtype`, []string{"old_base", "new_base", "distinct_bases"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, history+".cube"))
						r(fixture.Select(`SELECT t.typname,a.typname,t.typarray=a.oid AS array_identity,a.typelem=t.oid AS element_identity,t.typnamespace=a.typnamespace AS array_namespace,t.typowner=a.typowner AS array_owner FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=$1::regtype`, []string{"typname", "typname", "array_identity", "element_identity", "array_namespace", "array_owner"}, []string{"NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"cube", "_cube", true, true, true, true}}, history+".cube"))
						oldOperator := history + ".=(" + history + ".cube," + history + ".cube)"
						oldBacker := history + ".cube_eq(" + history + ".cube," + history + ".cube)"
						r(fixture.Select(`SELECT oprleft=$2::regtype AS left_identity,oprright=$2::regtype AS right_identity,oprcode=$3::regprocedure AS backer FROM pg_operator WHERE oid=$1::regoperator`, []string{"left_identity", "right_identity", "backer"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, oldOperator, history+".cube", oldBacker))
						r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_operator'::regclass AND objid=$1::regoperator AND refclassid='pg_extension'::regclass AND deptype='e'`, 0, oldOperator))
						r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_operator'::regclass AND objid=$1::regoperator AND refclassid='pg_proc'::regclass AND refobjid=$2::regprocedure AND deptype='n'`, 1, oldOperator, oldBacker))
					} else {
						r(fixture.OwnershipCount(`SELECT count(*) FROM pg_type WHERE oid IN ('eo_old.earth'::regtype,'eo_new.earth'::regtype) AND typbasetype='eo_other.cube'::regtype`, 2))
					}
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='eo_old.ll_to_earth(double precision,double precision)'::regprocedure AND deptype='e'`, 0))
					for _, schema := range []string{"eo_old", "eo_new"} {
						cubeSchema := "eo_other"
						if providerHistory {
							cubeSchema = "eo_cube_new"
							if schema == "eo_old" {
								cubeSchema = history
							}
						}
						r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=$1::regprocedure AND refclassid='pg_proc'::regclass AND refobjid IN ($2::regprocedure,$3::regprocedure) AND deptype='n'`, 2, schema+".ll_to_earth(double precision,double precision)", cubeSchema+".cube(double precision)", cubeSchema+".cube("+cubeSchema+".cube,double precision)"))
						s := fixture.Select("SELECT "+schema+".earth() AS radius,"+schema+".latitude("+schema+".ll_to_earth(0::float8,0::float8)) AS latitude,"+schema+".longitude(NULL::"+schema+".earth) AS missing", []string{"radius", "latitude", "missing"}, []string{"FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{float64(6378168), float64(0), nil}})
						r(s)
						r(fixture.Prepared(s))
						s = fixture.Select("SELECT latitude FROM "+schema+".body_consumer", []string{"latitude"}, []string{"FLOAT8"}, [][]any{{float64(0)}})
						r(s)
						r(fixture.Prepared(s))
					}
					if mode == "removed_history" {
						r(fixture.OwnershipCount(`SELECT count(*) FROM pg_namespace WHERE nspname='EO Cube Past'`, 0))
					} else if mode == "occupied_history" {
						r(fixture.Select(`SELECT i FROM "EO Cube Past".cube`, []string{"i"}, []string{"INT4"}, [][]any{{int64(17)}}))
					}
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_namespace WHERE nspname LIKE '__stuntgres_install%' OR nspname LIKE '__stuntgres_prerequisite%'`, 0))
					if !b.PG {
						fixture.OwnershipUnchanged(t, p, conn, bodies, captured)
					}
				}
				check(t, c, b.Recorder)
				if b.PG {
					return
				}
				saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
				for _, forbidden := range []string{`CREATE SCHEMA "pg_catalog"`, `DROP SCHEMA "pg_catalog"`, `SET SCHEMA "pg_catalog"`} {
					if strings.Contains(saved, forbidden) {
						t.Fatal("replay synthesized forbidden system-schema lifecycle", forbidden)
					}
				}
				const oldQuery = `SELECT prosqlbody FROM pg_proc WHERE oid='eo_old.ll_to_earth(double precision,double precision)'::regprocedure`
				var oldBody string
				if err := c.QueryRowContext(p.Ctx, oldQuery).Scan(&oldBody); err != nil {
					t.Fatal(err)
				}
				oldRows, err := c.QueryContext(p.Ctx, oldQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer oldRows.Close()
				connector, err := stuntgres.NewDriver().OpenConnector("eo_earth_body_coordinates_" + mode)
				if err != nil {
					t.Fatal(err)
				}
				recorded, recorder := fixture.RecordedNativePool(t, connector)
				independent := sql.OpenDB(connector)
				defer independent.Close()
				for _, destination := range []struct {
					name              string
					control, recorded *sql.DB
					recorder          *fixture.Recorder
				}{
					{"same_driver", p.Native, b.DB, b.Recorder},
					{"independent_driver", independent, recorded, recorder},
				} {
					t.Run(destination.name, func(t *testing.T) {
						if err := stuntgres.Reset(p.Ctx, destination.control); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, destination.control, strings.NewReader(saved)); err != nil {
							t.Fatal(err)
						}
						conn, err := destination.recorded.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						check(t, conn, destination.recorder)
						if destination.name == "same_driver" {
							var retained string
							if !oldRows.Next() {
								t.Fatal("lost pre-reset declaration body", oldRows.Err())
							}
							if err := oldRows.Scan(&retained); err != nil || retained != oldBody {
								t.Fatal("retained body changed", retained, err)
							}
							if oldRows.Next() || oldRows.Err() != nil {
								t.Fatal("retained body multiplicity", oldRows.Err())
							}
						}
						if again := fixture.ExtensionDump(t, p.Ctx, destination.control); again != saved {
							t.Fatal("immutable producer coordinates changed on redump")
						}
					})
				}
			})
		})
	}
}
