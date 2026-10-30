package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func defaultAMInvalid(value string) fixture.Diagnostic {
	if value == "btree" {
		return physicalDiagnostic("55000", `access method "btree" is not of type TABLE`)
	}
	d := physicalDiagnostic("22023", fmt.Sprintf(`invalid value for parameter "default_table_access_method": "%s"`, value))
	d.Detail = fmt.Sprintf(`Table access method "%s" does not exist.`, value)
	if value == "" {
		d.Detail = `"default_table_access_method" cannot be empty.`
	}
	return d
}

func TestReferenceDefaultTableAccessMethodValidation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			defaultAMCases(t, []string{"dam_login"}, func(s *physicalCase) {
				defaultAMWitness(s, "dam_sentinel", "heap", prepared)
				defaultAMSet(s, "settings_heap", prepared)
				defaultAMWitness(s, "dam_custom_sentinel", "settings_heap", prepared)
				for i, value := range []string{"", "missing", "btree", "SETTINGS_HEAP"} {
					for j, q := range []string{
						"SET default_table_access_method=" + physicalLiteral(value),
						"SELECT set_config('default_table_access_method'," + physicalLiteral(value) + ",false)",
					} {
						// Fresh Prepare is checked separately from Execute: no value
						// validation or installation may happen during preparation.
						step := physicalError(q, defaultAMInvalid(value), false, j == 1)
						if prepared {
							stmt, err := s.conn.PrepareContext(s.p.Ctx, q)
							if err != nil {
								s.t.Fatal(err)
							}
							s.clientReads(defaultAMSetting, "settings_heap", false)
							fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: stmt, SQL: q}, s.recorder, step)
							stmt.Close()
						} else {
							s.run(step)
						}
						defaultAMMetadata(s, true, prepared)
						defaultAMWitness(s, fmt.Sprintf("dam_valid_%d_%d", i, j), "settings_heap", prepared)
						s.run(defaultAMIdentity("dam_sentinel", "heap"))
						s.run(defaultAMRows("dam_sentinel", defaultAMKeptRows()))
						s.run(defaultAMIdentity("dam_custom_sentinel", "settings_heap"))
						s.run(defaultAMRows("dam_custom_sentinel", defaultAMKeptRows()))
					}
				}
				defaultAMRun(s, prepared, fixture.Exec("RESET default_table_access_method", 0))
				defaultAMRun(s, prepared, fixture.Exec("BEGIN", 0))
				defaultAMSet(s, "settings_heap", prepared)
				s.run(physicalError("SET default_table_access_method='missing'", defaultAMInvalid("missing"), prepared, false))
				aborted := physicalError("CREATE TABLE dam_aborted(id integer)", physicalDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"), prepared, false)
				if prepared {
					aborted.ErrorStage = "prepare"
				}
				s.run(aborted)
				defaultAMRun(s, prepared, fixture.Exec("ROLLBACK", 0))
				s.run(defaultAMAbsent("dam_aborted"))
				defaultAMWitness(s, "dam_healthy", "heap", prepared)
				defaultAMRun(s, prepared, fixture.Exec("CREATE ROLE dam_login LOGIN PASSWORD 'role-core-fixture'", 0))
				defaultAMRun(s, prepared, fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO dam_login", 0))
				ordinary := s.login("dam_login")
				ordinary.clientReads(defaultAMSetting, "heap", prepared)
				defaultAMSet(ordinary, "settings_heap", prepared)
				defaultAMMetadata(ordinary, true, prepared)
				defaultAMWitness(ordinary, "dam_ordinary", "settings_heap", prepared)
				d := physicalDiagnostic("42501", `permission denied to create access method "settings_denied"`)
				d.Hint = "Must be superuser to create an access method."
				ordinary.run(physicalError("CREATE ACCESS METHOD settings_denied TYPE TABLE HANDLER pg_catalog.heap_tableam_handler", d, prepared, false))
				ordinary.run(fixture.Select("SELECT amname FROM pg_am WHERE amname='settings_denied'", []string{"amname"}, []string{"NAME"}, nil))
				s.clientReads(defaultAMSetting, "heap", prepared)
				defaultAMMetadata(s, false, prepared)
			})
		})
	}
	t.Run("stale-provider", func(t *testing.T) {
		defaultAMCases(t, nil, func(s *physicalCase) {
			defaultAMWitness(s, "dam_stale_sentinel", "heap", false)
			defaultAMSet(s, "settings_heap", true)
			s.run(fixture.Exec("DROP ACCESS METHOD settings_heap", 0))
			s.clientReads(defaultAMSetting, "settings_heap", true)
			defaultAMMetadata(s, true, true)
			for i, prepared := range []bool{false, true} {
				name := fmt.Sprintf("dam_stale_%d", i)
				s.run(physicalError("CREATE TABLE "+name+"(id integer)", physicalDiagnostic("42704", `access method "settings_heap" does not exist`), prepared, false))
				s.run(defaultAMAbsent(name))
				s.run(defaultAMIdentity("dam_stale_sentinel", "heap"))
				s.run(defaultAMRows("dam_stale_sentinel", defaultAMKeptRows()))
			}
			s.run(fixture.Exec("RESET default_table_access_method", 0))
			defaultAMWitness(s, "dam_stale_recovered", "heap", true)
			s.run(fixture.Exec("CREATE ACCESS METHOD settings_heap TYPE TABLE HANDLER pg_catalog.heap_tableam_handler", 0))
			defaultAMSet(s, "settings_heap", false)
			defaultAMWitness(s, "dam_reinstalled", "settings_heap", true)
		})
	})
}
