package reference_test

import (
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func pgAuthIDPassword(t *testing.T, p *fixture.Pair, executor pgShadowQuerier, prepared bool, role string, want *string) string {
	t.Helper()
	q := "SELECT rolpassword FROM pg_catalog.pg_authid WHERE rolname=$1"
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := executor.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal("credential query prepare failed")
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx, role)
	} else {
		rows, err = executor.QueryContext(p.Ctx, q, role)
	}
	if err != nil {
		t.Fatal("credential query failed")
	}
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 1 || meta[0].Name() != "rolpassword" || meta[0].DatabaseTypeName() != "TEXT" {
		t.Fatal("credential metadata mismatch")
	}
	if !rows.Next() {
		t.Fatal("credential row missing")
	}
	var got sql.NullString
	if err := rows.Scan(&got); err != nil {
		t.Fatal("credential scan failed")
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("credential cardinality mismatch")
	}
	if want == nil {
		if got.Valid {
			t.Fatal("absent password not SQL NULL")
		}
	} else if !got.Valid || got.String != *want {
		t.Fatal("stored credential changed")
	}
	return got.String
}

func TestReferencePGAuthIDCredentials(t *testing.T) {
	pgUserScenario(t, []string{"pga_credentials", "pga_nologin"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgUserCreate(t, p, b, "pga_credentials")
		pgUserExec(t, p, b, "CREATE ROLE pga_nologin")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q := "SELECT rolpassword FROM pg_catalog.pg_authid WHERE rolname=$1"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("credential prepare failed")
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		for _, name := range []string{"pga_credentials", "pga_nologin"} {
			for _, prepared := range []bool{false, true} {
				pgAuthIDPassword(t, p, conn, prepared, name, nil)
			}
			pgAuthIDPassword(t, p, held, false, name, nil)
			for _, value := range []*string{nil, new(pgShadowMD5), new(pgShadowSCRAM(t)), nil, new("")} {
				pgShadowSetPassword(t, p, b, name, value)
				want := value
				if value != nil && *value == "" {
					want = nil
				}
				for _, prepared := range []bool{false, true} {
					pgAuthIDPassword(t, p, conn, prepared, name, want)
				}
				pgAuthIDPassword(t, p, held, false, name, want)
				pgAuthIDRoutes(t, p, b, fixture.Select("SELECT rolpassword FROM pg_roles WHERE rolname=$1", []string{"rolpassword"}, []string{"TEXT"}, [][]any{{"********"}}, name))
				expected := [][]any{}
				if name == "pga_credentials" {
					expected = [][]any{{"********"}}
				}
				pgAuthIDRoutes(t, p, b, fixture.Select("SELECT passwd FROM pg_user WHERE usename=$1", []string{"passwd"}, []string{"TEXT"}, expected, name))
			}
		}
		expirySQL := "SELECT rolvaliduntil::text AS expiry FROM pg_authid WHERE rolname='pga_credentials'"
		pgAuthIDRoutes(t, p, b, fixture.Select(expirySQL, []string{"expiry"}, []string{"TEXT"}, [][]any{{nil}}))
		for _, expiry := range []string{"2030-01-02 03:04:05+00", "infinity", "-infinity", "2000-01-01 00:00:00+00"} {
			pgUserExec(t, p, b, "ALTER ROLE pga_credentials VALID UNTIL '"+expiry+"'")
			pgAuthIDRoutes(t, p, b, fixture.Select(expirySQL, []string{"expiry"}, []string{"TEXT"}, [][]any{{expiry}}))
			var typed any = expiry
			switch expiry {
			case "2030-01-02 03:04:05+00":
				typed = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
			case "2000-01-01 00:00:00+00":
				typed = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			pgAuthIDRoutes(t, p, b, fixture.Select("SELECT rolvaliduntil FROM pg_authid WHERE rolname='pga_credentials'", []string{"rolvaliduntil"}, []string{"TIMESTAMPTZ"}, [][]any{{typed}}))
		}
		pgUserExec(t, p, b, "ALTER ROLE pga_credentials VALID UNTIL '2030-01-02 03:04:05+00'")
		expiryStmt, err := conn.PrepareContext(p.Ctx, expirySQL)
		if err != nil {
			t.Fatal(err)
		}
		defer expiryStmt.Close()
		for _, tc := range []struct{ zone, text string }{{"UTC", "2030-01-02 03:04:05+00"}, {"America/New_York", "2030-01-01 22:04:05-05"}} {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: expiryStmt, SQL: expirySQL}, b.Recorder, fixture.Select(expirySQL, []string{"expiry"}, []string{"TEXT"}, [][]any{{tc.text}}))
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET TIME ZONE", 0))
		for _, name := range []string{"pga_credentials", "pga_nologin"} {
			pgUserExec(t, p, b, "DROP ROLE "+name)
		}
	})
}
