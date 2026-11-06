package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"stuntgres/dual"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDualConnectorTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	d := newDualPair(t, p, dual.Options{}, nil)
	ctx := p.Ctx
	dualExec(t, ctx, d.DB, "CREATE TABLE dual_t (id int)")
	counts := func(want int64) {
		t.Helper()
		for name, db := range map[string]*sql.DB{"reference": d.Ref, "native": d.Native} {
			if n := dualCount(t, ctx, db, "dual_t"); n != want {
				t.Errorf("%s count %d, want %d", name, n, want)
			}
		}
	}

	tx, err := d.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var isolation, readOnly string
	const settings = "SELECT current_setting('transaction_isolation'), current_setting('transaction_read_only')"
	if err := tx.QueryRowContext(ctx, settings).Scan(&isolation, &readOnly); err != nil || isolation != "repeatable read" || readOnly != "on" {
		t.Fatalf("transaction settings %q %q, %v", isolation, readOnly, err)
	}
	const readOnlyInsert = "INSERT INTO dual_t VALUES (1)"
	_, err = tx.ExecContext(ctx, readOnlyInsert)
	if code, message := dualSQLState(err); code != "25006" || message != "cannot execute INSERT in a read-only transaction" {
		t.Fatalf("read-only INSERT: %q %q (%v)", code, message, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	rs := sqlRecords(d.Log.records(t))
	wantJSON(t, "tx_options", lastQuery(t, rs, "begin", "").TxOptions, `{"isolation":"Repeatable Read","read_only":true}`)
	if r := lastQuery(t, rs, "exec", readOnlyInsert); r.Error != "25006" || r.ErrorRef != "25006" {
		t.Errorf("read-only INSERT record: %q / %q", r.Error, r.ErrorRef)
	}

	end := func(commit bool, ids ...int) {
		t.Helper()
		tx, err := d.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) == 1 {
			dualExec(t, ctx, tx, "INSERT INTO dual_t VALUES ("+strconv.Itoa(ids[0])+")")
		} else {
			stmt, err := tx.PrepareContext(ctx, "INSERT INTO dual_t VALUES ($1)")
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range ids {
				dualStmtExec(t, ctx, stmt, id)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	end(false, 1)
	counts(0)
	end(true, 2)
	counts(1)
	end(false, 3, 4, 5)
	counts(1)
	end(true, 3, 4, 5)
	counts(4)

	for _, r := range d.Log.records(t) {
		if r.ResponseDiff != nil || r.Unmirrored != "" {
			t.Errorf("%s %q: diff %q unmirrored %q", r.Call, r.Query, r.ResponseDiff, r.Unmirrored)
		}
	}
}

func TestReferenceDualConnectorPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	d := newDualPair(t, p, dual.Options{}, nil)
	ctx := p.Ctx
	dualExec(t, ctx, d.DB, "CREATE TABLE dual_p (id int, v text)")
	dualExec(t, ctx, d.DB, "INSERT INTO dual_p SELECT i, 'v' || i FROM generate_series(1, 10) i")
	c, err := d.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	const query = "SELECT v FROM dual_p WHERE id = $1"
	stmt, err := c.PrepareContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 10; id++ {
		if id == 4 {
			// Between executions of the same statement, on the reference only.
			dualExec(t, ctx, d.Ref, "UPDATE dual_p SET v = 'z' WHERE id = 4")
		}
		var v string
		if err := stmt.QueryRowContext(ctx, id).Scan(&v); err != nil || v != fmt.Sprint("v", id) {
			t.Fatalf("id %d: %q, %v", id, v, err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}

	rs := sqlRecords(d.Log.records(t))
	prepare := lastQuery(t, rs, "prepare", query)
	wantJSON(t, "prepare response", prepare.Response, `{"num_input":1}`)
	wantJSON(t, "prepare response_ref", prepare.ResponseRef, `{"num_input":1}`)
	wantDiff(t, prepare)
	var executions []dualRecord
	for _, r := range rs {
		if r.Call == "stmt_query" {
			executions = append(executions, r)
		}
	}
	if len(executions) != 10 {
		t.Fatalf("%d stmt_query records, want 10", len(executions))
	}
	for i, r := range executions {
		id := strconv.Itoa(i + 1)
		if r.Statement != prepare.Cycle || len(r.Params) != 1 || r.Params[0].Value == nil || *r.Params[0].Value != id {
			t.Errorf("execution %s: statement %d (prepare %d) params %+v", id, r.Statement, prepare.Cycle, r.Params)
		}
		if i == 3 {
			wantDiff(t, r, `#0 row 0 v: stuntgres "v4", reference "z"`)
		} else {
			wantDiff(t, r)
		}
	}
	if r := lastQuery(t, rs, "stmt_close", query); r.ResponseDiff != nil || r.Unmirrored != "" || r.Statement != prepare.Cycle {
		t.Errorf("stmt_close record: %+v", r)
	}
}

func dualStmtExec(t *testing.T, ctx context.Context, stmt *sql.Stmt, args ...any) int64 {
	t.Helper()
	res, err := stmt.ExecContext(ctx, args...)
	if err != nil {
		t.Fatalf("statement %v: %v", args, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("statement %v rows affected: %v", args, err)
	}
	return n
}
