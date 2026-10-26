package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Expected rights are specified from REL_18_6 ACL semantics, not backend parity.
func accessSpecialScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step), bool)) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910201, 910202, 910203, 910204, 910205, 910206, 910207, 910208, 910209)
		defer func() {
			for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET lo_compat_privileges", "RESET application_name", "DROP OWNED BY og_object_reader CASCADE", "DROP OWNED BY og_object_other CASCADE", "REVOKE ALL ON PARAMETER lc_messages,work_mem,\"special.custom\" FROM PUBLIC", "GRANT ALL ON PARAMETER lc_messages,work_mem TO CURRENT_USER"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error("special cleanup", q, err)
				}
			}
		}()
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(largeObjectValue("lo_from_bytea(910201::oid,decode('00ff61','hex'))", "OID", int64(910201)))
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910201 TO og_object_reader", 0))
		})
		run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO og_object_reader", 0))
		body(t, p, b, c, run, prepared)
	})
}

type accessSpecialCall struct {
	query string
	args  []any
}

func accessSpecialCalls(t *testing.T, p *fixture.Pair, c *sql.Conn) []accessSpecialCall {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='og_object_reader'").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	return []accessSpecialCall{
		{"SELECT has_largeobject_privilege($1::name,$2::oid,$3::text) AS v", []any{"og_object_reader", int64(910201), "SELECT"}},
		{"SELECT has_largeobject_privilege($1::oid,$2::oid,$3::text) AS v", []any{oid, int64(910201), "SELECT"}},
		{"SELECT has_largeobject_privilege($1::oid,$2::text) AS v", []any{int64(910201), "SELECT"}},
		{"SELECT has_parameter_privilege($1::name,$2::text,$3::text) AS v", []any{"og_object_reader", "lc_messages", "SET"}},
		{"SELECT has_parameter_privilege($1::oid,$2::text,$3::text) AS v", []any{oid, "lc_messages", "SET"}},
		{"SELECT has_parameter_privilege($1::text,$2::text) AS v", []any{"lc_messages", "SET"}},
	}
}

func TestReferenceAccessSpecialOverloads(t *testing.T) {
	accessSpecialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		for _, call := range accessSpecialCalls(t, p, c) {
			held := accessExecutionHeld(t, p, c, call.query)
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				s := objectGrantAccessValue(call.query, true, call.args...)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					nullable, known := columns[0].Nullable()
					if b.PG {
						if known {
							t.Fatal("pq unexpectedly reports nullability")
						}
					} else if !known || !nullable {
						t.Fatal("typed inquiry BOOL nullability", nullable, known)
					}
				}
				run(s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				args := append([]any(nil), call.args...)
				if args[len(args)-1] == "SELECT" {
					args[len(args)-1] = "UPDATE"
				} else {
					args[len(args)-1] = "ALTER SYSTEM"
				}
				s = objectGrantAccessValue(call.query, false, args...)
				run(s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			})
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT has_largeobject_privilege(910201::oid,'SELECT'),has_parameter_privilege('LC_MESSAGES','SET')", []string{"has_largeobject_privilege", "has_parameter_privilege"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
			run(objectGrantAccessValue("SELECT has_largeobject_privilege(r,o,p) AS v FROM (VALUES('og_object_reader'::name,910201::oid,'SELECT'::text)) AS s(r,o,p)", true))
			run(objectGrantAccessValue("SELECT has_parameter_privilege(r,n,p) AS v FROM (VALUES('og_object_reader'::name,'lc_messages'::text,'SET'::text)) AS s(r,n,p)", true))
		})
		for _, tc := range []struct {
			oid       int
			signature string
		}{{6348, "has_largeobject_privilege(name,oid,text)"}, {6349, "has_largeobject_privilege(oid,text)"}, {6350, "has_largeobject_privilege(oid,oid,text)"}, {6205, "has_parameter_privilege(name,text,text)"}, {6206, "has_parameter_privilege(oid,text,text)"}, {6207, "has_parameter_privilege(text,text)"}} {
			run(largeObjectValue(fmt.Sprintf("'%s'::regprocedure::oid", tc.signature), "OID", int64(tc.oid)))
		}
	})
}
