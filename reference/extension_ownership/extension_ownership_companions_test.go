package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionOwnershipCompanions(t *testing.T) {
	t.Run("associated_array_final_names", func(t *testing.T) { ownershipArrayNameControls(t, false) })
	t.Run("range_actual_placement", func(t *testing.T) { ownershipRangePlacement(t, false) })
	t.Run("rowtype_array_final_names", func(t *testing.T) { ownershipRowtypeNames(t, false) })
	t.Run("range_constructor_prerequisite_collision", func(t *testing.T) { ownershipRangeConstructorCollision(t, false) })
	t.Run("range_exact_set", func(t *testing.T) {
		ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := ownershipRun(t, p, b, c)
			ownershipSetup(run)
			run(fixture.Exec(`CREATE TYPE eo_old.span AS RANGE (SUBTYPE=integer,MULTIRANGE_TYPE_NAME=eo_old.spans)`, 0))
			run(fixture.Exec(`ALTER EXTENSION unaccent ADD TYPE eo_old.span`, 0))
			run(fixture.Select(`SELECT t.typname FROM pg_type t JOIN pg_depend d ON d.classid='pg_type'::regclass AND d.objid=t.oid WHERE d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='unaccent') AND d.deptype='e' ORDER BY t.typname`, []string{"typname"}, []string{"NAME"}, [][]any{{"_span"}, {"_spans"}, {"span"}, {"spans"}}))
			ownershipMemberCount(run, 10)
			run(ownershipCount(`SELECT count(*) FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='unaccent') AND d.deptype='e' AND p.proname IN ('span','spans')`, 0))
			run(fixture.Exec(`ALTER EXTENSION unaccent DROP TYPE eo_old.span`, 0))
			ownershipMemberCount(run, 6)
		})
	})
	t.Run("relation_exact_set_and_mechanical_move", func(t *testing.T) {
		ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := ownershipRun(t, p, b, c)
			ownershipSetup(run)
			run(fixture.Exec(`CREATE TABLE eo_old.items(id serial PRIMARY KEY)`, 0))
			run(fixture.Exec(`ALTER EXTENSION unaccent ADD TABLE eo_old.items`, 0))
			ownershipMemberCount(run, 9)
			run(fixture.Select(`SELECT t.typname FROM pg_type t JOIN pg_depend d ON d.classid='pg_type'::regclass AND d.objid=t.oid WHERE d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='unaccent') AND d.deptype='e' ORDER BY t.typname`, []string{"typname"}, []string{"NAME"}, [][]any{{"_items"}, {"items"}}))
			run(ownershipCount(`SELECT count(*) FROM pg_depend d JOIN pg_class c ON c.oid=d.objid WHERE d.classid='pg_class'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='unaccent') AND d.deptype='e' AND c.relname IN ('items_pkey','items_id_seq')`, 0))
			id := ownershipID(t, p, c, `SELECT 'eo_old.items'::regclass::bigint`)
			run(fixture.Exec(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, 0))
			run(fixture.Select(`SELECT oid::bigint FROM pg_class WHERE relname='items' AND relnamespace='eo_new'::regnamespace`, []string{"oid"}, []string{"INT8"}, [][]any{{id}}))
			run(fixture.Select(`SELECT relname FROM pg_class WHERE relnamespace='eo_new'::regnamespace AND relname IN ('items','items_pkey','items_id_seq') ORDER BY relname`, []string{"relname"}, []string{"NAME"}, [][]any{{"items"}, {"items_id_seq"}, {"items_pkey"}}))
			run(fixture.Exec(`INSERT INTO eo_new.items DEFAULT VALUES`, 1))
			ownershipCallable(run, "eo_new")
		})
	})
	for _, action := range []string{"ADD", "DROP"} {
		t.Run("late_companion_"+action, func(t *testing.T) {
			ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := ownershipRun(t, p, b, c)
				ownershipSetup(run)
				for _, q := range []string{`CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`, `CREATE DOMAIN eo_old.positive AS integer`} {
					run(fixture.Exec(q, 0))
				}
				if action == "ADD" {
					run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD TYPE eo_old._positive`, 0))
				} else {
					run(fixture.Exec(`ALTER EXTENSION unaccent ADD DOMAIN eo_old.positive`, 0))
					run(fixture.Exec(`ALTER EXTENSION unaccent DROP TYPE eo_old._positive`, 0))
				}
				q := `SELECT classid::bigint,objid::bigint,refobjid::bigint,deptype FROM pg_depend WHERE refclassid='pg_extension'::regclass AND objid IN ('eo_old.positive'::regtype,'eo_old._positive'::regtype) ORDER BY classid,objid,refobjid,deptype`
				before := ownershipSnapshot(t, p, c, q)
				message := `type eo_old.positive[] is already a member of extension "uuid-ossp"`
				if action == "DROP" {
					message = `type eo_old.positive[] is not a member of extension "unaccent"`
				}
				run(ownershipError(`ALTER EXTENSION unaccent `+action+` DOMAIN eo_old.positive`, "55000", message))
				ownershipUnchanged(t, p, c, q, before)
			})
		})
	}
	t.Run("installed_base_type", func(t *testing.T) {
		ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := ownershipRun(t, p, b, c)
			ownershipSetup(run)
			run(fixture.Exec(`CREATE EXTENSION cube SCHEMA eo_other`, 0))
			run(fixture.Exec(`ALTER EXTENSION cube DROP TYPE eo_other.cube`, 0))
			run(fixture.Exec(`ALTER EXTENSION unaccent ADD TYPE eo_other.cube`, 0))
			ownershipMemberCount(run, 8)
			run(fixture.Select(`SELECT '(1,2)'::eo_other.cube::text AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"(1, 2)"}}))
			run(fixture.Exec(`ALTER EXTENSION unaccent DROP TYPE eo_other.cube`, 0))
			ownershipMemberCount(run, 6)
			run(fixture.Exec(`ALTER EXTENSION cube ADD TYPE eo_other.cube`, 0))
		})
	})
}
