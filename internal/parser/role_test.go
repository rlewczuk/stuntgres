package parser

import (
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestRoleGrammarAndIdentityProjection(t *testing.T) {
	for _, tc := range []struct {
		sql, name string
		login     bool
	}{
		{"CREATE ROLE Foo", "foo", false},
		{"CREATE USER Foo", "foo", true},
		{"CREATE GROUP Foo", "foo", false},
		{`CREATE ROLE "A" WITH LOGIN`, "A", true},
		{"CREATE USER a NOLOGIN", "a", false},
		{"CREATE ROLE local WITH LOGIN", "local", true},
		{"CREATE ROLE session NOLOGIN", "session", false},
		{"CREATE ROLE role WITH NOLOGIN", "role", false},
		{`CREATE ROLE "with" WITH LOGIN`, "with", true},
	} {
		stmts, err := Parse(tc.sql)
		if err != nil {
			t.Fatal(tc.sql, err)
		}
		role := stmts[0].(*CreateRole)
		login := role.DefaultLogin
		if role.Options.Present&RoleLogin != 0 {
			login = role.Options.Login
		}
		if role.Name.Text != tc.name || login != tc.login {
			t.Fatal(role)
		}
	}
	stmts, err := Parse("CREATE ROLE a LOGIN NOLOGIN; DROP USER a,a; SET SESSION AUTHORIZATION 'A'; RESET SESSION AUTHORIZATION; SELECT current_user,session_user,current_user")
	if err != nil {
		t.Fatal(err)
	}
	create := stmts[0].(*CreateRole)
	if create.Options.Present != RoleLogin || create.Options.Duplicate.Character != 21 {
		t.Fatal(create)
	}
	if !reflect.DeepEqual(stmts[1].(*DropRole).Names, []string{"a", "a"}) {
		t.Fatal(stmts[1])
	}
	if stmts[2].(*SessionAuthorization).Name != "A" || !stmts[3].(*SessionAuthorization).Default {
		t.Fatal(stmts)
	}
	selectStmt := stmts[4].(*Select)
	for i, want := range []string{"current_user", "session_user", "current_user"} {
		if selectStmt.Targets[i].(IdentityExpr).Name != want {
			t.Fatal(selectStmt)
		}
	}
	if _, err := Parse("CREATE TABLE role(login INT,nologin TEXT,reset BOOL)"); err != nil {
		t.Fatal("nonreserved identifier regression", err)
	}
}
func TestRoleGrammarFailureBoundaries(t *testing.T) {
	for _, tc := range []struct{ sql, code, message, position string }{
		{"CREATE ROLE public", "42939", `role name "public" is reserved`, "13"},
		{`CREATE USER "none"`, "42939", `role name "none" is reserved`, "13"},
		{"CREATE GROUP none", "42939", `role name "none" is reserved`, "14"},
		{"CREATE ROLE CURRENT_USER", "42939", "CURRENT_USER cannot be used as a role name here", "13"},
		{"CREATE USER SESSION_USER", "42939", "SESSION_USER cannot be used as a role name here", "13"},
		{"CREATE GROUP CURRENT_ROLE", "42939", "CURRENT_ROLE cannot be used as a role name here", "14"},
		// ADMIN requires a member; EOF is after all 24 source characters.
		{"CREATE ROLE a WITH ADMIN", "42601", "syntax error at end of input", "25"},
		{"CREATE ROLE a SYSID 12", "0A000", "unsupported role option", "15"},
		{"CREATE ROLE a WITH WITH LOGIN", "42601", `syntax error at or near "WITH"`, "20"},
		{"CREATE ROLE a LOGIN WITH NOLOGIN", "42601", `syntax error at or near "WITH"`, "21"},
		{"CREATE ROLE with LOGIN", "42601", `syntax error at or near "with"`, "13"},
		{"CREATE ROLE authorization LOGIN", "42601", `syntax error at or near "authorization"`, "13"},
		{"SET SESSION AUTHORIZATION $1", "42601", `syntax error at or near "$1"`, "27"},
	} {
		_, err := Parse(tc.sql)
		var got *sqlstate.Error
		if !errors.As(err, &got) {
			t.Fatal(tc.sql, err)
		}
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s got %#v want %#v", tc.sql, got, want)
		}
	}
	for _, sql := range []string{"SELECT session_user()", "SET SESSION AUTHORIZATION a garbage", "DROP ROLE a CASCADE", "CREATE ROLE a; garbage"} {
		if _, err := Parse(sql); err == nil {
			t.Fatal("accepted unsupported suffix", sql)
		}
	}
	for _, sql := range []string{`CREATE ROLE "PUBLIC"`, `CREATE USER "NONE"`} {
		if _, err := Parse(sql); err != nil {
			t.Fatal("quoted distinct reserved spelling", sql, err)
		}
	}
}

