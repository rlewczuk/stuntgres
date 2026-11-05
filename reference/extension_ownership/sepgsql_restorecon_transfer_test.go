package reference_test

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// These are native-control phases of DroppedColumns, not a second SQL provider
// or an active-host PostgreSQL comparison. Only a real tombstone may transfer.
func restoreconRetiredTransfer(t *testing.T, image string) {
	t.Helper()
	absent := restoreconTamper(t, image, func(r map[string]any) { delete(r, "retired_columns") })
	for _, tc := range []struct {
		name, context     string
		deny              []string
		seed, importImage string
	}{
		{"old_secret", sepgsqlS, []string{"setattr", "relabelfrom"}, image, image},
		{"new_secret", sepgsqlS, []string{"relabelto"}, absent, image},
		{"old_absent", sepgsqlU, []string{"setattr", "relabelfrom"}, absent, image},
		{"new_absent", sepgsqlU, []string{"relabelto"}, image, absent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []stuntgres.SepgsqlAuditEvent
			target := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
				config := sepgsqlConfig()
				config.AuditAllowed = true
				config.Audit = func(event stuntgres.SepgsqlAuditEvent) { events = append(events, event) }
				config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlA, Target: tc.context, Class: "db_column", Deny: tc.deny})
				return d.RegisterSepgsqlSimulation(config)
			})
			if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(tc.seed)); err != nil {
				t.Fatal("admitted initial destination image", err)
			}
			before := fixture.ExtensionDump(t, target.Ctx, target.Native)
			events = nil
			err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(tc.importImage))
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "SELinux: security policy violation"})
			found := false
			for _, event := range events {
				if event.Class == "db_column" && strings.HasSuffix(event.Name, `public.restorecon_dropped."........pg.dropped.2........"`) && reflect.DeepEqual(event.Denied, tc.deny) {
					found = true
					if event.Target != tc.context || event.Permissive {
						t.Fatal("wrong retired transfer authority", event)
					}
				}
			}
			if !found {
				t.Fatal("retired association bypassed destination consent", events)
			}
			if fixture.ExtensionDump(t, target.Ctx, target.Native) != before {
				t.Fatal("denied retired transfer changed whole destination")
			}
		})
	}
}
