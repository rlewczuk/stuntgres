package parser

import (
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestObjectGrantGrammar(t *testing.T) {
	statements, err := Parse(`GRANT SELECT(a,b), INSERT, REFERENCES(a), TRIGGER, MAINTAIN ON TABLE s.t,u TO PUBLIC,GROUP reader,CURRENT_USER,SESSION_USER WITH GRANT OPTION GRANTED BY CURRENT_ROLE; REVOKE ALL PRIVILEGES(a,b) ON t FROM reader RESTRICT; GRANT ALL ON ALL TABLES IN SCHEMA public,s TO reader`)
	if err != nil || len(statements) != 3 {
		t.Fatal(statements, err)
	}
	grant := statements[0].(*ObjectGrant)
	if len(grant.Privileges) != 5 || len(grant.Privileges[0].Columns) != 2 || len(grant.Relations) != 2 || len(grant.Relations[0].Parts) != 2 || len(grant.Grantees) != 4 || !grant.Grantees[0].Public || grant.Grantees[1].Role.Name != "reader" || !grant.GrantOption || grant.Grantor.Identity != "current" {
		t.Fatal(grant)
	}
	if grant.Privileges[0].Pos.Character != 7 || grant.Privileges[0].Columns[0].Pos.Character != 14 {
		t.Fatal("positions", grant.Privileges[0])
	}
	revoke := statements[1].(*ObjectGrant)
	if !revoke.Revoke || revoke.Cascade || !revoke.Privileges[0].All || len(revoke.Privileges[0].Columns) != 2 {
		t.Fatal(revoke)
	}
	expansion := statements[2].(*ObjectGrant)
	if len(expansion.Schemas) != 2 || expansion.Schemas[1].Text != "s" {
		t.Fatal(expansion)
	}
}

func TestObjectGrantContextualNamesAndBoundaries(t *testing.T) {
	for _, query := range []string{
		`GRANT trigger,truncate,maintain TO options`,
		`GRANT SELECT ON trigger,truncate TO "PUBLIC"`,
		`GRANT bogus ON t TO reader`,
		`GRANT SELECT ON SEQUENCE s TO reader`,
		`REVOKE GRANT OPTION FOR SELECT ON t FROM reader CASCADE`,
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		if query == `GRANT SELECT ON trigger,truncate TO "PUBLIC"` && statements[0].(*ObjectGrant).Grantees[0].Public {
			t.Fatal("quoted uppercase name misclassified as PUBLIC")
		}
	}
	for _, query := range []string{
		`GRANT SELECT(a,) ON t TO reader`,
		`GRANT SELECT ON ALL wrong IN SCHEMA public TO reader`,
		`GRANT SELECT ON t TO reader WITH GRANT`,
		`GRANT SELECT ON t TO reader; SELECT )`,
		`GRANT SELECT(a) TO reader`,
	} {
		statements, err := Parse(query)
		if err == nil || statements != nil {
			t.Fatal("accepted malformed whole input", query, statements, err)
		}
	}
}

func TestObjectGrantPublicRoleSpecs(t *testing.T) {
	for _, spec := range []string{`PUBLIC`, `"public"`, `U&"public"`, `GROUP PUBLIC`, `GROUP "public"`} {
		statements, err := Parse(`GRANT SELECT ON t TO ` + spec)
		if err != nil || len(statements) != 1 || !statements[0].(*ObjectGrant).Grantees[0].Public {
			t.Fatal("decoded PUBLIC role spec", spec, statements, err)
		}
	}
	for _, spec := range []string{`GROUP CURRENT_USER`, `GROUP CURRENT_ROLE`, `GROUP SESSION_USER`} {
		statements, err := Parse(`GRANT SELECT ON t TO ` + spec)
		if err != nil || len(statements) != 1 || statements[0].(*ObjectGrant).Grantees[0].Role.Identity == "" {
			t.Fatal("GROUP role spec", spec, statements, err)
		}
	}
}

func TestObjectGrantReferencesRetainsConstraintBoundary(t *testing.T) {
	for _, query := range []string{
		`CREATE TABLE t(a INT REFERENCES parent(a))`,
		`CREATE TABLE t(a INT CONSTRAINT fk REFERENCES parent(a))`,
	} {
		statements, err := Parse(query)
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal("foreign-key boundary changed by grant keyword", query, statements, err)
		}
	}
	statements, err := Parse(`GRANT REFERENCES(a) ON t TO reader`)
	if err != nil || len(statements) != 1 || statements[0].(*ObjectGrant).Privileges[0].Name != "references" {
		t.Fatal("REFERENCES grant", statements, err)
	}
}

