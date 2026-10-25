package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePhysicalSettingsInventory(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		values := s.inventory(false)
		if got := s.inventory(true); !reflect.DeepEqual(got, values) {
			s.t.Fatal("prepared inventory changed", got, values)
		}
		for _, prepared := range []bool{false, true} {
			s.reads(values, true, prepared)
			for _, def := range physicalSettings {
				for _, query := range []string{"SHOW " + def.name, "SELECT current_setting(" + physicalLiteral(def.name) + ")"} {
					count := int64(1)
					if strings.HasPrefix(query, "SHOW ") {
						count = 0
					}
					step := fixture.Exec(query, count)
					step.Prepared = prepared
					s.run(step)
				}
				query := "SELECT current_setting($1)"
				step := fixture.Select(query, []string{"current_setting"}, []string{"TEXT"}, [][]any{{values[def.name]}}, strings.ToUpper(def.name))
				step.Prepared = prepared
				s.run(step)
			}
			step := fixture.Select("SELECT current_setting(NULL)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{nil}})
			step.Prepared = prepared
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				settingsTextMetadata(t, rows, s.pg, true)
			}
			s.run(step)
			for _, query := range []string{"SHOW physical_unknown", "SELECT current_setting('physical_unknown')"} {
				step := physicalError(query, physicalDiagnostic("42704", `unrecognized configuration parameter "physical_unknown"`), prepared, true)
				if prepared && strings.HasPrefix(query, "SHOW ") {
					step.ErrorStage = "prepare"
				}
				s.run(step)
			}
		}
	})
}

func TestReferencePhysicalSettingsMutations(t *testing.T) {
	// A separate fixture per parameter keeps each matrix within the shared
	// scenario deadline, including real prepared protocol round trips.
	for _, def := range physicalSettings {
		t.Run(def.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				values := s.inventory(false)
				s.run(fixture.Exec("CREATE TABLE physical_mutation_sentinel (id integer, value text)", 0))
				s.run(fixture.Exec("INSERT INTO physical_mutation_sentinel VALUES (1,'kept'),(2,NULL)", 2))
				s.run(fixture.Exec("SET application_name='physical-owner'", 0))
				peer, err := s.connPoolPeer()
				if err != nil {
					s.t.Fatal(err)
				}
				defer peer.conn.Close()
				peer.run(fixture.Exec("SET application_name='physical-peer'", 0))
				for _, prepared := range []bool{false, true} {
					for _, value := range []string{values[def.name], "", "/nonexistent/physical/location", "relative/location", "目录/'; SELECT 1; --"} {
						q := "SET " + def.name + "=" + physicalLiteral(value)
						s.run(physicalError(q, physicalRestart(def.name), prepared, false))
						s.unchanged(values, "physical_mutation_sentinel", "physical-owner")
					}
					for _, q := range []string{
						"SET " + def.name + "=DEFAULT",
						"RESET " + def.name,
						"SET LOCAL " + def.name + "='/another/location'",
						"SET \"" + strings.ToUpper(def.name) + "\"='/another/location'",
					} {
						s.run(physicalError(q, physicalRestart(def.name), prepared, false))
						s.unchanged(values, "physical_mutation_sentinel", "physical-owner")
					}
					for _, name := range []string{def.name, strings.ToUpper(def.name)} {
						q := "SET \"" + name + "\"='a','b'"
						s.run(physicalError(q, physicalDiagnostic("22023", "SET "+name+" takes only one argument"), prepared, false))
					}
					for _, local := range []any{false, true, nil} {
						for _, value := range []any{"/another/location", "", nil} {
							step := physicalError("SELECT set_config($1,$2,$3)", physicalRestart(def.name), prepared, true, def.name, value, local)
							s.run(step)
							s.unchanged(values, "physical_mutation_sentinel", "physical-owner")
						}
					}
					step := physicalError("SELECT set_config(NULL,'v',false)", physicalDiagnostic("22004", "SET requires parameter name"), prepared, true)
					s.run(step)
				}
				// Held preparation must not mutate; the same executable can fail
				// repeatedly without corrupting data or the session's settings.
				for _, q := range []string{"SET " + def.name + "='/held/location'", "RESET " + def.name, "SELECT set_config('" + def.name + "','/held/location',false)"} {
					stmt, err := s.conn.PrepareContext(s.p.Ctx, q)
					if err != nil {
						s.t.Fatal("mutation must prepare successfully", q, err)
					}
					s.unchanged(values, "physical_mutation_sentinel", "physical-owner")
					for range 2 {
						step := physicalError(q, physicalRestart(def.name), false, strings.HasPrefix(q, "SELECT"))
						fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: stmt, SQL: q}, s.recorder, step)
					}
					stmt.Close()
				}
				s.run(fixture.Exec("BEGIN", 0))
				defer s.conn.ExecContext(s.p.Ctx, "ROLLBACK")
				for _, prepared := range []bool{false, true} {
					s.run(fixture.Exec("SAVEPOINT physical_expected_error", 0))
					s.run(physicalError("SET LOCAL "+def.name+"='/transaction/location'", physicalRestart(def.name), prepared, false))
					s.run(fixture.Exec("ROLLBACK TO SAVEPOINT physical_expected_error", 0))
					s.unchanged(values, "physical_mutation_sentinel", "physical-owner")
					s.run(fixture.Exec("RELEASE SAVEPOINT physical_expected_error", 0))
				}
				s.run(fixture.Exec("COMMIT", 0))
				s.run(fixture.Exec("RESET ALL", 0))
				s.unchanged(values, "physical_mutation_sentinel", "")
				peer.unchanged(values, "physical_mutation_sentinel", "physical-peer")
			})
		})
	}
}

