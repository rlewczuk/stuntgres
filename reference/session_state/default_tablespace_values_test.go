package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

var defaultTablespaceSetting = clientSetting{"default_tablespace", "string", "", "", "pg_global", "pg_global", "Sets the default tablespace to create tables and indexes in."}

func (s *physicalCase) tablespaceReads(value, source string) {
	s.clientReads(defaultTablespaceSetting, value, false)
	s.clientReads(defaultTablespaceSetting, value, true)
	want := []any{"default_tablespace", value, nil, "Client Connection Defaults / Statement Behavior", "Sets the default tablespace to create tables and indexes in.", "An empty string means use the database's default tablespace.", "user", "string", source, nil, nil, nil, "", "", nil, nil, false}
	step := fixture.Select("SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name='default_tablespace'", physicalColumns, physicalTypes, [][]any{want})
	s.run(step)
	step.Prepared = true
	s.run(step)
}

func defaultTablespaceValues(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.tablespaceReads("", "default")
		s.run(fixture.Exec("SET default_tablespace=''", 0))
		s.run(clientText("SELECT set_config('default_tablespace','',false)", "set_config", ""))
		s.run(fixture.Exec("CREATE TABLE ts_sentinel(v INT)", 0))
		s.run(fixture.Exec("INSERT INTO ts_sentinel VALUES(73)", 1))
		s.run(fixture.Exec("CREATE INDEX ts_sentinel_idx ON ts_sentinel(v)", 0))
		s.run(fixture.Select("SELECT relname,reltablespace::bigint FROM pg_class WHERE relname IN ('ts_sentinel','ts_sentinel_idx') ORDER BY relname", []string{"relname", "reltablespace"}, []string{"NAME", "INT8"}, [][]any{{"ts_sentinel", int64(0)}, {"ts_sentinel_idx", int64(0)}}))
		s.run(fixture.Select("SELECT tablespace FROM pg_tables WHERE tablename='ts_sentinel'", []string{"tablespace"}, []string{"NAME"}, [][]any{{nil}}))
		s.run(fixture.Select("SELECT tablespace FROM pg_indexes WHERE indexname='ts_sentinel_idx'", []string{"tablespace"}, []string{"NAME"}, [][]any{{nil}}))
		const heldSQL = "CREATE INDEX ts_held_idx ON ts_sentinel(v)"
		held, err := s.conn.PrepareContext(s.p.Ctx, heldSQL)
		if err != nil { s.t.Fatal(err) }
		defer held.Close()
		const readSQL = "SELECT current_setting('default_tablespace')"
		reader, err := s.conn.PrepareContext(s.p.Ctx, readSQL)
		if err != nil { s.t.Fatal(err) }
		defer reader.Close()
		s.run(fixture.Exec("SET default_tablespace='pg_global'", 0))
		s.tablespaceReads("pg_global", "session")
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: reader, SQL: readSQL}, s.recorder, clientText(readSQL, "current_setting", "pg_global"))
		s.run(physicalError("CREATE TABLE ts_failed(v INT)", defaultTablespaceGlobal(), false, false))
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: heldSQL}, s.recorder, physicalError(heldSQL, defaultTablespaceGlobal(), false, false))
		s.tablespaceAbsent("pg_class", "relname", "ts_failed")
		s.tablespaceAbsent("pg_type", "typname", "ts_failed")
		s.tablespaceAbsent("pg_class", "relname", "ts_held_idx")
		const resetSQL = "CREATE TABLE ts_after_reset(v INT)"
		after, err := s.conn.PrepareContext(s.p.Ctx, resetSQL)
		if err != nil { s.t.Fatal(err) }
		defer after.Close()
		s.run(fixture.Exec("RESET default_tablespace", 0))
		s.tablespaceReads("", "default")
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: after, SQL: resetSQL}, s.recorder, fixture.Exec(resetSQL, 0))
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: heldSQL}, s.recorder, fixture.Exec(heldSQL, 0))
		for i, reset := range []string{"SET default_tablespace=DEFAULT", "RESET ALL"} {
			s.run(fixture.Exec("SET default_tablespace='pg_global'", 0))
			s.run(fixture.Exec(reset, 0))
			s.tablespaceReads("", "default")
			s.run(fixture.Exec(fmt.Sprintf("CREATE TABLE ts_reset_%d(v INT)", i), 0))
		}
		s.run(fixture.Exec("SET default_tablespace='pg_default'", 0))
		s.tablespaceReads("pg_default", "session")
		s.run(fixture.Exec("CREATE TABLE ts_default(v INT)", 0))
		s.run(fixture.Select("SELECT reltablespace::bigint FROM pg_class WHERE relname='ts_default'", []string{"reltablespace"}, []string{"INT8"}, [][]any{{int64(0)}}))
		s.tablespaceSentinel()
	})
}

