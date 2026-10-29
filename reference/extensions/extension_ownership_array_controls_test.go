package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func ownershipArrayMembershipControls(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "detached_array"
		if foreign {
			name = "foreign_array"
		}
		t.Run(name, func(t *testing.T) {
			ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := ownershipRun(t, p, b, c)
				ownershipSetup(run)
				for _, q := range []string{`CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`, `CREATE DOMAIN eo_old.positive AS integer CHECK(VALUE>0)`, `ALTER EXTENSION unaccent ADD DOMAIN eo_old.positive`, `ALTER EXTENSION unaccent DROP TYPE eo_old._positive`} {
					run(fixture.Exec(q, 0))
				}
				if foreign {
					run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD TYPE eo_old._positive`, 0))
				}
				check := func(conn *sql.Conn, recorder *fixture.Recorder) {
					r := ownershipRunWithRecorder(t, p, conn, recorder)
					ownershipMemberCount(r, 7)
					arrayExtension := ""
					uuidMembers := int64(10)
					if foreign {
						arrayExtension = "uuid-ossp"
						uuidMembers = 11
					}
					r(fixture.Select(`SELECT t.typname,COALESCE(e.extname::text,'') AS member_of FROM pg_type t LEFT JOIN pg_depend d ON d.classid='pg_type'::regclass AND d.objid=t.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid WHERE t.typnamespace='eo_old'::regnamespace AND t.typname IN ('positive','_positive') ORDER BY t.typname`, []string{"typname", "member_of"}, []string{"NAME", "TEXT"}, [][]any{{"_positive", arrayExtension}, {"positive", "unaccent"}}))
					r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='uuid-ossp') AND deptype='e'`, uuidMembers))
					r(ownershipCount(`SELECT count(*) FROM pg_type parent JOIN pg_type child ON child.oid=parent.typarray AND child.typelem=parent.oid WHERE parent.oid='eo_old.positive'::regtype AND child.oid='eo_old._positive'::regtype AND parent.typowner=child.typowner AND parent.typnamespace=child.typnamespace`, 1))
					s := fixture.Select(`SELECT 3::eo_old.positive::integer AS scalar,'{1,2}'::eo_old.positive[]::text AS array_value`, []string{"scalar", "array_value"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "{1,2}"}})
					r(s)
					r(fixture.Prepared(s))
				}
				check(c, b.Recorder)
				if b.PG {
					return
				}
				saved := extensionDump(t, p.Ctx, p.Native)
				connector, err := stuntgres.NewDriver().OpenConnector("eo_array_" + name)
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
