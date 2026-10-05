package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

func TestTimestampSQLTransactionChainClock(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK", "ABORTED_COMMIT"} {
		t.Run(end, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			timestampSQLChainClock(t, context.Background(), db, end)
		})
	}
}

func timestampSQLChainClock(t *testing.T, ctx context.Context, db *sql.DB, end string) {
	t.Helper()
	aborted := end == "ABORTED_COMMIT"
	if aborted {
		end = "COMMIT"
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exec := func(q string, count int64, args ...any) {
		t.Helper()
		r, err := conn.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != count {
			t.Fatal(q, n, err)
		}
	}
	exec("CREATE TABLE chain_anchor(n INTEGER)", 0)
	exec("INSERT INTO chain_anchor VALUES(1)", 1)
	exec("CREATE TABLE chain_values(v TIMESTAMP)", 0)
	defer conn.ExecContext(ctx, "ROLLBACK")
	before := time.Now().UTC()
	exec("BEGIN", 0)
	after := time.Now().UTC()
	read := func() time.Time {
		t.Helper()
		var now, today, tomorrow, yesterday time.Time
		err := conn.QueryRowContext(ctx, "SELECT TIMESTAMP 'now',TIMESTAMP 'today',TIMESTAMP 'tomorrow',TIMESTAMP 'yesterday' FROM chain_anchor").Scan(&now, &today, &tomorrow, &yesterday)
		if err != nil {
			t.Fatal(err)
		}
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		if !today.Equal(midnight) || !tomorrow.Equal(midnight.AddDate(0, 0, 1)) || !yesterday.Equal(midnight.AddDate(0, 0, -1)) {
			t.Fatal(now, today, tomorrow, yesterday)
		}
		exec("INSERT INTO chain_values VALUES($1)", 1, "now")
		var parameter time.Time
		if err = conn.QueryRowContext(ctx, "SELECT v FROM chain_values ORDER BY v DESC").Scan(&parameter); err != nil || !parameter.Equal(now) {
			t.Fatal("parameter start", parameter, now, err)
		}
		return now
	}
	first := read()
	if first.Before(before.Add(-time.Microsecond)) || first.After(after.Add(time.Microsecond)) {
		t.Fatal("BEGIN start", first, before, after)
	}
	time.Sleep(3 * time.Millisecond)
	if got := read(); !got.Equal(first) {
		t.Fatal("old transaction unstable", got, first)
	}
	if aborted {
		_, err := conn.ExecContext(ctx, "INSERT INTO chain_values VALUES($1)", "nonsense")
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "22007" {
			t.Fatal("expected timestamp failure", err)
		}
	}
	before = time.Now().UTC()
	exec(end+" AND CHAIN", 0)
	after = time.Now().UTC()
	fresh := read()
	if !fresh.After(first) || fresh.Before(before.Add(-time.Microsecond)) || fresh.After(after.Add(time.Microsecond)) {
		t.Fatal("CHAIN start", fresh, first, before, after)
	}
	time.Sleep(3 * time.Millisecond)
	if got := read(); !got.Equal(fresh) {
		t.Fatal("chained transaction unstable", got, fresh)
	}
	exec(end, 0)
	time.Sleep(3 * time.Millisecond)
	var implicit time.Time
	if err = conn.QueryRowContext(ctx, "SELECT TIMESTAMP 'now' FROM chain_anchor").Scan(&implicit); err != nil || !implicit.After(fresh) {
		t.Fatal("plain end retained old clock", implicit, fresh, err)
	}
}
