package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Nonfatal comparison deliberately records every unsettled portable field during
// initial oracle discovery; it never treats an unexpected diagnostic as success.
func alterError(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, want fixture.Diagnostic, stage string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, query := range []bool{false, true} {
			var err error
			var stmt *sql.Stmt
			if prepared {
				stmt, err = c.PrepareContext(p.Ctx, q)
				if stmt != nil {
					defer stmt.Close()
				}
			}
			gotStage := "prepare"
			if !prepared || err == nil {
				gotStage = "execute"
				if query {
					var rows *sql.Rows
					if prepared {
						rows, err = stmt.QueryContext(p.Ctx)
					} else {
						rows, err = c.QueryContext(p.Ctx, q)
					}
					if rows != nil {
						rows.Close()
					}
				} else {
					if prepared {
						_, err = stmt.ExecContext(p.Ctx)
					} else {
						_, err = c.ExecContext(p.Ctx, q)
					}
				}
			}
			if err == nil {
				t.Errorf("%q prepared=%t query=%t unexpectedly succeeded", q, prepared, query)
				continue
			}
			got := fixture.ExtractDiagnostic(t, err)
			expectedStage := "execute"
			if prepared {
				expectedStage = stage
			}
			if got != want || gotStage != expectedStage {
				t.Errorf("ORACLE %q prepared=%t query=%t stage=%s wantStage=%s diagnostic=%#v want=%#v", q, prepared, query, gotStage, expectedStage, got, want)
			}
		}
	}
}
func alterForms(name string) []string {
	return []string{
		"ALTER MATERIALIZED VIEW " + name + " RENAME COLUMN i TO key",
		"ALTER MATERIALIZED VIEW " + name + " RENAME TO saved",
		"ALTER MATERIALIZED VIEW " + name + " OWNER TO absent_owner",
		"ALTER MATERIALIZED VIEW " + name + " SET SCHEMA absent_schema",
		"ALTER MATERIALIZED VIEW " + name + " DEPENDS ON EXTENSION absent_ext",
		"ALTER MATERIALIZED VIEW " + name + " NO DEPENDS ON EXTENSION absent_ext",
	}
}
func TestReferenceMaterializedAlterDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			alterSeed(run)
			run(fixture.Exec("CREATE VIEW wrong_view AS SELECT i FROM mv_base", 0))
			run(fixture.Exec("CREATE SEQUENCE wrong_seq", 0))
			run(fixture.Exec("CREATE INDEX wrong_index ON mv_base(i)", 0))
			for _, name := range []string{"missing_mv", "mv_base", "wrong_view", "wrong_seq", "wrong_index"} {
				for action, q := range alterForms(name) {
					if action == 0 && name != "missing_mv" && name != "wrong_seq" {
						// PostgreSQL's column callback deliberately admits table/view/index targets.
						// Reset after every route so success cannot contaminate a later expectation.
						for _, prepared := range []bool{false, true} {
							for _, query := range []bool{false, true} {
								s := fixture.Exec(q, 0)
								if query {
									s = fixture.Select(q, []string{}, []string{}, nil)
								}
								s.Prepared = prepared
								run(s)
								if name == "wrong_index" {
									if b.PG {
										run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='wrong_index'::regclass AND attnum=1", []string{"attname"}, []string{"NAME"}, [][]any{{"key"}}))
									}
									// An index descriptor's label is not its bound parent key expression.
									run(fixture.Select("SELECT pg_get_indexdef('wrong_index'::regclass) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{"CREATE INDEX wrong_index ON public.mv_base USING btree (i)"}}))
								} else {
									run(fixture.Select("SELECT key FROM "+name+" ORDER BY key", []string{"key"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}))
								}
								reset := "ALTER MATERIALIZED VIEW " + name + " RENAME COLUMN key TO i"
								s = fixture.Exec(reset, 0)
								if query {
									s = fixture.Select(reset, []string{}, []string{}, nil)
								}
								s.Prepared = prepared
								run(s)
								if name == "wrong_index" {
									if b.PG {
										run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='wrong_index'::regclass AND attnum=1", []string{"attname"}, []string{"NAME"}, [][]any{{"i"}}))
									}
									run(fixture.Select("SELECT pg_get_indexdef('wrong_index'::regclass) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{"CREATE INDEX wrong_index ON public.mv_base USING btree (i)"}}))
								} else {
									run(fixture.Select("SELECT i FROM "+name+" ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}))
								}
							}
						}
						continue
					}
					code, message := "42809", fmt.Sprintf(`"%s" is not a materialized view`, name)
					if name == "missing_mv" {
						code, message = "42P01", `relation "missing_mv" does not exist`
					}
					want := fixture.ControlsReferenceDiagnostic(code, message)
					if action == 0 && name == "wrong_seq" {
						want.Message = `cannot rename columns of relation "wrong_seq"`
						want.Detail = "This operation is not supported for sequences."
					}
					alterError(t, p, c, q, want, "execute")
				}
			}
			cases := []struct{ suffix, code, message string }{
				{"RENAME COLUMN absent TO key", "42703", `column "absent" does not exist`},
				{"RENAME COLUMN i TO s", "42701", `column "s" of relation "alter_mv" already exists`},
				{"RENAME COLUMN i TO i", "42701", `column "i" of relation "alter_mv" already exists`},
				{"RENAME COLUMN ctid TO key", "0A000", `cannot rename system column "ctid"`},
				{"RENAME COLUMN i TO ctid", "42701", `column name "ctid" conflicts with a system column name`},
				{"RENAME TO mv_base", "42P07", `relation "mv_base" already exists`},
				{"OWNER TO absent_owner", "42704", `role "absent_owner" does not exist`},
				{"SET SCHEMA absent_schema", "3F000", `schema "absent_schema" does not exist`},
				{"DEPENDS ON EXTENSION absent_ext", "42704", `extension "absent_ext" does not exist`},
				{"NO DEPENDS ON EXTENSION absent_ext", "42704", `extension "absent_ext" does not exist`},
			}
			for _, tc := range cases {
				alterError(t, p, c, "ALTER MATERIALIZED VIEW alter_mv "+tc.suffix, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), "execute")
			}
			run(materializedViewBag("alter_mv"))
		})
	}
}
func TestReferenceMaterializedAlterDiagnosticPrecedence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_intruder"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			records := new([]fixture.Diagnostic)
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, records)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			alterSeed(run)
			for _, suffix := range []string{"RENAME COLUMN absent TO taken", "RENAME TO taken", "OWNER TO absent_owner", "SET SCHEMA absent_schema"} {
				*records = nil
				run(fixture.Exec("ALTER MATERIALIZED VIEW IF EXISTS absent_mv "+suffix, 0))
				want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "absent_mv" does not exist, skipping`}
				if len(*records) != 1 || (*records)[0] != want {
					t.Errorf("ORACLE conditional %s notices=%#v want=%#v", suffix, *records, want)
				}
			}
			run(fixture.Exec("CREATE USER alter_intruder", 0))
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO alter_intruder", 0))
			}
			run(fixture.Exec("GRANT SELECT,MAINTAIN ON alter_mv TO alter_intruder", 0))
			run(fixture.Exec("GRANT SELECT ON mv_base TO alter_intruder", 0))
			run(fixture.Exec("SET search_path=public", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION alter_intruder", 0))
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			run(materializedViewBag("alter_mv"))
			run(materializedViewBag("mv_base"))
			for _, q := range alterForms("alter_mv") {
				alterError(t, p, c, q, fixture.ControlsReferenceDiagnostic("42501", "must be owner of materialized view alter_mv"), "execute")
			}
			for _, q := range alterForms("mv_base") {
				want := fixture.ControlsReferenceDiagnostic("42501", "must be owner of table mv_base")
				if strings.Contains(q, "DEPENDS ON EXTENSION") {
					want = fixture.ControlsReferenceDiagnostic("42809", `"mv_base" is not a materialized view`)
				}
				alterError(t, p, c, q, want, "execute")
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	}
}
