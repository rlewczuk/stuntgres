package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceSubscriptionOptionMatrix(t *testing.T) {
	ordinary := []string{"slot_name", "synchronous_commit", "binary", "streaming", "two_phase", "disable_on_error", "password_required", "run_as_owner", "failover", "origin"}
	command := []string{"connect", "enabled", "create_slot", "copy_data", "refresh", "lsn"}
	for _, action := range []string{"options", "publications", "refresh"} {
		for _, name := range append(append([]string(nil), ordinary...), command...) {
			allowed := action == "options" && containsSubscriptionOption(ordinary, name) || action == "publications" && (name == "refresh" || name == "copy_data") || action == "refresh" && name == "copy_data"
			if allowed {
				continue
			}
			t.Run(action+"_"+name, func(t *testing.T) {
				p := subscriptionScenario(t, "sub_option_matrix")
				for _, b := range p.Backends() {
					subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_option_matrix CONNECTION '' PUBLICATION p WITH (connect=false)", false)
					for _, prepared := range []bool{false, true} {
						query := "ALTER SUBSCRIPTION sub_option_matrix SET (" + name + "=false)"
						if action == "publications" {
							query = "ALTER SUBSCRIPTION sub_option_matrix SET PUBLICATION q WITH (" + name + "=false)"
						}
						var e fixture.Executor = b.DB
						if action == "refresh" {
							tx, err := b.DB.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							subscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION sub_option_matrix ENABLE", false)
							e = tx
							query = "ALTER SUBSCRIPTION sub_option_matrix REFRESH PUBLICATION WITH (" + name + "=false)"
							subscriptionError(t, p, b, e, query, "42601", fmt.Sprintf(`unrecognized subscription parameter: "%s"`, name), prepared)
							tx.Rollback()
						} else {
							subscriptionError(t, p, b, e, query, "42601", fmt.Sprintf(`unrecognized subscription parameter: "%s"`, name), prepared)
						}
						subscriptionList(t, p, b, b.DB, "sub_option_matrix", "{p}", false)
					}
					subscriptionDetach(t, p, b, b.DB, "sub_option_matrix")
				}
			})
		}
	}
	t.Run("duplicate_position", func(t *testing.T) {
		p := subscriptionScenario(t, "sub_option_duplicate")
		query := "/*雪*/ CREATE SUBSCRIPTION sub_option_duplicate CONNECTION '' PUBLICATION p WITH (connect=false,binary=true,binary=false)"
		position := utf8.RuneCountInString(query[:strings.LastIndex(query, "binary")]) + 1
		// PostgreSQL reports the repeated DefElem's scanner location as a
		// one-based character cursor, not a byte index (the comment is UTF8).
		want := fixture.ControlsReferenceDiagnostic("42601", "conflicting or redundant options")
		want.Position = fixture.Position(position)
		for _, b := range p.Backends() {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(query, want)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				subscriptionRun(t, p, b, b.DB, s)
			}
		}
	})
	t.Run("connection_keyword_inventory", func(t *testing.T) {
		p := subscriptionScenario(t, "sub_conn_inventory")
		// Independently enumerated REL_18_6 PQconninfoOptions, including debug keys
		// and the backwards-compatible requiressl alias. Values are not connect-time
		// validation: disconnected metadata neither loads files nor fills defaults.
		keys := strings.Fields("service user password passfile channel_binding connect_timeout dbname host hostaddr port client_encoding options application_name fallback_application_name keepalives keepalives_idle keepalives_interval keepalives_count tcp_user_timeout sslmode sslnegotiation sslcompression sslcert sslkey sslcertmode sslpassword sslrootcert sslcrl sslcrldir sslsni requirepeer require_auth min_protocol_version max_protocol_version ssl_min_protocol_version ssl_max_protocol_version gssencmode krbsrvname gsslib gssdelegation replication target_session_attrs load_balance_hosts scram_client_key scram_server_key oauth_issuer oauth_client_id oauth_client_secret oauth_scope sslkeylogfile requiressl")
		var connection strings.Builder
		for _, key := range keys {
			connection.WriteString(key + "=unvalidated ")
		}
		query := "CREATE SUBSCRIPTION sub_conn_inventory CONNECTION '" + connection.String() + "' PUBLICATION p WITH (connect=false,slot_name=NONE)"
		for _, b := range p.Backends() {
			for _, prepared := range []bool{false, true} {
				subscriptionExec(t, p, b, b.DB, query, prepared)
				subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subconninfo FROM pg_subscription WHERE subname='sub_conn_inventory'", []string{"subconninfo"}, []string{"TEXT"}, [][]any{{connection.String()}}))
				subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_conn_inventory", prepared)
				subscriptionRun(t, p, b, b.DB, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_conn_inventory'", false))
			}
		}
	})
	t.Run("synchronous_commit_aliases", func(t *testing.T) {
		p := subscriptionScenario(t, "sub_sync_alias")
		for _, b := range p.Backends() {
			for _, prepared := range []bool{false, true} {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, value := range []string{"yes", "no", "1", "0", "TRUE", "OFF", "local", "REMOTE_WRITE", "remote_apply"} {
					subscriptionExec(t, p, b, conn, "CREATE SUBSCRIPTION sub_sync_alias CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE,synchronous_commit='"+value+"')", prepared)
					subscriptionRun(t, p, b, conn, fixture.Select("SELECT subsynccommit,current_setting('synchronous_commit') AS session_value FROM pg_subscription WHERE subname='sub_sync_alias'", []string{"subsynccommit", "session_value"}, []string{"TEXT", "TEXT"}, [][]any{{value, "on"}}))
					subscriptionExec(t, p, b, conn, "ALTER SUBSCRIPTION sub_sync_alias SET (synchronous_commit='"+value+"')", prepared)
					subscriptionRun(t, p, b, conn, fixture.Select("SELECT subsynccommit,current_setting('synchronous_commit') AS session_value FROM pg_subscription WHERE subname='sub_sync_alias'", []string{"subsynccommit", "session_value"}, []string{"TEXT", "TEXT"}, [][]any{{value, "on"}}))
					subscriptionExec(t, p, b, conn, "DROP SUBSCRIPTION sub_sync_alias", prepared)
					subscriptionRun(t, p, b, conn, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_sync_alias'", false))
				}
				conn.Close()
			}
		}
	})
}
func containsSubscriptionOption(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

// IDs are independently resolved relationships, not allocator goldens. Retaining
// this complete expected row also detects replacement or partial mutation.
func subscriptionValidationRow(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) fixture.Step {
	t.Helper()
	id := subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_subscription WHERE subname='"+name+"'")
	db := subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_database WHERE datname=current_database()")
	owner := subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_roles WHERE rolname=current_user")
	if id <= 0 || db <= 0 || owner <= 0 {
		t.Fatal("invalid subscription identity", id, db, owner)
	}
	step := subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='"+name+"'", false)
	step.Rows = [][]any{{id, db, "0/0", name, owner, false, false, "p", "d", false, true, false, false, "", nil, "off", "{p,q}", "any"}}
	return step
}

func TestReferenceSubscriptionExactMembership(t *testing.T) {
	// REL_18_6 parse_subscription_options uses strcmp; conninfo_storeval
	// looks up a whole decoded keyword, never a run of adjacent entries.
	for _, name := range []string{"binary streaming", "slot_name synchronous_commit"} {
		t.Run("option_"+name, func(t *testing.T) {
			p := subscriptionScenario(t, "sub_exact_membership", "sub_exact_invalid")
			for _, b := range p.Backends() {
				subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_exact_membership CONNECTION '' PUBLICATION p,q WITH (connect=false,slot_name=NONE)", false)
				row := subscriptionValidationRow(t, p, b, "sub_exact_membership")
				for _, prepared := range []bool{false, true} {
					option := `"` + name + `"=true`
					message := fmt.Sprintf(`unrecognized subscription parameter: "%s"`, name)
					subscriptionError(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_exact_invalid CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE,"+option+")", "42601", message, prepared)
					subscriptionRun(t, p, b, b.DB, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_exact_invalid'", false))
					subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_exact_membership SET (binary=true,"+option+")", "42601", message, prepared)
					fixture.OidReferenceRows(t, p.Ctx, b, b.DB, row)
				}
				subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_exact_membership", false)
			}
		})
	}
}

func TestReferenceSubscriptionValueKinds(t *testing.T) {
	// Released define.c distinguishes Integer nodes from String and Float nodes.
	for _, tc := range []struct{ option, message string }{
		{"binary='1'", "binary requires a Boolean value"},
		{"binary='0'", "binary requires a Boolean value"},
		{"binary=1.0", "binary requires a Boolean value"},
		{"binary=0e0", "binary requires a Boolean value"},
		{"streaming='1'", `streaming requires a Boolean value or "parallel"`},
		{"streaming='0'", `streaming requires a Boolean value or "parallel"`},
		{"streaming=1.0", `streaming requires a Boolean value or "parallel"`},
	} {
		t.Run(tc.option, func(t *testing.T) {
			p := subscriptionScenario(t, "sub_value_kinds", "sub_value_invalid")
			for _, b := range p.Backends() {
				subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_value_kinds CONNECTION '' PUBLICATION p,q WITH (connect=false,slot_name=NONE)", false)
				row := subscriptionValidationRow(t, p, b, "sub_value_kinds")
				for _, prepared := range []bool{false, true} {
					subscriptionError(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_value_invalid CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE,"+tc.option+")", "42601", tc.message, prepared)
					subscriptionRun(t, p, b, b.DB, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_value_invalid'", false))
					subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_value_kinds SET (run_as_owner=true,"+tc.option+")", "42601", tc.message, prepared)
					fixture.OidReferenceRows(t, p.Ctx, b, b.DB, row)
				}
				subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_value_kinds", false)
			}
		})
	}
	for _, tc := range []struct {
		value   string
		boolean bool
		stream  string
	}{
		{"01", true, "t"}, {"+01", true, "t"}, {"0x1", true, "t"}, {"0b1", true, "t"},
		{"-0", false, "f"}, {"00", false, "f"}, {"0o0", false, "f"}, {"0_0", false, "f"},
	} {
		t.Run("integer_"+tc.value, func(t *testing.T) {
			p := subscriptionScenario(t, "sub_numeric_value")
			for _, b := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					options := "binary=" + tc.value + ",streaming=" + tc.value + ",disable_on_error=" + tc.value + ",password_required=" + tc.value + ",run_as_owner=" + tc.value + ",synchronous_commit=" + tc.value
					subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_numeric_value CONNECTION '' PUBLICATION p,q WITH (connect=false,slot_name=NONE,"+options+")", prepared)
					row := subscriptionValidationRow(t, p, b, "sub_numeric_value")
					for _, i := range []int{6, 9, 10, 11} {
						row.Rows[0][i] = tc.boolean
					}
					row.Rows[0][7] = tc.stream
					row.Rows[0][15] = "0"
					if tc.boolean {
						row.Rows[0][15] = "1"
					}
					fixture.OidReferenceRows(t, p.Ctx, b, b.DB, row)
					subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_numeric_value SET (binary=off,streaming=parallel,disable_on_error=off,password_required=on,run_as_owner=off,synchronous_commit=off)", prepared)
					subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_numeric_value SET ("+options+")", prepared)
					fixture.OidReferenceRows(t, p.Ctx, b, b.DB, row)
					subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_numeric_value", prepared)
				}
			}
		})
	}
	for _, tc := range []struct{ value, stored string }{
		{"001", "1"}, {"'001'", "001"}, {"+001", "1"}, {"-0", "0"},
		{"0x0a", "10"}, {"1_0", "10"}, {"0002147483648", "0002147483648"},
	} {
		t.Run("string_"+tc.value, func(t *testing.T) {
			p := subscriptionScenario(t, "sub_numeric_string")
			for _, b := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_numeric_string CONNECTION '' PUBLICATION p,q WITH (connect=false,slot_name="+tc.value+")", prepared)
					row := subscriptionValidationRow(t, p, b, "sub_numeric_string")
					row.Rows[0][14] = tc.stored
					fixture.OidReferenceRows(t, p.Ctx, b, b.DB, row)
					subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_numeric_string SET (slot_name='different')", prepared)
					subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_numeric_string SET (slot_name="+tc.value+")", prepared)
					fixture.OidReferenceRows(t, p.Ctx, b, b.DB, row)
					subscriptionDetach(t, p, b, b.DB, "sub_numeric_string")
				}
			}
		})
	}
}

