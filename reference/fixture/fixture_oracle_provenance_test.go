package fixture

import (
	"strings"
	"testing"
)

func TestReferenceFixtureOracleProvenance(t *testing.T) {
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				step := Select("SELECT 6*7 AS answer", []string{"answer"}, []string{"INT4"}, [][]any{{int64(42)}})
				step.Prepared = prepared
				RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
			}
			if backend.PG {
				var version, number, encoding, collate, ctype, messages string
				err := backend.DB.QueryRowContext(p.Ctx, "SELECT current_setting('server_version'), current_setting('server_version_num'), current_setting('server_encoding'), datcollate, datctype, current_setting('lc_messages') FROM pg_database WHERE datname=current_database()").Scan(&version, &number, &encoding, &collate, &ctype, &messages)
				if err != nil {
					t.Fatal(err)
				}
				if (version != "18.6" && !strings.HasPrefix(version, "18.6 ")) || number != "180006" || encoding != "UTF8" || collate != "C" || ctype != "C" || messages != "C" {
					t.Fatalf("oracle provenance: version=%q number=%q encoding=%q collate=%q ctype=%q messages=%q; want PostgreSQL 18.6/180006 UTF8/C/C/C", version, number, encoding, collate, ctype, messages)
				}
			}
		})
	}
	// The unwrapped native control remains usable, without claiming PG metadata.
	var answer int64
	if err := p.Native.QueryRowContext(p.Ctx, "SELECT 6*7 AS answer").Scan(&answer); err != nil || answer != 42 {
		t.Fatalf("native control: answer=%d error=%v; want 42", answer, err)
	}
}
