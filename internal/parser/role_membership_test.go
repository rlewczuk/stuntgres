package parser

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestMembershipSymbolicGrammar(t *testing.T) {
	statements, err := Parse("GRANT r TO CURRENT_USER,SESSION_USER WITH ADMIN FALSE, INHERIT OPTION, ADMIN TRUE GRANTED BY CURRENT_ROLE; REVOKE SET OPTION FOR r FROM m GRANTED BY g CASCADE; ALTER GROUP CURRENT_USER ADD USER m; ALTER GROUP g DROP USER SESSION_USER")
	if err != nil {
		t.Fatal(err)
	}
	grant := statements[0].(*RoleMembership)
	if grant.Members[0].Identity != "current" || grant.Members[1].Identity != "session" || grant.Grantor.Identity != "current" || len(grant.Options) != 3 || grant.Options[0].Value || !grant.Options[1].Value || !grant.Options[2].Value {
		t.Fatal(grant)
	}
	revoke := statements[1].(*RoleMembership)
	if !revoke.Revoke || !revoke.Cascade || revoke.RevokeOption != "set" || revoke.Grantor.Name != "g" {
		t.Fatal(revoke)
	}
	add := statements[2].(*RoleMembership)
	drop := statements[3].(*RoleMembership)
	if !add.Alias || add.Revoke || add.Targets[0].Identity != "current" || !drop.Alias || !drop.Revoke || drop.Members[0].Identity != "session" {
		t.Fatal(add, drop)
	}
	statements, err = Parse("CREATE ROLE n LOGIN IN GROUP p USER m ADMIN a")
	if err != nil {
		t.Fatal(err)
	}
	create := statements[0].(*CreateRole)
	var kinds []string
	for _, option := range create.Options {
		kinds = append(kinds, option.Kind)
	}
	if !create.Login || !reflect.DeepEqual(kinds, []string{"login", "in", "role", "admin"}) {
		t.Fatal(create)
	}
	// Unknown and repeated option names remain ordered for execution validation.
	statements, err = Parse("GRANT r TO m WITH mystery TRUE, SET FALSE, SET TRUE")
	if err != nil || len(statements[0].(*RoleMembership).Options) != 3 {
		t.Fatal(statements, err)
	}
}

func TestMembershipGrammarDiagnostics(t *testing.T) {
	for _, tc := range []struct{ query, code, message, position string }{
		{"GRANT r TO none", "42939", `role name "none" is reserved`, "12"},
		{"GRANT r TO m WITH ADMIN 1", "42601", `syntax error at or near "1"`, "25"},
		{"ALTER GROUP r ADD USER m CASCADE", "42601", `syntax error at or near "CASCADE"`, "26"},
	} {
		statements, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
		if statements != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatal(tc.query, statements, err, want)
		}
	}
	for _, q := range []string{"CREATE ROLE earlier; GRANT r TO $1", "GRANT r TO 'm'", "REVOKE r FROM m WITH ADMIN TRUE", "ALTER GROUP r DROP USER m GRANTED BY g"} {
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(q, statements, err)
		}
	}
	for _, q := range []string{"GRANT SELECT ON t TO m", "REVOKE SELECT ON TABLE t FROM m"} {
		_, err := Parse(q)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal("object grant enabled", q, err)
		}
	}
}