func TestReferenceDefaultTablespaceValuesAndMetadata(t *testing.T) { defaultTablespaceValues(t) }

func defaultTablespaceAuthority(t *testing.T) {
	physicalCases(t, []string{"ts_setting_login"}, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE ROLE ts_setting_login LOGIN PASSWORD 'role-core-fixture'", 0))
		s.run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO ts_setting_login", 0))
		ordinary := s.login("ts_setting_login")
		ordinary.run(fixture.Exec("CREATE TABLE ts_sentinel(v INT)", 0))
		ordinary.run(fixture.Exec("INSERT INTO ts_sentinel VALUES(73)", 1))
		const query = "CREATE INDEX ts_authority_idx ON ts_sentinel(v)"
		held, err := ordinary.conn.PrepareContext(s.p.Ctx, query)
		if err != nil { s.t.Fatal(err) }
		defer held.Close()
		ordinary.run(fixture.Exec("SET default_tablespace='pg_global'", 0))
		ordinary.run(clientText("SELECT set_config('default_tablespace','pg_global',false)", "set_config", "pg_global"))
		denied := physicalDiagnostic("42501", "permission denied for tablespace pg_global")
		for _, q := range []string{"CREATE TABLE ts_denied(v INT)", "ALTER TABLE ts_sentinel ADD CONSTRAINT ts_denied_key UNIQUE(v)"} {
			ordinary.run(physicalError(q, denied, false, false))
		}
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: held, SQL: query}, ordinary.recorder, physicalError(query, denied, false, false))
		s.run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_global TO ts_setting_login", 0))
		for _, q := range []string{"CREATE TABLE ts_denied(v INT)", "ALTER TABLE ts_sentinel ADD CONSTRAINT ts_denied_key UNIQUE(v)"} {
			ordinary.run(physicalError(q, defaultTablespaceGlobal(), true, false))
		}
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: held, SQL: query}, ordinary.recorder, physicalError(query, defaultTablespaceGlobal(), false, false))
		s.run(fixture.Exec("REVOKE CREATE ON TABLESPACE pg_global FROM ts_setting_login", 0))
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: held, SQL: query}, ordinary.recorder, physicalError(query, denied, false, false))
		ordinary.run(fixture.Exec("SET default_tablespace='pg_default'", 0))
		ordinary.run(fixture.Exec("CREATE TABLE ts_allowed(v INT)", 0))
		ordinary.tablespaceAbsent("pg_class", "relname", "ts_denied")
		ordinary.tablespaceAbsent("pg_class", "relname", "ts_authority_idx")
		ordinary.tablespaceAbsent("pg_constraint", "conname", "ts_denied_key")
		ordinary.tablespaceSentinel()
		// Revoke schema grants before the shared role-cleanup phase.
		s.run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM ts_setting_login", 0))
	})
}

func TestReferenceDefaultTablespaceAuthorityAndDiagnostics(t *testing.T) {
	t.Run("authority", defaultTablespaceAuthority)
	t.Run("invalid-values", func(t *testing.T) {
		physicalCases(t, nil, func(s *physicalCase) {
			s.run(fixture.Exec("CREATE TABLE ts_sentinel(v INT)", 0))
			s.run(fixture.Exec("INSERT INTO ts_sentinel VALUES(73)", 1))
			s.run(fixture.Exec("SET default_tablespace='pg_global'", 0))
			for _, input := range []string{"missing", "PG_DEFAULT", " pg_default "} {
				d := clientInvalid("default_tablespace", input, "Tablespace \""+input+"\" does not exist.", "")
				for _, prepared := range []bool{false, true} {
					s.run(physicalError("SET default_tablespace="+physicalLiteral(input), d, prepared, false))
					s.run(physicalError("SELECT set_config('default_tablespace',"+physicalLiteral(input)+",false)", d, prepared, true))
					s.tablespaceReads("pg_global", "session")
				}
			}
			s.tablespaceSentinel()
		})
	})
}
