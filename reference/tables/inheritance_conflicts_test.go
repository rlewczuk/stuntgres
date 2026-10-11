package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceConflicts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setup      []string
		query      string
		diagnostic fixture.Diagnostic
		notices    []fixture.Diagnostic
	}{
		{"duplicate", []string{"CREATE TABLE p(a INT)"}, "CREATE TABLE c() INHERITS(p,p)", fixture.Diagnostic{Code: "42P07", Message: `relation "p" would be inherited from more than once`}, nil},
		{"resolved_duplicate", []string{"CREATE TABLE p(a INT)"}, "CREATE TABLE c() INHERITS(p,public.p)", fixture.Diagnostic{Code: "42P07", Message: `relation "p" would be inherited from more than once`}, nil},
		{"parent_type", []string{"CREATE TABLE p(a INT)", "CREATE TABLE q(a BIGINT)"}, "CREATE TABLE c() INHERITS(p,q)", fixture.Diagnostic{Code: "42804", Message: `inherited column "a" has a type conflict`, Detail: "integer versus bigint"}, []fixture.Diagnostic{inheritanceMergeNotice("a")}},
		{"local_type", []string{"CREATE TABLE p(a INT)"}, "CREATE TABLE c(a BIGINT) INHERITS(p)", fixture.Diagnostic{Code: "42804", Message: `column "a" has a type conflict`, Detail: "integer versus bigint"}, []fixture.Diagnostic{inheritanceLocalNotice("a", false)}},
		{"parent_typmod", []string{"CREATE TABLE p(a VARCHAR(3))", "CREATE TABLE q(a VARCHAR(4))"}, "CREATE TABLE c() INHERITS(p,q)", fixture.Diagnostic{Code: "42804", Message: `inherited column "a" has a type conflict`, Detail: "character varying(3) versus character varying(4)"}, []fixture.Diagnostic{inheritanceMergeNotice("a")}},
		{"local_typmod", []string{"CREATE TABLE p(a VARCHAR(3))"}, "CREATE TABLE c(a VARCHAR(4)) INHERITS(p)", fixture.Diagnostic{Code: "42804", Message: `column "a" has a type conflict`, Detail: "character varying(3) versus character varying(4)"}, []fixture.Diagnostic{inheritanceLocalNotice("a", false)}},
		{"parent_collation", []string{`CREATE TABLE p(a TEXT COLLATE "C")`, `CREATE TABLE q(a TEXT COLLATE "POSIX")`}, "CREATE TABLE c() INHERITS(p,q)", fixture.Diagnostic{Code: "42P21", Message: `inherited column "a" has a collation conflict`, Detail: `"C" versus "POSIX"`}, []fixture.Diagnostic{inheritanceMergeNotice("a")}},
		{"local_collation", []string{`CREATE TABLE p(a TEXT COLLATE "C")`}, `CREATE TABLE c(a TEXT COLLATE "POSIX") INHERITS(p)`, fixture.Diagnostic{Code: "42P21", Message: `column "a" has a collation conflict`, Detail: `"C" versus "POSIX"`}, []fixture.Diagnostic{inheritanceLocalNotice("a", false)}},
		{"defaults", []string{"CREATE TABLE p(a INT DEFAULT 7)", "CREATE TABLE q(a INT DEFAULT 8)"}, "CREATE TABLE c() INHERITS(p,q)", fixture.Diagnostic{Code: "42611", Message: `column "a" inherits conflicting default values`, Hint: "To resolve the conflict, specify a default explicitly."}, []fixture.Diagnostic{inheritanceMergeNotice("a")}},
		{"cooked_not_evaluated", []string{"CREATE TABLE p(a INT DEFAULT 1+1)", "CREATE TABLE q(a INT DEFAULT 2)"}, "CREATE TABLE c() INHERITS(p,q)", fixture.Diagnostic{Code: "42611", Message: `column "a" inherits conflicting default values`, Hint: "To resolve the conflict, specify a default explicitly."}, []fixture.Diagnostic{inheritanceMergeNotice("a")}},
		{"parent_check", []string{"CREATE TABLE p(a INT,CONSTRAINT ck CHECK(a>0))", "CREATE TABLE q(a INT,CONSTRAINT ck CHECK(a>1))"}, "CREATE TABLE c() INHERITS(p,q)", fixture.Diagnostic{Code: "42710", Message: `check constraint name "ck" appears multiple times but with different expressions`}, []fixture.Diagnostic{inheritanceMergeNotice("a")}},
		{"check_not_algebraic", []string{"CREATE TABLE p(a INT,CONSTRAINT ck CHECK(a>0))", "CREATE TABLE q(a INT,CONSTRAINT ck CHECK(0<a))"}, "CREATE TABLE c() INHERITS(p,q)", fixture.Diagnostic{Code: "42710", Message: `check constraint name "ck" appears multiple times but with different expressions`}, []fixture.Diagnostic{inheritanceMergeNotice("a")}},
		{"local_check", []string{"CREATE TABLE p(a INT,CONSTRAINT ck CHECK(a>0))"}, "CREATE TABLE c(CONSTRAINT ck CHECK(a>1)) INHERITS(p)", fixture.Diagnostic{Code: "42710", Message: `constraint "ck" for relation "c" already exists`}, nil},
		{"local_noinherit", []string{"CREATE TABLE p(a INT,CONSTRAINT ck CHECK(a>0))"}, "CREATE TABLE c(CONSTRAINT ck CHECK(a>0) NO INHERIT) INHERITS(p)", fixture.Diagnostic{Code: "42P17", Message: `constraint "ck" conflicts with inherited constraint on relation "c"`}, nil},
		{"local_duplicate", []string{"CREATE TABLE p(a INT)"}, "CREATE TABLE c(a INT,a INT) INHERITS(p)", fixture.Diagnostic{Code: "42701", Message: `column "a" specified more than once`}, nil},
		{"view", []string{"CREATE VIEW p AS SELECT 1 AS a"}, "CREATE TABLE c() INHERITS(p)", fixture.Diagnostic{Code: "42809", Message: `inherited relation "p" is not a table or foreign table`}, nil},
		{"sequence", []string{"CREATE SEQUENCE p"}, "CREATE TABLE c() INHERITS(p)", fixture.Diagnostic{Code: "42809", Message: `inherited relation "p" is not a table or foreign table`}, nil},
		{"composite", []string{"CREATE TYPE p AS(a INT)"}, "CREATE TABLE c() INHERITS(p)", fixture.Diagnostic{Code: "42809", Message: `cannot open relation "p"`, Detail: "This operation is not supported for composite types."}, nil},
		{"missing", nil, "CREATE TABLE c() INHERITS(p)", fixture.Diagnostic{Code: "42P01", Message: `relation "p" does not exist`}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				for _, q := range tc.setup {
					s.exec(q, 0)
				}
				*s.notices = nil
				s.fail(tc.query, tc.diagnostic)
				if !reflect.DeepEqual(*s.notices, tc.notices) {
					s.t.Fatalf("conflict notices got %#v want %#v", *s.notices, tc.notices)
				}
				s.absent("c")
				// Reusing the failed destination proves CREATE published no shell.
				s.exec("CREATE TABLE c(healthy INT)", 0)
				s.exec("INSERT INTO c VALUES(17)", 1)
				s.values("SELECT * FROM c", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(17)}})
			})
		})
	}
}

func TestReferenceInheritanceOwnership(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE ROLE inheritance_owner", 0)
		defer func() {
			cleanup := []string{"RESET ROLE", "DROP ROLE inheritance_owner"}
			if s.b.PG {
				cleanup = []string{"RESET ROLE", "DROP OWNED BY inheritance_owner CASCADE", "DROP ROLE inheritance_owner"}
			}
			for _, q := range cleanup {
				if _, err := s.c.ExecContext(s.p.Ctx, q); err != nil {
					s.t.Error(err)
				}
			}
		}()
		s.exec("CREATE TABLE p(a INT)", 0)
		// Match native public-schema visibility and CREATE policy, then grant
		// SELECT without ownership so the failure reaches the parent owner check.
		if s.b.PG {
			s.exec("GRANT USAGE,CREATE ON SCHEMA public TO inheritance_owner", 0)
			s.exec("GRANT SELECT ON p TO inheritance_owner", 0)
		}
		s.exec("SET ROLE inheritance_owner", 0)
		s.fail("CREATE TABLE c() INHERITS(p)", fixture.Diagnostic{Code: "42501", Message: "must be owner of table p"})
		s.absent("c")
	})
}
