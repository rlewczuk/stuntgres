package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSubscriptionValidation(t *testing.T) {
	errors := []struct{ options, code, message, hint string }{
		{"enabled=true", "42601", "connect = false and enabled = true are mutually exclusive options", ""},
		{"create_slot=true", "42601", "connect = false and create_slot = true are mutually exclusive options", ""},
		{"copy_data=true", "42601", "connect = false and copy_data = true are mutually exclusive options", ""},
		{"binary=yes", "42601", "binary requires a Boolean value", ""},
		{"binary='t'", "42601", "binary requires a Boolean value", ""},
		{"binary=2", "42601", "binary requires a Boolean value", ""},
		{"binary=1.0", "42601", "binary requires a Boolean value", ""},
		{"streaming='pa'", "42601", `streaming requires a Boolean value or "parallel"`, ""},
		{"slot_name=''", "42602", `replication slot name "" is too short`, ""},
		{"slot_name='UPPER'", "42602", `replication slot name "UPPER" contains invalid character`, "Replication slot names may only contain lower case letters, numbers, and the underscore character."},
		{"slot_name='NONE'", "42602", `replication slot name "NONE" contains invalid character`, "Replication slot names may only contain lower case letters, numbers, and the underscore character."},
		{"origin='other'", "22023", `unrecognized origin value: "other"`, ""},
		{"origin", "42601", "origin requires a parameter", ""},
		{"slot_name", "42601", "slot_name requires a parameter", ""},
		{"synchronous_commit", "42601", "synchronous_commit requires a parameter", ""},
		{"refresh=false", "42601", `unrecognized subscription parameter: "refresh"`, ""},
		{"lsn=none", "42601", `unrecognized subscription parameter: "lsn"`, ""},
		{"unknown=true", "42601", `unrecognized subscription parameter: "unknown"`, ""},
		{"slot_name='" + strings.Repeat("x", 64) + "'", "42622", `replication slot name "` + strings.Repeat("x", 64) + `" is too long`, ""},
	}
	for i, tc := range errors {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			p := subscriptionScenario(t, "sub_validation")
			for _, b := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					want.Hint = tc.hint
					s := fixture.ErrorDiagnostic("CREATE SUBSCRIPTION sub_validation CONNECTION '' PUBLICATION p WITH (connect=false,"+tc.options+")", want)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					subscriptionRun(t, p, b, b.DB, s)
					subscriptionRun(t, p, b, b.DB, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_validation'", false))
				}
			}
		})
	}
	for _, value := range []string{"true", "false", "on", "off", "0", "1", "'TRUE'", "'OFF'"} {
		t.Run("boolean_"+value, func(t *testing.T) {
			p := subscriptionScenario(t, "sub_boolean")
			for _, b := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_boolean CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE,binary="+value+",disable_on_error="+value+",password_required="+value+",run_as_owner="+value+",failover="+value+",two_phase="+value+")", prepared)
					subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_boolean", prepared)
					subscriptionRun(t, p, b, b.DB, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_boolean'", false))
				}
			}
		})
	}
	connections := []struct{ text, code, message string }{
		{"", "", ""}, {"port=-1", "", ""}, {"password=first password=last", "", ""}, {"password='a\\'b' application_name=a\\ b", "", ""},
		{"postgres://u:p@host1:1,host2:2/db?application_name=a+b", "", ""},
		{"postgresql://u:p@[::1]:-1/db?host=%2Ftmp&ssl=true", "", ""},
		{"postgresql:///db?requiressl=1", "", ""},
		{"testconn", "42601", "invalid connection string syntax: missing \"=\" after \"testconn\" in connection info string\n"},
		{"i_dont_exist=1", "42601", "invalid connection string syntax: invalid connection option \"i_dont_exist\"\n"},
		{"host='unterminated", "42601", "invalid connection string syntax: unterminated quoted string in connection info string\n"},
		{"postgresql://host/db?x=1", "42601", "invalid connection string syntax: invalid URI query parameter: \"x\"\n"},
		{"postgresql://host/db?host", "42601", "invalid connection string syntax: missing key/value separator \"=\" in URI query parameter: \"host\"\n"},
		{"postgresql://host/db?host=a=b", "42601", "invalid connection string syntax: extra key/value separator \"=\" in URI query parameter: \"host\"\n"},
		{"postgresql://host/%xy", "42601", "invalid connection string syntax: invalid percent-encoded token: \"%xy\"\n"},
		{"postgresql://host/%00", "42601", "invalid connection string syntax: forbidden value %00 in percent-encoded value: \"%00\"\n"},
	}
	for i, tc := range connections {
		t.Run(fmt.Sprintf("connection_%d", i), func(t *testing.T) {
			p := subscriptionScenario(t, "sub_conn_validation")
			for _, b := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					query := "CREATE SUBSCRIPTION sub_conn_validation CONNECTION '" + strings.ReplaceAll(tc.text, "'", "''") + "' PUBLICATION p WITH (connect=false,slot_name=NONE)"
					if tc.code != "" {
						subscriptionError(t, p, b, b.DB, query, tc.code, tc.message, prepared)
					} else {
						subscriptionExec(t, p, b, b.DB, query, prepared)
						subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_conn_validation CONNECTION '"+strings.ReplaceAll(tc.text, "'", "''")+"'", prepared)
						subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_conn_validation", prepared)
						subscriptionRun(t, p, b, b.DB, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_conn_validation'", false))
					}
				}
			}
		})
	}
}

