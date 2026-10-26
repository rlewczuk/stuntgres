package reference_test

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceShortSettingsLifecycle(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.shortCreateSentinel()
		peer := s.lockPeer()
		for _, prepared := range []bool{false, true} {
			for _, d := range shortSettings {
				show, raw, source, _ := shortInitial(d, s.pg)
				if !shortMutable(d) {
					s.run(physicalError("SET "+d.name+"='bogus'", developerContext(developerSetting{name: d.name, context: d.context}), prepared, false))
					s.shortRead(d.name, show, raw, source, prepared)
					s.shortSentinel()
					continue
				}
				alternate := "on"
				if strings.HasPrefix(d.name, "enable_") { alternate = "off" }
				if d.name == "DateStyle" { alternate = "SQL, DMY" }
				if d.name == "work_mem" { alternate = "1024" }
				if d.name == "log_min_messages" { alternate = "error" }
				altShow := alternate; if d.name == "work_mem" { altShow = "1MB" }
				query := "SET "+d.name+"="+physicalLiteral(alternate)
				held, err := s.conn.PrepareContext(s.p.Ctx, query); if err != nil { s.t.Fatal(err) }
				s.shortRead(d.name, show, raw, source, prepared) // Prepare is inert.
				held.Close()
				readQuery := "SELECT current_setting("+physicalLiteral(d.name)+")"
				reader, err := s.conn.PrepareContext(s.p.Ctx, readQuery); if err != nil { s.t.Fatal(err) }
				s.developerExec("BEGIN", prepared)
				s.developerExec(query, prepared)
				s.developerExec("COMMIT", prepared)
				s.shortRead(d.name, altShow, alternate, "session", prepared)
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: reader, SQL: readQuery}, s.recorder, clientText(readQuery, "current_setting", altShow))
				reader.Close()
				peer.shortRead(d.name, show, raw, source, prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("SET "+d.name+"=DEFAULT", prepared)
				s.developerExec("ROLLBACK", prepared)
				s.shortRead(d.name, altShow, alternate, "session", prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("SAVEPOINT short_life_save", prepared)
				s.developerExec("SET LOCAL "+d.name+"=DEFAULT", prepared)
				s.shortRead(d.name, show, raw, source, prepared)
				s.developerExec("ROLLBACK TO short_life_save", prepared)
				s.shortRead(d.name, altShow, alternate, "session", prepared)
				s.developerExec("SET "+d.name+"=DEFAULT", prepared)
				s.developerExec("SET LOCAL "+d.name+"="+physicalLiteral(alternate), prepared)
				s.developerExec("SAVEPOINT short_masked_save", prepared)
				s.developerExec("SET "+d.name+"="+physicalLiteral(alternate), prepared)
				s.developerExec("ROLLBACK TO short_masked_save", prepared)
				s.developerExec("COMMIT", prepared)
				s.shortRead(d.name, show, raw, "session", prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("SET LOCAL "+d.name+"="+physicalLiteral(alternate), prepared)
				s.developerExec("COMMIT", prepared)
				s.shortRead(d.name, show, raw, "session", prepared)
				s.shortSentinel()
				s.developerExec("RESET "+d.name, prepared)
				s.shortRead(d.name, show, raw, source, prepared)
			}
		}
		// Materialized metadata belongs to native Rows, not the live store.
		if !s.pg {
			retained, err := s.conn.QueryContext(s.p.Ctx, "SELECT setting,source FROM pg_settings WHERE name='enable_seqscan'")
			if err != nil { s.t.Fatal(err) }
			s.developerExec("SET enable_seqscan=off", false)
			if !retained.Next() { s.t.Fatal(retained.Err()) }
			var value, source string
			if err := retained.Scan(&value, &source); err != nil || value != "on" || source != "default" { s.t.Fatal(value, source, err) }
			if retained.Next() || retained.Err() != nil { s.t.Fatal(retained.Err()) }; retained.Close()
			s.shortRead("enable_seqscan", "off", "off", "session", true)
			s.shortControls(map[string][2]string{"enable_seqscan": {"off", "off"}})
			s.shortRead("enable_seqscan", "off", "off", "session", true)
			s.developerExec("RESET ALL", false)
		}
		// Own exactly one physical borrower after closing the peer.
		peer.conn.Close()
		pool := s.p.Memory; if s.pg { pool = s.p.Postgres }
		pool.SetMaxOpenConns(1); pool.SetMaxIdleConns(0); pool.SetMaxIdleConns(1)
		for _, name := range []string{"enable_seqscan", "log_parser_stats"} { value := "on"; if name == "enable_seqscan" { value = "off" }; s.developerExec("SET "+name+"="+value, false) }
		identity := fixture.SettingsPhysical(s.t, s.conn)
		s.conn.Close()
		conn, err := pool.Conn(s.p.Ctx); if err != nil { s.t.Fatal(err) }; s.conn = conn
		if fixture.SettingsPhysical(s.t, conn) != identity { s.t.Fatal("physical connection not reused") }
		s.shortRead("enable_seqscan", "off", "off", "session", true)
		s.shortRead("log_parser_stats", "on", "on", "session", true)
		if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) { s.t.Fatal(err) }; conn.Close()
		replacement, err := pool.Conn(s.p.Ctx); if err != nil { s.t.Fatal(err) }; defer replacement.Close(); s.conn = replacement
		if fixture.SettingsPhysical(s.t, replacement) == identity { s.t.Fatal("physical connection not replaced") }
		for _, d := range shortSettings { show, raw, source, _ := shortInitial(d, s.pg); s.shortRead(d.name, show, raw, source, true) }
		s.shortSentinel()
	})
}

