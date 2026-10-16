package fixture

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// SQLCursorCancelDemand cancels an admitted oracle query and checks its complete diagnostic.
func SQLCursorCancelDemand(t *testing.T, p *Pair, c *sql.Conn, query string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
	defer cancel()
	var pid int
	if err := c.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		err     error
		exposed bool
	}
	done := make(chan outcome, 1)
	go func() {
		rows, err := c.QueryContext(ctx, query)
		exposed := rows != nil
		if rows != nil {
			rows.Close()
		}
		done <- outcome{err, exposed}
	}()
	joinFailure := func(message string, err error) {
		cancel()
		result := <-done
		t.Fatalf("%s: %v; joined FETCH: %v", message, err, result.err)
	}
	for {
		var active bool
		if err := Admin.QueryRowContext(ctx, "SELECT COALESCE((SELECT state='active' AND query=$2 FROM pg_stat_activity WHERE pid=$1),FALSE)", pid, query).Scan(&active); err != nil {
			joinFailure("admission barrier", err)
		}
		if active {
			break
		}
		select {
		case result := <-done:
			t.Fatalf("FETCH completed before observed admission: %#v", result)
		default:
		}
	}
	var signaled bool
	if err := Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
		joinFailure("server cancellation", err)
	}
	result := <-done
	if result.exposed {
		t.Fatal("canceled FETCH exposed partial rows")
	}
	want := Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}
	AssertDiagnostic(t, result.err, &want)
}
