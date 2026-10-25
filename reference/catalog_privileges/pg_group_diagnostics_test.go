package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGGroupDiagnostics(t *testing.T) {
	pgGroupScenario(t, []string{"pgg_diag", "pgg_diag_member"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgGroupCreate(t, p, b, "pgg_diag", "NOLOGIN")
		member := pgGroupCreate(t, p, b, "pgg_diag_member", "LOGIN")
		pgRolesExec(t, p, b, "GRANT pgg_diag TO pgg_diag_member WITH ADMIN FALSE, INHERIT FALSE, SET FALSE")
		pgRolesExec(t, p, b, "CREATE TABLE pgg_diag_sentinel(a INT)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgg_diag_sentinel VALUES(7)", 1))
		check := func(t *testing.T) {
			t.Helper()
			pgGroupBoth(t, p, b, "SELECT * FROM pg_group WHERE grosysid=$1", [][]any{pgGroupRow("pgg_diag", id, member)}, id)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a FROM pgg_diag_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		cases := []struct{ name, q, code, message, position string }{
			{"missing", "SELECT missing FROM pg_catalog.pg_group", "42703", `column "missing" does not exist`, "8"},
			{"credential", "SELECT rolpassword FROM pg_catalog.pg_group", "42703", `column "rolpassword" does not exist`, "8"},
			{"flag", "SELECT rolcanlogin FROM pg_catalog.pg_group", "42703", `column "rolcanlogin" does not exist`, "8"},
			{"utf8", "/*é*/ SELECT missing FROM pg_catalog.pg_group", "42703", `column "missing" does not exist`, "14"},
			{"oid_literal", "SELECT grosysid FROM pg_group WHERE grosysid='bad'", "22P02", `invalid input syntax for type oid: "bad"`, "46"},
			{"relation", "SELECT * FROM pg_catalog.pg_group_absent", "42P01", `relation "pg_catalog.pg_group_absent" does not exist`, "15"},
			{"syntax", "SELECT groname FROM pg_group WHERE", "42601", "syntax error at end of input", "35"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					check(t)
				}
			})
		}
		for _, prepared := range []bool{false, true} {
			want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type oid: "bad"`)
			want.Context = "unnamed portal parameter $1 = '...'"
			s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_group WHERE grosysid=$1", want, "bad")
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			check(t)
		}
		pgGroupBoth(t, p, b, "SELECT * FROM pg_group WHERE grosysid=$1", nil, nil)
		// Native catalog writes/locking are capability exclusions, not a false
		// PostgreSQL 55000 claim. Never modify real PostgreSQL system catalogs.
		if !b.PG {
			for _, q := range []string{
				"INSERT INTO pg_catalog.pg_group(groname) VALUES('pgg_forbidden')",
				"INSERT INTO pg_catalog.pg_group(groname,grosysid,grolist) VALUES('pgg_forbidden',1,'{}')",
				"UPDATE pg_catalog.pg_group SET groname='pgg_forbidden' WHERE FALSE",
				"UPDATE pg_catalog.pg_group SET grolist='{}' WHERE FALSE",
				"DELETE FROM pg_catalog.pg_group WHERE FALSE",
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					check(t)
				}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT * FROM pg_group WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				check(t)
			}
		}
		check(t)
	})
}
