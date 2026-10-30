package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

var tempTablespacesSetting = clientSetting{"temp_tablespaces", "string", "", "", "pg_global", "pg_global", "Sets the tablespace(s) to use for temporary tables and sort files."}

func (s *physicalCase) tempTablespacesReads(value, source string) {
	s.t.Helper()
	for _, prepared := range []bool{false, true} {
		s.clientReads(tempTablespacesSetting, value, prepared)
		want := []any{"temp_tablespaces", value, nil, "Client Connection Defaults / Statement Behavior", "Sets the tablespace(s) to use for temporary tables and sort files.", "An empty string means use the database's default tablespace.", "user", "string", source, nil, nil, nil, "", "", nil, nil, false}
		step := fixture.Select("SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name='temp_tablespaces'", physicalColumns, physicalTypes, [][]any{want})
		step.Prepared = prepared
		s.run(step)
	}
}

func (s *physicalCase) tempTablespacesSentinel() {
	s.t.Helper()
	s.run(fixture.Select("SELECT v FROM tt_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
}

func (s *physicalCase) tempTablespacesAbsent(names ...string) {
	s.t.Helper()
	for _, name := range names {
		for _, pair := range [][2]string{{"pg_class", "relname"}, {"pg_type", "typname"}, {"pg_constraint", "conname"}} {
			s.tablespaceAbsent(pair[0], pair[1], name)
		}
	}
}

func (s *physicalCase) tempTablespacesHeld(query string) *sql.Stmt {
	s.t.Helper()
	stmt, err := s.conn.PrepareContext(s.p.Ctx, query)
	if err != nil { s.t.Fatal(err) }
	return stmt
}

func (s *physicalCase) tempTablespacesExecute(stmt *sql.Stmt, step fixture.Step) {
	s.t.Helper()
	fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: stmt, SQL: step.Query}, s.recorder, step)
}

func tempTablespacesValues(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.tempTablespacesReads("", "default")
		s.run(fixture.Exec("CREATE TEMP TABLE tt_sentinel(v INT)", 0))
		s.run(fixture.Exec("INSERT INTO tt_sentinel VALUES(73)", 1))
		s.run(fixture.Exec("CREATE INDEX tt_sentinel_idx ON tt_sentinel(v)", 0))
		s.run(fixture.Select("SELECT relname,reltablespace::bigint FROM pg_class WHERE relnamespace=pg_my_temp_schema() AND relname IN ('tt_sentinel','tt_sentinel_idx') ORDER BY relname", []string{"relname", "reltablespace"}, []string{"NAME", "INT8"}, [][]any{{"tt_sentinel", int64(0)}, {"tt_sentinel_idx", int64(0)}}))
		s.run(fixture.Select("SELECT tablespace FROM pg_tables WHERE tablename='tt_sentinel'", []string{"tablespace"}, []string{"NAME"}, [][]any{{nil}}))
		s.run(fixture.Select("SELECT tablespace FROM pg_indexes WHERE indexname='tt_sentinel_idx'", []string{"tablespace"}, []string{"NAME"}, [][]any{{nil}}))
		const tableSQL = "CREATE TEMP TABLE tt_held(v INT)"
		const indexSQL = "CREATE INDEX tt_held_idx ON tt_sentinel(v)"
		const readSQL = "SELECT current_setting('temp_tablespaces')"
		table := s.tempTablespacesHeld(tableSQL)
		index := s.tempTablespacesHeld(indexSQL)
		reader := s.tempTablespacesHeld(readSQL)
		s.tempTablespacesAbsent("tt_held", "tt_held_idx")
		defer table.Close()
		defer index.Close()
		defer reader.Close()
		s.run(fixture.Exec("SET temp_tablespaces='pg_global'", 0))
		s.run(clientText("SELECT set_config('temp_tablespaces','pg_global',false)", "set_config", "pg_global"))
		s.tempTablespacesReads("pg_global", "session")
		s.tempTablespacesExecute(reader, clientText(readSQL, "current_setting", "pg_global"))
		for _, q := range []string{"CREATE TEMP TABLE tt_failed(v SERIAL PRIMARY KEY)", "CREATE INDEX tt_direct_idx ON tt_sentinel(v)"} {
			s.run(physicalError(q, defaultTablespaceGlobal(), false, false))
		}
		s.tempTablespacesExecute(table, physicalError(tableSQL, defaultTablespaceGlobal(), false, false))
		s.tempTablespacesExecute(index, physicalError(indexSQL, defaultTablespaceGlobal(), false, false))
		s.tempTablespacesAbsent("tt_failed", "tt_failed_v_seq", "tt_failed_pkey", "tt_direct_idx", "tt_held", "tt_held_idx")
		s.tempTablespacesSentinel()
		s.run(fixture.Exec("CREATE TABLE tt_permanent(v INT)", 0))
		s.run(fixture.Exec("CREATE INDEX tt_permanent_idx ON tt_permanent(v)", 0))
		s.run(fixture.Exec("RESET temp_tablespaces", 0))
		s.tempTablespacesReads("", "default")
		s.tempTablespacesExecute(reader, clientText(readSQL, "current_setting", ""))
		s.tempTablespacesExecute(table, fixture.Exec(tableSQL, 0))
		s.tempTablespacesExecute(index, fixture.Exec(indexSQL, 0))
		s.run(fixture.Exec("SET default_tablespace='pg_global'", 0))
		s.run(fixture.Exec("CREATE TEMP TABLE tt_independent(v INT)", 0))
		s.run(fixture.Exec("RESET default_tablespace", 0))
		for i, reset := range []string{"SET temp_tablespaces=DEFAULT", "RESET ALL"} {
			s.run(fixture.Exec("SET temp_tablespaces='pg_global'", 0))
			s.run(fixture.Exec(reset, 0))
			s.tempTablespacesReads("", "default")
			s.run(fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE tt_reset_%d(v INT)", i), 0))
		}
		s.tempTablespacesSentinel()
	})
}

func TestReferenceTempTablespacesValuesAndEffects(t *testing.T) { tempTablespacesValues(t) }

func tempTablespacesPhysical(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE tt_sentinel(v INT)", 0))
		s.run(fixture.Exec("INSERT INTO tt_sentinel VALUES(73)", 1))
		s.run(fixture.Exec("SET temp_tablespaces='pg_global'", 0))
		const directory = "/tmp/stuntgres_temp_tablespaces_contract_absent/location"
		d := physicalDiagnostic("0A000", "CREATE TABLESPACE physical locations are unsupported in embedded mode")
		if s.pg { d = physicalDiagnostic("58P01", "directory \""+directory+"\" does not exist") }
		s.run(physicalError("CREATE TABLESPACE tt_physical LOCATION '"+directory+"'", d, false, false))
		s.tablespaceAbsent("pg_tablespace", "spcname", "tt_physical")
		s.run(physicalError("SET temp_tablespaces='tt_physical'", physicalDiagnostic("42704", `tablespace "tt_physical" does not exist`), false, false))
		s.tempTablespacesReads("pg_global", "session")
		s.run(physicalError("CREATE TEMP TABLE tt_physical_failed(v INT)", defaultTablespaceGlobal(), false, false))
		s.tempTablespacesAbsent("tt_physical_failed")
		s.tempTablespacesSentinel()
	})
}

func TestReferenceTempTablespacesPhysicalBoundary(t *testing.T) { tempTablespacesPhysical(t) }
