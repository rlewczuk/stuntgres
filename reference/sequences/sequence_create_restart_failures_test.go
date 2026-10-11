package reference_test

import (
	"context"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceCreateRestartFailuresAndNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					// This legacy notice pool and its peer are one engine; no p.native controls.
					f := fixture.NewControlsReferenceFixture(t, p, backend.PG)
					c := f.A
					exec := func(q string) { fixture.RunStep(t, p.Ctx, c, f.Recorder, fixture.Step{Query: q, Prepared: prepared}) }
					exec("CREATE SEQUENCE sentinel START 41")
					sequenceRestartValue(t, p, c, f.Recorder, "SELECT nextval('sentinel')", "nextval", 41)
					for _, tc := range []struct{ name, options, code, msg string }{
						{"low", "MINVALUE 1 MAXVALUE 10 START 5 RESTART 0", "22023", "RESTART value (0) cannot be less than MINVALUE (1)"},
						{"high", "MINVALUE 1 MAXVALUE 10 START 5 RESTART 11", "22023", "RESTART value (11) cannot be greater than MAXVALUE (10)"},
						{"duplicate", "START 5 RESTART 9 RESTART WITH 8", "42601", "conflicting or redundant options"},
					} {
						q := "CREATE SEQUENCE " + tc.name + " " + tc.options
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
						if tc.code == "42601" {
							want.Position = strconv.Itoa(strings.LastIndex(q, "RESTART") + 1)
						}
						fixture.RunStep(t, p.Ctx, c, f.Recorder, fixture.Step{Query: q, Prepared: prepared, State: tc.code, Diagnostic: &want, ErrorStage: "execute"})
						missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "`+tc.name+`" does not exist`)
						missing.Position = "24"
						var value int64
						err := c.QueryRowContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, f.Recorder), "SELECT last_value FROM "+tc.name).Scan(&value)
						fixture.AssertDiagnostic(t, err, &missing)
						sequenceRestartState(t, p, c, f.Recorder, backend.PG, "sentinel", 41, true)
						sequenceRestartValue(t, p, c, f.Recorder, "SELECT currval('sentinel')", "currval", 41)
						sequenceRestartValue(t, p, c, f.Recorder, "SELECT lastval()", "lastval", 41)
					}
					exec("CREATE SEQUENCE IF NOT EXISTS sentinel RESTART 0")
					want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "sentinel" already exists, skipping`}
					if len(*f.Notices) != 1 || (*f.Notices)[0] != want {
						t.Fatalf("notices %#v want %#v", *f.Notices, want)
					}
					sequenceRestartState(t, p, c, f.Recorder, backend.PG, "sentinel", 41, true)
					sequenceRestartValue(t, p, c, f.Recorder, "SELECT currval('sentinel')", "currval", 41)
					sequenceRestartValue(t, p, c, f.Recorder, "SELECT lastval()", "lastval", 41)
					sequenceRestartValue(t, p, c, f.Recorder, "SELECT nextval('sentinel')", "nextval", 42)
				})
			}
		})
	}
}
