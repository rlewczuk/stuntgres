package parser

import (
	"errors"
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
