package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceClientSettingsSimulationSmoke(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE client_smoke_parent(v integer PRIMARY KEY)", 0))
		s.run(fixture.Exec("CREATE TABLE client_smoke_child(v integer CONSTRAINT client_smoke_fk REFERENCES client_smoke_parent(v))", 0))
		s.run(fixture.Exec("INSERT INTO client_smoke_parent VALUES(1),(2),(3)", 3))
		s.run(fixture.Exec("INSERT INTO client_smoke_child VALUES(1)", 1))
		for _, prepared := range []bool{false, true} {
			step := fixture.Exec("SET gin_pending_list_limit='8MB'", 0)
			step.Prepared = prepared
			s.run(step)
			for _, read := range []fixture.Step{
				clientText("SHOW gin_pending_list_limit", "gin_pending_list_limit", "8MB"),
				clientText("SELECT current_setting('gin_pending_list_limit')", "current_setting", "8MB"),
				fixture.Select("SELECT setting FROM pg_settings WHERE name='gin_pending_list_limit'", []string{"setting"}, []string{"TEXT"}, [][]any{{"8192"}}),
			} {
				read.Prepared = prepared
				s.run(read)
			}
			for _, query := range []string{"SET session_replication_role='replica'", "SET check_function_bodies=off", "SET default_toast_compression='lz4'", "SET gin_fuzzy_search_limit=1", "SET default_tablespace='pg_default'", "SET temp_tablespaces='pg_default'", "SET xmlbinary='hex'", "SET XML OPTION DOCUMENT"} {
				step := fixture.Exec(query, 0)
				step.Prepared = prepared
				s.run(step)
			}
			s.run(clientText("SHOW session_replication_role", "session_replication_role", "replica"))
			s.run(clientText("SHOW check_function_bodies", "check_function_bodies", "off"))
			s.run(clientText("SHOW xmloption", "xmloption", "document"))
			const invalidInsert = "INSERT INTO client_smoke_child VALUES(99)"
			const invalidRoutine = "CREATE FUNCTION client_smoke_invalid() RETURNS integer LANGUAGE SQL AS $$SELECT 'x'::text$$"
			if s.pg {
				// PostgreSQL really suppresses FK triggers and quoted-body validation.
				// Explicitly assert that difference rather than weaken native safety.
				s.run(fixture.Exec(invalidInsert, 1))
				s.run(fixture.Exec("DELETE FROM client_smoke_child WHERE v=99", 1))
				s.run(fixture.Exec(invalidRoutine, 0))
				s.run(fixture.Exec("DROP FUNCTION client_smoke_invalid()", 0))
			} else {
				s.run(physicalError(invalidInsert, fixture.ForeignKeyChild("client_smoke_child", "client_smoke_fk", `Key (v)=(99) is not present in table "client_smoke_parent".`), prepared, false))
				d := physicalDiagnostic("42P13", "return type mismatch in function declared to return integer")
				d.Detail, d.Context = "Actual return type is text.", `SQL function "client_smoke_invalid"`
				s.run(physicalError(invalidRoutine, d, prepared, false))
				s.run(fixture.Select("SELECT proname FROM pg_proc WHERE proname='client_smoke_invalid'", []string{"proname"}, []string{"NAME"}, nil))
			}
			for _, name := range []string{"dynamic_library_path", "extension_control_path", "local_preload_libraries", "session_preload_libraries"} {
				input := `../absent_client_module,"unclosed`
				step := clientText("SELECT set_config("+physicalLiteral(name)+","+physicalLiteral(input)+",false)", "set_config", input)
				step.Prepared = prepared
				s.run(step)
			}
			s.run(fixture.Select("SELECT v FROM client_smoke_parent ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
			s.run(fixture.Select("SELECT v FROM client_smoke_child ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			s.run(physicalError("SET shared_preload_libraries='absent_client_module'", physicalRestart("shared_preload_libraries"), prepared, false))
			s.run(fixture.Exec("SET session_replication_role='origin'", 0))
			s.run(fixture.Exec("SET check_function_bodies=on", 0))
		}
		s.run(fixture.Exec("CREATE FUNCTION client_smoke_valid() RETURNS integer LANGUAGE SQL AS $$SELECT 8$$", 0))
		s.run(fixture.Select("SELECT client_smoke_valid()", []string{"client_smoke_valid"}, []string{"INT4"}, [][]any{{int64(8)}}))
	})
}
