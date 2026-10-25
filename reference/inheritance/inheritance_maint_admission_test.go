package reference_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInheritanceMaintSyntax(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec(`DROP SCHEMA IF EXISTS "maint schema" CASCADE`, 0)
		s.exec(`CREATE SCHEMA "maint schema"`, 0)
		s.exec(`CREATE TABLE "maint schema"."π parent"(a INT)`, 0)
		s.exec(`CREATE TABLE "maint schema"."子"(a INT)`, 0)
		s.exec(`ALTER TABLE ONLY "maint schema"."子" INHERIT "maint schema"."π parent"`, 0)
		s.exec(`ALTER TABLE "maint schema"."子" * NO INHERIT "maint schema"."π parent"`, 0)
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c(a INT)", 0)
		for _, tc := range []struct{ query, token string }{
			{"ALTER TABLE c INHERIT p garbage", "garbage"},
			{"ALTER TABLE c INHERIT p,", ""},
			{"ALTER TABLE c NO INHERIT", ""},
			{`ALTER TABLE "maint schema"."子" INHERIT "maint schema"."π parent" garbage`, "garbage"},
		} {
			message := "syntax error at end of input"
			position := utf8.RuneCountInString(tc.query) + 1
			if tc.token != "" {
				message = `syntax error at or near "` + tc.token + `"`
				position = utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.token)]) + 1
			}
			s.fail(tc.query, fixture.Diagnostic{Code: "42601", Message: message, Position: strconv.Itoa(position)})
			s.edges("c")
		}
		s.notice("ALTER TABLE IF EXISTS absent_child INHERIT absent_parent", inheritanceNotice(`relation "absent_child" does not exist, skipping`))
		s.notice("ALTER TABLE IF EXISTS absent_child NO INHERIT absent_parent", inheritanceNotice(`relation "absent_child" does not exist, skipping`))
		stmt, err := s.c.PrepareContext(s.p.Ctx, "ALTER TABLE deferred_child INHERIT deferred_parent")
		if err != nil {
			s.t.Fatal("syntax-only utility Prepare resolved absent endpoint", err)
		}
		defer stmt.Close()
		s.exec("CREATE TABLE deferred_parent(a INT)", 0)
		s.exec("CREATE TABLE deferred_child(a INT)", 0)
		result, err := stmt.ExecContext(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 0 {
			s.t.Fatal("held utility affected", count, err)
		}
		s.edges("deferred_child", "deferred_parent")
		s.exec(`DROP SCHEMA "maint schema" CASCADE`, 0)
	})
}

func TestReferenceInheritanceMaintAdmission(t *testing.T) {
	t.Run("EndpointOwnershipAndDetach", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("GRANT USAGE ON SCHEMA public TO maint_child_owner,maint_other_owner", 0)
			s.exec("CREATE TABLE p(a INT)", 0)
			s.exec("CREATE TABLE c(a INT)", 0)
			s.exec("ALTER TABLE c OWNER TO maint_child_owner", 0)
			s.exec("SET ROLE maint_child_owner", 0)
			s.fail("ALTER TABLE c INHERIT p", fixture.Diagnostic{Code: "42501", Message: "must be owner of table p"})
			s.exec("RESET ROLE", 0)
			s.edges("c")
			s.exec("ALTER TABLE c INHERIT p", 0)
			s.exec("SET ROLE maint_child_owner", 0)
			s.exec("ALTER TABLE c NO INHERIT p", 0)
			s.exec("RESET ROLE", 0)
			s.edges("c")
			s.exec("SET ROLE maint_other_owner", 0)
			s.fail("ALTER TABLE c INHERIT p", fixture.Diagnostic{Code: "42501", Message: "must be owner of table c"})
			s.exec("RESET ROLE", 0)
		}, "maint_child_owner", "maint_other_owner")
	})
	t.Run("ReadOnlyBeforeResolution", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			for _, action := range []string{"INHERIT", "NO INHERIT"} {
				s.exec("BEGIN READ ONLY", 0)
				s.fail("ALTER TABLE absent_child "+action+" absent_parent", fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"})
				s.exec("ROLLBACK", 0)
			}
		})
	})
	for _, tc := range []struct{ name, setup, query, message string }{
		{"TypedChild", "CREATE TYPE maint_shape AS(a INT); CREATE TABLE p(a INT); CREATE TABLE c OF maint_shape", "ALTER TABLE c INHERIT p", "cannot change inheritance of typed table"},
		{"PartitionedChild", "CREATE TABLE p(a INT); CREATE TABLE c(a INT) PARTITION BY RANGE(a)", "ALTER TABLE c INHERIT p", "cannot change inheritance of partitioned table"},
		{"PartitionedParent", "CREATE TABLE p(a INT) PARTITION BY RANGE(a); CREATE TABLE c(a INT)", "ALTER TABLE c INHERIT p", `cannot inherit from partitioned table "p"`},
		{"PartitionChild", "CREATE TABLE p(a INT); CREATE TABLE r(a INT) PARTITION BY RANGE(a); CREATE TABLE c PARTITION OF r FOR VALUES FROM(0) TO(10)", "ALTER TABLE c INHERIT p", "cannot change inheritance of a partition"},
		{"TemporaryParent", "CREATE TEMP TABLE p(a INT); CREATE TABLE c(a INT)", "ALTER TABLE c INHERIT p", `cannot inherit from temporary relation "p"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				for _, q := range strings.Split(tc.setup, "; ") {
					s.exec(q, 0)
				}
				s.fail(tc.query, fixture.Diagnostic{Code: "42809", Message: tc.message})
				s.values("SELECT count(*) AS n FROM c", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			})
		})
	}
}
