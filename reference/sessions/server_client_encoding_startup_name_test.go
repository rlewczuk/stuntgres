//go:build !noservertests

package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceClientEncodingStartupNameBoundary(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, c := range []struct {
				name, raw, clipped, application string
				notice                          bool
			}{
				{"sixteen_raw", strings.Repeat("\xe9", 16), "", strings.Repeat(`\xe9`, 16), false},
				{"sixtythree_raw", strings.Repeat("\xe9", 63), "", strings.Repeat(`\xe9`, 63), false},
				{"sixteen_utf8", strings.Repeat("é", 16), "", strings.Repeat(`\xc3\xa9`, 16), false},
				{"ascii_over63", strings.Repeat("a", 64), strings.Repeat("a", 63), strings.Repeat("a", 63), true},
				{"raw_over63", strings.Repeat("\xe9", 64), strings.Repeat("\xe9", 63), strings.Repeat(`\xe9`, 63), true},
				// The database is already UTF8 when startup GUC NAME clipping
				// calls pg_mbcliplen; requested LATIN1 is not a decoder here.
				{"raw_boundary", strings.Repeat("a", 62) + "\xe9tail", strings.Repeat("a", 62), strings.Repeat("a", 62), true},
				{"utf8_boundary", strings.Repeat("a", 62) + "é_tail", strings.Repeat("a", 62), strings.Repeat("a", 62), true},
			} {
				t.Run(c.name, func(t *testing.T) {
					dsn := f.DSNFor(b)
					w := serverDial(t, dsn)
					frames := w.login(t, dsn, 196608, "client_encoding", "LATIN1", "application_name", c.raw)
					if serverCode(frames) != "" || w.statuses["application_name"] != c.application || w.statuses["client_encoding"] != "LATIN1" {
						t.Fatalf("startup raw%x clipped%x application%x want%x status%q frames%s", c.raw, c.clipped, w.statuses["application_name"], c.application, w.statuses, clientEncodingTranscript(frames))
					}
					var notices []serverFrame
					for _, frame := range frames {
						if frame.tag == 'N' {
							notices = append(notices, frame)
						}
					}
					if c.notice {
						if len(notices) != 1 {
							t.Fatalf("NAME clipping notices %s", clientEncodingTranscript(notices))
						}
						message := `identifier "` + c.raw + `" will be truncated to "` + c.clipped + `"`
						clientEncodingDiagnostic(t, notices, 'N', map[byte]string{'S': "NOTICE", 'V': "NOTICE", 'C': "42622", 'M': message})
					} else if len(notices) != 0 {
						t.Fatalf("expansion alone must not truncate or emit NOTICE: %s", clientEncodingTranscript(notices))
					}
					serverScalar(t, w, "SHOW application_name", c.application)
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
		})
	}
}
