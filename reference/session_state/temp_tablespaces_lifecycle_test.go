package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func tempTablespacesLifecycle(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		pool := s.p.Memory
		if s.pg { pool = s.p.Postgres }
		peer, err := pool.Conn(s.p.Ctx)
		if err != nil { s.t.Fatal(err) }
		defer peer.Close()
		other := &physicalCase{s.t, s.p, peer, s.pg, s.recorder}
		s.run(fixture.Exec("CREATE TEMP TABLE tt_sentinel(v INT)", 0))
		s.run(fixture.Exec("INSERT INTO tt_sentinel VALUES(73)", 1))
		const heldSQL = "CREATE TEMP TABLE tt_lifecycle_held(v INT)"
		held := s.tempTablespacesHeld(heldSQL)
		defer held.Close()
		s.run(fixture.Exec("SET temp_tablespaces='pg_global'", 0))
		s.tempTablespacesReads("pg_global", "session")
		other.tempTablespacesReads("", "default")
		other.run(fixture.Exec("CREATE TEMP TABLE tt_peer(v INT)", 0))
		s.run(physicalError("CREATE TEMP TABLE tt_isolated(v INT)", defaultTablespaceGlobal(), false, false))
		s.run(fixture.Exec("RESET temp_tablespaces", 0))
		other.tempTablespacesReads("", "default")
		for i, setting := range []string{"SET", "SET LOCAL"} {
			for j, end := range []string{"COMMIT", "ROLLBACK"} {
				s.run(fixture.Exec("BEGIN", 0))
				s.run(fixture.Exec("SAVEPOINT tt_before", 0))
				s.run(fixture.Exec(setting+" temp_tablespaces='pg_global'", 0))
				s.run(fixture.Exec("SAVEPOINT tt_failure", 0))
				s.tempTablespacesExecute(held, physicalError(heldSQL, defaultTablespaceGlobal(), false, false))
				s.run(physicalError("SELECT current_setting('temp_tablespaces')", physicalDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"), false, true))
				s.run(fixture.Exec("ROLLBACK TO tt_failure", 0))
				s.tempTablespacesReads("pg_global", "session")
				s.tempTablespacesAbsent("tt_lifecycle_held")
				s.run(fixture.Exec("ROLLBACK TO tt_before", 0))
				s.tempTablespacesReads("", "default")
				s.run(fixture.Exec("SAVEPOINT tt_nested", 0))
				s.run(fixture.Exec("SET LOCAL temp_tablespaces='pg_global'", 0))
				s.run(fixture.Exec("RELEASE tt_nested", 0))
				s.tempTablespacesReads("pg_global", "session")
				s.run(fixture.Exec("ROLLBACK TO tt_before", 0))
				s.run(fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE tt_restored_%d_%d(v INT)", i, j), 0))
				s.run(fixture.Exec(setting+" temp_tablespaces='pg_global'", 0))
				s.run(fixture.Exec(end, 0))
				if setting == "SET" && end == "COMMIT" {
					s.tempTablespacesReads("pg_global", "session")
					s.run(physicalError("CREATE TEMP TABLE tt_commit_blocked(v INT)", defaultTablespaceGlobal(), false, false))
					s.run(fixture.Exec("RESET temp_tablespaces", 0))
				} else {
					s.tempTablespacesReads("", "default")
					s.run(fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE tt_end_%d_%d(v INT)", i, j), 0))
				}
			}
		}
		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("SET temp_tablespaces='pg_global'", 0))
		s.run(fixture.Exec("SET LOCAL temp_tablespaces=''", 0))
		s.tempTablespacesReads(`""`, "session")
		s.run(fixture.Exec("CREATE TEMP TABLE tt_local_override(v INT)", 0))
		s.run(fixture.Exec("COMMIT", 0))
		s.tempTablespacesReads("pg_global", "session")
		s.tempTablespacesExecute(held, physicalError(heldSQL, defaultTablespaceGlobal(), false, false))
		s.run(fixture.Exec("RESET temp_tablespaces", 0))
		s.tempTablespacesExecute(held, fixture.Exec(heldSQL, 0))
		s.tempTablespacesSentinel()
	})
}

func TestReferenceTempTablespacesLifecycle(t *testing.T) { tempTablespacesLifecycle(t) }

func TestReferenceTempTablespacesNestedSettings(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE FUNCTION tt_nested() RETURNS text LANGUAGE plpgsql SET temp_tablespaces='' AS $$ BEGIN CREATE TEMP TABLE tt_nested_ok(v INT); PERFORM set_config('temp_tablespaces','pg_global',true); BEGIN CREATE TEMP TABLE tt_nested_failed(v INT); EXCEPTION WHEN SQLSTATE '22023' THEN RETURN current_setting('temp_tablespaces'); END; RETURN 'unexpected success'; END $$", 0))
		s.run(fixture.Exec("CREATE FUNCTION tt_scoped() RETURNS text LANGUAGE plpgsql SET temp_tablespaces='' AS $$ BEGIN CREATE TEMP TABLE tt_scoped_ok(v INT); RETURN current_setting('temp_tablespaces'); END $$", 0))
		s.run(fixture.Exec("SET temp_tablespaces='pg_global'", 0))
		s.run(fixture.Select("SELECT tt_scoped()", []string{"tt_scoped"}, []string{"TEXT"}, [][]any{{`""`}}))
		s.tempTablespacesReads("pg_global", "session")
		s.run(physicalError("CREATE TEMP TABLE tt_scoped_outer(v INT)", defaultTablespaceGlobal(), false, false))
		s.run(fixture.Select("SELECT tt_nested()", []string{"tt_nested"}, []string{"TEXT"}, [][]any{{"pg_global"}}))
		s.run(fixture.Select("SELECT relkind::text FROM pg_class WHERE oid='tt_nested_ok'::regclass", []string{"relkind"}, []string{"TEXT"}, [][]any{{"r"}}))
		s.tempTablespacesAbsent("tt_nested_failed")
		s.tempTablespacesReads("pg_global", "session")
		s.run(physicalError("CREATE TEMP TABLE tt_outer_blocked(v INT)", defaultTablespaceGlobal(), false, false))
		s.run(fixture.Exec("SET temp_tablespaces=''; CREATE TEMP TABLE tt_batch_ok(v INT); RESET temp_tablespaces", 0))
		s.run(fixture.Select("SELECT relkind::text FROM pg_class WHERE oid='tt_batch_ok'::regclass", []string{"relkind"}, []string{"TEXT"}, [][]any{{"r"}}))
		s.tempTablespacesReads("", "default")
	})
}

func TestReferenceTempTablespacesFeatureSmoke(t *testing.T) {
	t.Run("values-held-metadata", tempTablespacesValues)
	t.Run("isolation-savepoint", tempTablespacesLifecycle)
	t.Run("retained-authority", tempTablespacesAuthority)
	t.Run("list-selection", tempTablespacesListSelection)
	t.Run("same-value-assignment", tempTablespacesSameValueAssignment)
	t.Run("index-definition-phases", func(t *testing.T) {
		tempTablespacesIndexDefinitionPhases(t, []bool{false})
	})
	t.Run("physical-boundary", tempTablespacesPhysical)
}