func TestReferenceSubscriptionBoundarySmoke(t *testing.T) {
	p := subscriptionScenario(t, "sub_boundary_smoke", "sub_boundary_connected")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			subscriptionExec(t, p, b, b.DB, "CREATE TABLE sub_boundary_sentinel(v int)", false)
			subscriptionRun(t, p, b, b.DB, fixture.Exec("INSERT INTO sub_boundary_sentinel VALUES (77)", 1))
			for _, prepared := range []bool{false, true} {
				subscriptionError(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_boundary_connected CONNECTION 'testconn' PUBLICATION p WITH (connect=false)", "42601", "invalid connection string syntax: missing \"=\" after \"testconn\" in connection info string\n", prepared)
				subscriptionError(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_boundary_connected CONNECTION '' PUBLICATION p WITH (connect=false,unknown=true)", "42601", `unrecognized subscription parameter: "unknown"`, prepared)
				slot := fixture.ControlsReferenceDiagnostic("42602", `replication slot name "UPPER" contains invalid character`)
				slot.Hint = "Replication slot names may only contain lower case letters, numbers, and the underscore character."
				invalid := fixture.ErrorDiagnostic("CREATE SUBSCRIPTION sub_boundary_connected CONNECTION '' PUBLICATION p WITH (connect=false,slot_name='UPPER')", slot)
				invalid.Prepared = prepared
				invalid.ErrorStage = "execute"
				subscriptionRun(t, p, b, b.DB, invalid)
				subscriptionRun(t, p, b, b.DB, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_boundary_connected'", false))
			}
			subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_boundary_smoke CONNECTION '' PUBLICATION p,q WITH (connect=false,slot_name=NONE)", false)
			subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_boundary_smoke ENABLE", "55000", "cannot enable subscription that does not have a slot name", true)
			subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_boundary_smoke REFRESH PUBLICATION", "55000", "ALTER SUBSCRIPTION ... REFRESH is not allowed for disabled subscriptions", false)
			if !b.PG {
				for _, prepared := range []bool{false, true} {
					subscriptionError(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_boundary_connected CONNECTION '' PUBLICATION p", "0A000", "subscription publisher connections are not available", prepared)
					subscriptionError(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_boundary_connected CONNECTION '' PUBLICATION p WITH (enabled=false,create_slot=false,copy_data=false)", "0A000", "subscription publisher connections are not available", prepared)
				}
				subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_boundary_smoke SET (slot_name='boundary_slot')", false)
				subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_boundary_smoke ENABLE", false)
				for _, prepared := range []bool{false, true} {
					for _, action := range []string{"REFRESH PUBLICATION WITH (copy_data=false)", "SET PUBLICATION r", "ADD PUBLICATION r", "DROP PUBLICATION q"} {
						subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_boundary_smoke "+action, "0A000", "subscription publisher connections are not available", prepared)
						subscriptionList(t, p, b, b.DB, "sub_boundary_smoke", "{p,q}", true)
					}
					subscriptionError(t, p, b, b.DB, "DROP SUBSCRIPTION sub_boundary_smoke", "0A000", "subscription replication slot removal is not available", prepared)
				}
				subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_boundary_smoke DISABLE", false)
				for _, option := range []string{"failover=true", "failover=false", "two_phase=false"} {
					subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_boundary_smoke SET ("+option+")", "0A000", "subscription replication slot alteration is not available", true)
				}
				subscriptionDetach(t, p, b, b.DB, "sub_boundary_smoke")
			} else {
				subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_boundary_smoke", false)
			}
			subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT count(*) AS n FROM pg_subscription_rel", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT v FROM sub_boundary_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
			subscriptionRun(t, p, b, b.DB, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_boundary_connected'", false))
		})
	}
}
