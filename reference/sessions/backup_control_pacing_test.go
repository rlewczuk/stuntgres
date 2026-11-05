package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// The default-fast witness must perform a genuine spread checkpoint without
// consuming most of the scenario's fixed hang guard. Restore exact prior file
// authority; this is stock-only pacing, not an engine or fixture configuration.
func backupCheckpointPacing(t *testing.T, p *fixture.Pair, pg bool) {
	t.Helper()
	if !pg {
		return
	}
	var value, source, reset string
	if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT setting,source,reset_val FROM pg_settings WHERE name='checkpoint_completion_target'").Scan(&value, &source, &reset); err != nil {
		t.Fatal(err)
	}
	apply := func(ctx context.Context, sql, want string) error {
		if _, err := fixture.Admin.ExecContext(ctx, sql); err != nil {
			return err
		}
		var reloaded bool
		if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_reload_conf()").Scan(&reloaded); err != nil {
			return err
		}
		if !reloaded {
			return fmt.Errorf("checkpoint pacing reload returned false")
		}
		for {
			var actual string
			if err := fixture.Admin.QueryRowContext(ctx, "SHOW checkpoint_completion_target").Scan(&actual); err != nil {
				return err
			}
			if actual == want {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}
	restoreSQL := "ALTER SYSTEM RESET checkpoint_completion_target"
	// Capture the actual auto-conf entry as well as the effective setting so
	// RESET restores configuration-file/default provenance rather than adding it.
	var fileValue string
	err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT setting FROM pg_file_settings WHERE name='checkpoint_completion_target' AND sourcefile LIKE '%/postgresql.auto.conf' AND applied ORDER BY seqno DESC LIMIT 1").Scan(&fileValue)
	if err == nil {
		restoreSQL = "ALTER SYSTEM SET checkpoint_completion_target = '" + fileValue + "'"
	} else if err == sql.ErrNoRows {
		restoreSQL = "ALTER SYSTEM RESET checkpoint_completion_target"
	} else {
		t.Fatal(err)
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := apply(ctx, restoreSQL, value); err != nil {
			t.Error("checkpoint pacing cleanup", err)
			return
		}
		for {
			var gotValue, gotSource, gotReset string
			if err := fixture.Admin.QueryRowContext(ctx, "SELECT setting,source,reset_val FROM pg_settings WHERE name='checkpoint_completion_target'").Scan(&gotValue, &gotSource, &gotReset); err != nil {
				t.Error("checkpoint pacing authority cleanup", err)
				return
			}
			if gotValue == value && gotSource == source && gotReset == reset {
				return
			}
			if err := ctx.Err(); err != nil {
				t.Error("checkpoint pacing authority not restored", gotValue, gotSource, gotReset, err)
				return
			}
		}
	}
	t.Cleanup(cleanup)
	ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
	defer cancel()
	if err := apply(ctx, "ALTER SYSTEM SET checkpoint_completion_target = 0", "0"); err != nil {
		t.Fatal(err)
	}
}
