package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionOwnershipRelocation(t *testing.T) {
	t.Run("identity_and_bound_consumers", func(t *testing.T) {
		fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := fixture.OwnershipRun(t, p, b, c)
			ownershipSetup(run)
			ownershipMemberCount(run, 6)
			run(fixture.Select(`SELECT classid::bigint,count(*) FROM pg_depend WHERE refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='unaccent') AND deptype='e' GROUP BY classid ORDER BY classid`, []string{"classid", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(1255), int64(4)}, {int64(3600), int64(1)}, {int64(3764), int64(1)}}))
			for _, q := range []string{
				`CREATE TABLE eo_old.bound(v text DEFAULT eo_old.unaccent('Hôtel'), CONSTRAINT plain CHECK(eo_old.unaccent(v)=v))`,
				`CREATE VIEW eo_old.bound_view AS SELECT eo_old.unaccent('Hôtel') AS v`,
			} {
				run(fixture.Exec(q, 0))
			}
			procs := fixture.OwnershipSnapshot(t, p, c, ownershipProcedures)
			edges := fixture.OwnershipSnapshot(t, p, c, ownershipEdges)
			ext := fixture.OwnershipID(t, p, c, `SELECT oid::bigint FROM pg_extension WHERE extname='unaccent'`)
			dict := fixture.OwnershipID(t, p, c, `SELECT oid::bigint FROM pg_ts_dict WHERE dictnamespace='eo_old'::regnamespace AND dictname='unaccent'`)
			template := fixture.OwnershipID(t, p, c, `SELECT oid::bigint FROM pg_ts_template WHERE tmplnamespace='eo_old'::regnamespace AND tmplname='unaccent'`)
			old, err := b.DB.QueryContext(p.Ctx, `SELECT proname,pronamespace::bigint FROM pg_proc WHERE pronamespace='eo_old'::regnamespace ORDER BY oid`)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			oldNS := fixture.OwnershipID(t, p, c, `SELECT oid::bigint FROM pg_namespace WHERE nspname='eo_old'`)
			run(fixture.Prepared(fixture.Exec("ALTER EXTENSION unaccent SET SCHEMA eo_new", 0)))
			fixture.OwnershipUnchanged(t, p, c, ownershipProcedures, procs)
			fixture.OwnershipUnchanged(t, p, c, ownershipEdges, edges)
			run(fixture.Select(`SELECT oid::bigint,extversion,extrelocatable,extnamespace='eo_new'::regnamespace AS moved,extconfig,extcondition FROM pg_extension WHERE extname='unaccent'`, []string{"oid", "extversion", "extrelocatable", "moved", "extconfig", "extcondition"}, []string{"INT8", "TEXT", "BOOL", "BOOL", "_OID", "_TEXT"}, [][]any{{ext, "1.1", true, true, nil, nil}}))
			run(fixture.Select(`SELECT oid::bigint,dictnamespace='eo_new'::regnamespace AS moved FROM pg_ts_dict WHERE dictname='unaccent'`, []string{"oid", "moved"}, []string{"INT8", "BOOL"}, [][]any{{dict, true}}))
			run(fixture.Select(`SELECT oid::bigint,tmplnamespace='eo_new'::regnamespace AS moved FROM pg_ts_template WHERE tmplname='unaccent'`, []string{"oid", "moved"}, []string{"INT8", "BOOL"}, [][]any{{template, true}}))
			run(fixture.OwnershipCount(`SELECT count(*) FROM pg_proc WHERE pronamespace='eo_new'::regnamespace AND oid IN (SELECT objid FROM pg_depend WHERE refclassid='pg_extension'::regclass AND refobjid=$1 AND classid='pg_proc'::regclass AND deptype='e')`, 4, ext))
			run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_extension'::regclass AND objid=$1 AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'`, 1, ext))
			ownershipCallable(run, "eo_new")
			run(fixture.Exec(`INSERT INTO eo_old.bound DEFAULT VALUES`, 1))
			for _, q := range []string{`SELECT v FROM eo_old.bound`, `SELECT v FROM eo_old.bound_view`} {
				run(fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
			}
			s := fixture.OwnershipError(`SELECT eo_old.unaccent('Hôtel')`, "42883", `function eo_old.unaccent(unknown) does not exist`)
			s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			s.Diagnostic.Position = "8"
			s.SelectRows = true
			run(s)
			n := 0
			for old.Next() {
				var name string
				var ns int64
				if err := old.Scan(&name, &ns); err != nil {
					t.Fatal(err)
				}
				if ns != oldNS {
					t.Fatalf("old Rows changed namespace: %s %d", name, ns)
				}
				n++
			}
			if err := old.Err(); err != nil {
				t.Fatal(err)
			}
			if n != 4 {
				t.Fatalf("old procedure rows=%d", n)
			}
		})
	})
	t.Run("late_dictionary_collision", func(t *testing.T) {
		fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := fixture.OwnershipRun(t, p, b, c)
			ownershipSetup(run)
			run(fixture.Exec(`CREATE TEXT SEARCH DICTIONARY eo_new.unaccent (TEMPLATE=pg_catalog.simple)`, 0))
			q := `SELECT p.oid::bigint,p.pronamespace::bigint,p.proowner::bigint FROM pg_proc p WHERE p.pronamespace='eo_old'::regnamespace ORDER BY p.oid`
			before := fixture.OwnershipSnapshot(t, p, c, q)
			edges := fixture.OwnershipSnapshot(t, p, c, ownershipEdges)
			run(fixture.OwnershipError(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, "42710", `text search dictionary "unaccent" already exists in schema "eo_new"`))
			fixture.OwnershipUnchanged(t, p, c, q, before)
			fixture.OwnershipUnchanged(t, p, c, ownershipEdges, edges)
			run(fixture.OwnershipCount(`SELECT count(*) FROM pg_extension WHERE extname='unaccent' AND extnamespace='eo_old'::regnamespace`, 1))
			run(fixture.OwnershipCount(`SELECT count(*) FROM pg_ts_template WHERE tmplname='unaccent' AND tmplnamespace='eo_old'::regnamespace`, 1))
			ownershipCallable(run, "eo_old")
		})
	})
}