// PostgreSQL supports these commands; these are native capability exclusions,
// not PostgreSQL error-parity expectations.
func TestRoleExcludedCommands(t *testing.T) {
	for _, tc := range []struct{ sql, message, position string }{
		{"SET LOCAL ROLE reader", "SET LOCAL ROLE is unsupported", "11"},
		{"SET LOCAL ROLE 'reader'", "SET LOCAL ROLE is unsupported", "11"},
		{"SET LOCAL ROLE TO DEFAULT", "SET LOCAL ROLE is unsupported", "11"},
		{"SET LOCAL ROLE = NONE", "SET LOCAL ROLE is unsupported", "11"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			stmts, err := Parse(tc.sql)
			var got *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: tc.message, Position: tc.position}
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) || stmts != nil {
				t.Fatalf("statements=%#v diagnostic=%#v want=%#v", stmts, err, want)
			}
		})
	}
	for _, query := range []string{"SET ROLE", "SET SESSION ROLE", "SET LOCAL ROLE", "SET ROLE $1", "SET ROLE TO", "SET ROLE ="} {
		stmts, err := Parse(query)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "42601" || stmts != nil {
			t.Fatalf("malformed %s: statements=%#v diagnostic=%#v", query, stmts, err)
		}
	}
	for _, query := range []string{
		"CREATE TABLE role(role INT)",
		"INSERT INTO role(role) VALUES(7)",
		"SELECT role FROM role WHERE role=7 ORDER BY role",
		"CREATE ROLE role",
		"DROP ROLE role",
		"SET SESSION AUTHORIZATION role",
		"SHOW role",
		"SET application_name TO 'SET ROLE reader'",
		"/* SET ROLE reader */ SET application_name TO retained",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatalf("ordinary identifier/text %s: %v", query, err)
		}
	}
}

func TestRoleLocalSettingBoundary(t *testing.T) {
	for _, query := range []string{
		"SET LOCAL TO retained", "SET LOCAL = retained",
		"SET SESSION LOCAL TO retained", "SET SESSION LOCAL = retained",
		`SET "local" TO retained`,
	} {
		stmts, err := Parse(query)
		if err != nil || len(stmts) != 1 {
			t.Fatalf("%s: statements=%#v error=%v", query, stmts, err)
		}
		set, ok := stmts[0].(*Set)
		if !ok || set.Name.Text != "local" || set.Default || !reflect.DeepEqual(set.Values, []string{"retained"}) {
			t.Fatalf("%s: %#v", query, stmts[0])
		}
	}
	for _, query := range []string{"SET LOCAL TO DEFAULT", "SET SESSION LOCAL = DEFAULT"} {
		stmts, err := Parse(query)
		if err != nil || len(stmts) != 1 {
			t.Fatalf("%s: statements=%#v error=%v", query, stmts, err)
		}
		set, ok := stmts[0].(*Set)
		if !ok || set.Name.Text != "local" || !set.Default || len(set.Values) != 0 {
			t.Fatalf("%s: %#v", query, stmts[0])
		}
	}
	for _, query := range []string{"SET LOCAL TO", "SET LOCAL ="} {
		stmts, err := Parse(query)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "42601" || stmts != nil {
			t.Fatalf("%s: statements=%#v diagnostic=%#v", query, stmts, err)
		}
	}
}

