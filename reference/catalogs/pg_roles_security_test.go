package reference_test

import (
	"encoding/base64"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGRolesSecurity(t *testing.T) {
	pgRolesScenario(t, []string{"pgr_secret", "pgr_reader", "pgr_privileged"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgRolesCreate(t, p, b, "pgr_secret")
		pgRolesCreate(t, p, b, "pgr_reader")
		priv := pgRolesCreate(t, p, b, "pgr_privileged")
		pgRolesExec(t, p, b, "ALTER ROLE pgr_reader LOGIN")
		pgRolesExec(t, p, b, "ALTER ROLE pgr_privileged SUPERUSER CREATEROLE CREATEDB REPLICATION BYPASSRLS CONNECTION LIMIT 0 VALID UNTIL '-infinity'")
		var notices []fixture.Diagnostic
		_, conn, _ := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		key := base64.StdEncoding.EncodeToString(make([]byte, 32))
		scram := "SCRAM-SHA-256$4096:" + base64.StdEncoding.EncodeToString([]byte("roles-safe-salt")) + "$" + key + ":" + key
		for _, tc := range []struct {
			sql     string
			notices []fixture.Diagnostic
		}{
			{"NULL", nil},
			{"''", []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "empty string is not a valid password, clearing password"}}},
			{"'roles-private-plaintext'", nil},
			{"'md501234567890123456789012345678901'", []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01P01", Message: "setting an MD5-encrypted password", Detail: "MD5 password support is deprecated and will be removed in a future release of PostgreSQL.", Hint: "Refer to the PostgreSQL documentation for details about migrating to another password type."}}},
			{"'" + scram + "'", nil},
		} {
			notices = nil
			// Secret setup never enters the recorder or any diagnostic failure output.
			if _, err := conn.ExecContext(p.Ctx, "ALTER ROLE pgr_secret PASSWORD "+tc.sql); err != nil {
				t.Fatal("credential setup failed")
			}
			if !reflect.DeepEqual(notices, tc.notices) {
				t.Fatal("credential setup notice mismatch")
			}
			for _, ordinary := range []bool{false, true} {
				if ordinary {
					if _, err := conn.ExecContext(p.Ctx, "SET ROLE pgr_reader"); err != nil {
						t.Fatal(err)
					}
				}
				for _, prepared := range []bool{false, true} {
					s := pgRolesSelect("SELECT * FROM pg_catalog.pg_roles WHERE oid=$1", [][]any{pgRolesRow("pgr_secret", id, nil)}, id)
					s.Prepared = prepared
					fixture.PgDescriptionExecutorRows(t, p, b, conn, s)
					row := []any{"pgr_privileged", true, true, true, true, false, true, int64(0), "********", "-infinity", true, nil, priv}
					s = pgRolesSelect("SELECT * FROM pg_roles WHERE oid=$1", [][]any{row}, priv)
					s.Prepared = prepared
					fixture.PgDescriptionExecutorRows(t, p, b, conn, s)
				}
				if ordinary {
					if _, err := conn.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		pgRolesExec(t, p, b, "ALTER ROLE pgr_privileged LOGIN")
		pgRolesBoth(t, p, b, fixture.Select("SELECT rolcanlogin,rolpassword FROM pg_roles WHERE oid=$1", []string{"rolcanlogin", "rolpassword"}, []string{"BOOL", "TEXT"}, [][]any{{true, "********"}}, priv))
		pgRolesBoth(t, p, b, pgRolesSelect("SELECT * FROM pg_roles WHERE rolname='public'", [][]any{}))
	})
}

func TestReferencePGRolesMembership(t *testing.T) {
	pgRolesScenario(t, []string{"pgr_member", "pgr_group"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgRolesCreate(t, p, b, "pgr_member")
		pgRolesCreate(t, p, b, "pgr_group")
		pgRolesExec(t, p, b, "ALTER ROLE pgr_group SUPERUSER CREATEROLE CREATEDB REPLICATION BYPASSRLS LOGIN")
		row := pgRolesRow("pgr_member", id, nil)
		for _, q := range []string{"GRANT pgr_group TO pgr_member WITH ADMIN TRUE, INHERIT TRUE, SET TRUE", "GRANT pgr_group TO pgr_member WITH ADMIN FALSE, INHERIT FALSE, SET FALSE", "ALTER ROLE pgr_member NOINHERIT", "GRANT pgr_group TO pgr_member WITH SET TRUE", "REVOKE ADMIN OPTION FOR pgr_group FROM pgr_member", "REVOKE pgr_group FROM pgr_member"} {
			pgRolesExec(t, p, b, q)
			if q == "ALTER ROLE pgr_member NOINHERIT" {
				row[2] = false
			}
			pgRolesBoth(t, p, b, pgRolesSelect("SELECT * FROM pg_roles WHERE oid=$1", [][]any{row}, id))
		}
		pgRolesExec(t, p, b, "GRANT pgr_group TO pgr_member WITH SET TRUE")
		pgRolesExec(t, p, b, "ALTER ROLE pgr_group NOSUPERUSER")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, q := range []string{"SET SESSION AUTHORIZATION pgr_member", "SET ROLE pgr_group"} {
			if _, err := conn.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.PgDescriptionExecutorRows(t, p, b, conn, pgRolesSelect("SELECT * FROM pg_roles WHERE oid=$1", [][]any{row}, id))
		if _, err := conn.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Fatal(err)
		}
	})
}
