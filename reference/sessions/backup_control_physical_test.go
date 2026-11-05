package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceBackupControlPhysicalBoundary(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		backupCheckpointPacing(t, p, b.PG)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE backup_physical_sentinel(v integer)", 0))
		run(fixture.Exec("INSERT INTO backup_physical_sentinel VALUES(7)", 1))
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		backupRoutes(t, p, b, c, backupAbsent("SELECT * FROM pg_backup_stop(false)", b.PG))
		for _, tc := range []struct {
			start, stop string
			args        []any
			label       string
		}{
			{"SELECT pg_backup_start('default_backup')", "SELECT * FROM pg_backup_stop()", nil, "default_backup"},
			{"SELECT pg_backup_start(fast=>true,label=>'named_backup')", "SELECT * FROM pg_backup_stop(true)", nil, "named_backup"},
		} {
			backupPair(t, p, b, c, c, tc.start, tc.label, tc.stop, true, tc.args...)
		}
		q := "SELECT pg_backup_start($1::text,$2::boolean)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, label := range []string{"", "x", strings.Repeat("a", 1023), strings.Repeat("a", 1024), strings.Repeat("é", 512), " \t ", "quote'", "line\nbreak"} {
			backupPair(t, p, b, c, held, q, label, "SELECT * FROM pg_backup_stop(false)", false, label, true)
		}
		for _, label := range []string{strings.Repeat("a", 1025), strings.Repeat("é", 512) + "a", strings.Repeat("é", 513)} {
			for _, fast := range []bool{false, true} {
				s := backupError(q, "22023", "backup label too long (max 1024 bytes)", "", label, fast)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, backupSelect(b.PG, q, []string{"pg_backup_start"}, []string{"PG_LSN"}, [][]any{{nil}}, nil, true))
		backupRoutes(t, p, b, c, backupAbsent("SELECT * FROM pg_backup_stop(false)", b.PG))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, backupAbsent("SELECT pg_backup_stop(false)", b.PG))
		peer.Close()
		run(fixture.Select("SELECT v FROM backup_physical_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := c.QueryContext(ctx, q, "x", true)
			if !errors.Is(err, context.Canceled) {
				t.Fatal("host cancellation", err)
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() {
				t.Fatal("physical failure changed logical state")
			}
		}
	})
}
