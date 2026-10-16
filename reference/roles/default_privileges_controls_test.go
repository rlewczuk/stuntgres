package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoleDefaultPrivilegesTransactions(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		for i, end := range []string{"ROLLBACK", "COMMIT"} {
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
			run(fixture.Exec("BEGIN", 0))
			q := "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader"
			if b.PG {
				run(fixture.Exec(q, 0))
				run(fixture.Exec(end, 0))
			} else {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				s = fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				run(fixture.Exec("ROLLBACK", 0))
			}
			name := []string{"dp_rollback", "dp_commit"}[i]
			dpTable(run, name, 9)
			fixture.RoleGrantActor(run, "dp_reader", func() {
				if b.PG && end == "COMMIT" {
					dpRead(run, name, 9)
				} else {
					fixture.RoleGrantPermissionDenied(run, "SELECT x FROM "+name, "table", name)
				}
			})
		}
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
		q := "ALTER DEFAULT PRIVILEGES FOR ROLE dp_missing IN SCHEMA dp_missing GRANT SELECT ON TABLES TO dp_missing"
		var statement *sql.Stmt
		if prepared {
			var err error
			statement, err = c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("utility Prepare before names", err)
			}
			defer statement.Close()
		}
		run(fixture.Exec("BEGIN READ ONLY", 0))
		var source fixture.Executor = c
		if statement != nil {
			source = fixture.HeldStatement{Executor: c, Statement: statement, SQL: q}
		}
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER DEFAULT PRIVILEGES in a read-only transaction"})
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, source, b.Recorder, s)
		run(fixture.Exec("ROLLBACK", 0))
		dpTable(run, "dp_readonly_after", 11)
		fixture.RoleGrantActor(run, "dp_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM dp_readonly_after", "table", "dp_readonly_after")
		})
	})
}

func TestReferenceRoleDefaultPrivilegesControls(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		dpTable(run, "dp_control_old", 7)
		// Default-only absent-provider state must not silently disappear in a Dump.
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON ROUTINES TO dp_reader", 0))
		if !b.PG {
			var out bytes.Buffer
			err := stuntgres.Dump(p.Ctx, p.Native, &out)
			var state interface{ SQLState() string }
			if err == nil || errors.As(err, &state) || out.Len() != 0 {
				t.Fatal("default-only Dump must fail ordinarily before output", out.String(), err)
			}
		}
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader", 0))
		dpTable(run, "dp_control_new", 9)
		if !b.PG {
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE dp_replacement(x INT); ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_other"))
			fixture.State(t, err, "0A000")
		}
		fixture.RoleGrantActor(run, "dp_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM dp_control_old", "table", "dp_control_old")
			dpRead(run, "dp_control_new", 9)
		})
		dpTable(run, "dp_control_after_restore", 11)
		fixture.RoleGrantActor(run, "dp_reader", func() { dpRead(run, "dp_control_after_restore", 11) })
		run(fixture.Exec("GRANT dp_other TO dp_reader WITH INHERIT TRUE", 0))
		if !b.PG {
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
			var out bytes.Buffer
			err := stuntgres.Dump(p.Ctx, p.Native, &out)
			var state interface{ SQLState() string }
			if err == nil || errors.As(err, &state) || out.Len() != 0 {
				t.Fatal("materialized ACL Dump boundary", out.String(), err)
			}
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION dp_reader", 0))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"dp_reader", "dp_reader"}}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			dpTable(run, "dp_reset_new", 13)
			fixture.RoleGrantActor(run, "dp_reader", func() { fixture.RoleGrantPermissionDenied(run, "SELECT x FROM dp_reset_new", "table", "dp_reset_new") })
			run(fixture.Exec("GRANT SELECT ON dp_reset_new TO dp_other", 0))
			fixture.RoleGrantActor(run, "dp_reader", func() { dpRead(run, "dp_reset_new", 13) })
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("CREATE TYPE dp_clean_enum AS ENUM('a')", 0))
			run(fixture.Exec("CREATE DOMAIN dp_clean_domain AS INT", 0))
			run(fixture.Exec("CREATE TYPE dp_clean_composite AS(x INT)", 0))
			dpTable(run, "dp_clean", 17)
			var clean bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &clean); err != nil {
				t.Fatal("pristine baseline Dump", err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(clean.String())); err != nil {
				t.Fatal("pristine baseline Restore", err)
			}
			dpRead(run, "dp_clean", 17)
			fixture.RoleGrantActor(run, "dp_reader", func() { fixture.RoleGrantPermissionDenied(run, "SELECT x FROM dp_clean", "table", "dp_clean") })
		} else {
			// The oracle has no native replacement API. Its shared preceding SQL proves
			// directive/materialized separation; real transactional state is covered above.
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
			dpTable(run, "dp_reset_new", 13)
			fixture.RoleGrantActor(run, "dp_reader", func() { fixture.RoleGrantPermissionDenied(run, "SELECT x FROM dp_reset_new", "table", "dp_reset_new") })
		}
	})
}
