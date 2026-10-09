package reference_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceSequenceCreateRestartFailuresAndNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					// This legacy notice pool and its peer are one engine; no p.native controls.
					f := newControlsReferenceFixture(t, p, backend.pg)
					c := f.a
					exec := func(q string) { referenceRunStep(t, p.ctx, c, f.recorder, referenceStep{query: q, prepared: prepared}) }
					exec("CREATE SEQUENCE sentinel START 41")
					sequenceRestartValue(t, p, c, f.recorder, "SELECT nextval('sentinel')", "nextval", 41)
					for _, tc := range []struct{ name, options, code, msg string }{
						{"low", "MINVALUE 1 MAXVALUE 10 START 5 RESTART 0", "22023", "RESTART value (0) cannot be less than MINVALUE (1)"},
						{"high", "MINVALUE 1 MAXVALUE 10 START 5 RESTART 11", "22023", "RESTART value (11) cannot be greater than MAXVALUE (10)"},
						{"duplicate", "START 5 RESTART 9 RESTART WITH 8", "42601", "conflicting or redundant options"},
					} {
						q := "CREATE SEQUENCE " + tc.name + " " + tc.options
						want := controlsReferenceDiagnostic(tc.code, tc.msg)
						if tc.code == "42601" {
							want.Position = strconv.Itoa(strings.LastIndex(q, "RESTART") + 1)
						}
						referenceRunStep(t, p.ctx, c, f.recorder, referenceStep{query: q, prepared: prepared, state: tc.code, diagnostic: &want, errorStage: "execute"})
						missing := controlsReferenceDiagnostic("42P01", `relation "`+tc.name+`" does not exist`)
						missing.Position = "24"
						var value int64
						err := c.QueryRowContext(context.WithValue(p.ctx, referenceRecorderKey{}, f.recorder), "SELECT last_value FROM "+tc.name).Scan(&value)
						referenceAssertDiagnostic(t, err, &missing)
						sequenceRestartState(t, p, c, f.recorder, backend.pg, "sentinel", 41, true)
						sequenceRestartValue(t, p, c, f.recorder, "SELECT currval('sentinel')", "currval", 41)
						sequenceRestartValue(t, p, c, f.recorder, "SELECT lastval()", "lastval", 41)
					}
					exec("CREATE SEQUENCE IF NOT EXISTS sentinel RESTART 0")
					want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "sentinel" already exists, skipping`}
					if len(*f.notices) != 1 || (*f.notices)[0] != want {
						t.Fatalf("notices %#v want %#v", *f.notices, want)
					}
					sequenceRestartState(t, p, c, f.recorder, backend.pg, "sentinel", 41, true)
					sequenceRestartValue(t, p, c, f.recorder, "SELECT currval('sentinel')", "currval", 41)
					sequenceRestartValue(t, p, c, f.recorder, "SELECT lastval()", "lastval", 41)
					sequenceRestartValue(t, p, c, f.recorder, "SELECT nextval('sentinel')", "nextval", 42)
				})
			}
		})
	}
}
