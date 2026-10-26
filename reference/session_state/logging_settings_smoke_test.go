package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func (s *physicalCase) loggingConstraints(prepared bool) {
	d := physicalDiagnostic("23505", `duplicate key value violates unique constraint "logging_smoke_pkey"`)
	d.Detail, d.Schema, d.Table, d.Constraint = "Key (v)=(1) already exists.", "public", "logging_smoke", "logging_smoke_pkey"
	s.run(physicalError("INSERT INTO logging_smoke VALUES(1),(4)", d, prepared, false))
	d = physicalDiagnostic("23502", `null value in column "v" of relation "logging_smoke" violates not-null constraint`)
	d.Detail, d.Schema, d.Table, d.Column = "Failing row contains (null).", "public", "logging_smoke", "v"
	s.run(physicalError("INSERT INTO logging_smoke VALUES(NULL),(4)", d, prepared, false))
	s.compatibleSentinel("logging_smoke", prepared)
}

func TestReferenceLoggingSettingsSimulationSmoke(t *testing.T) {
	physicalCases(t, []string{"logging_smoke_login"}, func(s *physicalCase) {
		s.developerExec("CREATE ROLE logging_smoke_login LOGIN PASSWORD 'role-core-fixture'", false)
		plain := s.login("logging_smoke_login")
		peer, err := s.connPoolPeer()
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.conn.Close()
		s.developerExec("CREATE TABLE logging_smoke(v integer PRIMARY KEY)", false)
		s.run(fixture.Exec("INSERT INTO logging_smoke VALUES(1),(2),(3)", 3))
		var before bytes.Buffer
		if !s.pg {
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &before); err != nil {
				s.t.Fatal(err)
			}
		}
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{"SET application_name='logging-smoke'", "SET debug_print_parse=on", "SET debug_pretty_print=off", "SET log_statement='all'", "SET log_min_duration_statement='2s'", "SET log_parameter_max_length='1kB'", "SET log_parameter_max_length_on_error='1kB'", "SET log_temp_files='2MB'", "SET log_error_verbosity=terse", "SET log_min_messages=panic", "SET log_statement_sample_rate='0.25'", "SET log_transaction_sample_rate='0.5'"} {
				s.developerExec(query, prepared)
			}
			s.loggingConstraints(prepared)
			for _, tc := range loggingSettings {
				initial, raw, source := s.loggingInitial(tc)
				if loggingMutable(tc) {
					s.developerExec("SET "+tc.name+"="+physicalLiteral(tc.alternate), prepared)
					s.loggingRead(tc, tc.show, prepared)
					s.loggingMetadata(tc, tc.raw, "session", prepared)
				} else {
					s.run(physicalError("SET "+tc.name+"="+physicalLiteral(tc.alternate), loggingContext(tc), prepared, false))
					s.loggingRead(tc, initial, prepared)
					s.loggingMetadata(tc, raw, source, prepared)
				}
				peer.loggingRead(tc, initial, prepared)
				s.compatibleSentinel("logging_smoke", prepared)
				s.loggingConstraints(prepared)
			}
			plain.developerExec("SET log_parameter_max_length_on_error='1kB'", prepared)
			plain.run(physicalError("SET log_statement='all'", developerDenied("log_statement"), prepared, false))
			plain.run(physicalError("SELECT current_setting('log_directory',true)", physicalDenied("log_directory"), prepared, true))
			s.developerExec("SET TimeZone='UTC'", prepared)
			for _, value := range []string{"Europe/Paris", "unknown/zone"} {
				s.run(physicalError("SET log_timezone="+physicalLiteral(value), physicalDiagnostic("55P02", `parameter "log_timezone" cannot be changed now`), prepared, false))
				s.run(fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"UTC"}}))
				step := fixture.Select("SELECT TIMESTAMPTZ '2000-01-01 00:00:00+00'::text AS rendered", []string{"rendered"}, []string{"TEXT"}, [][]any{{"2000-01-01 00:00:00+00"}})
				step.Prepared = prepared
				s.run(step)
			}
			s.developerExec("BEGIN", prepared)
			s.developerExec("SAVEPOINT logging_smoke_save", prepared)
			s.developerExec("SET LOCAL log_statement=none", prepared)
			s.developerExec("ROLLBACK TO logging_smoke_save", prepared)
			s.run(fixture.Select("SHOW log_statement", []string{"log_statement"}, []string{"TEXT"}, [][]any{{"all"}}))
			s.developerExec("COMMIT", prepared)
			s.developerExec("RESET ALL", prepared)
			plain.developerExec("RESET ALL", prepared)
			for _, tc := range loggingSettings {
				initial, raw, source := s.loggingInitial(tc)
				s.loggingRead(tc, initial, prepared)
				s.loggingMetadata(tc, raw, source, prepared)
			}
			s.loggingConstraints(prepared)
		}
		if !s.pg {
			var after bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &after); err != nil {
				s.t.Fatal(err)
			}
			if before.String() != after.String() {
				s.t.Fatal("logging operations changed logical dump")
			}
			s.developerExec("SET application_name='retained-logging'", false)
			retained, err := s.conn.QueryContext(s.p.Ctx, "SELECT v FROM logging_smoke ORDER BY v")
			if err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
				retained.Close()
				s.t.Fatal(err)
			}
			seen := 0
			for retained.Next() {
				var value int64
				if err := retained.Scan(&value); err != nil {
					s.t.Fatal(err)
				}
				seen++
				if value != int64(seen) {
					s.t.Fatal("retained snapshot", value, seen)
				}
			}
			if retained.Err() != nil || seen != 3 {
				s.t.Fatal("retained snapshot count", seen, retained.Err())
			}
			retained.Close()
			s.run(fixture.Select("SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_name='logging_smoke'", []string{"table_name"}, []string{"NAME"}, nil))
			s.run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"retained-logging"}}))
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(before.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			s.loggingConstraints(true)
			s.run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"retained-logging"}}))
			peer.run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{""}}))
			fresh, err := s.connPoolPeer()
			if err != nil {
				s.t.Fatal(err)
			}
			defer fresh.conn.Close()
			fresh.run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{""}}))
		}
	})
}
