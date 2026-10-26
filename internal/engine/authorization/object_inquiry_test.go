package authorization

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestAccessExecutionPrivilegeParsing(t *testing.T) {
	for _, tc := range []struct {
		text                       string
		allowed, ordinary, options Privileges
	}{
		{"execute", ExecutePrivilege, ExecutePrivilege, 0},
		{" USage \t", UsagePrivilege, UsagePrivilege, 0},
		{"EXECUTE WITH GRANT OPTION", ExecutePrivilege, 0, ExecutePrivilege},
		{" execute, EXECUTE WITH GRANT OPTION ", ExecutePrivilege, ExecutePrivilege, ExecutePrivilege},
		{"USAGE,usage", UsagePrivilege, UsagePrivilege, 0},
	} {
		ordinary, options, err := ParseObjectPrivileges(tc.text, tc.allowed)
		if err != nil || ordinary != tc.ordinary || options != tc.options {
			t.Fatal(tc, ordinary, options, err)
		}
	}
	for _, tc := range []struct{ text, offending string }{
		{"", ""}, {",", ""}, {"EXECUTE,", ""}, {",EXECUTE", ""}, {"EXECUTE,bad", "bad"}, {"ALL", "ALL"}, {"USAGE", "USAGE"}, {"EXECUTE  WITH GRANT OPTION", "EXECUTE  WITH GRANT OPTION"}, {"EXECUTE\tWITH GRANT OPTION", "EXECUTE\tWITH GRANT OPTION"}, {"EXECUTE WITH GRANT OPTION suffix", "EXECUTE WITH GRANT OPTION suffix"},
	} {
		_, _, err := ParseObjectPrivileges(tc.text, ExecutePrivilege)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != `unrecognized privilege type: "`+tc.offending+`"` {
			t.Fatal(tc, err)
		}
	}
}

func TestAccessNamespacesPrivilegeParsing(t *testing.T) {
	const database = CreatePrivilege | ConnectPrivilege | TemporaryPrivilege
	const schema = CreatePrivilege | UsagePrivilege
	for _, tc := range []struct {
		text                       string
		allowed, ordinary, options Privileges
	}{
		{"CREATE,CONNECT", database, CreatePrivilege | ConnectPrivilege, 0},
		{"CREATE WITH GRANT OPTION,CONNECT", database, ConnectPrivilege, CreatePrivilege},
		{"CREATE WITH GRANT OPTION,CONNECT WITH GRANT OPTION", database, 0, CreatePrivilege | ConnectPrivilege},
		{"TEMP,TEMPORARY WITH GRANT OPTION", database, TemporaryPrivilege, TemporaryPrivilege},
		{"temporary,TeMp WITH GRANT OPTION", database, TemporaryPrivilege, TemporaryPrivilege},
		{"\r\n create, USAGE \t", schema, CreatePrivilege | UsagePrivilege, 0},
		{"usage with grant option", UsagePrivilege, 0, UsagePrivilege},
		{"CREATE", CreatePrivilege, CreatePrivilege, 0},
	} {
		ordinary, options, err := ParseObjectPrivileges(tc.text, tc.allowed)
		if err != nil || ordinary != tc.ordinary || options != tc.options {
			t.Fatal(tc, ordinary, options, err)
		}
	}
	for _, tc := range []struct {
		text, offending string
		allowed         Privileges
	}{
		{"CONNECT, bad,ALL", "bad", database},
		{"USAGE,", "", schema},
		{"TEMP", "TEMP", schema},
		{"CREATE", "CREATE", UsagePrivilege},
		{"USAGE", "USAGE", CreatePrivilege},
		{"USAGE  WITH GRANT OPTION", "USAGE  WITH GRANT OPTION", schema},
		{"USAGE\tWITH GRANT OPTION", "USAGE\tWITH GRANT OPTION", schema},
	} {
		ordinary, options, err := ParseObjectPrivileges(tc.text, tc.allowed)
		var diagnostic *sqlstate.Error
		if ordinary != 0 || options != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != `unrecognized privilege type: "`+tc.offending+`"` {
			t.Fatal(tc, ordinary, options, err)
		}
	}
}
