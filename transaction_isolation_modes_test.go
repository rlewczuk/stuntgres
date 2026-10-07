package stuntgres_test

import (
	"context"
	"testing"
)

func TestTransactionIsolationModes(t *testing.T) {
	_, a, _, notices := controlsFixture(t)
	ctx := context.Background()
	held, err := a.PrepareContext(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	controlsNotices(t, notices, "", "")
	result, err := held.ExecContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
	controlsNotices(t, notices, "25P01", "SET TRANSACTION can only be used in transaction blocks")
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	isolationShow(t, a, "transaction_isolation", "read committed")
	isolationShow(t, a, "transaction_read_only", "off")
	transactionAffected(t, ctx, a, "SHOW application_name", false, 0)
	transactionAffected(t, ctx, a, "RESET application_name", false, 0)
	transactionAffected(t, ctx, a, "SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED READ ONLY, READ WRITE, ISOLATION LEVEL REPEATABLE READ", true, 0)
	isolationShow(t, a, "transaction_isolation", "repeatable read")
	isolationShow(t, a, "transaction_read_only", "off")
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
	_, err = a.ExecContext(ctx, "BEGIN READ ONLY junk")
	sqlState(t, err, "42601")
	transactionAffected(t, ctx, a, "BEGIN READ ONLY,READ WRITE ISOLATION LEVEL READ UNCOMMITTED", false, 0)
	isolationShow(t, a, "transaction_isolation", "read uncommitted")
	isolationShow(t, a, "transaction_read_only", "off")
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
}
func TestTransactionIsolationReadOnlyAnalysisPrecedence(t *testing.T) {
	for _, query := range []string{"UPDATE t SET missing=2", "INSERT INTO missing VALUES(2)", "DELETE FROM t WHERE missing=2"} {
		_, a, b := transactionConnections(t)
		ctx := context.Background()
		transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
		transactionAffected(t, ctx, a, "BEGIN READ ONLY", false, 0)
		_, err := a.ExecContext(ctx, query)
		want := "42703"
		if query == "INSERT INTO missing VALUES(2)" {
			want = "42P01"
		}
		sqlState(t, err, want)
		transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
	}
	_, a, b := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
	transactionAffected(t, ctx, a, "SET default_transaction_read_only TO on", false, 0)
	isolationError(t, a, "INSERT INTO t VALUES(1)", "25006", "cannot execute INSERT in a read-only transaction", false)
	isolationError(t, a, "CREATE TABLE forbidden(v INT)", "25006", "cannot execute CREATE TABLE in a read-only transaction", false)
	transactionAffected(t, ctx, a, "BEGIN READ ONLY", false, 0)
	transactionAffected(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", true, 0)
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t", false)
}

func TestTransactionIsolationOwnerSettingReads(t *testing.T) {
	_, a, _ := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, a, "SET default_transaction_read_only TO on", false, 0)
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := tx.QueryRowContext(ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != "off" {
		t.Fatal(got, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", false, 0)
	for name, want := range map[string]string{"transaction_isolation": "repeatable read", "transaction_read_only": "on", "default_transaction_isolation": "read committed", "default_transaction_read_only": "on"} {
		if err := a.QueryRowContext(ctx, "SELECT current_setting('"+name+"')").Scan(&got); err != nil || got != want {
			t.Fatal(name, got, err)
		}
	}
	rows, err := a.QueryContext(ctx, "SHOW ALL")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for rows.Next() {
		var name, value, description string
		if err := rows.Scan(&name, &value, &description); err != nil {
			t.Fatal(err)
		}
		seen[name] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if seen["transaction_isolation"] != "repeatable read" || seen["transaction_read_only"] != "on" {
		t.Fatal(seen)
	}
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
}
