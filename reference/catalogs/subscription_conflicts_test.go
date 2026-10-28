package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSubscriptionOptionConflicts(t *testing.T) {
	for _, tc := range []struct{ options, message string }{
		{"slot_name=NONE", "subscription with slot_name = NONE must also set enabled = false"},
		{"slot_name=NONE,enabled=true", "slot_name = NONE and enabled = true are mutually exclusive options"},
		{"slot_name=NONE,enabled=false", "subscription with slot_name = NONE must also set create_slot = false"},
		{"slot_name=NONE,enabled=false,create_slot=true", "slot_name = NONE and create_slot = true are mutually exclusive options"},
	} {
		t.Run(tc.options, func(t *testing.T) {
			p := subscriptionScenario(t, "sub_conflict")
			for _, b := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					subscriptionError(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_conflict CONNECTION '' PUBLICATION p WITH ("+tc.options+")", "42601", tc.message, prepared)
				}
			}
		})
	}
	p := subscriptionScenario(t, "sub_enabled_guard")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_enabled_guard CONNECTION '' PUBLICATION p WITH (connect=false)", false)
			for _, tc := range []struct{ option, message string }{
				{"slot_name=NONE", "cannot set slot_name = NONE for enabled subscription"},
				{"two_phase=true", `cannot set option "two_phase" for enabled subscription`},
				{"two_phase=false", `cannot set option "two_phase" for enabled subscription`},
				{"failover=true", `cannot set option "failover" for enabled subscription`},
				{"failover=false", `cannot set option "failover" for enabled subscription`},
			} {
				for _, prepared := range []bool{false, true} {
					tx, err := b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					subscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION sub_enabled_guard ENABLE", false)
					subscriptionError(t, p, b, tx, "ALTER SUBSCRIPTION sub_enabled_guard SET ("+tc.option+")", "55000", tc.message, prepared)
					tx.Rollback()
					subscriptionList(t, p, b, b.DB, "sub_enabled_guard", "{p}", false)
				}
			}
			for _, option := range []string{"failover=false", "two_phase=false"} {
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				name := "failover"
				if option == "two_phase=false" {
					name = "two_phase"
				}
				subscriptionError(t, p, b, tx, "ALTER SUBSCRIPTION sub_enabled_guard SET ("+option+")", "25001", "ALTER SUBSCRIPTION ... SET ("+name+") cannot run inside a transaction block", true)
				tx.Rollback()
			}
			subscriptionDetach(t, p, b, b.DB, "sub_enabled_guard")
		})
	}
}
func TestReferenceSubscriptionNotices(t *testing.T) {
	p := subscriptionScenario(t, "sub_notice")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE SUBSCRIPTION sub_notice CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)", 0))
			want := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "subscription was created, but is not connected", Hint: "To initiate replication, you must manually create the replication slot, enable the subscription, and refresh the subscription."}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatal("subscription disconnected warning", notices, want)
			}
			notices = nil
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DROP SUBSCRIPTION sub_notice", 0))
			fixture.RunStep(t, p.Ctx, conn, recorder, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_notice'", false))
			if len(notices) != 0 {
				t.Fatal("unexpected subscription drop notice", notices)
			}
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(fixture.Exec("DROP SUBSCRIPTION IF EXISTS sub_notice", 0)))
			want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `subscription "sub_notice" does not exist, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatal("subscription missing notice", notices, want)
			}
		})
	}
}
