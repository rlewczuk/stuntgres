package subscription

import (
	"fmt"
	"reflect"
	"strings"
	"stuntgres/internal/engine/replication"
	"stuntgres/internal/parser"
	"testing"
)

func validationOption(name, value string, numeric bool) parser.DictionaryOption {
	return parser.DictionaryOption{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: name}, Value: value, Numeric: numeric}, Supplied: true}
}

func TestSubscriptionDefElemConversions(t *testing.T) {
	for _, tc := range []struct {
		value   string
		numeric bool
		text    string
		boolean bool
		valid   bool
	}{
		{"01", true, "1", true, true}, {"+01", true, "1", true, true}, {"-0", true, "0", false, true},
		{"0x01", true, "1", true, true}, {"0b1", true, "1", true, true}, {"0o0", true, "0", false, true}, {"0_0", true, "0", false, true},
		{"1", false, "1", false, false}, {"0", false, "0", false, false}, {"01", false, "01", false, false},
		{"TRUE", false, "TRUE", true, true}, {"On", false, "On", true, true}, {"OFF", false, "OFF", false, true}, {"false", false, "false", false, true},
		{"yes", false, "yes", false, false}, {"t", false, "t", false, false}, {"2", true, "2", false, false},
		{"1.0", true, "1.0", false, false}, {"0e0", true, "0e0", false, false},
		{"0002147483647", true, "2147483647", false, false}, {"0002147483648", true, "0002147483648", false, false},
		{"-02147483648", true, "-02147483648", false, false}, {"1_0", true, "10", false, false},
	} {
		t.Run(fmt.Sprintf("%s_%t", tc.value, tc.numeric), func(t *testing.T) {
			o := validationOption("binary", tc.value, tc.numeric)
			if got := optionString(o); got != tc.text {
				t.Fatalf("defGetString = %q, want %q", got, tc.text)
			}
			got, err := boolean(o)
			if tc.valid {
				if err != nil || got != tc.boolean {
					t.Fatalf("defGetBoolean = %t, %v", got, err)
				}
			} else if !reflect.DeepEqual(err, failure("42601", "binary requires a Boolean value")) {
				t.Fatalf("defGetBoolean error = %#v", err)
			}
		})
	}
	if got, err := boolean(parser.DictionaryOption{}); !got || err != nil {
		t.Fatal("bare boolean", got, err)
	}
}

func TestSubscriptionOptionInventory(t *testing.T) {
	ordinary := strings.Fields("slot_name synchronous_commit binary streaming two_phase disable_on_error password_required run_as_owner failover origin")
	command := strings.Fields("connect enabled create_slot copy_data refresh lsn")
	for _, action := range []string{"create", "options", "set", "add", "remove", "refresh", "skip", "connection"} {
		for _, name := range append(ordinary, command...) {
			want := false
			for _, candidate := range ordinary {
				if candidate == name && (action == "create" || action == "options") {
					want = true
				}
			}
			switch name {
			case "connect", "enabled", "create_slot":
				want = action == "create"
			case "copy_data":
				want = action == "create" || action == "set" || action == "add" || action == "remove" || action == "refresh"
			case "refresh":
				want = action == "set" || action == "add" || action == "remove"
			case "lsn":
				want = action == "skip"
			}
			if got := allowedOption(action, name); got != want {
				t.Fatalf("%s/%s = %t, want %t", action, name, got, want)
			}
		}
		for _, name := range []string{"", "binary streaming", " binary", "binary ", "BINARY", "refresh copy_data", "unknown"} {
			if allowedOption(action, name) {
				t.Fatalf("accepted %s/%q", action, name)
			}
		}
	}
}

