package engine

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// SessionStart.Defaults is an internal startup seam, not a public connector
// option or PostgreSQL API. Drive the real setting publication/journal paths;
// inspect their metadata and statement guard projections without map writes.
func TestRowSecurityProviderLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name       string
		value      string
		supplied   bool
		noProvider bool
	}{
		{name: "supplied_off", value: "off", supplied: true},
		{name: "supplied_boot_equal_on", value: "on", supplied: true},
		{name: "provider_returns_absent", value: "on"},
		{name: "no_provider", value: "on", noProvider: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _, _, reader := ownershipUnitRoles()
			provider := &settingsDefaultProviderForTest{values: map[string]string{}, calls: map[string]int{}}
			if tc.supplied {
				provider.values["row_security"] = tc.value
			}
			var defaults settings.SessionDefaultProvider = provider
			if tc.noProvider {
				defaults = nil
			}
			s := sessionStart(t, e, defaults)
			source := "default"
			if tc.supplied {
				source = "client"
			}
			view := e.CatalogRead()
			d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.SettingsOID})
			ref := e.catalog.Domain.Ref(view.Epoch(), d.Address())
			roles := e.Roles()
			protected := &catalog.Relation{ObjectName: "provider_target", Owner: reader, Security: catalog.RelationSecurity{RLSEnabled: true, RLSForced: true}}
			check := func(sess *session.Session, value, reset, provenance string) {
				t.Helper()
				if got, ok := sess.Setting("row_security"); !ok || got != value {
					t.Fatal("effective setting", got, ok, value)
				}
				rows, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Ref: ref, Source: d, Runtime: engineCatalogRuntime{settings: sess.Settings}}, capturePGSettings)
				if err != nil || len(rows.Anonymous) != 1 {
					t.Fatal(rows, err)
				}
				for index, want := range map[int]string{1: value, 8: provenance, 12: "on", 13: reset} {
					got, err := rows.Anonymous[0][index].Text()
					if err != nil || got != want {
						t.Fatal("metadata", index, got, want, err)
					}
				}
				if !rows.Anonymous[0][14].IsNull() || !rows.Anonymous[0][15].IsNull() {
					t.Fatal("invented file provenance")
				}
				scope := newStatementScope(&session.Operation{Session: sess}, &session.TransactionOwner{}, time.Time{})
				err = access.RowSecuritySettingGuard(scope.rowSecurity, roles, protected, reader)
				var want error
				if value == "off" {
					want = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: `query would be affected by row-level security policy for table "provider_target"`, Hint: "To disable the policy for the table's owner, use ALTER TABLE NO FORCE ROW LEVEL SECURITY."}
				}
				if !reflect.DeepEqual(err, want) {
					t.Fatalf("statement guard: %#v want %#v", err, want)
				}
			}
			baseline := func() { check(s, tc.value, tc.value, source) }
			baseline()
			other := "off"
			if tc.value == "off" {
				other = "on"
			}
			// Changes to callback storage cannot change an existing physical
			// session, including RESET after transaction/savepoint restoration.
			provider.values["row_security"] = other
			apply := func(input *string, local bool) {
				t.Helper()
				op, err := s.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				if _, err := e.applySetting(op, s.Settings, e.settings.Definitions()["row_security"], input, local); err != nil {
					t.Fatal(err)
				}
			}
			reset := func() {
				t.Helper()
				apply(nil, false)
				s.Settings.FinishTransaction(true)
			}
			for _, ending := range []string{"COMMIT", "ROLLBACK"} {
				for _, kind := range []string{"SESSION", "LOCAL"} {
					apply(&other, kind == "LOCAL")
					check(s, other, tc.value, "session")
					s.Settings.FinishTransaction(ending == "COMMIT")
					if kind == "SESSION" && ending == "COMMIT" {
						check(s, other, tc.value, "session")
					} else {
						baseline()
					}
					reset()
					baseline()
				}
			}
			// SESSION followed by LOCAL has a distinct commit candidate.
			for _, ending := range []string{"COMMIT", "ROLLBACK"} {
				apply(&other, false)
				apply(&tc.value, true)
				check(s, tc.value, tc.value, "session")
				mark := s.Settings.Savepoint()
				identity := s.Authorization.State
				apply(nil, false)
				baseline()
				s.Settings.RestoreSavepoint(mark.Clone(), identity)
				check(s, tc.value, tc.value, "session")
				s.Settings.FinishTransaction(ending == "COMMIT")
				if ending == "COMMIT" {
					check(s, other, tc.value, "session")
				} else {
					baseline()
				}
				reset()
				baseline()
			}
			// A mark before either kind must recover absent override provenance,
			// including explicit boot-equal provider values.
			for _, kind := range []string{"SESSION", "LOCAL"} {
				mark := s.Settings.Savepoint()
				identity := s.Authorization.State
				apply(&other, kind == "LOCAL")
				check(s, other, tc.value, "session")
				s.Settings.RestoreSavepoint(mark.Clone(), identity)
				baseline()
				s.Settings.FinishTransaction(true)
				baseline()
			}
			if !tc.noProvider && provider.calls["row_security"] != 1 {
				t.Fatal("replayed provider", provider.calls)
			}
			// A separately started physical session recaptures the now-supplied
			// value; its startup and settings do not alter the original session.
			replacement := sessionStart(t, e, defaults)
			if tc.noProvider {
				check(replacement, "on", "on", "default")
			} else {
				check(replacement, other, other, "client")
				if provider.calls["row_security"] != 2 {
					t.Fatal("missing physical recapture", provider.calls)
				}
			}
			baseline()
		})
	}
}