func TestObjectRevokeGrammar(t *testing.T) {
	query := `REVOKE GRANT OPTION FOR UPDATE(b,a),SELECT,REFERENCES(a) ON TABLE s.t,u FROM PUBLIC,GROUP reader,CURRENT_USER GRANTED BY CURRENT_ROLE CASCADE`
	statements, err := Parse(query)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	revoke := statements[0].(*ObjectGrant)
	if !revoke.Revoke || !revoke.OptionOnly || !revoke.Cascade || revoke.GrantOption || revoke.Kind != "" || len(revoke.Privileges) != 3 || len(revoke.Relations) != 2 || len(revoke.Grantees) != 3 || !revoke.Grantees[0].Public || revoke.Grantees[1].Role.Name != "reader" || revoke.Grantees[2].Role.Identity != "current" || revoke.Grantor == nil || revoke.Grantor.Identity != "current" {
		t.Fatal("option/cascade/operand flags", revoke)
	}
	if revoke.Privileges[0].Name != "update" || revoke.Privileges[1].Name != "select" || revoke.Privileges[2].Name != "references" || len(revoke.Privileges[0].Columns) != 2 || revoke.Privileges[0].Columns[0].Text != "b" || revoke.Privileges[0].Columns[1].Text != "a" || len(revoke.Privileges[1].Columns) != 0 || revoke.Relations[0].Parts[0].Text != "s" || revoke.Relations[0].Parts[1].Text != "t" || revoke.Relations[1].Parts[0].Text != "u" {
		t.Fatal("ordered mixed operands", revoke)
	}
	for _, operand := range []struct {
		pos  Position
		text string
	}{
		{revoke.Privileges[0].Pos, "UPDATE"},
		{revoke.Privileges[0].Columns[0].Pos, "b,a"},
		{revoke.Privileges[0].Columns[1].Pos, "a),SELECT"},
		{revoke.Privileges[1].Pos, "SELECT"},
		{revoke.Privileges[2].Pos, "REFERENCES"},
		{revoke.Relations[0].Parts[0].Pos, "s.t"},
		{revoke.Relations[1].Parts[0].Pos, "u FROM"},
	} {
		if operand.pos.Character != strings.Index(query, operand.text)+1 {
			t.Fatal("operand position", operand, query)
		}
	}
	for _, suffix := range []string{"", " RESTRICT", " CASCADE"} {
		for _, prefix := range []string{"REVOKE ", "REVOKE GRANT OPTION FOR "} {
			statements, err := Parse(prefix + "ALL PRIVILEGES(a,b) ON t FROM PUBLIC" + suffix)
			if err != nil || len(statements) != 1 {
				t.Fatal(prefix, suffix, err)
			}
			revoke := statements[0].(*ObjectGrant)
			if !revoke.Revoke || revoke.OptionOnly != (prefix != "REVOKE ") || revoke.Cascade != (suffix == " CASCADE") || revoke.GrantOption || !revoke.Privileges[0].All || len(revoke.Privileges[0].Columns) != 2 || !revoke.Grantees[0].Public {
				t.Fatal("default/explicit drop behavior", revoke)
			}
		}
	}
	for _, query := range []string{
		`REVOKE GRANT OPTION SELECT ON t FROM reader`,
		`REVOKE GRANT OPTION FOR SELECT(a,) ON t FROM reader CASCADE`,
		`REVOKE SELECT ON t FROM reader CASCADE RESTRICT`,
		`REVOKE GRANT OPTION FOR SELECT ON t FROM reader CASCADE; SELECT )`,
		`REVOKE SELECT ON t FROM reader RESTRICT junk`,
	} {
		if statements, err := Parse(query); err == nil || statements != nil {
			t.Fatal("accepted malformed revoke whole input", query, statements, err)
		}
	}
}