func TestSubscriptionConnectionInventory(t *testing.T) {
	// Independent released PQconninfoOptions inventory, including legacy alias.
	for _, key := range strings.Fields("service user password passfile channel_binding connect_timeout dbname host hostaddr port client_encoding options application_name fallback_application_name keepalives keepalives_idle keepalives_interval keepalives_count tcp_user_timeout sslmode sslnegotiation sslcompression sslcert sslkey sslcertmode sslpassword sslrootcert sslcrl sslcrldir sslsni requirepeer require_auth min_protocol_version max_protocol_version ssl_min_protocol_version ssl_max_protocol_version gssencmode krbsrvname gsslib gssdelegation replication target_session_attrs load_balance_hosts scram_client_key scram_server_key oauth_issuer oauth_client_id oauth_client_secret oauth_scope sslkeylogfile requiressl") {
		if _, err := parseConnection(key + "=x"); err != nil {
			t.Fatalf("keyword %s: %v", key, err)
		}
		if _, err := parseConnection("postgresql:///db?" + key + "=x"); err != nil {
			t.Fatalf("URI keyword %s: %v", key, err)
		}
	}
	for _, key := range []string{"", "host hostaddr", "host\thostaddr", " host", "host ", "HOST", "unknown"} {
		if connectionKeyword(key) {
			t.Fatalf("accepted keyword %q", key)
		}
	}
}

func TestSubscriptionURIDecodeOrder(t *testing.T) {
	for _, tc := range []struct{ raw, value, diagnostic string }{
		{"", "", ""}, {"   ", "", ""}, {"  db  ", "db", ""}, {"a%20b", "a b", ""}, {" %20x%20 ", " x ", ""}, {"\t", "\t", ""},
		{"bad%zz x", "", `invalid percent-encoded token: "bad%zz x"`},
		{"  bad%zz x  ", "", `invalid percent-encoded token: "  bad%zz x  "`},
		{"bad%00 x", "", `forbidden value %00 in percent-encoded value: "bad%00 x"`},
		{"bad x%zz", "", `unexpected spaces found in "bad x%zz", use percent-encoded spaces (%20) instead`},
		{"%", "", `invalid percent-encoded token: "%"`}, {"%0", "", `invalid percent-encoded token: "%0"`},
	} {
		got, err := decodeURI(tc.raw)
		if tc.diagnostic == "" {
			if err != nil || got != tc.value {
				t.Fatalf("decode %q = %q, %v", tc.raw, got, err)
			}
		} else if !reflect.DeepEqual(err, connectionError("%s", tc.diagnostic)) {
			t.Fatalf("decode %q error = %#v", tc.raw, err)
		}
	}
}

func TestSubscriptionConnectionLexical(t *testing.T) {
	for _, tc := range []struct {
		text       string
		password   bool
		diagnostic string
	}{
		{"", false, ""}, {"port=-1 service=missing passfile=missing", false, ""},
		{"password=first password=", false, ""}, {"password= password='last'", true, ""},
		{"password='a\\'b' application_name=a\\ b", true, ""},
		{"postgresql://u:p@[::1]:-1/db?password=", false, ""},
		{"postgres://u@host1:1,host2:2/db?password=p&ssl=true", true, ""},
		{"testconn", false, `missing "=" after "testconn" in connection info string`},
		{"unknown=x", false, `invalid connection option "unknown"`},
		{"host='unterminated", false, "unterminated quoted string in connection info string"},
		{"postgresql:///db?host%20hostaddr=x", false, `invalid URI query parameter: "host hostaddr"`},
		{"postgresql:///db?=x", false, `invalid URI query parameter: ""`},
		{"postgresql:///db?host", false, `missing key/value separator "=" in URI query parameter: "host"`},
		{"postgresql:///db?host=a=b", false, `extra key/value separator "=" in URI query parameter: "host"`},
	} {
		got, err := parseConnection(tc.text)
		if tc.diagnostic == "" {
			if err != nil || got != tc.password {
				t.Fatalf("parse %q = %t, %v", tc.text, got, err)
			}
		} else if !reflect.DeepEqual(err, connectionError("%s", tc.diagnostic)) {
			t.Fatalf("parse %q error = %#v", tc.text, err)
		}
	}
}

func TestSubscriptionSlotValidation(t *testing.T) {
	for _, name := range []string{"0", "slot_1", strings.Repeat("s", 63)} {
		if err := replication.ValidateSlotName(name); err != nil {
			t.Fatal(name, err)
		}
	}
	for _, tc := range []struct{ name, code, message, hint string }{
		{"", "42602", `replication slot name "" is too short`, ""},
		{strings.Repeat("s", 64), "42622", `replication slot name "` + strings.Repeat("s", 64) + `" is too long`, ""},
		{"雪", "42602", `replication slot name "雪" contains invalid character`, "Replication slot names may only contain lower case letters, numbers, and the underscore character."},
		{"Upper", "42602", `replication slot name "Upper" contains invalid character`, "Replication slot names may only contain lower case letters, numbers, and the underscore character."},
	} {
		want := failure(tc.code, tc.message)
		want.Hint = tc.hint
		if got := replication.ValidateSlotName(tc.name); !reflect.DeepEqual(got, want) {
			t.Fatalf("slot %q = %#v", tc.name, got)
		}
	}
}

