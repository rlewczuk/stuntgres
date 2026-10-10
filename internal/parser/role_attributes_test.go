package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestRoleAttributesGrammar(t *testing.T) {
	for _, prefix := range []string{"CREATE ROLE r", "CREATE USER r", "CREATE GROUP r", "ALTER ROLE r", "ALTER USER r"} {
		for _, options := range []string{"", "WITH", `"login"`, "WITH SUPERUSER CREATEDB CREATEROLE LOGIN NOINHERIT REPLICATION BYPASSRLS CONNECTION LIMIT -1 PASSWORD 'secret' VALID UNTIL 'infinity'", "NOSUPERUSER NOCREATEDB NOCREATEROLE NOLOGIN INHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT +2147483647 PASSWORD NULL", "ENCRYPTED PASSWORD 'encoded'"} {
			stmts, err := Parse(prefix + " " + options)
			if err != nil || len(stmts) != 1 {
				t.Fatal(prefix, options, err)
			}
			var o RoleOptions
			switch s := stmts[0].(type) {
			case *CreateRole:
				o = s.Options
				if s.DefaultLogin != strings.HasPrefix(prefix, "CREATE USER") {
					t.Fatal("alias default", s)
				}
			case *AlterRole:
				o = s.Options
			default:
				t.Fatal(stmts)
			}
			if strings.Contains(options, "SUPERUSER CREATEDB") && (o.Present != RoleSuperuser|RoleCreateDB|RoleCreateRole|RoleLogin|RoleInherit|RoleReplication|RoleBypassRLS|RoleConnectionLimit|RolePassword|RoleValidUntil || !o.Superuser || !o.CreateDB || !o.CreateRole || !o.Login || o.Inherit || !o.Replication || !o.BypassRLS || o.ConnectionLimit != -1 || o.Password != "secret" || o.ValidUntil != "infinity") {
				t.Fatal("shared options", o)
			}
			if options == `"login"` && (!o.Login || o.Present != RoleLogin) {
				t.Fatal("decoded contextual option", o)
			}
		}
	}
	for _, spec := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER"} {
		stmts, err := Parse("ALTER ROLE " + spec + " PASSWORD NULL")
		if err != nil {
			t.Fatal(err)
		}
		if stmts[0].(*AlterRole).Identity != strings.ToLower(spec) {
			t.Fatal(stmts)
		}
	}
	for _, pair := range []string{"SUPERUSER NOSUPERUSER", "CREATEDB NOCREATEDB", "CREATEROLE NOCREATEROLE", "LOGIN NOLOGIN", "INHERIT NOINHERIT", "REPLICATION NOREPLICATION", "BYPASSRLS NOBYPASSRLS", "CONNECTION LIMIT 1 CONNECTION LIMIT 2", "PASSWORD NULL ENCRYPTED PASSWORD 'x'", "VALID UNTIL 'infinity' VALID UNTIL '-infinity'"} {
		stmts, err := Parse("ALTER USER r " + pair)
		if err != nil {
			t.Fatal(err)
		}
		o := stmts[0].(*AlterRole).Options
		if o.Duplicate.Character == 0 || o.Positions == ([10]Position{}) {
			t.Fatal("duplicate positions absent", o)
		}
	}
	for _, query := range []string{"CREATE TABLE password(connection INT,valid TEXT,encrypted INT)", "SELECT password FROM password", `CREATE ROLE "password" PASSWORD 'valid'`, "CREATE ROLE a LOGIN; ALTER USER a NOLOGIN"} {
		if _, err := Parse(query); err != nil {
			t.Fatal("context escaped role options", query, err)
		}
	}
}