func TestReferenceShortSettingsSimulationSmoke(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.shortCreateSentinel()
		s.developerExec("SET enable_seqscan=off", true)
		s.shortRead("enable_seqscan", "off", "off", "session", true)
		for _, name := range shortStages { s.developerExec("SET "+name+"=on", true) }
		s.shortSnapshot([]string{"on", "on", "on", "off"}, []string{"session", "session", "session", "default"}, true)
		s.run(physicalError("SET log_statement_stats=on", shortConflict("log_statement_stats"), true, false))
		s.shortSnapshot([]string{"on", "on", "on", "off"}, []string{"session", "session", "session", "default"}, false)
		for _, name := range shortStages { s.developerExec("SET "+name+"=off", true) }
		s.developerExec("SET log_statement_stats=on", true)
		s.shortRead("log_statement_stats", "on", "on", "session", true)
		s.developerExec("RESET ALL", true)
		for _, d := range shortSettings { if strings.HasPrefix(d.name, "enable_") || strings.HasSuffix(d.name, "_stats") { s.shortRead(d.name, d.show, d.boot, "default", true) } }
		s.run(physicalError("SET fsync='bogus'", developerContext(developerSetting{name: "fsync", context: "sighup"}), true, false))
		fsyncValue, fsyncSource := "on", "default"
		if s.pg { fsyncValue, fsyncSource = "off", "command line" }
		s.shortRead("fsync", fsyncValue, fsyncValue, fsyncSource, true)
		s.developerExec("SET work_mem='1MB'", true)
		s.shortRead("work_mem", "1MB", "1024", "session", true)
		s.developerExec("SET DateStyle=euro", true)
		s.shortRead("DateStyle", "ISO, DMY", "ISO, DMY", "session", true)
		d := physicalDiagnostic("23505", `duplicate key value violates unique constraint "short_sentinel_pkey"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Key (v)=(7) already exists.", "public", "short_sentinel", "short_sentinel_pkey"
		s.run(physicalError("INSERT INTO short_sentinel VALUES(13),(7)", d, true, false))
		s.shortSentinel()
		s.shortControls(map[string][2]string{"work_mem": {"1MB", "1024"}, "DateStyle": {"ISO, DMY", "ISO, DMY"}})
		s.shortRead("work_mem", "1MB", "1024", "session", true)
		s.shortRead("DateStyle", "ISO, DMY", "ISO, DMY", "session", true)
	})
}