func TestSubscriptionOptionDefaultsConflicts(t *testing.T) {
	for _, action := range []string{"create", "options", "set", "add", "remove", "refresh", "skip"} {
		o, err := parseOptions(&parser.Subscription{Action: action}, nil)
		if err != nil || !o.connect || !o.copyData || !o.refresh || o.enabled != (action == "create") || o.createSlot != (action == "create") || len(o.values) != 0 || len(o.supplied) != 0 {
			t.Fatalf("defaults %s: %#v, %v", action, o, err)
		}
	}
	off := validationOption("connect", "false", false)
	none := validationOption("slot_name", "none", false)
	for _, tc := range []struct {
		action  string
		options []parser.DictionaryOption
		message string
	}{
		{"create", []parser.DictionaryOption{off, validationOption("enabled", "true", false)}, "connect = false and enabled = true are mutually exclusive options"},
		{"create", []parser.DictionaryOption{off, validationOption("create_slot", "true", false)}, "connect = false and create_slot = true are mutually exclusive options"},
		{"create", []parser.DictionaryOption{off, validationOption("copy_data", "true", false)}, "connect = false and copy_data = true are mutually exclusive options"},
		{"create", []parser.DictionaryOption{none}, "subscription with slot_name = NONE must also set enabled = false"},
		{"create", []parser.DictionaryOption{none, validationOption("enabled", "false", false)}, "subscription with slot_name = NONE must also set create_slot = false"},
		{"create", []parser.DictionaryOption{none, validationOption("enabled", "true", false)}, "slot_name = NONE and enabled = true are mutually exclusive options"},
		{"create", []parser.DictionaryOption{none, validationOption("enabled", "false", false), validationOption("create_slot", "true", false)}, "slot_name = NONE and create_slot = true are mutually exclusive options"},
		{"create", []parser.DictionaryOption{validationOption("binary", "yes", false), validationOption("unknown", "true", false)}, "binary requires a Boolean value"},
		{"options", []parser.DictionaryOption{validationOption("binary streaming", "true", false), validationOption("binary", "yes", false)}, `unrecognized subscription parameter: "binary streaming"`},
		{"options", []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "origin"}}}}, "origin requires a parameter"},
		{"options", []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "slot_name"}}}}, "slot_name requires a parameter"},
		{"options", []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "synchronous_commit"}}}}, "synchronous_commit requires a parameter"},
		{"skip", []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "lsn"}}}}, "lsn requires a parameter"},
	} {
		_, err := parseOptions(&parser.Subscription{Action: tc.action, Options: tc.options}, nil)
		if !reflect.DeepEqual(err, failure("42601", tc.message)) {
			t.Fatalf("%s/%v: %#v", tc.action, tc.options, err)
		}
	}
	o, err := parseOptions(&parser.Subscription{Action: "create", Options: []parser.DictionaryOption{off, none}}, nil)
	if err != nil || o.enabled || o.createSlot || o.copyData || o.connect {
		t.Fatal("disconnected defaults", o, err)
	}
	repeated := validationOption("binary", "false", false)
	repeated.Name.Pos.Character = 19
	_, err = parseOptions(&parser.Subscription{Action: "options", Options: []parser.DictionaryOption{validationOption("binary", "true", false), repeated}}, nil)
	want := failure("42601", "conflicting or redundant options")
	want.Position = "19"
	if !reflect.DeepEqual(err, want) {
		t.Fatalf("duplicate position: %#v", err)
	}
	for _, tc := range []struct {
		value, stored string
		numeric       bool
	}{
		{"parallel", "p", false}, {"PARALLEL", "p", false}, {"01", "t", true}, {"-0", "f", true}, {"off", "f", false},
	} {
		o, err := parseOptions(&parser.Subscription{Action: "options", Options: []parser.DictionaryOption{validationOption("streaming", tc.value, tc.numeric)}}, nil)
		if err != nil || o.values["streaming"] != tc.stored {
			t.Fatal("streaming", tc, o, err)
		}
	}
}
