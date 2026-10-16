package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type objectGrantWriteProbe struct{ calls int }

func (w *objectGrantWriteProbe) Write(p []byte) (int, error) {
	w.calls++
	return len(p), nil
}

func TestReferenceObjectGrantTypeControls(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT USAGE ON TYPE pg_catalog.int4 TO PUBLIC"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error("builtin ACL cleanup", q, err)
				}
			}
		}()
		rejectDump := func() {
			if b.PG {
				return // Native host controls have no PostgreSQL counterpart.
			}
			var writer objectGrantWriteProbe
			err := stuntgres.Dump(p.Ctx, p.Native, &writer)
			if err == nil || writer.calls != 0 {
				t.Fatal("type ACL Dump must reject before every writer callback", err, writer.calls)
			}
		}
		run(fixture.Exec("REVOKE USAGE ON TYPE pg_catalog.int4 FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "CREATE TEMP TABLE og_builtin_denied(x INT)", "type", "integer")
		})
		rejectDump()
		run(fixture.Exec("GRANT USAGE ON TYPE pg_catalog.int4 TO PUBLIC", 0))
		run(fixture.Exec("CREATE TYPE og_control_enum AS ENUM('a')", 0))
		run(fixture.Exec("REVOKE USAGE ON TYPE og_control_enum FROM PUBLIC", 0))
		rejectDump()
		run(fixture.Exec("GRANT USAGE ON TYPE og_control_enum TO PUBLIC", 0))
		if !b.PG {
			// Regrant retains an explicit ACL even when effective rights match
			// creation defaults; Dump cannot discard that raw provenance.
			rejectDump()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal("Reset explicit type ACL provenance", err)
			}
			var baseline bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
				t.Fatal("baseline type ACL Dump", err)
			}
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TYPE og_restore_prefix AS ENUM('b'); GRANT USAGE ON TYPE pg_catalog.int4 TO PUBLIC"))
			fixture.State(t, err, "0A000")
			run(fixture.ImplicitError("GRANT USAGE ON TYPE og_restore_prefix TO PUBLIC", fixture.Diagnostic{Code: "42704", Message: `type "og_restore_prefix" does not exist`}))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal("Reset builtin ACL baseline", err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(baseline.String())); err != nil {
				t.Fatal("baseline type Restore/remap", err)
			}
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("CREATE TEMP TABLE og_builtin_allowed(x INT)", 0))
			run(fixture.Exec("INSERT INTO og_builtin_allowed VALUES(9)", 1))
			run(fixture.Select("SELECT x FROM og_builtin_allowed", []string{"x"}, []string{"INT4"}, [][]any{{int64(9)}}))
		})
	})
}
