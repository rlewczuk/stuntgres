package remote_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/remote"
)

func TestDblinkConnectionConninfo(t *testing.T) {
	t.Setenv("PGPASSWORD", "ambient-cannot-prove-original-secret")
	for _, tc := range []struct {
		input      string
		password   bool
		diagnostic string
	}{
		{"", false, ""},
		{" service=ambient passfile=/ambient user=remote ", false, ""},
		{"password=", false, ""},
		{"password='secret' password=''", false, ""},
		{" password = 'p\\'a\\\\ss word' ", true, ""},
		{`password=p\ a\'ss\\word`, true, ""},
		{"password=ignored password=", false, ""},
		{"postgresql://user:@localhost/database", false, ""},
		{"postgres://user:p%20a%2Bss@[::1]:5432/db?password=", false, ""},
		{"postgresql://wrong:bad@one:5432,two:5433/db?user=remote&password=p%20a%2Bss", true, ""},
		{"postgresql://localhost/db?password=+", true, ""},
		{"dbname", false, `missing "=" after "dbname" in connection info string`},
		{"dbname='x", false, "unterminated quoted string in connection info string"},
		{"dbc_unknown=x", false, `invalid connection option "dbc_unknown"`},
		{"postgresql://localhost:invalid/postgres", false, `invalid integer value "invalid" for connection option "port"`},
		{"postgresql://localhost/db?port=+", false, `invalid integer value "+" for connection option "port"`},
	} {
		parsed, err := remote.ParseConninfo(tc.input)
		if err == nil {
			err = parsed.Validate()
		}
		if parsed.PasswordPresent != tc.password || (err == nil) != (tc.diagnostic == "") || err != nil && err.Error() != tc.diagnostic {
			t.Fatalf("credential/parser outcome got password=%t err=%v; want password=%t diagnostic=%q", parsed.PasswordPresent, err, tc.password, tc.diagnostic)
		}
	}
	for _, input := range []string{"postgresql://user:p%00ss@localhost/db", "postgresql://user:p%zz@localhost/db", "postgresql://[::1/db"} {
		if parsed, err := remote.ParseConninfo(input); err == nil || parsed.PasswordPresent {
			t.Fatal("invalid URI admitted")
		}
	}
	encoded := remote.ConninfoOptions(nil, []catalog.ForeignOption{{Name: "password", Value: `a'b\c`}, {Name: "user", Value: "other role"}})
	if encoded != `password='a\'b\\c' user='other role'` {
		t.Fatal("escaped option boundary", encoded)
	}
	if parsed, err := remote.ParseConninfo(encoded); err != nil || !parsed.PasswordPresent {
		t.Fatal("escaped original secret", parsed.PasswordPresent, err)
	}
	for _, name := range []string{"replication", "client_encoding", "oauth_client_secret", "not_a_parameter"} {
		if remote.DblinkOption(name, false) || remote.DblinkOption(name, true) {
			t.Fatal("invalid alias option context", name)
		}
	}
	if !remote.DblinkOption("password", true) || remote.DblinkOption("password", false) || !remote.DblinkOption("host", false) || remote.DblinkOption("host", true) {
		t.Fatal("mapping/server credential separation")
	}
	parsed, err := remote.ParseConninfo("password=explicit port=invalid")
	if err != nil || !parsed.PasswordPresent {
		t.Fatal("numeric validation contaminated syntax-only credential admission", err)
	}
	if err := parsed.Validate(); err == nil || err.Error() != `invalid integer value "invalid" for connection option "port"` {
		t.Fatal("deferred numeric validation", err)
	}
}

func TestDblinkConnectionPortBounds(t *testing.T) {
	for _, tc := range []struct{ port, diagnostic string }{
		{"", ""},
		{"1", ""},
		{"65535", ""},
		{"1,65535", ""},
		{"0", `invalid port number: "0"`},
		{"65536", `invalid port number: "65536"`},
		{"-1", `invalid port number: "-1"`},
		{"65535,0", `invalid port number: "0"`},
		{"65536,1", `invalid port number: "65536"`},
		{"2147483647", `invalid port number: "2147483647"`},
		{"-2147483648", `invalid port number: "-2147483648"`},
		{"2147483648", `invalid integer value "2147483648" for connection option "port"`},
	} {
		for _, input := range []string{
			"password=explicit port='" + tc.port + "'",
			"postgresql://localhost/postgres?password=explicit&port=" + tc.port,
		} {
			parsed, err := remote.ParseConninfo(input)
			if err != nil || !parsed.PasswordPresent {
				t.Fatal("port validation ran before syntax-only credential admission", tc.port, err)
			}
			err = parsed.Validate()
			if (err == nil) != (tc.diagnostic == "") || err != nil && err.Error() != tc.diagnostic {
				t.Fatalf("port %q validation got %v; want %q", tc.port, err, tc.diagnostic)
			}
		}
	}
}

func TestDblinkConnectionOwnerNames(t *testing.T) {
	for _, tc := range []struct{ raw, key string }{
		{"", ""}, {"=", "="}, {"A", "A"}, {"a", "a"},
		{strings.Repeat("a", 63), strings.Repeat("a", 63)},
		{strings.Repeat("a", 64), strings.Repeat("a", 63)},
		{strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)},
		{strings.Repeat("a", 61) + "éx", strings.Repeat("a", 61) + "é"},
	} {
		if key := remote.DblinkName(tc.raw); key != tc.key || !utf8.ValidString(key) {
			t.Fatal("physical-session key boundary", key, tc.key)
		}
	}
}

func TestDblinkConnectionEstablishmentError(t *testing.T) {
	cause := errors.New("actual connection refused")
	marked := &catalog.ExternalSQLConnectError{Err: cause}
	if marked.Error() != cause.Error() || !errors.Is(marked, cause) {
		t.Fatal("host establishment cause lost")
	}
	if _, sql := any(marked).(interface{ SQLState() string }); sql {
		t.Fatal("host establishment marker fabricated SQLSTATE")
	}
	var matched *catalog.ExternalSQLConnectError
	if !errors.As(marked, &matched) || matched != marked {
		t.Fatal("host marker identity lost")
	}
}
