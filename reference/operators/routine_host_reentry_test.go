package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoutineHostReentry(t *testing.T) {
	for _, action := range []string{"query", "reset", "restore", "replace", "type", "acl"} {
		t.Run(action, func(t *testing.T) {
			hostNativeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, native *hostNativeRuntime) {
				hostFunction(t, p, b, c, "host_reentry_good", "increment-v1", hostIncrementPython, "", true)
				hostBag(t, p, b, c, "host_reentry_good('a=>7,b=>NULL'::hstore)", [][]any{{"a", "8"}, {"b", nil}, {"c", "3"}})
				if b.PG {
					return
				}
				hostFunction(t, p, b, c, "host_reentry_wait", "wait-v1", hostIncrementPython, "", true)
				hostExec(t, p, b, c, "CREATE TABLE host_reentry_witness(v integer)")
				var saved bytes.Buffer
				if action == "restore" {
					if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
						t.Fatal(err)
					}
				}
				if action == "acl" {
					hostExec(t, p, b, c, "CREATE ROLE host_reentry_reader")
					defer routineLanguagesCleanup(t, c, "RESET ROLE", "DROP OWNED BY host_reentry_reader CASCADE", "DROP ROLE host_reentry_reader")()
					hostExec(t, p, b, c, "SET ROLE host_reentry_reader")
				}
				q := "SELECT key,value FROM each(host_reentry_wait($1::hstore)) ORDER BY key"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				native.mu.Lock()
				native.gate = func(ctx context.Context) error {
					switch action {
					case "query":
						var n int
						if err := p.Native.QueryRowContext(ctx, "SELECT 40+2").Scan(&n); err != nil {
							return err
						}
						if n != 42 {
							return fmt.Errorf("independent callback query=%d", n)
						}
						_, err := p.Native.ExecContext(ctx, "INSERT INTO host_reentry_witness VALUES (42)")
						return err
					case "reset":
						return stuntgres.Reset(ctx, p.Native)
					case "restore":
						return stuntgres.Restore(ctx, p.Native, bytes.NewReader(saved.Bytes()))
					case "replace":
						_, err := p.Native.ExecContext(ctx, "CREATE OR REPLACE FUNCTION host_reentry_wait(v hstore) RETURNS hstore LANGUAGE go_host TRANSFORM FOR TYPE hstore AS 'double-v1'")
						return err
					case "type":
						if _, err := p.Native.ExecContext(ctx, "DROP EXTENSION hstore CASCADE"); err != nil {
							return err
						}
						_, err := p.Native.ExecContext(ctx, "CREATE EXTENSION hstore")
						return err
					case "acl":
						_, err := p.Native.ExecContext(ctx, "REVOKE EXECUTE ON FUNCTION host_reentry_wait(hstore) FROM PUBLIC")
						return err
					}
					return errors.New("unknown reentry action")
				}
				native.mu.Unlock()
				if action == "query" {
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					hostRows(t, p, b, held, q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "8"}, {"c", "3"}}, "a=>7")
					hostRows(t, p, b, c, "SELECT v FROM host_reentry_witness", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
				} else {
					rows, err := stmt.QueryContext(p.Ctx, "a=>7")
					if rows != nil {
						for rows.Next() {
						}
						if err == nil {
							err = rows.Err()
						}
						rows.Close()
					}
					if err == nil {
						t.Fatal("stale callback continuation published success")
					}
					if action == "acl" {
						fixture.AssertDiagnostic(t, err, new(fixture.ControlsReferenceDiagnostic("42501", "permission denied for function host_reentry_wait")))
						hostExec(t, p, b, c, "RESET ROLE")
						hostExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION host_reentry_wait(hstore) TO PUBLIC")
					}
					if action == "reset" {
						hostExec(t, p, b, c, "CREATE EXTENSION hstore")
					}
					hostFunction(t, p, b, c, "host_reentry_after", "double-v1", hostDoublePython, "", true)
					hostBag(t, p, b, c, "host_reentry_after('a=>7,b=>NULL'::hstore)", [][]any{{"a", "14"}, {"b", nil}, {"d", "second"}})
				}
			})
		})
	}
}
