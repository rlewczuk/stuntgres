package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func extensionConcurrentInstall(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, db *sql.DB, peer *sql.Conn) {
	t.Helper()
	if pg {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("DROP EXTENSION unaccent", 0))
	} else {
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
	defer cancel()
	first, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback()
	second, err := peer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback()
	if _, err := first.ExecContext(ctx, "CREATE EXTENSION unaccent"); err != nil {
		t.Fatal(err)
	}
	extensionCount(t, p, first, r, 1)
	observerBefore, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	extensionCount(t, p, observerBefore, r, 0)
	observerBefore.Close()
	var loser error
	if pg {
		// PostgreSQL blocks the competing INSERT into pg_extension. Its genuine
		// activity projection is synchronization-only oracle setup, not advertised
		// native SQL. Native transactions stage privately and fence at publication.
		var pid int
		if err := second.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { _, err := second.ExecContext(ctx, "CREATE EXTENSION unaccent"); result <- err }()
		joined := false
		defer func() {
			cancel()
			if !joined {
				select {
				case <-result:
				case <-time.After(15 * time.Second):
					t.Error("competing install worker did not join")
				}
			}
		}()
		observer, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		for {
			var waiting bool
			if err := observer.QueryRowContext(ctx, "SELECT COALESCE(wait_event_type = 'Lock',false) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case err := <-result:
				joined = true
				t.Fatal("competitor failed before lock boundary", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			default:
				runtime.Gosched()
			}
		}
		if err := first.Commit(); err != nil {
			t.Fatal(err)
		}
		select {
		case loser = <-result:
			joined = true
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	} else {
		if _, err := second.ExecContext(ctx, "CREATE EXTENSION unaccent"); err != nil {
			t.Fatal(err)
		}
		extensionCount(t, p, second, r, 1)
		if err := first.Commit(); err != nil {
			t.Fatal(err)
		}
		loser = second.Commit()
	}
	want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_extension_name_index"`, Detail: "Key (extname)=(unaccent) already exists.", Schema: "pg_catalog", Table: "pg_extension", Constraint: "pg_extension_name_index"}
	fixture.AssertDiagnostic(t, loser, &want)
	_ = second.Rollback()
	extensionCount(t, p, c, r, 1)
	extensionInventory(t, p, c)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
}
