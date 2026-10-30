package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDefaultToastCompressionSettings(t *testing.T) {
	toastScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := toastRun(t, p, b, c)
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		for _, s := range toastState(b.PG, "pglz", "default") {
			run(s)
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, s)
		}
		columns := []string{"name", "setting", "boot_val", "reset_val", "category", "short_desc", "context", "vartype", "enumvals", "unit", "min_val", "max_val", "extra_desc", "source", "sourcefile", "sourceline", "pending_restart"}
		types := []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL"}
		metadataSQL := "SELECT name,setting,boot_val,reset_val,category,short_desc,context,vartype,enumvals::text,unit,min_val,max_val,extra_desc,source,sourcefile,sourceline,pending_restart FROM pg_settings WHERE name='default_toast_compression'"
		metadata := func(value, source string) fixture.Step {
			return fixture.Select(metadataSQL, columns, types, [][]any{{toastSetting, value, "pglz", "pglz", "Client Connection Defaults / Statement Behavior", "Sets the default compression method for compressible values.", "user", "enum", "{pglz,lz4}", nil, nil, nil, nil, source, nil, nil, false}})
		}
		run(metadata("pglz", "default"))
		run(fixture.Prepared(metadata("pglz", "default")))
		show, err := c.PrepareContext(p.Ctx, "SHOW default_toast_compression")
		if err != nil {
			t.Fatal(err)
		}
		defer show.Close()
		current, err := c.PrepareContext(p.Ctx, "SELECT current_setting('default_toast_compression')")
		if err != nil {
			t.Fatal(err)
		}
		defer current.Close()
		mutation, err := c.PrepareContext(p.Ctx, "SET default_toast_compression='LZ4'")
		if err != nil {
			t.Fatal(err)
		}
		defer mutation.Close()
		run(toastText(b.PG, "SHOW default_toast_compression", toastSetting, "pglz"))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: mutation, SQL: "SET default_toast_compression='LZ4'"}, b.Recorder, fixture.Exec("SET default_toast_compression='LZ4'", 0))
		for _, value := range []string{"lz4", "pglz"} {
			if value == "pglz" {
				run(fixture.Exec("SET default_toast_compression='PGLZ'", 0))
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: show, SQL: "SHOW default_toast_compression"}, b.Recorder, toastText(b.PG, "SHOW default_toast_compression", toastSetting, value))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: current, SQL: "SELECT current_setting('default_toast_compression')"}, b.Recorder, toastText(b.PG, "SELECT current_setting('default_toast_compression')", "current_setting", value))
			run(metadata(value, "session"))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, toastText(b.PG, "SHOW default_toast_compression", toastSetting, "pglz"))
		}
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				toastText(b.PG, "SELECT set_config('default_toast_compression','LZ4',false)", "set_config", "lz4"),
				fixture.Exec("SELECT current_setting('default_toast_compression')", 1),
				fixture.Exec("SET default_toast_compression=DEFAULT", 0),
				toastText(b.PG, "SHOW default_toast_compression", toastSetting, "pglz"),
				fixture.Exec("SET default_toast_compression=lz4", 0),
				fixture.Exec("RESET default_toast_compression", 0),
				toastText(b.PG, "SHOW default_toast_compression", toastSetting, "pglz"),
			} {
				s.Prepared = prepared
				run(s)
			}
		}
	})
}

func TestReferenceDefaultToastCompressionTransactions(t *testing.T) {
	toastScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				commands []string
				commit bool
				visible, end string
			}{
				{[]string{"SET default_toast_compression=lz4"}, false, "lz4", "pglz"},
				{[]string{"SET default_toast_compression=lz4"}, true, "lz4", "lz4"},
				{[]string{"SET LOCAL default_toast_compression=lz4"}, true, "lz4", "pglz"},
				{[]string{"SET LOCAL default_toast_compression=lz4"}, false, "lz4", "pglz"},
				{[]string{"SELECT set_config('default_toast_compression','lz4',true)"}, true, "lz4", "pglz"},
				{[]string{"SET default_toast_compression=lz4", "SAVEPOINT toast_s", "SET default_toast_compression=pglz", "ROLLBACK TO toast_s", "RELEASE toast_s"}, false, "lz4", "pglz"},
				{[]string{"SAVEPOINT toast_s", "SET LOCAL default_toast_compression=lz4", "ROLLBACK TO toast_s", "RELEASE toast_s"}, true, "pglz", "pglz"},
			} {
				run := toastRun(t, p, b, c)
				run(fixture.Exec("RESET default_toast_compression", 0))
				var baselineSource string
				if err := c.QueryRowContext(p.Ctx, "SELECT source FROM pg_settings WHERE name='default_toast_compression'").Scan(&baselineSource); err != nil {
					t.Fatal(err)
				}
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				txrun := toastRun(t, p, b, tx)
				for _, q := range tc.commands {
					s := fixture.Exec(q, 0)
					if q == "SELECT set_config('default_toast_compression','lz4',true)" {
						s = toastText(b.PG, q, "set_config", "lz4")
					}
					s.Prepared = prepared
					txrun(s)
				}
				visibleSource := "session"
				if tc.visible == "pglz" {
					visibleSource = baselineSource
				}
				for _, s := range toastState(b.PG, tc.visible, visibleSource) {
					s.Prepared = prepared
					txrun(s)
				}
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, toastText(b.PG, "SHOW default_toast_compression", toastSetting, "pglz"))
				if tc.commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				endSource := baselineSource
				if tc.end == "lz4" {
					endSource = "session"
				}
				for _, s := range toastState(b.PG, tc.end, endSource) {
					s.Prepared = prepared
					run(s)
				}
			}
		}
	})
}
