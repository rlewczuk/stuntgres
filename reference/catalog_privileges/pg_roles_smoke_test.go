package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGRolesFeatureSmoke(t *testing.T) {
	pgRolesScenario(t, []string{"pgr_smoke_group", "pgr_smoke_member", "pgr_smoke_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		group := pgRolesCreate(t, p, b, "pgr_smoke_group")
		member := pgRolesCreate(t, p, b, "pgr_smoke_member")
		pgRolesCreate(t, p, b, "pgr_smoke_reader")
		for _, q := range []string{"ALTER ROLE pgr_smoke_group SUPERUSER CREATEROLE CREATEDB REPLICATION BYPASSRLS", "ALTER ROLE pgr_smoke_member LOGIN", "ALTER ROLE pgr_smoke_reader LOGIN", "GRANT pgr_smoke_group TO pgr_smoke_member WITH INHERIT TRUE, SET TRUE", "CREATE TABLE pgr_smoke_sentinel(a INT)"} {
			pgRolesExec(t, p, b, q)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgr_smoke_sentinel VALUES(7)", 1))
		row := []any{"pgr_smoke_group", true, true, true, true, false, true, int64(-1), "********", nil, true, nil, group}
		pgRolesBoth(t, p, b, pgRolesSelect("SELECT * FROM pg_roles WHERE oid=$1", [][]any{row}, group))
		query := "SELECT * FROM pg_roles WHERE oid=$1"
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
		memberRow := pgRolesRow("pgr_smoke_member", member, nil)
		memberRow[5] = true
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgRolesSelect(query, [][]any{memberRow}, member))
		pgRolesExec(t, p, b, "ALTER ROLE pgr_smoke_member CREATEDB CONNECTION LIMIT 3")
		memberRow[4], memberRow[7] = true, int64(3)
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgRolesSelect(query, [][]any{memberRow}, member))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(p.Ctx, "SET ROLE pgr_smoke_reader"); err != nil {
			t.Fatal(err)
		}
		defer conn.ExecContext(p.Ctx, "RESET ROLE")
		for _, prepared := range []bool{false, true} {
			s := pgRolesSelect(query, [][]any{row}, group)
			s.Prepared = prepared
			fixture.PgDescriptionExecutorRows(t, p, b, conn, s)
		}
		if _, err := conn.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			q    string
			want fixture.Diagnostic
		}{{"UPDATE pg_roles SET rolpassword=NULL WHERE FALSE", pgRolesWriteDiagnostic("update")}, {"SELECT absent FROM pg_roles", fixture.Diagnostic{Code: "42703", Message: `column "absent" does not exist`, Position: "8"}}} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, tc.want)
				s.Prepared = prepared
				s.SelectRows = tc.q[0] == 'S'
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a FROM pgr_smoke_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgRolesSelect(query, [][]any{memberRow}, member))
	})
}
