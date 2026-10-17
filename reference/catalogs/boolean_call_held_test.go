package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBooleanCallHeldPaths(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bcp_role_a", "bcp_role_b"})
	for _, schema := range []string{"bcp_role_a", "bcp_role_b"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := booleanCallScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ids := booleanCallObjects(t, p, conn, b)
			run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			for kind, fn := range booleanCallFunctions {
				for _, prefix := range []string{"", "pg_catalog."} {
					for _, query := range []string{
						"SELECT " + prefix + fn + "($1) AS visible",
						"SELECT NOT (FALSE OR " + prefix + fn + "(s.id)) AS visible FROM bcp_pred_off.inputs s ORDER BY s.seq",
						"SELECT TRUE AS visible WHERE " + prefix + fn + "($1)",
					} {
						stmt, err := conn.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(query, err)
						}
						func() {
							defer stmt.Close()
							held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
							for round, path := range []string{"bcp_pred_a,bcp_pred_b,pg_catalog", "bcp_pred_b,bcp_pred_a,pg_catalog", "bcp_pred_a,bcp_pred_b,pg_catalog"} {
								run(fixture.Exec("SET search_path="+path, 0))
								a, bv := round != 1, round == 1
								if query == "SELECT NOT (FALSE OR "+prefix+fn+"(s.id)) AS visible FROM bcp_pred_off.inputs s ORDER BY s.seq" {
									run(fixture.Exec("TRUNCATE bcp_pred_off.inputs", 0))
									run(fixture.Exec("INSERT INTO bcp_pred_off.inputs VALUES(1,$1),(2,$2),(3,$3),(4,$4),(5,NULL)", 5, ids[kind][0], ids[kind][1], ids[kind][2], ids[kind][3]))
									s := fixture.Select(query, []string{"visible"}, []string{"BOOL"}, [][]any{{!a}, {!bv}, {true}, {nil}, {nil}})
									s.CheckMetadata = fixture.VisibilityBoolMetadata
									fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
									continue
								}
								for i, arg := range []any{ids[kind][0], ids[kind][1], ids[kind][2], ids[kind][3], nil} {
									value := []any{a, bv, false, nil, nil}[i]
									rows := [][]any{{value}}
									if query == "SELECT TRUE AS visible WHERE "+prefix+fn+"($1)" {
										rows = nil
										if value == true {
											rows = [][]any{{true}}
										}
									}
									s := fixture.Select(query, []string{"visible"}, []string{"BOOL"}, rows, arg)
									s.CheckMetadata = fixture.VisibilityBoolMetadata
									fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
								}
							}
						}()
					}
				}
			}
			// A retained eager native cursor must not be retargeted by path refresh.
			// pq streams results, so finish that cursor before issuing another command.
			q := "SELECT pg_table_is_visible($1) AS visible"
			run(fixture.Exec("SET search_path=bcp_pred_a,bcp_pred_b,pg_catalog", 0))
			rows, err := conn.QueryContext(p.Ctx, q, ids[0][0])
			if err != nil {
				t.Fatal(err)
			}
			if !b.PG {
				run(fixture.Exec("SET search_path=bcp_pred_b,bcp_pred_a,pg_catalog", 0))
			}
			visibilityInquiryRows(t, rows, nil, "visible", []any{true})
			run(fixture.Exec("SET search_path=bcp_pred_b,bcp_pred_a,pg_catalog", 0))
			run(fixture.Select(q, []string{"visible"}, []string{"BOOL"}, [][]any{{false}}, ids[0][0]))
			for _, q := range []string{"CREATE ROLE bcp_role_a", "CREATE ROLE bcp_role_b", "CREATE SCHEMA bcp_role_a AUTHORIZATION bcp_role_a", "CREATE SCHEMA bcp_role_b AUTHORIZATION bcp_role_b", "CREATE TABLE bcp_role_a.bcp_role_object(v int)", "CREATE TABLE bcp_role_b.bcp_role_object(v int)"} {
				run(fixture.Exec(q, 0))
			}
			var roleID int64
			if err := conn.QueryRowContext(p.Ctx, "SELECT c.oid FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='bcp_role_a' AND c.relname='bcp_role_object'").Scan(&roleID); err != nil || roleID <= 0 {
				t.Fatal(roleID, err)
			}
			run(fixture.Exec(`SET search_path="$user",public`, 0))
			roleQuery := "SELECT TRUE AS visible WHERE pg_catalog.pg_table_is_visible($1)"
			stmt, err := conn.PrepareContext(p.Ctx, roleQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			defer func() { run(fixture.Exec("RESET ROLE", 0)); run(fixture.Exec("SET search_path=public", 0)) }()
			for _, role := range []string{"bcp_role_a", "bcp_role_b", "bcp_role_a"} {
				run(fixture.Exec("SET ROLE "+role, 0))
				var rows [][]any
				if role == "bcp_role_a" {
					rows = [][]any{{true}}
				}
				s := fixture.Select(roleQuery, []string{"visible"}, []string{"BOOL"}, rows, roleID)
				s.CheckMetadata = fixture.VisibilityBoolMetadata
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: roleQuery}, b.Recorder, s)
			}
		})
	}
}
