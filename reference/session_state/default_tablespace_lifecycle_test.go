package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDefaultTablespaceLifecycle(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		pool := s.p.Memory
		if s.pg { pool = s.p.Postgres }
		peer, err := pool.Conn(s.p.Ctx)
		if err != nil { s.t.Fatal(err) }
		defer peer.Close()
		other := &physicalCase{s.t, s.p, peer, s.pg, s.recorder}
		const heldSQL = "CREATE TABLE ts_transaction_held(v INT)"
		held, err := s.conn.PrepareContext(s.p.Ctx, heldSQL)
		if err != nil { s.t.Fatal(err) }
		defer held.Close()
		s.run(fixture.Exec("SET default_tablespace='pg_global'", 0))
		s.tablespaceReads("pg_global", "session")
		other.tablespaceReads("", "default")
		other.run(fixture.Exec("CREATE TABLE ts_peer(v INT)", 0))
		s.run(physicalError("CREATE TABLE ts_isolated(v INT)", defaultTablespaceGlobal(), false, false))
		s.run(fixture.Exec("RESET default_tablespace", 0))
		for i, setting := range []string{"SET", "SET LOCAL"} {
			for j, end := range []string{"COMMIT", "ROLLBACK"} {
				s.run(fixture.Exec("BEGIN", 0))
				s.run(fixture.Exec("SAVEPOINT ts_before", 0))
				s.run(fixture.Exec(setting+" default_tablespace='pg_global'", 0))
				s.run(fixture.Exec("SAVEPOINT ts_failure", 0))
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: heldSQL}, s.recorder, physicalError(heldSQL, defaultTablespaceGlobal(), false, false))
				s.run(physicalError("SELECT current_setting('default_tablespace')", physicalDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"), false, true))
				s.run(fixture.Exec("ROLLBACK TO ts_failure", 0))
				s.tablespaceReads("pg_global", "session")
				s.run(fixture.Exec("ROLLBACK TO ts_before", 0))
				s.tablespaceReads("", "default")
				s.run(fixture.Exec("SAVEPOINT ts_nested", 0))
				s.run(fixture.Exec("SET LOCAL default_tablespace='pg_global'", 0))
				s.run(fixture.Exec("RELEASE ts_nested", 0))
				s.run(fixture.Exec("ROLLBACK TO ts_before", 0))
				s.run(fixture.Exec(fmt.Sprintf("CREATE TABLE ts_rollback_%d_%d(v INT)", i, j), 0))
				s.run(fixture.Exec(setting+" default_tablespace='pg_global'", 0))
				s.run(fixture.Exec(end, 0))
				if setting == "SET" && end == "COMMIT" {
					s.tablespaceReads("pg_global", "session")
					s.run(physicalError("CREATE TABLE ts_commit_blocked(v INT)", defaultTablespaceGlobal(), false, false))
					s.run(fixture.Exec("RESET default_tablespace", 0))
				} else { s.tablespaceReads("", "default") }
			}
		}
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: heldSQL}, s.recorder, fixture.Exec(heldSQL, 0))
	})
}

func TestReferenceDefaultTablespaceNestedSettings(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		// Function entry must refresh the captured image before nested DDL;
		// set_config must refresh it again, and exit restores the caller image.
		s.run(fixture.Exec("CREATE FUNCTION ts_nested() RETURNS text LANGUAGE plpgsql SET default_tablespace='pg_default' AS $$ BEGIN CREATE TABLE ts_nested_ok(v INT); PERFORM set_config('default_tablespace','',true); CREATE TABLE ts_nested_empty(v INT) PARTITION BY RANGE(v); RETURN current_setting('default_tablespace'); END $$", 0))
		s.run(fixture.Exec("SET default_tablespace='pg_global'", 0))
		s.run(fixture.Select("SELECT ts_nested()", []string{"ts_nested"}, []string{"TEXT"}, [][]any{{""}}))
		// A stale pg_default image would reject this partition-root creation.
		s.run(fixture.Select("SELECT relkind::text FROM pg_class WHERE oid='ts_nested_empty'::regclass", []string{"relkind"}, []string{"TEXT"}, [][]any{{"p"}}))
		s.tablespaceReads("pg_global", "session")
		s.run(physicalError("CREATE TABLE ts_outer_blocked(v INT)", defaultTablespaceGlobal(), false, false))
		// The outer image is still global. SET empty in this same batch must
		// refresh creation policy before CREATE; a stale global image fails.
		s.run(fixture.Exec("SET default_tablespace=''; CREATE TABLE ts_batch_ok(v INT); RESET default_tablespace", 0))
		s.run(fixture.Select("SELECT relkind::text FROM pg_class WHERE oid='ts_batch_ok'::regclass", []string{"relkind"}, []string{"TEXT"}, [][]any{{"r"}}))
		s.tablespaceReads("", "default")
	})
}

func defaultTablespacePhysical(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE ts_sentinel(v INT)", 0))
		s.run(fixture.Exec("INSERT INTO ts_sentinel VALUES(73)", 1))
		const directory = "/tmp/stuntgres_tablespace_contract_absent/location"
		query := "CREATE TABLESPACE ts_physical LOCATION '"+directory+"'"
		d := physicalDiagnostic("0A000", "CREATE TABLESPACE physical locations are unsupported in embedded mode")
		if s.pg { d = physicalDiagnostic("58P01", "directory \""+directory+"\" does not exist") }
		s.run(physicalError(query, d, false, false))
		s.tablespaceAbsent("pg_tablespace", "spcname", "ts_physical")
		s.run(physicalError("SET default_tablespace='ts_physical'", clientInvalid("default_tablespace", "ts_physical", "Tablespace \"ts_physical\" does not exist.", ""), false, false))
		s.tablespaceSentinel()
	})
}

func TestReferenceDefaultTablespacePhysicalBoundary(t *testing.T) { defaultTablespacePhysical(t) }

func TestReferenceDefaultTablespaceFeatureSmoke(t *testing.T) {
	t.Run("effect-prepared-metadata", defaultTablespaceValues)
	t.Run("authority", defaultTablespaceAuthority)
	t.Run("temporary-partition", TestReferenceDefaultTablespaceTemporaryAndPartitionDefaults)
	t.Run("invalid", func(t *testing.T) {
		physicalCases(t, nil, func(s *physicalCase) {
			s.run(physicalError("SET default_tablespace='missing'", clientInvalid("default_tablespace", "missing", "Tablespace \"missing\" does not exist.", ""), false, false))
		})
	})
	t.Run("physical", defaultTablespacePhysical)
}