func TestRoleAttributesContextualMembershipNames(t *testing.T) {
	for _, word := range []string{"password", "connection", "valid", "encrypted", "unencrypted"} {
		for _, members := range []string{word, "ordinary," + word, word + ",ordinary"} {
			for _, clause := range []string{"IN ROLE", "IN GROUP", "ROLE", "USER", "ADMIN"} {
				for _, alias := range []string{"ROLE", "USER", "GROUP"} {
					query := "CREATE " + alias + " child LOGIN " + clause + " " + members + " CONNECTION LIMIT 2 PASSWORD 'secret' VALID UNTIL 'infinity'"
					statements, err := Parse(query)
					if err != nil || len(statements) != 1 {
						t.Fatal(query, err)
					}
					create := statements[0].(*CreateRole)
					options := create.Options
					if !options.Login || options.ConnectionLimit != 2 || options.Password != "secret" || options.ValidUntil != "infinity" || len(options.InitialMemberships) != 1 {
						t.Fatal("membership name swallowed attributes", query, options)
					}
					got := options.InitialMemberships[0].Members
					want := strings.Split(members, ",")
					if len(got) != len(want) {
						t.Fatal("membership list changed", query, got)
					}
					for i, name := range want {
						if got[i].Name != name || got[i].Pos.Character == 0 {
							t.Fatal("membership text or position lost", query, got)
						}
					}
				}
			}
			for _, verb := range []string{"ADD", "DROP"} {
				query := "ALTER GROUP parent " + verb + " USER " + members
				statements, err := Parse(query)
				if err != nil || len(statements) != 1 {
					t.Fatal(query, err)
				}
				membership := statements[0].(*RoleMembership)
				want := strings.Split(members, ",")
				if !membership.Alias || membership.Revoke != (verb == "DROP") || len(membership.Members) != len(want) {
					t.Fatal(query, membership)
				}
				for i, name := range want {
					if membership.Members[i].Name != name || membership.Members[i].Pos.Character == 0 {
						t.Fatal("ALTER GROUP member text or position lost", query, membership)
					}
				}
			}
		}
	}
}
func TestRoleAttributesGrammarDiagnostics(t *testing.T) {
	cases := []struct{ options, token, code, message, hint string }{
		{"CONNECTION LIMIT 2147483648", "2147483648", "42601", `syntax error at or near "2147483648"`, ""},
		{"CONNECTION LIMIT '1'", "'1'", "42601", `syntax error at or near "'1'"`, ""},
		{"CONNECTION LIMIT $1", "$1", "42601", `syntax error at or near "$1"`, ""},
		{"CONNECTION LIMIT 1+1", "+", "42601", `syntax error at or near "+"`, ""},
		{"VALID UNTIL NULL", "NULL", "42601", `syntax error at or near "NULL"`, ""},
		{"VALID UNTIL $1", "$1", "42601", `syntax error at or near "$1"`, ""},
		{`"LOGIN"`, `"LOGIN"`, "42601", `unrecognized role option "LOGIN"`, ""},
		{"foo", "foo", "42601", `unrecognized role option "foo"`, ""},
		{"LOGIN trailing", "trailing", "42601", `syntax error at or near "trailing"`, ""},
		{"ENCRYPTED PASSWORD NULL", "NULL", "42601", `syntax error at or near "NULL"`, ""},
		{"UNENCRYPTED PASSWORD 'x'", "UNENCRYPTED", "0A000", "UNENCRYPTED PASSWORD is no longer supported", "Remove UNENCRYPTED to store the password in encrypted form instead."},
	}
	for _, prefix := range []string{"CREATE ROLE rag_absent ", "ALTER ROLE rag_target ", "CREATE USER rag_absent ", "ALTER USER rag_target "} {
		for _, tc := range cases {
			query := prefix + tc.options
			stmts, err := Parse(query)
			var d *sqlstate.Error
			if stmts != nil || !errors.As(err, &d) {
				t.Fatal(query, stmts, err)
			}
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint, Position: strconv.Itoa(strings.LastIndex(query, tc.token) + 1)}
			if *d != want {
				t.Fatalf("%s: %#v want %#v", query, d, want)
			}
		}
	}
	for _, query := range []string{"ALTER ROLE r SET application_name TO x", "ALTER ROLE r RESET ALL", "ALTER ROLE r RENAME TO other"} {
		_, err := Parse(query)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "statement is unsupported" || d.Position != "1" {
			t.Fatal("adjacent boundary", query, err)
		}
	}
}