func TestReferenceSubscriptionURILexicalOrder(t *testing.T) {
	// REL_18_6 conninfo_uri_decode scans escapes before looking for trailing
	// literal spaces; decoded spaces never terminate the component.
	for _, tc := range []struct{ uri, diagnostic string }{
		{"postgresql:///db?host%20hostaddr=x", `invalid URI query parameter: "host hostaddr"`},
		{"postgresql:///db?host%09hostaddr=x", "invalid URI query parameter: \"host\thostaddr\""},
		{"postgresql:///db?=x", `invalid URI query parameter: ""`},
		{"postgresql:///bad%zz x", `invalid percent-encoded token: "bad%zz x"`},
		{"postgresql:///  bad%zz x  ", `invalid percent-encoded token: "  bad%zz x  "`},
		{"postgresql:///bad%00 x", `forbidden value %00 in percent-encoded value: "bad%00 x"`},
		{"postgresql:///bad x%zz", `unexpected spaces found in "bad x%zz", use percent-encoded spaces (%20) instead`},
		{"postgresql:///db?application_name=bad%zz x", `invalid percent-encoded token: "bad%zz x"`},
	} {
		t.Run(tc.uri, func(t *testing.T) {
			p := subscriptionScenario(t, "sub_uri_order", "sub_uri_invalid")
			for _, b := range p.Backends() {
				subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_uri_order CONNECTION '' PUBLICATION p,q WITH (connect=false,slot_name=NONE)", false)
				row := subscriptionValidationRow(t, p, b, "sub_uri_order")
				for _, prepared := range []bool{false, true} {
					message := "invalid connection string syntax: " + tc.diagnostic + "\n"
					subscriptionError(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_uri_invalid CONNECTION '"+tc.uri+"' PUBLICATION p WITH (connect=false,slot_name=NONE)", "42601", message, prepared)
					subscriptionRun(t, p, b, b.DB, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_uri_invalid'", false))
					subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_uri_order CONNECTION '"+tc.uri+"'", "42601", message, prepared)
					fixture.OidReferenceRows(t, p.Ctx, b, b.DB, row)
				}
				subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_uri_order", false)
			}
		})
	}
	for _, uri := range []string{"postgresql:///  db  ", "postgresql:///a%20b", "postgresql:///db?application_name= %20x%20 "} {
		t.Run("valid_"+uri, func(t *testing.T) {
			p := subscriptionScenario(t, "sub_uri_valid")
			for _, b := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_uri_valid CONNECTION '"+uri+"' PUBLICATION p,q WITH (connect=false,slot_name=NONE)", prepared)
					row := subscriptionValidationRow(t, p, b, "sub_uri_valid")
					row.Rows[0][13] = uri
					fixture.OidReferenceRows(t, p.Ctx, b, b.DB, row)
					subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_uri_valid CONNECTION '"+uri+"'", prepared)
					fixture.OidReferenceRows(t, p.Ctx, b, b.DB, row)
					subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_uri_valid", prepared)
				}
			}
		})
	}
}