func TestRoleKeywordNames(t *testing.T) {
	for _, k := range keywords {
		if !k.columnName() || k.word == "none" {
			continue
		}
		for _, query := range []string{"CREATE ROLE " + k.word + " WITH LOGIN", "DROP ROLE " + k.word, "SET SESSION AUTHORIZATION " + k.word} {
			stmts, err := Parse(query)
			if err != nil || len(stmts) != 1 {
				t.Fatalf("nonreserved role name %s: statements=%#v error=%v", query, stmts, err)
			}
		}
	}
}

// Conditional role removal is valid PostgreSQL syntax, but is only recognized
// here to reject it. Invalid conditional syntax must not acquire that category.
func TestRoleDropConditionalBoundary(t *testing.T) {
	for _, tc := range []struct{ sql, code, message, position string }{
		{"DROP ROLE IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "11"},
		{"DROP USER IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "11"},
		{"DROP GROUP IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "12"},
		{`DROP ROLE IF EXISTS if,exists,"if","exists","Reader"`, "0A000", "DROP ROLE IF EXISTS is unsupported", "11"},
		{`DROP USER IF EXISTS "if",exists`, "0A000", "DROP ROLE IF EXISTS is unsupported", "11"},
		{`DROP GROUP IF EXISTS if,"exists"`, "0A000", "DROP ROLE IF EXISTS is unsupported", "12"},
		{"DROP ROLE IF EXISTS absent", "0A000", "DROP ROLE IF EXISTS is unsupported", "11"},
		{"/* é */ DROP ROLE IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "19"},
		{"INSERT INTO sentinel VALUES(9); DROP ROLE IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "43"},
		{"INSERT INTO sentinel VALUES(9); DROP USER IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "43"},
		{"INSERT INTO sentinel VALUES(9); DROP GROUP IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "44"},
		{"CREATE ROLE leaked; DROP USER IF EXISTS reader", "0A000", "DROP ROLE IF EXISTS is unsupported", "31"},
		{"DROP ROLE IF EXISTS", "42601", "syntax error at end of input", "20"},
		{"DROP USER IF EXISTS", "42601", "syntax error at end of input", "20"},
		{"DROP GROUP IF EXISTS", "42601", "syntax error at end of input", "21"},
		{"DROP ROLE IF reader", "42601", `syntax error at or near "reader"`, "14"},
		{"DROP ROLE IF EXISTS ,reader", "42601", `syntax error at or near ","`, "21"},
		{"DROP ROLE IF EXISTS reader,", "42601", "syntax error at end of input", "28"},
		{"DROP ROLE IF EXISTS 'reader'", "42601", `syntax error at or near "'reader'"`, "21"},
		{"DROP ROLE IF EXISTS $1", "42601", `syntax error at or near "$1"`, "21"},
		{"DROP ROLE IF EXISTS reader CASCADE", "42601", `syntax error at or near "CASCADE"`, "28"},
		{"DROP ROLE reader CASCADE", "42601", `syntax error at or near "CASCADE"`, "18"},
		{"DROP USER reader CASCADE", "42601", `syntax error at or near "CASCADE"`, "18"},
		{"DROP GROUP reader CASCADE", "42601", `syntax error at or near "CASCADE"`, "19"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			stmts, err := Parse(tc.sql)
			var got *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) || stmts != nil {
				t.Fatalf("statements=%#v diagnostic=%#v want=%#v", stmts, err, want)
			}
		})
	}
	for _, kind := range []string{"ROLE", "USER", "GROUP"} {
		for _, tc := range []struct {
			targets string
			names   []string
		}{
			{"if", []string{"if"}},
			{"exists", []string{"exists"}},
			{`"if"`, []string{"if"}},
			{`"exists"`, []string{"exists"}},
			{`if,exists,"if","exists","IF","EXISTS"`, []string{"if", "exists", "if", "exists", "IF", "EXISTS"}},
		} {
			query := "DROP " + kind + " " + tc.targets
			stmts, err := Parse(query)
			if err != nil || len(stmts) != 1 {
				t.Fatalf("%s: statements=%#v error=%v", query, stmts, err)
			}
			drop, ok := stmts[0].(*DropRole)
			if !ok || !reflect.DeepEqual(drop.Names, tc.names) {
				t.Fatalf("%s: %#v", query, stmts[0])
			}
		}
	}
}
