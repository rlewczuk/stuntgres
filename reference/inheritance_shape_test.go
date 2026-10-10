package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceInheritanceShapeDefaults(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p1(a INT DEFAULT 7,x TEXT)", 0)
		s.exec("CREATE TABLE p2(y BOOL,a INTEGER DEFAULT 7)", 0)
		s.notice("CREATE TABLE c(z BIGINT,a INT DEFAULT 9) INHERITS(p1,p2)", inheritanceMergeNotice("a"), inheritanceLocalNotice("a", true))
		names, types := []string{"a", "x", "y", "z"}, []string{"INT4", "TEXT", "BOOL", "INT8"}
		s.values("SELECT * FROM c", names, types, nil)
		referenceTableDDLMetadata(s.t, s.p.ctx, s.c, s.b.pg, s.prepared, "SELECT * FROM c", names, types, []bool{true, true, true, true}, 0)
		s.exec("INSERT INTO c(z) VALUES($1)", 1, 11)
		s.exec("INSERT INTO c(a,x,y,z) VALUES($1,$2,$3,$4)", 1, 2, "two", true, 22)
		rows := [][]any{{int64(9), nil, nil, int64(11)}, {int64(2), "two", true, int64(22)}}
		s.values("SELECT * FROM c ORDER BY z", names, types, rows)
		referenceTableDDLMetadata(s.t, s.p.ctx, s.c, s.b.pg, s.prepared, "SELECT * FROM c ORDER BY z", names, types, []bool{true, true, true, true}, 2)
		s.exec("CREATE TABLE absent_default(a INT)", 0)
		s.exec("CREATE TABLE one_default() INHERITS(absent_default,p1)", 0)
		s.exec("INSERT INTO one_default DEFAULT VALUES", 1)
		s.values("SELECT * FROM one_default", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}})
		s.exec("CREATE TABLE other_default(a INT DEFAULT 8)", 0)
		s.exec("CREATE TABLE resolved(a INT DEFAULT 9) INHERITS(p1,other_default)", 0)
		s.exec("CREATE TABLE resolved_null(a INT DEFAULT NULL) INHERITS(p1,other_default)", 0)
		for _, name := range []string{"resolved", "resolved_null"} {
			s.exec("INSERT INTO "+name+" DEFAULT VALUES", 1)
		}
		s.values("SELECT a FROM resolved", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}})
		s.values("SELECT a FROM resolved_null", []string{"a"}, []string{"INT4"}, [][]any{{nil}})
		s.exec("CREATE SEQUENCE seed START 31", 0)
		s.exec("CREATE TABLE volatile1(a BIGINT DEFAULT nextval('seed'))", 0)
		s.exec("CREATE TABLE volatile2(a BIGINT DEFAULT nextval('seed'::regclass))", 0)
		s.exec("CREATE TABLE volatile_child() INHERITS(volatile1,volatile2)", 0)
		s.values("SELECT last_value,is_called FROM seed", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(31), false}})
		s.exec("INSERT INTO volatile_child DEFAULT VALUES", 1)
		s.values("SELECT a FROM volatile_child", []string{"a"}, []string{"INT8"}, [][]any{{int64(31)}})
	})
}

func TestReferenceInheritanceSyntax(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec(`CREATE SCHEMA "Inherit Schema"`, 0)
		defer func() {
			if _, err := s.c.ExecContext(s.p.ctx, `DROP SCHEMA "Inherit Schema" CASCADE`); err != nil {
				s.t.Error(err)
			}
		}()
		s.exec(`CREATE TABLE "Inherit Schema"."P one"(a INT DEFAULT 7)`, 0)
		s.exec(`CREATE TABLE "P two"(b TEXT)`, 0)
		s.exec(`CREATE TABLE "C é"() INHERITS("Inherit Schema"."P one",public."P two")`, 0)
		s.exec(`INSERT INTO "C é" DEFAULT VALUES`, 1)
		s.values(`SELECT * FROM "C é"`, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}})
		s.notice(`CREATE TABLE IF NOT EXISTS "C é"() INHERITS(missing_parent)`, referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "C é" already exists, skipping`})
		s.notice(`CREATE TABLE IF NOT EXISTS "C é"(a TEXT) INHERITS("Inherit Schema"."P one")`, referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "C é" already exists, skipping`})
		s.exec("CREATE TEMP TABLE temp_parent(a INT)", 0)
		s.exec("CREATE TEMP TABLE temp_child() INHERITS(temp_parent) ON COMMIT PRESERVE ROWS", 0)
		s.exec("INSERT INTO temp_child VALUES(4)", 1)
		s.values("SELECT * FROM temp_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}})
		s.exec("CREATE TABLE pg_temp.implicit_child() INHERITS(temp_parent)", 0)
		s.exec("INSERT INTO pg_temp.implicit_child VALUES(5)", 1)
		s.values("SELECT * FROM pg_temp.implicit_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(5)}})
		s.fail("CREATE TABLE c() INHERITS(temp_parent)", referenceDiagnostic{Code: "42809", Message: `cannot inherit from temporary relation "temp_parent"`})
		s.absent("c")
		s.exec("CREATE TABLE c(a INT)", 0)
		s.exec("CREATE SCHEMA inline_inheritance CREATE TABLE p(a INT DEFAULT 5) CREATE TABLE c() INHERITS(p)", 0)
		defer func() {
			if _, err := s.c.ExecContext(s.p.ctx, "DROP SCHEMA inline_inheritance CASCADE"); err != nil {
				s.t.Error(err)
			}
		}()
		s.exec("INSERT INTO inline_inheritance.c DEFAULT VALUES", 1)
		s.values("SELECT * FROM inline_inheritance.c", []string{"a"}, []string{"INT4"}, [][]any{{int64(5)}})
	})
}

func TestReferenceInheritanceSyntaxAtomicity(t *testing.T) {
	for _, tc := range []struct{ query, token string }{
		{"CREATE TABLE bad() INHERITS()", ")"},
		{"CREATE TABLE bad() INHERITS(p,)", ")"},
		{"CREATE TABLE bad() INHERITS(p) INHERITS(q)", "INHERITS(q)"},
		{"CREATE TABLE bad() INHERITS(p) trailing", "trailing"},
		{`CREATE TABLE "é"() INHERITS(p) trailing`, "trailing"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT)", 0)
				// Closing-list syntax uses its last ')' rather than CREATE's ')'.
				position := len([]rune(tc.query))
				token := tc.token
				if tc.token != ")" {
					position = len([]rune(tc.query[:strings.LastIndex(tc.query, tc.token)])) + 1
				}
				if tc.token == "INHERITS(q)" {
					token = "INHERITS"
				}
				s.fail(tc.query, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: referencePosition(position)})
				// PostgreSQL parses a complete simple-query script before executing it;
				// native must likewise leave the prefix unpublished.
				prefix := "CREATE TABLE prefix(a INT); "
				d := referenceDiagnostic{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: referencePosition(position + len([]rune(prefix)))}
				if !s.prepared {
					s.fail(prefix+tc.query, d)
					s.absent("prefix")
				}
			})
		})
	}
}
