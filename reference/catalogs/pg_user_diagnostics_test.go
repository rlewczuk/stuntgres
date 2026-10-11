package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func pgUserWriteDiagnostic(verb string, password bool) fixture.Diagnostic {
	if password {
		return fixture.Diagnostic{Code: "0A000", Message: "cannot " + verb + " column \"passwd\" of view \"pg_user\"", Detail: "View columns that are not columns of their base relation are not updatable."}
	}
	gerund := map[string]string{"insert into": "inserting into", "update": "updating", "delete from": "deleting from"}[verb]
	action := map[string]string{"insert into": "INSERT", "update": "UPDATE", "delete from": "DELETE"}[verb]
	return fixture.Diagnostic{Code: "55000", Message: "cannot " + verb + " view \"pg_shadow\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + gerund + " the view, provide an INSTEAD OF " + action + " trigger or an unconditional ON " + action + " DO INSTEAD rule."}
}

func TestReferencePGUserDiagnostics(t *testing.T) {
	pgUserScenario(t, []string{"pgu_diag"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgUserCreate(t, p, b, "pgu_diag")
		pgUserExec(t, p, b, "CREATE TABLE pgu_sentinel(a INT)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgu_sentinel VALUES(7)", 1))
		cases := []struct {
			name, q string
			want    fixture.Diagnostic
		}{
			{"update_mask", "UPDATE pg_catalog.pg_user SET passwd='********' WHERE FALSE", pgUserWriteDiagnostic("update", true)},
			{"update_null", "UPDATE pg_catalog.pg_user SET passwd=NULL WHERE FALSE", pgUserWriteDiagnostic("update", true)},
			{"update_both", "UPDATE pg_catalog.pg_user SET usecreatedb=true,passwd=NULL WHERE FALSE", pgUserWriteDiagnostic("update", true)},
			{"insert_mask", "INSERT INTO pg_catalog.pg_user(passwd) VALUES('********')", pgUserWriteDiagnostic("insert into", true)},
			{"insert_null", "INSERT INTO pg_catalog.pg_user(passwd) VALUES(NULL)", pgUserWriteDiagnostic("insert into", true)},
			{"insert_all", "INSERT INTO pg_catalog.pg_user(usename,usesysid,usecreatedb,usesuper,userepl,usebypassrls,passwd,valuntil,useconfig) VALUES('pgu_forbidden',1,false,false,false,false,NULL,NULL,NULL)", pgUserWriteDiagnostic("insert into", true)},
			{"insert_implicit", "INSERT INTO pg_catalog.pg_user VALUES('pgu_forbidden',1,false,false,false,false,'********',NULL,NULL)", pgUserWriteDiagnostic("insert into", true)},
			{"insert_base", "INSERT INTO pg_catalog.pg_user(usename) VALUES('pgu_forbidden')", pgUserWriteDiagnostic("insert into", false)},
			{"update_base", "UPDATE pg_catalog.pg_user SET usecreatedb=true WHERE FALSE", pgUserWriteDiagnostic("update", false)},
			{"delete", "DELETE FROM pg_catalog.pg_user WHERE FALSE", pgUserWriteDiagnostic("delete from", false)},
			{"read_missing", "SELECT missing FROM pg_catalog.pg_user", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}},
			{"read_utf8", "SELECT /*é*/ missing FROM pg_catalog.pg_user", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "14"}},
			{"target_missing", "UPDATE pg_catalog.pg_user SET missing=true", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "pg_user" does not exist`}},
			{"assignment", "UPDATE pg_catalog.pg_user SET usecreatedb=1", fixture.Diagnostic{Code: "42804", Message: `column "usecreatedb" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression."}},
			{"where", "DELETE FROM pg_catalog.pg_user WHERE 1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer"}},
			{"returning", "DELETE FROM pg_catalog.pg_user WHERE FALSE RETURNING missing", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`}},
			{"oid_literal", "SELECT * FROM pg_catalog.pg_user WHERE usesysid='oops'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "oops"`}},
		}
		for _, tc := range cases {
			if tc.want.Position == "" {
				token := ""
				switch tc.name {
				case "target_missing", "returning":
					token = "missing"
				case "assignment":
					token = "1"
				case "where":
					token = "1"
				case "oid_literal":
					token = "'oops'"
				}
				if token != "" {
					tc.want.Position = strconv.Itoa(utf8.RuneCountInString(tc.q[:strings.LastIndex(tc.q, token)]) + 1)
				}
			}
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					route := "direct"
					if prepared {
						route = "prepared"
					}
					t.Run(route, func(t *testing.T) {
						s := fixture.ImplicitError(tc.q, tc.want)
						s.Prepared = prepared
						s.SelectRows = strings.HasPrefix(tc.q, "SELECT")
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						fixture.PgDescriptionRows(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{pgUserRow("pgu_diag", id, nil)}, id))
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a FROM pgu_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
					})
				}
			})
		}
		t.Run("oid_parameter", func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				want := fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "oops"`, Context: "unnamed portal parameter $1 = '...'"}
				s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_user WHERE usesysid=$1", want, "oops")
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		})
		pgUserExec(t, p, b, "DROP ROLE pgu_diag")
	})
}
