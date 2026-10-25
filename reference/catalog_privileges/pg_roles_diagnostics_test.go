package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func pgRolesWriteDiagnostic(verb string) fixture.Diagnostic {
	gerund := map[string]string{"insert into": "inserting into", "update": "updating", "delete from": "deleting from"}[verb]
	action := map[string]string{"insert into": "INSERT", "update": "UPDATE", "delete from": "DELETE"}[verb]
	return fixture.Diagnostic{Code: "55000", Message: "cannot " + verb + " view \"pg_roles\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + gerund + " the view, provide an INSTEAD OF " + action + " trigger or an unconditional ON " + action + " DO INSTEAD rule."}
}

func TestReferencePGRolesDiagnostics(t *testing.T) {
	pgRolesScenario(t, []string{"pgr_diag"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgRolesCreate(t, p, b, "pgr_diag")
		pgRolesExec(t, p, b, "CREATE TABLE pgr_sentinel(a INT)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgr_sentinel VALUES(7)", 1))
		cases := []struct {
			name, q string
			want    fixture.Diagnostic
		}{
			{"update_mask", "UPDATE pg_catalog.pg_roles SET rolpassword='********' WHERE FALSE", pgRolesWriteDiagnostic("update")},
			{"update_null", "UPDATE pg_catalog.pg_roles SET rolpassword=NULL WHERE FALSE", pgRolesWriteDiagnostic("update")},
			{"update_both", "UPDATE pg_catalog.pg_roles SET rolcreatedb=true,rolpassword=NULL WHERE FALSE", pgRolesWriteDiagnostic("update")},
			{"insert_mask", "INSERT INTO pg_catalog.pg_roles(rolpassword) VALUES('********')", pgRolesWriteDiagnostic("insert into")},
			{"insert_null", "INSERT INTO pg_catalog.pg_roles(rolpassword) VALUES(NULL)", pgRolesWriteDiagnostic("insert into")},
			{"insert_all", "INSERT INTO pg_catalog.pg_roles(rolname,rolsuper,rolinherit,rolcreaterole,rolcreatedb,rolcanlogin,rolreplication,rolconnlimit,rolpassword,rolvaliduntil,rolbypassrls,rolconfig,oid) VALUES('pgr_forbidden',false,true,false,false,false,false,-1,NULL,NULL,false,NULL,1)", pgRolesWriteDiagnostic("insert into")},
			{"insert_implicit", "INSERT INTO pg_catalog.pg_roles VALUES('pgr_forbidden',false,true,false,false,false,false,-1,'********',NULL,false,NULL,1)", pgRolesWriteDiagnostic("insert into")},
			{"insert_base", "INSERT INTO pg_catalog.pg_roles(rolname) VALUES('pgr_forbidden')", pgRolesWriteDiagnostic("insert into")},
			{"update_base", "UPDATE pg_catalog.pg_roles SET rolcreatedb=true WHERE FALSE", pgRolesWriteDiagnostic("update")},
			{"delete", "DELETE FROM pg_catalog.pg_roles WHERE FALSE", pgRolesWriteDiagnostic("delete from")},
			{"read_missing", "SELECT missing FROM pg_catalog.pg_roles", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}},
			{"read_utf8", "SELECT /*é*/ missing FROM pg_catalog.pg_roles", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "14"}},
			{"target_missing", "UPDATE pg_catalog.pg_roles SET missing=true", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "pg_roles" does not exist`}},
			{"assignment", "UPDATE pg_catalog.pg_roles SET rolcreatedb=1", fixture.Diagnostic{Code: "42804", Message: `column "rolcreatedb" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression."}},
			{"where", "DELETE FROM pg_catalog.pg_roles WHERE 1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer"}},
			{"returning", "DELETE FROM pg_catalog.pg_roles WHERE FALSE RETURNING missing", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`}},
			{"oid_literal", "SELECT oid FROM pg_roles WHERE oid='bad'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`}},
			{"missing_relation", "SELECT * FROM pg_catalog.pg_roles_missing", fixture.Diagnostic{Code: "42P01", Message: `relation "pg_catalog.pg_roles_missing" does not exist`}},
			{"missing_schema", "SELECT * FROM pgr_absent_schema.pg_roles", fixture.Diagnostic{Code: "42P01", Message: `relation "pgr_absent_schema.pg_roles" does not exist`}},
			{"quoted_relation", "SELECT * FROM \"PG_ROLES\"", fixture.Diagnostic{Code: "42P01", Message: `relation "PG_ROLES" does not exist`}},
			{"qualified_column", "SELECT r.absent FROM pg_roles r WHERE FALSE", fixture.Diagnostic{Code: "42703", Message: `column r.absent does not exist`}},
			{"missing_alias", "SELECT absent.rolname FROM pg_roles r", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "absent"`}},
			{"malformed", "SELECT FROM", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input"}},
		}
		// Unobserved character anchors and protocol phases are oracle checkpoints:
		// root runs postgres subtests before accepting these independently reasoned
		// expectations. Never replace them with native output.
		for _, tc := range cases {
			if tc.want.Position == "" {
				token := ""
				switch tc.name {
				case "missing_relation":
					token = "pg_catalog.pg_roles_missing"
				case "missing_schema":
					token = "pgr_absent_schema.pg_roles"
				case "quoted_relation":
					token = "\"PG_ROLES\""
				case "qualified_column":
					token = "r.absent"
				case "missing_alias":
					token = "absent.rolname"
				case "malformed":
					tc.want.Position = strconv.Itoa(utf8.RuneCountInString(tc.q) + 1)
				case "target_missing", "returning":
					token = "missing"
				case "assignment":
					token = "1"
				case "where":
					token = "1"
				case "oid_literal":
					token = "'bad'"
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
						fixture.PgDescriptionRows(t, p, b, pgRolesSelect("SELECT * FROM pg_roles WHERE oid=$1", [][]any{pgRolesRow("pgr_diag", id, nil)}, id))
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a FROM pgr_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
					})
				}
			})
		}
		t.Run("oid_parameter", func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				want := fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "oops"`, Context: "unnamed portal parameter $1 = '...'"}
				s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_roles WHERE oid=$1", want, "oops")
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		})
		// Cancellation is an ordinary adapter error, not a fabricated SQLSTATE.
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if rows, err := b.DB.QueryContext(canceled, "SELECT * FROM pg_roles WHERE oid=$1", id); !errors.Is(err, context.Canceled) || rows != nil {
			if rows != nil {
				rows.Close()
			}
			t.Fatal("canceled pg_roles query", err)
		}
		if !b.PG {
			for _, prepared := range []bool{false, true} {
				step := fixture.ImplicitError("SELECT * FROM pg_roles WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
				step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
			}
		}
		pgRolesExec(t, p, b, "DROP ROLE pgr_diag")
	})
}
