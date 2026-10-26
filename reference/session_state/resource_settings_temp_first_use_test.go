package reference_test

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Empty-source opening is an explicit native logical simulation boundary, not
// an assertion about when PostgreSQL allocates a physical local buffer. Every
// case has a fresh physical owner and performs no INSERT or COPY FROM first.
func resourceTemporarySourceInitialization(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"select", "SELECT v FROM resource_first_use"},
		{"update", "UPDATE resource_first_use SET v=12"},
		{"delete", "DELETE FROM resource_first_use"},
		{"merge", "MERGE INTO resource_first_use t USING (VALUES(1)) s(v) ON t.v=s.v WHEN MATCHED THEN UPDATE SET v=s.v"},
		{"copy-to", "COPY resource_first_use TO STDOUT"},
	} {
		for _, prepared := range []bool{false, true} {
			// Streaming COPY has its own public pinned transport, not a prepared
			// SQL execution route. Exercise that distinct source opening once.
			if tc.name == "copy-to" && prepared { continue }
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				physicalCases(t, nil, func(s *physicalCase) {
					if s.pg { return } // Populated PostgreSQL preservation cases remain shared.
					if tc.name == "copy-to" {
						resourceTemporaryCopyToInitialization(s)
						return
					}
					probe := s // Ordinary SQL uses its actual recording middleware owner.
					probe.run(fixture.Exec("CREATE TEMP TABLE resource_first_use(v integer)", 0))
					// This must succeed after CREATE, while still uninitialized.
					probe.run(fixture.Exec("SET temp_buffers=2048", 0))
					probe.resourceValue("temp_buffers", "16MB", "2048", "session", false)
					step := fixture.Exec(tc.query, 0)
					if tc.name == "select" { step = fixture.Select(tc.query, []string{"v"}, []string{"INT4"}, [][]any{}) }
					step.Prepared = prepared
					probe.run(step)
					// No other table-data operation intervenes: this rejection
					// independently proves initialization by the named source path.
					probe.run(physicalError("SET temp_buffers='8MB'", resourceTempError("1024"), prepared, false))
					probe.resourceValue("temp_buffers", "16MB", "2048", "session", true)
					probe.run(fixture.Exec("RESET temp_buffers", 0))
					probe.resourceValue("temp_buffers", "8MB", "1024", "default", false)
					probe.run(physicalError("SELECT set_config('temp_buffers',$1,false)", resourceTempError("128"), prepared, true, "1MB"))
					probe.resourceValue("temp_buffers", "8MB", "1024", "default", true)
				})
			})
		}
	}
}

// Streaming COPY uses an unwrapped native transport. Its SQL assertions are
// explicit public API checks, not fixture Steps paired with another owner's
// middleware recorder.
func resourceTemporaryCopyToInitialization(s *physicalCase) {
	s.t.Helper()
	var notices []fixture.Diagnostic
	conn := fixture.CopyNoticeConnection(s.t, s.p, false, &notices, nil)
	checkValue := func(show, raw, source string) {
		s.t.Helper()
		var gotShow, gotRaw, gotSource string
		if err := conn.QueryRowContext(s.p.Ctx, "SELECT current_setting('temp_buffers')").Scan(&gotShow); err != nil { s.t.Fatal(err) }
		if err := conn.QueryRowContext(s.p.Ctx, "SELECT setting,source FROM pg_settings WHERE name='temp_buffers'").Scan(&gotRaw, &gotSource); err != nil { s.t.Fatal(err) }
		if gotShow != show || gotRaw != raw || gotSource != source { s.t.Fatalf("COPY owner setting got %q/%q/%q want %q/%q/%q", gotShow, gotRaw, gotSource, show, raw, source) }
	}
	checkError := func(err error, want fixture.Diagnostic) {
		s.t.Helper()
		if err == nil { s.t.Fatal("COPY owner assignment unexpectedly succeeded") }
		if got := fixture.ExtractDiagnostic(s.t, err); !reflect.DeepEqual(got, want) { s.t.Fatalf("COPY owner diagnostic got %#v want %#v", got, want) }
	}
	fixture.LockExec(s.t, s.p.Ctx, conn, "CREATE TEMP TABLE resource_first_use(v integer)", 0)
	fixture.LockExec(s.t, s.p.Ctx, conn, "SET temp_buffers=2048", 0)
	checkValue("16MB", "2048", "session")
	var out bytes.Buffer
	result, err := stuntgres.CopyTo(s.p.Ctx, conn, "COPY resource_first_use TO STDOUT", &out)
	if err != nil || result.Count != 0 || result.CommandTag != "COPY 0" || out.Len() != 0 { s.t.Fatal("empty COPY TO", result, out.String(), err) }
	_, err = conn.ExecContext(s.p.Ctx, "SET temp_buffers='8MB'")
	checkError(err, resourceTempError("1024"))
	checkValue("16MB", "2048", "session")
	fixture.LockExec(s.t, s.p.Ctx, conn, "RESET temp_buffers", 0)
	checkValue("8MB", "1024", "default")
	held, err := conn.PrepareContext(s.p.Ctx, "SELECT set_config('temp_buffers',$1,false)")
	if err != nil { s.t.Fatal(err) }
	defer held.Close()
	var value string
	checkError(held.QueryRowContext(s.p.Ctx, "1MB").Scan(&value), resourceTempError("128"))
	checkValue("8MB", "1024", "default")
}

func resourceTemporaryInitializedValidation(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TEMP TABLE resource_initialized_validation(v integer)", 0))
		s.run(fixture.Exec("SET temp_buffers=2048", 0))
		s.run(fixture.Exec("INSERT INTO resource_initialized_validation VALUES(11)", 1))
		// Current1024 intentionally differs from the captured initialized2048.
		s.run(fixture.Exec("RESET temp_buffers", 0))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ input string; diagnostic fixture.Diagnostic }{
				{"bogus", resourceInvalid("temp_buffers", "bogus")},
				{"99", physicalDiagnostic("22023", `99 8kB is outside the valid range for parameter "temp_buffers" (100 8kB .. 1073741823 8kB)`)},
				{"1MB", resourceTempError("128")},
				{"8MB", resourceTempError("1024")},
			} {
				for _, rows := range []bool{false, true} {
					s.run(physicalError("SET temp_buffers="+physicalLiteral(tc.input), tc.diagnostic, prepared, rows))
					s.resourceValue("temp_buffers", "8MB", "1024", "default", prepared)
				}
				s.run(physicalError("SELECT set_config('temp_buffers',$1,false)", tc.diagnostic, prepared, true, tc.input))
				s.resourceValue("temp_buffers", "8MB", "1024", "default", prepared)
			}
		}
	})
}
