package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Script views are ordinary mutable SQL consumers, not sealed C declarations.
// Their current definitions and memberships, including absence, own replay.
func TestReferencePgStatStatementsConsumerLifecycle(t *testing.T) {
	t.Run("changed_moved_main", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "pgss_consumer_move")
		fixture.RoleReferenceCleanup(t, []string{"pgss_consumer_owner", "pgss_consumer_reader"})
		pgssCase(t, true, func(s *pgssScenario) {
			pgssConsumerOriginal(s)
			s.both(fixture.ImplicitError("COMMENT ON VIEW pgss_part.pgss_consumer_rows IS 'rejected'", fixture.Diagnostic{Code: "42809", Message: `"pgss_consumer_rows" is not a view`}))
			s.both(fixture.Select("SELECT obj_description('pgss_part.pgss_consumer_rows'::regclass,'pg_class') IS NULL AS unchanged", []string{"unchanged"}, []string{"BOOL"}, [][]any{{true}}))
			s.exec("CREATE ROLE pgss_consumer_owner")
			s.exec("CREATE ROLE pgss_consumer_reader")
			s.exec("CREATE SCHEMA pgss_consumer_move")
			s.exec("CREATE VIEW pgss_part.pgss_consumer_dependent AS SELECT query,calls FROM pgss_part.pg_stat_statements")
			s.exec("ALTER VIEW pgss_part.pg_stat_statements RENAME TO pgss_consumer_saved")
			s.exec("ALTER VIEW pgss_part.pgss_consumer_saved SET SCHEMA pgss_consumer_move")
			s.exec("CREATE OR REPLACE VIEW pgss_consumer_move.pgss_consumer_saved WITH (security_barrier=true) AS SELECT s.*,17::integer AS marker FROM pgss_part.pg_stat_statements(true) s")
			s.exec("ALTER VIEW pgss_consumer_move.pgss_consumer_saved OWNER TO pgss_consumer_owner")
			s.exec("REVOKE SELECT ON pgss_consumer_move.pgss_consumer_saved FROM PUBLIC")
			s.exec("GRANT SELECT ON pgss_consumer_move.pgss_consumer_saved TO pgss_consumer_reader")
			s.both(fixture.Exec("COMMENT ON VIEW pgss_consumer_move.pgss_consumer_saved IS 'retained ordinary consumer'", 0))
			// This is source-owned unrelated data at the freed script label. Cleanup
			// may retire only a destination's freshly scripted views, never this table.
			s.exec("CREATE TABLE pgss_part.pg_stat_statements(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pg_stat_statements VALUES(73)", 1))
			check := func(run func(fixture.Step)) {
				names := append(append([]string(nil), pgssNames...), "marker")
				types := append(append([]string(nil), pgssTypes...), "INT4")
				run(fixture.Select("SELECT * FROM pgss_consumer_move.pgss_consumer_saved WHERE false", names, types, nil))
				run(fixture.Select("SELECT marker FROM pgss_consumer_move.pgss_consumer_saved WHERE query='SELECT k FROM pgss_part.pgss_consumer_rows'", []string{"marker"}, []string{"INT4"}, [][]any{{int64(17)}}))
				run(fixture.Select("SELECT count(*) AS n FROM pgss_part.pgss_consumer_dependent WHERE query='SELECT k FROM pgss_part.pgss_consumer_rows'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				run(fixture.Select("SELECT k FROM pgss_part.pg_stat_statements", []string{"k"}, []string{"INT4"}, [][]any{{int64(73)}}))
				run(fixture.Select("SELECT r.rolname::text AS owner,c.reloptions::text AS options,obj_description(c.oid,'pg_class') AS comment,has_table_privilege('pgss_consumer_reader',c.oid,'SELECT') AS readable FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='pgss_consumer_move' AND c.relname='pgss_consumer_saved'", []string{"owner", "options", "comment", "readable"}, []string{"TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{"pgss_consumer_owner", "{security_barrier=true}", "retained ordinary consumer", true}}))
				run(fixture.Select("SELECT count(*) AS n FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace,aclexplode(c.relacl) a WHERE n.nspname='pgss_consumer_move' AND c.relname='pgss_consumer_saved' AND a.grantee=0 AND a.privilege_type='SELECT'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				pgssConsumerTypeRelationships(run, "pgss_consumer_move", "pgss_consumer_saved")
				run(fixture.Select("SELECT e.extname::text AS parent FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='pg_class'::regclass AND d.objid='pgss_consumer_move.pgss_consumer_saved'::regclass AND d.refclassid='pg_extension'::regclass AND d.deptype='e'", []string{"parent"}, []string{"TEXT"}, [][]any{{"pg_stat_statements"}}))
				pgssConsumerFunctionDependency(run, "pgss_consumer_move", "pgss_consumer_saved", "pgss_part")
			}
			pgssConsumerMeasured(s)
			pgssConsumerAssertBoth(s, check)
			pgssConsumerRoundtrip(s, check, []string{"CREATE ROLE pgss_consumer_owner", "CREATE ROLE pgss_consumer_reader"})
		})
	})
	for _, mode := range []string{"removed_info", "detached_main", "foreign_info"} {
		t.Run(mode, func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				pgssConsumerOriginal(s)
				var check func(func(fixture.Step))
				switch mode {
				case "removed_info":
					s.exec("ALTER EXTENSION pg_stat_statements DROP VIEW pgss_part.pg_stat_statements_info")
					s.exec("DROP VIEW pgss_part.pg_stat_statements_info")
					check = func(run func(fixture.Step)) {
						run(fixture.Select("SELECT to_regclass('pgss_part.pg_stat_statements_info') IS NULL AS absent", []string{"absent"}, []string{"BOOL"}, [][]any{{true}}))
						run(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements_info() WHERE false", []string{"dealloc", "stats_reset"}, []string{"INT8", "TIMESTAMPTZ"}, nil))
						run(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements WHERE false", pgssNames, pgssTypes, nil))
					}
				case "detached_main":
					s.exec("ALTER EXTENSION pg_stat_statements DROP VIEW pgss_part.pg_stat_statements")
					s.exec("ALTER VIEW pgss_part.pg_stat_statements RENAME TO pgss_consumer_detached")
					check = func(run func(fixture.Step)) {
						run(fixture.Select("SELECT * FROM pgss_part.pgss_consumer_detached WHERE false", pgssNames, pgssTypes, nil))
						run(fixture.Select("SELECT count(*) AS n FROM pgss_part.pgss_consumer_detached WHERE query='SELECT k FROM pgss_part.pgss_consumer_rows'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
						run(fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_class'::regclass AND objid='pgss_part.pgss_consumer_detached'::regclass AND refclassid='pg_extension'::regclass AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
						run(fixture.Select("SELECT to_regclass('pgss_part.pg_stat_statements') IS NULL AS absent", []string{"absent"}, []string{"BOOL"}, [][]any{{true}}))
						pgssConsumerFunctionDependency(run, "pgss_part", "pgss_consumer_detached", "pgss_part")
					}
				case "foreign_info":
					if s.b.PG {
						defer s.observer.ExecContext(s.p.Ctx, "DROP EXTENSION IF EXISTS hstore CASCADE")
					}
					s.exec("CREATE EXTENSION hstore SCHEMA pgss_part")
					s.exec("ALTER EXTENSION pg_stat_statements DROP VIEW pgss_part.pg_stat_statements_info")
					s.exec("ALTER EXTENSION hstore ADD VIEW pgss_part.pg_stat_statements_info")
					check = func(run func(fixture.Step)) {
						run(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements_info WHERE false", []string{"dealloc", "stats_reset"}, []string{"INT8", "TIMESTAMPTZ"}, nil))
						run(fixture.Select("SELECT e.extname::text AS parent FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='pg_class'::regclass AND d.objid='pgss_part.pg_stat_statements_info'::regclass AND d.refclassid='pg_extension'::regclass AND d.deptype='e'", []string{"parent"}, []string{"TEXT"}, [][]any{{"hstore"}}))
						run(fixture.Select("SELECT count(*) AS n FROM pgss_part.pg_stat_statements_info", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
				}
				pgssConsumerMeasured(s)
				pgssConsumerAssertBoth(s, check)
				pgssConsumerRoundtrip(s, check, nil)
			})
		})
	}
	t.Run("detached_old_cohort", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "pgss_consumer_next")
		pgssCase(t, true, func(s *pgssScenario) {
			pgssConsumerOriginal(s)
			for _, object := range []string{"VIEW pgss_part.pg_stat_statements", "VIEW pgss_part.pg_stat_statements_info", "FUNCTION pgss_part.pg_stat_statements(boolean)", "FUNCTION pgss_part.pg_stat_statements_info()"} {
				s.exec("ALTER EXTENSION pg_stat_statements DROP " + object)
			}
			s.exec("DROP EXTENSION pg_stat_statements")
			s.exec("CREATE SCHEMA pgss_consumer_next")
			s.exec("CREATE EXTENSION pg_stat_statements SCHEMA pgss_consumer_next VERSION '1.12'")
			check := func(run func(fixture.Step)) {
				for _, schema := range []string{"pgss_part", "pgss_consumer_next"} {
					run(fixture.Select("SELECT * FROM "+schema+".pg_stat_statements WHERE false", pgssNames, pgssTypes, nil))
					run(fixture.Select("SELECT count(*) AS n FROM "+schema+".pg_stat_statements WHERE query='SELECT k FROM pgss_part.pgss_consumer_rows'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					pgssConsumerFunctionDependency(run, schema, "pg_stat_statements", schema)
				}
				run(fixture.Select("SELECT n.nspname::text AS schema FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='pg_stat_statements'", []string{"schema"}, []string{"TEXT"}, [][]any{{"pgss_consumer_next"}}))
				run(fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_class'::regclass AND objid='pgss_part.pg_stat_statements'::regclass AND refclassid='pg_extension'::regclass AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			pgssConsumerMeasured(s)
			pgssConsumerAssertBoth(s, check)
			pgssConsumerRoundtrip(s, check, nil)
		})
	})
}

func pgssConsumerOriginal(s *pgssScenario) {
	s.t.Helper()
	s.both(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements WHERE false", pgssNames, pgssTypes, nil))
	s.both(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements_info WHERE false", []string{"dealloc", "stats_reset"}, []string{"INT8", "TIMESTAMPTZ"}, nil))
	pgssConsumerTypeRelationships(func(step fixture.Step) { s.both(step) }, "pgss_part", "pg_stat_statements")
	pgssConsumerTypeRelationships(func(step fixture.Step) { s.both(step) }, "pgss_part", "pg_stat_statements_info")
	s.both(fixture.Select("SELECT c.relname::text AS name,c.relowner=e.extowner AS owned,EXISTS(SELECT 1 FROM aclexplode(c.relacl) a WHERE a.grantee=0 AND a.privilege_type='SELECT') AS public_read,d.deptype='e' AS member FROM pg_extension e JOIN pg_depend d ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid AND d.classid='pg_class'::regclass JOIN pg_class c ON c.oid=d.objid WHERE e.extname='pg_stat_statements' AND c.relkind='v' ORDER BY c.relname", []string{"name", "owned", "public_read", "member"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"pg_stat_statements", true, true, true}, {"pg_stat_statements_info", true, true, true}}))
	s.exec("CREATE TABLE pgss_part.pgss_consumer_rows(k integer)")
	s.run(fixture.Exec("INSERT INTO pgss_part.pgss_consumer_rows VALUES(17)", 1))
}
func pgssConsumerMeasured(s *pgssScenario) {
	s.t.Helper()
	s.work(fixture.Select("SELECT k FROM pgss_part.pgss_consumer_rows", []string{"k"}, []string{"INT4"}, [][]any{{int64(17)}}))
}
func pgssConsumerAssertBoth(s *pgssScenario, check func(func(fixture.Step))) {
	s.t.Helper()
	check(func(step fixture.Step) { s.both(step) })
}
func pgssConsumerFunctionDependency(run func(fixture.Step), schema, name, functionSchema string) {
	q := "SELECT DISTINCT pn.nspname::text AS function_schema FROM pg_rewrite r JOIN pg_class c ON c.oid=r.ev_class JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_depend d ON d.classid='pg_rewrite'::regclass AND d.objid=r.oid AND d.refclassid='pg_proc'::regclass JOIN pg_proc p ON p.oid=d.refobjid JOIN pg_namespace pn ON pn.oid=p.pronamespace WHERE n.nspname=$1 AND c.relname=$2 AND p.proname='pg_stat_statements'"
	run(fixture.Select(q, []string{"function_schema"}, []string{"TEXT"}, [][]any{{functionSchema}}, schema, name))
}

func pgssConsumerTypeRelationships(run func(fixture.Step), schema, name string) {
	q := "SELECT t.typrelid=c.oid AS relation_link,t.typowner=c.relowner AS row_owned,a.typelem=t.oid AS element_link,a.typowner=t.typowner AS array_owned FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE n.nspname=$1 AND c.relname=$2"
	run(fixture.Select(q, []string{"relation_link", "row_owned", "element_link", "array_owned"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, schema, name))
}

func pgssConsumerRoundtrip(s *pgssScenario, check func(func(fixture.Step)), roles []string) {
	s.t.Helper()
	if s.b.PG {
		return
	}
	s.exec("CREATE TABLE pgss_part.pgss_consumer_atomic(k integer)")
	s.run(fixture.Exec("INSERT INTO pgss_part.pgss_consumer_atomic VALUES(31)", 1))
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
		s.t.Fatal(err)
	}
	s.run(fixture.Exec("UPDATE pgss_part.pgss_consumer_atomic SET k=91", 1))
	err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump.String()+"\nCREATE TABLE pgss_part.pgss_consumer_late(k integer); INSERT INTO pgss_part.pgss_consumer_late VALUES(1/0);"))
	want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
	fixture.AssertDiagnostic(s.t, err, &want)
	s.both(fixture.Select("SELECT k FROM pgss_part.pgss_consumer_atomic", []string{"k"}, []string{"INT4"}, [][]any{{int64(91)}}))
	pgssConsumerAssertBoth(s, check)
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	pgssConsumerMeasured(s)
	pgssConsumerAssertBoth(s, check)
	if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
		s.t.Fatal(err)
	}
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	pgssConsumerMeasured(s)
	pgssConsumerAssertBoth(s, check)
	connector, err := stuntgres.NewDriver().OpenConnector("pgss_consumer_fresh")
	if err != nil {
		s.t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	defer raw.Close()
	pool, recorder := fixture.RecordedNativePool(s.t, connector)
	c, err := pool.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	defer c.Close()
	run := func(step fixture.Step) { fixture.RunStep(s.t, s.p.Ctx, c, recorder, step) }
	run(fixture.Exec("SET pg_stat_statements.track = 'none'", 0))
	for _, role := range roles {
		run(fixture.Exec(role, 0))
	}
	if err := stuntgres.Restore(s.p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	run(fixture.Exec("SET pg_stat_statements.track = 'top'", 0))
	run(fixture.Select("SELECT k FROM pgss_part.pgss_consumer_rows", []string{"k"}, []string{"INT4"}, [][]any{{int64(17)}}))
	run(fixture.Exec("SET pg_stat_statements.track = 'none'", 0))
	check(func(step fixture.Step) { run(step); run(fixture.Prepared(step)) })
}
