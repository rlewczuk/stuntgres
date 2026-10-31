package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsAlterSystemNamespace(t *testing.T) {
	withStatisticsAlter(t, func(t *testing.T, s ndistinctScenario) {
		statisticsAlterSetup(s)
		defer s.conn.ExecContext(s.pair.Ctx, "DROP STATISTICS IF EXISTS pg_catalog.sa_system_definition")
		var owner string
		if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT current_user").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		oid := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_before'")
		for _, q := range []string{"CREATE ROLE sa_actor", "GRANT USAGE ON SCHEMA public TO sa_actor", "SET ROLE sa_actor"} {
			s.run(fixture.Exec(q, 0))
		}
		defer s.conn.ExecContext(s.pair.Ctx, "RESET ROLE")
		// Destination CREATE admission precedes source ownership, even for pg_catalog.
		s.run(statisticsError("ALTER STATISTICS public.sa_before SET SCHEMA pg_catalog", "42501", "permission denied for schema pg_catalog"))
		s.run(fixture.Exec("RESET ROLE", 0))
		s.run(statisticsAlterMetadata("sa_before", "public", owner, nil))
		statisticsAlterFacts(t, s, "sa_before")
		s.run(fixture.Exec("ALTER STATISTICS public.sa_before RENAME TO sa_system_definition", 0))
		s.run(fixture.Exec("ALTER STATISTICS public.sa_system_definition SET SCHEMA pg_catalog", 0))
		assertIdentity := func(schema string, target any) {
			s.run(statisticsAlterMetadata("sa_system_definition", schema, owner, target))
			if got := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_system_definition'"); got != oid {
				t.Fatal("system movement replaced original definition", got, oid)
			}
			// PostgreSQL omits dependency rows referencing pinned objects.
			// pg_catalog is pinned; public is not. The descriptor's namespace
			// remains exact in the metadata assertion above in either case.
			edges := [][]any{{int64(1259), int64(1), "a", true}, {int64(1259), int64(2), "a", true}}
			var namespaces [][]any
			switch schema {
			case "public":
				edges = append(edges, []any{int64(2615), int64(0), "n", true})
				namespaces = [][]any{{"public"}}
			case "pg_catalog":
				// The two input-column AUTO edges remain; no namespace edge.
			default:
				t.Fatal("missing independent namespace dependency contract", schema)
			}
			s.run(fixture.Select("SELECT d.refclassid::bigint,d.refobjsubid,d.deptype,d.refobjid=CASE WHEN d.refclassid=1259 THEN s.stxrelid ELSE s.stxnamespace END AS reference_link FROM pg_depend d JOIN pg_statistic_ext s ON s.oid=d.objid WHERE d.classid=3381 AND s.stxname='sa_system_definition' ORDER BY d.refclassid,d.refobjsubid", []string{"refclassid", "refobjsubid", "deptype", "reference_link"}, []string{"INT8", "INT4", "CHAR", "BOOL"}, edges))
			s.run(fixture.Select("SELECT n.nspname FROM pg_depend d JOIN pg_namespace n ON n.oid=d.refobjid JOIN pg_statistic_ext s ON s.oid=d.objid WHERE d.classid=3381 AND d.refclassid=2615 AND s.stxname='sa_system_definition'", []string{"nspname"}, []string{"NAME"}, namespaces))
			statisticsAlterFacts(t, s, "sa_system_definition")
		}
		assertIdentity("pg_catalog", nil)
		s.run(fixture.Exec("ALTER STATISTICS pg_catalog.sa_system_definition OWNER TO sa_actor", 0))
		s.run(fixture.Exec("GRANT CREATE ON SCHEMA public TO sa_actor", 0))
		s.run(fixture.Exec("SET ROLE sa_actor", 0))
		s.run(statisticsError("ALTER STATISTICS pg_catalog.sa_system_definition SET SCHEMA pg_catalog", "42501", "permission denied for schema pg_catalog"))
		s.run(fixture.Exec("ALTER STATISTICS pg_catalog.sa_system_definition SET SCHEMA public", 0))
		s.run(fixture.Exec("RESET ROLE", 0))
		s.run(statisticsAlterMetadata("sa_system_definition", "public", "sa_actor", nil))
		s.run(fixture.Exec("ALTER STATISTICS public.sa_system_definition OWNER TO CURRENT_USER", 0))
		assertIdentity("public", nil)
		// The bounded system destination path must not relax namespace transitions.
		s.run(statisticsError("ALTER STATISTICS public.sa_system_definition SET SCHEMA pg_toast", "0A000", "cannot move objects into or out of TOAST schema"))
		s.run(fixture.Exec("CREATE TEMP TABLE sa_system_temp(a INT)", 0))
		s.run(statisticsError("ALTER STATISTICS public.sa_system_definition SET SCHEMA pg_temp", "0A000", "cannot move objects into or out of temporary schemas"))
		assertIdentity("public", nil)
		s.run(fixture.Exec("ALTER STATISTICS public.sa_system_definition SET SCHEMA pg_catalog", 0))
		s.run(fixture.Exec("ALTER STATISTICS pg_catalog.sa_system_definition SET STATISTICS 0", 0))
		assertIdentity("pg_catalog", int64(0))
		s.run(fixture.Exec("INSERT INTO sa_input VALUES(3,30)", 1))
		statisticsAlterFacts(t, s, "sa_system_definition")
		if !s.pg {
			var saved bytes.Buffer
			if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(saved.String(), `CREATE STATISTICS "pg_catalog"."sa_system_definition"`) {
				t.Fatal("system definition missing from actual dump")
			}
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector(t.Name() + "-system-restore")
			if err != nil {
				t.Fatal(err)
			}
			other := sql.OpenDB(connector)
			defer other.Close()
			if _, err = other.ExecContext(s.pair.Ctx, "CREATE ROLE sa_actor; CREATE ROLE sa_recipient; CREATE TABLE collision(v INT)"); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(s.pair.Ctx, other, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal("restoring a moved system definition", err)
			}
			recorded, rec := fixture.RecordedNativePool(t, connector)
			restoredConn, err := recorded.Conn(s.pair.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer restoredConn.Close()
			run := func(step fixture.Step) { fixture.RunStep(t, s.pair.Ctx, restoredConn, rec, step) }
			run(statisticsAlterMetadata("sa_system_definition", "pg_catalog", owner, int64(0)))
			statisticsAlterFacts(t, ndistinctScenario{run: run, conn: restoredConn, pair: s.pair}, "sa_system_definition")
			run(fixture.Select("SELECT count(*) FROM sa_input", []string{"count"}, []string{"INT8"}, [][]any{{int64(5)}}))
			var again bytes.Buffer
			if err = stuntgres.Dump(s.pair.Ctx, other, &again); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(saved.Bytes(), again.Bytes()) {
				t.Fatal("system definition/state redump changed")
			}
			restoredID := fixture.DeparseDefinitionID(t, restoredConn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_system_definition'")
			run(fixture.Exec("ALTER STATISTICS pg_catalog.sa_system_definition SET SCHEMA public", 0))
			run(statisticsAlterMetadata("sa_system_definition", "public", owner, int64(0)))
			if got := fixture.DeparseDefinitionID(t, restoredConn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_system_definition'"); got != restoredID {
				t.Fatal("restored move replaced identity")
			}
		}
	})
}
