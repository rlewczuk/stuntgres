package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePGRolesAttributes(t *testing.T) {
	pgRolesScenario(t, []string{"pgr_attr_role", "pgr_attr_group", "pgr_attr_user"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			name, kind string
			login      bool
		}{{"pgr_attr_role", "ROLE", false}, {"pgr_attr_group", "GROUP", false}, {"pgr_attr_user", "USER", true}} {
			pgRolesExec(t, p, b, "CREATE "+tc.kind+" "+tc.name)
			// Obtain identity from a separately owned sequence, never this view.
			pgRolesExec(t, p, b, "CREATE SEQUENCE "+tc.name+"_id")
			pgRolesExec(t, p, b, "ALTER SEQUENCE "+tc.name+"_id OWNER TO "+tc.name)
			var id int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT relowner FROM pg_class WHERE relname=$1", tc.name+"_id").Scan(&id); err != nil || id <= 0 {
				t.Fatal("independent role identity", id, err)
			}
			pgRolesExec(t, p, b, "DROP SEQUENCE "+tc.name+"_id")
			row := pgRolesRow(tc.name, id, nil)
			row[5] = tc.login
			query := "SELECT * FROM pg_roles WHERE oid=$1"
			pgRolesBoth(t, p, b, pgRolesSelect(query, [][]any{row}, id))
			pgRolesExec(t, p, b, "ALTER ROLE "+tc.name+" SUPERUSER NOINHERIT CREATEROLE CREATEDB LOGIN REPLICATION BYPASSRLS CONNECTION LIMIT 0 VALID UNTIL '2030-01-02 03:04:05+00'")
			row = []any{tc.name, true, false, true, true, true, true, int64(0), "********", time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC), true, nil, id}
			pgRolesBoth(t, p, b, pgRolesSelect(query, [][]any{row}, id))
			// A single-option ALTER must retain all omitted fields.
			pgRolesExec(t, p, b, "ALTER ROLE "+tc.name+" CONNECTION LIMIT 9")
			row[7] = int64(9)
			pgRolesBoth(t, p, b, pgRolesSelect(query, [][]any{row}, id))
			pgRolesExec(t, p, b, "ALTER ROLE "+tc.name+" NOSUPERUSER INHERIT NOCREATEROLE NOCREATEDB NOLOGIN NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1")
			row = pgRolesRow(tc.name, id, time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
			pgRolesBoth(t, p, b, pgRolesSelect(query, [][]any{row}, id))
			pgRolesBoth(t, p, b, fixture.Select("SELECT usename FROM pg_user WHERE usesysid=$1", []string{"usename"}, []string{"NAME"}, [][]any{}, id))
			pgRolesExec(t, p, b, "DROP ROLE "+tc.name)
		}
	})
}
