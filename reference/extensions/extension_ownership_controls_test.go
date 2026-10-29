package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExtensionOwnershipControls(t *testing.T) {
	t.Run("stock_absence_controls", ownershipStockAbsenceControls)
	t.Run("generated_array_membership", ownershipArrayMembershipControls)
	t.Run("earth_scalar_survivors", func(t *testing.T) { ownershipEarthScalarSurvivors(t, true) })
	t.Run("earth_body_coordinates", func(t *testing.T) { ownershipEarthBodyCoordinates(t, true) })
	t.Run("associated_array_final_names", func(t *testing.T) { ownershipArrayNameControls(t, true) })
	t.Run("range_actual_placement", func(t *testing.T) { ownershipRangePlacement(t, true) })
	t.Run("rowtype_array_final_names", func(t *testing.T) { ownershipRowtypeNames(t, true) })
	t.Run("range_constructor_prerequisite_collision", func(t *testing.T) { ownershipRangeConstructorCollision(t, true) })
	t.Run("tablefunc_survivors", func(t *testing.T) { ownershipTablefuncSurvivors(t, true) })
	t.Run("pageinspect_survivors", func(t *testing.T) { ownershipPageinspectSurvivors(t, true) })
	t.Run("connectby_survivors", func(t *testing.T) { ownershipConnectbySurvivors(t, true) })
	t.Run("pageinspect_families_survivors", func(t *testing.T) { ownershipPageinspectFamiliesSurvivors(t, true) })
	t.Run("crosstab_survivors", func(t *testing.T) { ownershipCrosstabSurvivors(t, true) })
	t.Run("hstore_survivors", func(t *testing.T) { ownershipHstoreSurvivors(t, true) })
	t.Run("provider_metadata_survivors", func(t *testing.T) { ownershipProviderMetadataSurvivors(t, true) })
	t.Run("template_survivors", func(t *testing.T) { ownershipTemplateSurvivors(t, true) })
	t.Run("mixed_members_owners_stock_deltas", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"eo_owner", "eo_object_owner", "eo_acl"})
		ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := ownershipRun(t, p, b, c)
			for _, q := range []string{`CREATE ROLE eo_owner SUPERUSER`, `CREATE ROLE eo_object_owner`, `CREATE ROLE eo_acl`, `CREATE SCHEMA eo_old`, `CREATE SCHEMA eo_new`, `CREATE SCHEMA eo_other`, `SET SESSION AUTHORIZATION eo_owner`, `CREATE EXTENSION unaccent SCHEMA eo_old`, `RESET SESSION AUTHORIZATION`, `CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`, `ALTER EXTENSION unaccent SET SCHEMA eo_new`, `CREATE FUNCTION eo_new.member(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1+1'`, `CREATE TABLE eo_new.rows(v text)`, `INSERT INTO eo_new.rows VALUES ('Hotel'),(NULL)`, `CREATE TYPE eo_new.label AS ENUM ('one','two')`, `CREATE TYPE eo_new.span AS RANGE (SUBTYPE=integer,MULTIRANGE_TYPE_NAME=eo_new.spans)`, `ALTER EXTENSION unaccent ADD FUNCTION eo_new.member(integer)`, `ALTER EXTENSION unaccent ADD TABLE eo_new.rows`, `ALTER EXTENSION unaccent ADD TYPE eo_new.label`, `ALTER EXTENSION unaccent ADD TYPE eo_new.span`, `ALTER FUNCTION eo_new.member(integer) OWNER TO eo_object_owner`, `GRANT USAGE ON SCHEMA eo_new TO eo_object_owner,eo_acl`, `REVOKE ALL ON FUNCTION eo_new.member(integer) FROM PUBLIC`, `GRANT EXECUTE ON FUNCTION eo_new.member(integer) TO eo_acl`, `ALTER EXTENSION unaccent DROP FUNCTION eo_new.unaccent(text)`, `ALTER EXTENSION "uuid-ossp" ADD FUNCTION eo_new.unaccent(text)`} {
				n := int64(0)
				if strings.HasPrefix(q, "INSERT ") {
					n = 2
				}
				run(fixture.Exec(q, n))
			}
			defer ownershipStockCleanup(t, c)()
			for _, q := range []string{`ALTER EXTENSION plpgsql DROP LANGUAGE plpgsql`, `ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_inline_handler(internal)`} {
				run(fixture.Exec(q, 0))
			}
			check := func(conn *sql.Conn, recorder *fixture.Recorder) {
				r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
				ownershipCallable(r, "eo_new")
				r(fixture.Select(`SELECT eo_new.member(4) AS answer`, []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}}))
				r(fixture.Select(`SELECT v FROM eo_new.rows ORDER BY v NULLS LAST`, []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}, {nil}}))
				r(ownershipCount(`SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='unaccent' AND d.deptype='e'`, 15))
				r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='eo_new.unaccent(text)'::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='uuid-ossp') AND deptype='e'`, 1))
				r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='plpgsql') AND deptype='e'`, 2))
				r(fixture.Select(`SELECT r.rolname FROM pg_roles r JOIN pg_extension e ON e.extowner=r.oid WHERE e.extname='unaccent'`, []string{"rolname"}, []string{"NAME"}, [][]any{{"eo_owner"}}))
				r(fixture.Select(`SELECT r.rolname,has_function_privilege('eo_acl',p.oid,'EXECUTE') AS allowed,has_function_privilege('eo_owner',p.oid,'EXECUTE') AS owner_allowed FROM pg_roles r JOIN pg_proc p ON p.proowner=r.oid WHERE p.oid='eo_new.member(integer)'::regprocedure`, []string{"rolname", "allowed", "owner_allowed"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"eo_object_owner", true, true}}))
				r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_extension'::regclass AND objid=(SELECT oid FROM pg_extension WHERE extname='unaccent') AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'`, 1))
				s := ownershipError(`DROP TABLE eo_new.rows`, "2BP01", `cannot drop table eo_new.rows because extension unaccent requires it`)
				s.Diagnostic.Hint = "You can drop extension unaccent instead."
				r(s)
				r(fixture.Exec(`SET ROLE eo_acl`, 0))
				r(ownershipError(`ALTER EXTENSION unaccent DROP FUNCTION eo_new.member(integer)`, "42501", `must be owner of extension unaccent`))
				r(fixture.Exec(`RESET ROLE`, 0))
			}
			check(c, b.Recorder)
			if b.PG {
				return
			}
			saved := extensionDump(t, p.Ctx, p.Native)
			// Private membership is excluded from committed-only capture.
			run(fixture.Exec(`BEGIN`, 0))
			run(fixture.Exec(`ALTER EXTENSION unaccent DROP TABLE eo_new.rows`, 0))
			if got := extensionDump(t, p.Ctx, p.Native); got != saved {
				t.Fatal("Dump exposed private membership")
			}
			run(fixture.Exec(`ROLLBACK`, 0))
			old, err := b.DB.QueryContext(p.Ctx, `SELECT v FROM eo_new.rows ORDER BY v NULLS LAST`)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(saved)); err != nil {
				t.Fatal(err)
			}
			check(c, b.Recorder)
			n := 0
			for old.Next() {
				var v sql.NullString
				if err := old.Scan(&v); err != nil {
					t.Fatal(err)
				}
				if n == 0 && (!v.Valid || v.String != "Hotel") {
					t.Fatal("old Rows changed", v)
				}
				if n == 1 && v.Valid {
					t.Fatal("old NULL changed", v)
				}
				n++
			}
			if err := old.Err(); err != nil {
				t.Fatal(err)
			}
			if n != 2 {
				t.Fatal("old row count", n)
			}
			old.Close()
			connector, err := stuntgres.NewDriver().OpenConnector("eo_control_destination")
			if err != nil {
				t.Fatal(err)
			}
			recorded, recorder := fixture.RecordedNativePool(t, connector)
			other := sql.OpenDB(connector)
			defer other.Close()
			for _, q := range []string{`CREATE ROLE eo_owner SUPERUSER`, `CREATE ROLE eo_object_owner`, `CREATE ROLE eo_acl`} {
				if _, err := other.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(saved)); err != nil {
				t.Fatal(err)
			}
			target, err := recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			check(target, recorder)
			ownershipControlFailures(t, p, c, b.Recorder, saved)
		})
	})
	t.Run("absent_installation_and_deleted_declarations", func(t *testing.T) {
		ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := ownershipRun(t, p, b, c)
			ownershipSetup(run)
			for _, target := range []string{"FUNCTION eo_old.unaccent(text)", "FUNCTION eo_old.unaccent(regdictionary,text)", "FUNCTION eo_old.unaccent_init(internal)", "FUNCTION eo_old.unaccent_lexize(internal,internal,internal,internal)", "TEXT SEARCH DICTIONARY eo_old.unaccent", "TEXT SEARCH TEMPLATE eo_old.unaccent"} {
				run(fixture.Exec("ALTER EXTENSION unaccent DROP "+target, 0))
			}
			run(fixture.Exec(`DROP FUNCTION eo_old.unaccent(regdictionary,text)`, 0))
			run(fixture.Exec(`DROP EXTENSION unaccent`, 0))
			check := func(conn *sql.Conn, recorder *fixture.Recorder) {
				r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
				ownershipCallable(r, "eo_old")
				r(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='unaccent'`, 0))
				r(ownershipCount(`SELECT count(*) FROM pg_proc WHERE pronamespace='eo_old'::regnamespace AND proname='unaccent'`, 1))
				r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='eo_old.unaccent(text)'::regprocedure AND deptype='e'`, 0))
			}
			check(c, b.Recorder)
			if b.PG {
				return
			}
			saved := extensionDump(t, p.Ctx, p.Native)
			connector, err := stuntgres.NewDriver().OpenConnector("eo_absent_origin")
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

func ownershipControlFailures(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, saved string) {
	t.Helper()
	before := extensionDump(t, p.Ctx, p.Native)
	for _, script := range []string{
		strings.Replace(saved, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1),
		strings.Replace(saved, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v2", 1),
		saved + "\nCREATE EXTENSION missing_provider;\n",
		saved + "\nCREATE TABLE eo_new.rows(v integer);\n",
		strings.ReplaceAll(saved, "eo_object_owner", "eo_missing_owner"),
	} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore accepted")
		}
		if after := extensionDump(t, p.Ctx, p.Native); after != before {
			t.Fatal("failed restore published partial state")
		}
	}
	if !strings.Contains(saved, "-- stuntgres-extension-owners-v3 ") {
		t.Fatal("missing strict v3 membership owner metadata")
	}
	var output bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &output); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, r, ownershipCount(`SELECT count(*) FROM eo_new.rows`, 2))
}
