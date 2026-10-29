package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func ownershipRowtypeNames(t *testing.T) {
	for _, kind := range []string{"TABLE", "VIEW"} {
		t.Run(strings.ToLower(kind), func(t *testing.T) {
			ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := ownershipRun(t, p, b, c)
				ownershipSetup(run)
				if kind == "TABLE" {
					run(fixture.Exec(`CREATE TABLE eo_old.items(v integer)`, 0))
					run(fixture.Exec(`INSERT INTO eo_old.items VALUES(1),(NULL)`, 2))
				} else {
					run(fixture.Exec(`CREATE VIEW eo_old.items AS SELECT v FROM (VALUES(1),(NULL::integer)) AS source(v)`, 0))
				}
				relation := ownershipID(t, p, c, `SELECT 'eo_old.items'::regclass::bigint`)
				rowtype := ownershipID(t, p, c, `SELECT 'eo_old.items'::regtype::bigint`)
				array := ownershipID(t, p, c, `SELECT typarray::bigint FROM pg_type WHERE oid='eo_old.items'::regtype`)
				for _, q := range []string{`CREATE DOMAIN eo_old._items AS integer`, `DROP DOMAIN eo_old._items`, `CREATE DOMAIN eo_old.__items AS integer`, `DROP DOMAIN eo_old.__items`, "ALTER EXTENSION unaccent ADD " + kind + " eo_old.items"} {
					run(fixture.Exec(q, 0))
				}
				member := "unaccent"
				members := int64(9)
				if kind == "VIEW" {
					for _, q := range []string{`CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`, `ALTER EXTENSION unaccent DROP TYPE eo_old.___items`, `ALTER EXTENSION "uuid-ossp" ADD TYPE eo_old.___items`} {
						run(fixture.Exec(q, 0))
					}
					member = "uuid-ossp"
					members = 8
				}
				run(fixture.Exec(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, 0))
				run(fixture.Select(`SELECT c.oid::bigint,c.reltype::bigint,t.typarray::bigint FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='eo_new.items'::regclass`, []string{"oid", "reltype", "typarray"}, []string{"INT8", "INT8", "INT8"}, [][]any{{relation, rowtype, array}}))
				check := func(conn *sql.Conn, recorder *fixture.Recorder) {
					r := ownershipRunWithRecorder(t, p, conn, recorder)
					ownershipMemberCount(r, members)
					r(fixture.Select(`SELECT typname FROM pg_type WHERE typnamespace='eo_new'::regnamespace ORDER BY typname`, []string{"typname"}, []string{"NAME"}, [][]any{{"___items"}, {"items"}}))
					r(ownershipCount(`SELECT count(*) FROM pg_class c JOIN pg_type p ON p.oid=c.reltype AND p.typrelid=c.oid JOIN pg_type a ON a.oid=p.typarray AND a.typelem=p.oid WHERE c.oid='eo_new.items'::regclass AND a.oid='eo_new.___items'::regtype AND c.relnamespace=p.typnamespace AND p.typnamespace=a.typnamespace AND c.relowner=p.typowner AND p.typowner=a.typowner`, 1))
					r(fixture.Select(`SELECT p.typname,e.extname FROM pg_type p JOIN pg_depend d ON d.classid='pg_type'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE p.typnamespace='eo_new'::regnamespace ORDER BY p.typname`, []string{"typname", "extname"}, []string{"NAME", "NAME"}, [][]any{{"___items", member}, {"items", "unaccent"}}))
					r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_type'::regclass AND objid='eo_new.___items'::regtype AND refclassid='pg_type'::regclass AND refobjid='eo_new.items'::regtype AND deptype='i'`, 1))
					r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_type'::regclass AND objid='eo_new.items'::regtype AND refclassid='pg_class'::regclass AND refobjid='eo_new.items'::regclass AND deptype='i'`, 1))
					s := fixture.Select(`SELECT v FROM eo_new.items ORDER BY v NULLS LAST`, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}})
					r(s)
					r(fixture.Prepared(s))
					ownershipCallable(r, "eo_new")
					r(ownershipCount(`SELECT count(*) FROM pg_namespace WHERE nspname NOT IN ('public','eo_old','eo_new','eo_other','information_schema') AND nspname NOT LIKE 'pg_%'`, 0))
				}
				check(c, b.Recorder)
				if b.PG {
					return
				}
				saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
				connector, err := stuntgres.NewDriver().OpenConnector("eo_rowtype_" + strings.ToLower(kind))
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
