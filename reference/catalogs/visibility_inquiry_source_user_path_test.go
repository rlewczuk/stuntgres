package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVisibilityInquiryUserPathSourceConsumerRoutes(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgv_user_a", "pgv_user_b"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			db.SetMaxOpenConns(1)
			roles := []string{"pgv_user_a", "pgv_user_b"}
			var createdRoles []string
			t.Cleanup(func() {
				for _, q := range []string{"RESET ROLE", "SET search_path=public", "DROP SCHEMA IF EXISTS pgv_user_a CASCADE", "DROP SCHEMA IF EXISTS pgv_user_b CASCADE"} {
					if _, err := db.ExecContext(context.Background(), q); err != nil {
						t.Error(q, err)
					}
				}
				if !backend.PG {
					for _, role := range createdRoles {
						if _, err := db.ExecContext(context.Background(), "DROP ROLE "+role); err != nil {
							t.Error("native role cleanup", role, err)
						}
					}
				}
			})
			for i, q := range []string{"CREATE ROLE pgv_user_a", "CREATE ROLE pgv_user_b", "CREATE SCHEMA pgv_user_a AUTHORIZATION pgv_user_a", "CREATE SCHEMA pgv_user_b AUTHORIZATION pgv_user_b", "CREATE TABLE pgv_user_a.pgv_user_pair(id INT)", "CREATE TABLE pgv_user_b.pgv_user_pair(id INT)"} {
				s := fixture.Exec(q, 0)
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, db, backend.Recorder, s)
				if i < len(roles) {
					createdRoles = append(createdRoles, roles[i])
				}
			}
			conn, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			a, b := referenceVisibilityPair(t, conn, "pgv_user_pair", "pgv_user_a,pgv_user_b", "pgv_user_b,pgv_user_a")
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(`SET search_path="$user",public`, 0))
			held, err := conn.PrepareContext(p.Ctx, "SELECT pg_table_is_visible($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, tc := range []struct {
				role   string
				av, bv bool
			}{{"pgv_user_a", true, false}, {"pgv_user_b", false, true}, {"NONE", false, false}} {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET ROLE "+tc.role, 0))
				for _, obj := range []struct {
					id   int64
					want bool
				}{{a, tc.av}, {b, tc.bv}} {
					s := fixture.Select("SELECT pg_table_is_visible($1)", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{obj.want}}, obj.id)
					s.CheckMetadata = fixture.VisibilityBoolMetadata
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
					var got bool
					if err := held.QueryRowContext(p.Ctx, obj.id).Scan(&got); err != nil || got != obj.want {
						t.Fatal(tc.role, obj, got, err)
					}
				}
			}
		})
	}
}