func (s *physicalCase) connPoolPeer() (*physicalCase, error) {
	pool := s.p.Memory
	if s.pg {
		pool = s.p.Postgres
	}
	conn, err := pool.Conn(s.p.Ctx)
	if err != nil {
		return nil, err
	}
	return &physicalCase{s.t, s.p, conn, s.pg, s.recorder}, nil
}

func (s *physicalCase) unchanged(values map[string]string, table, application string) {
	s.t.Helper()
	var expected [][]any
	for _, def := range physicalSettings {
		expected = append(expected, []any{def.name, values[def.name]})
	}
	s.run(fixture.Select("SELECT name,setting FROM pg_settings WHERE "+physicalPredicate+" ORDER BY name", []string{"name", "setting"}, []string{"TEXT", "TEXT"}, expected))
	s.run(fixture.Select("SELECT id,value FROM "+table+" ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "kept"}, {int64(2), nil}}))
	s.run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{application}}))
}

func TestReferencePhysicalSettingsNonEffects(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		values := s.inventory(false)
		s.run(fixture.Exec("CREATE TABLE physical_noneffects_sentinel (id integer, value text)", 0))
		s.run(fixture.Exec("INSERT INTO physical_noneffects_sentinel VALUES (1,'kept'),(2,NULL)", 2))
		s.run(fixture.Exec("SET application_name='physical-owner'", 0))
		peer, err := s.connPoolPeer()
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.conn.Close()
		peer.run(fixture.Exec("SET application_name='physical-peer'", 0))
		for _, def := range physicalSettings {
			s.run(physicalError("SET "+def.name+"='/not-an-operative-file'", physicalRestart(def.name), false, false))
			s.run(physicalError("SELECT set_config($1,$2,$3)", physicalRestart(def.name), true, true, def.name, "/not-an-operative-file", false))
		}
		s.run(fixture.Exec("RESET ALL", 0))
		s.unchanged(values, "physical_noneffects_sentinel", "")
		peer.unchanged(values, "physical_noneffects_sentinel", "physical-peer")
		if got := s.inventory(true); !reflect.DeepEqual(got, values) {
			s.t.Fatal("startup values changed", got, values)
		}
	})
}
