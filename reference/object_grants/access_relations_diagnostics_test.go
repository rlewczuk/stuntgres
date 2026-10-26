package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAccessRelationsBindingDiagnostics(t *testing.T) {
	for _, name := range []string{"has_table_privilege", "has_sequence_privilege", "has_any_column_privilege", "has_column_privilege"} {
		t.Run(name, func(t *testing.T) {
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				cases := []struct{ args, types string }{{"", ""}, {"true", "boolean"}, {"true,true,true,true,true", "boolean, boolean, boolean, boolean, boolean"}, {"true,'x','x'", "boolean, unknown, unknown"}, {"'x',true,'x'", "unknown, boolean, unknown"}, {"'x','x',true", "unknown, unknown, boolean"}}
				if name == "has_column_privilege" {
					cases = append(cases, struct{ args, types string }{"'ar_t',1,'SELECT'", "unknown, integer, unknown"}, struct{ args, types string }{"'x','x','x',true", "unknown, unknown, unknown, boolean"})
				}
				if name == "has_column_privilege" {
					cases = append(cases, struct{ args, types string }{"true,'x','x','x'", "boolean, unknown, unknown, unknown"}, struct{ args, types string }{"'x',true,'x','x'", "unknown, boolean, unknown, unknown"}, struct{ args, types string }{"'x','x',true,'x'", "unknown, unknown, boolean, unknown"})
				}
				for _, prefix := range []string{"SELECT ", "/* é */ SELECT "} {
					for _, tc := range cases {
						q := prefix + name + "(" + tc.args + ")"
						pos := "8"
						if prefix != "SELECT " {
							pos = "16"
						}
						for _, prepared := range []bool{false, true} {
							for _, rows := range []bool{false, true} {
								s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "function " + name + "(" + tc.types + ") does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: pos})
								s.Prepared = prepared
								s.SelectRows = rows
								s.ErrorStage = "execute"
								if prepared {
									s.ErrorStage = "prepare"
								}
								run(s)
							}
						}
					}
				}
				args := "'ar_t','SELECT'"
				types := "unknown, unknown"
				if name == "has_column_privilege" {
					args = "'ar_t','a','SELECT'"
					types = "unknown, unknown, unknown"
				}
				failures := []struct {
					q string
					d fixture.Diagnostic
				}{
					{"SELECT \"" + strings.ToUpper(name) + "\"(" + args + ")", fixture.Diagnostic{Code: "42883", Message: "function " + strings.ToUpper(name) + "(" + types + ") does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}},
					{"SELECT ar_missing." + name + "(" + args + ")", fixture.Diagnostic{Code: "3F000", Message: `schema "ar_missing" does not exist`, Position: "8"}},
					{"SELECT " + name + "(who => 'x', privilege => 'y')", fixture.Diagnostic{Code: "42883", Message: "function " + name + "(who => unknown, privilege => unknown) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}},
				}
				for _, dec := range []string{"DISTINCT", "ORDER BY", "FILTER"} {
					q := "SELECT " + name + "(" + args + ") FILTER (WHERE TRUE)"
					if dec == "DISTINCT" {
						q = "SELECT " + name + "(DISTINCT " + args + ")"
					}
					if dec == "ORDER BY" {
						q = "SELECT " + name + "(" + args + " ORDER BY 1)"
					}
					failures = append(failures, struct {
						q string
						d fixture.Diagnostic
					}{q, fixture.Diagnostic{Code: "42809", Message: dec + " specified, but " + name + " is not an aggregate function", Position: "8"}})
				}
				for _, tc := range failures {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(tc.q, tc.d)
						s.SelectRows = true
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
					}
				}
			})
		})
	}
}
func TestReferenceAccessRelationsDiagnostics(t *testing.T) {
	for _, name := range []string{"has_table_privilege", "has_sequence_privilege", "has_any_column_privilege", "has_column_privilege"} {
		t.Run(name, func(t *testing.T) {
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				object := "ar_t"
				column := ""
				if name == "has_sequence_privilege" {
					object = "ar_s"
				}
				if name == "has_column_privilege" {
					column = ",'a'"
				}
				q := "SELECT " + name + "($1::name,$2::text" + column + ",$3::text) AS v"
				for _, tc := range []struct{ role, relation, token, code, message string }{
					{"ar_missing", object, "SELECT", "42704", `role "ar_missing" does not exist`},
					{"ar_reader", "ar_missing", "SELECT", "42P01", `relation "ar_missing" does not exist`},
					{"ar_reader", object, "bad", "22023", `unrecognized privilege type: "bad"`},
				} {
					accessRelationsRoutes(t, p, b, c, accessRelationsError(q, tc.code, tc.message, tc.role, tc.relation, tc.token))
				}
				if name == "has_column_privilege" {
					accessRelationsRoutes(t, p, b, c, accessRelationsError("SELECT has_column_privilege('ar_reader','ar_t','missing','SELECT') AS v", "42703", `column "missing" of relation "ar_t" does not exist`))
				}
				wrong := "USAGE"
				if name == "has_sequence_privilege" {
					wrong = "INSERT"
				}
				run(accessRelationsError(q, "22023", fmt.Sprintf(`unrecognized privilege type: "%s"`, wrong), "ar_reader", object, wrong))
			})
		})
	}
}
func TestReferenceAccessRelationsPrecedence(t *testing.T) {
	cases := []struct {
		q, code, message string
		value            any
	}{
		{"has_table_privilege('ar_missing','ar_missing','bad')", "42704", `role "ar_missing" does not exist`, nil},
		{"has_table_privilege('ar_reader','ar_missing','bad')", "42P01", `relation "ar_missing" does not exist`, nil},
		{"has_any_column_privilege('ar_reader','ar_missing','bad')", "42P01", `relation "ar_missing" does not exist`, nil},
		{"has_sequence_privilege('ar_reader','ar_missing','bad')", "22023", `unrecognized privilege type: "bad"`, nil},
		{"has_sequence_privilege('ar_reader','ar_t','bad')", "22023", `unrecognized privilege type: "bad"`, nil},
		{"has_column_privilege('ar_reader','ar_t','missing','bad')", "42703", `column "missing" of relation "ar_t" does not exist`, nil},
		{"has_column_privilege('ar_reader','ar_t',0::smallint,'bad')", "22023", `unrecognized privilege type: "bad"`, nil},
		{"has_column_privilege('ar_reader',4294967294::oid,'a','bad')", "22023", `unrecognized privilege type: "bad"`, nil},
	}
	for _, name := range []string{"has_table_privilege", "has_sequence_privilege", "has_any_column_privilege", "has_column_privilege"} {
		column := ""
		if name == "has_column_privilege" {
			column = ",1::smallint"
		}
		for _, actor := range []string{"'ar_reader'", "current_user"} {
			for _, bad := range []bool{false, true} {
				token := "SELECT"
				code := ""
				msg := ""
				if bad {
					token = "bad"
					code = "22023"
					msg = `unrecognized privilege type: "bad"`
				}
				cases = append(cases, struct {
					q, code, message string
					value            any
				}{name + "(" + actor + ",4294967294::oid" + column + ",'" + token + "')", code, msg, nil})
			}
		}
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				q := "SELECT " + tc.q + " AS v"
				s := objectGrantAccessValue(q, tc.value)
				if tc.code != "" {
					s = accessRelationsError(q, tc.code, tc.message)
				}
				accessRelationsRoutes(t, p, b, c, s)
			})
		})
	}
}
func TestReferenceAccessRelationsAttributes(t *testing.T) {
	accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{"ALTER TABLE ar_t ADD COLUMN c INT", "ALTER TABLE ar_t DROP COLUMN b", "ALTER TABLE ar_t ADD COLUMN d INT", "GRANT SELECT(c),SELECT(d) ON ar_t TO ar_reader"} {
			run(fixture.Exec(q, 0))
		}
		q := "SELECT has_column_privilege('ar_reader','ar_t',$1::smallint,'SELECT') AS v"
		for _, tc := range []struct {
			n int64
			v any
		}{{0, nil}, {1, false}, {2, nil}, {3, true}, {4, true}, {-7, nil}, {32767, nil}} {
			accessRelationsRoutes(t, p, b, c, objectGrantAccessValue(q, tc.v, tc.n))
		}
		run(accessRelationsError("SELECT has_column_privilege('ar_reader','ar_t','b','SELECT')", "42703", `column "b" of relation "ar_t" does not exist`))
		run(fixture.Exec("ALTER TABLE ar_t RENAME COLUMN c TO renamed", 0))
		run(objectGrantAccessValue(q, true, int64(3)))
		run(objectGrantAccessValue("SELECT has_column_privilege('ar_reader','ar_t','renamed','SELECT') AS v", true))
		run(fixture.Exec("GRANT SELECT ON ar_t TO ar_reader", 0))
		for i, name := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
			run(objectGrantAccessValue(q, true, int64(-i-1)))
			run(objectGrantAccessValue("SELECT has_column_privilege('ar_reader','ar_t',$1::text,'SELECT') AS v", true, name))
		}
		run(fixture.Exec("CREATE VIEW ar_v AS SELECT a FROM ar_t", 0))
		run(objectGrantAccessValue("SELECT has_column_privilege(current_user,'ar_v',-1::smallint,'SELECT') AS v", nil))
		run(accessRelationsError("SELECT has_column_privilege(current_user,'ar_v','ctid','SELECT')", "42703", `column "ctid" of relation "ar_v" does not exist`))
		run(fixture.Exec("CREATE TABLE ar_zero()", 0))
		run(objectGrantAccessValue("SELECT has_any_column_privilege('ar_reader','ar_zero','SELECT') AS v", false))
		run(fixture.Exec("GRANT SELECT ON ar_zero TO ar_reader", 0))
		run(objectGrantAccessValue("SELECT has_any_column_privilege('ar_reader','ar_zero','SELECT') AS v", true))
		var dropped string
		if err := c.QueryRowContext(p.Ctx, "SELECT attname FROM pg_attribute WHERE attrelid='ar_t'::regclass AND attnum=2").Scan(&dropped); err != nil {
			t.Fatal(err)
		}
		run(objectGrantAccessValue("SELECT has_column_privilege(current_user,'ar_t',$1::text,'SELECT') AS v", nil, dropped))
	})
}
func TestReferenceAccessRelationsNames(t *testing.T) {
	accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{"CREATE SCHEMA ar_scope", `CREATE TABLE ar_scope."Mixed"("Exact" INT)`, `GRANT SELECT("Exact") ON ar_scope."Mixed" TO ar_reader`} {
			run(fixture.Exec(q, 0))
		}
		run(objectGrantAccessValue(`SELECT has_column_privilege('ar_reader','ar_scope."Mixed"','Exact','SELECT') AS v`, true))
		run(accessRelationsError(`SELECT has_column_privilege('ar_reader','ar_scope."Mixed"','exact','SELECT')`, "42703", `column "exact" of relation "Mixed" does not exist`))
		run(accessRelationsError("SELECT has_table_privilege('ar_reader','ar_scope..ar_t','SELECT')", "42602", "invalid name syntax"))
		run(accessRelationsError("SELECT has_table_privilege('ar_reader','ar_other_database.public.ar_t','SELECT')", "0A000", `cross-database references are not implemented: "ar_other_database.public.ar_t"`))
		run(fixture.Exec("SET search_path=ar_scope,public", 0))
		defer c.ExecContext(p.Ctx, "RESET search_path")
		run(objectGrantAccessValue(`SELECT has_any_column_privilege('ar_reader','"Mixed"','SELECT') AS v`, true))
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'ar_t'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("GRANT SELECT ON ar_t TO ar_reader", 0))
		text := accessExecutionHeld(t, p, c, "SELECT has_table_privilege('ar_reader',$1::text,'SELECT') AS v")
		number := accessExecutionHeld(t, p, c, "SELECT has_table_privilege('ar_reader',$1::oid,'SELECT') AS v")
		fixture.RunStep(t, p.Ctx, text, b.Recorder, objectGrantAccessValue(text.SQL, true, "ar_t"))
		run(fixture.Exec("ALTER TABLE ar_t RENAME TO ar_new", 0))
		fixture.RunStep(t, p.Ctx, text, b.Recorder, accessRelationsError(text.SQL, "42P01", `relation "ar_t" does not exist`, "ar_t"))
		fixture.RunStep(t, p.Ctx, number, b.Recorder, objectGrantAccessValue(number.SQL, true, oid))
		run(fixture.Exec("DROP TABLE ar_new", 0))
		fixture.RunStep(t, p.Ctx, number, b.Recorder, objectGrantAccessValue(number.SQL, nil, oid))
		run(fixture.Exec("REVOKE ALL ON SCHEMA ar_scope FROM PUBLIC", 0))
		var mixed int64
		if err := c.QueryRowContext(p.Ctx, `SELECT 'ar_scope."Mixed"'::regclass::oid`).Scan(&mixed); err != nil {
			t.Fatal(err)
		}
		fixture.RoleGrantActor(run, "ar_reader", func() {
			run(objectGrantAccessValue("SELECT has_any_column_privilege($1::oid,'SELECT') AS v", true, mixed))
		})
	})
}
