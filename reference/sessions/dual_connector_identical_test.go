package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"slices"
	"stuntgres/dual"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDualConnectorIdentical(t *testing.T) {
	p := fixture.NewScenario(t)
	d := newDualPair(t, p, dual.Options{}, nil)
	ctx := p.Ctx
	c, err := d.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dualExec(t, ctx, c, "CREATE TABLE dual_items (id int PRIMARY KEY, name text NOT NULL)")
	const insert = "INSERT INTO dual_items VALUES ($1, $2), ($3, $4)"
	if n := dualExec(t, ctx, c, insert, 1, "a", 2, "b"); n != 2 {
		t.Fatalf("INSERT affected %d, want 2", n)
	}

	const selectAll = "SELECT id, name FROM dual_items ORDER BY id"
	rows, err := c.QueryContext(ctx, selectAll)
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"INT4", "TEXT"} {
		nullable, known := types[i].Nullable()
		if types[i].DatabaseTypeName() != want || nullable || !known {
			t.Errorf("column %d: %s nullable %t known %t, want %s known not-null", i, types[i].DatabaseTypeName(), nullable, known, want)
		}
	}
	var got [][2]any
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		got = append(got, [2]any{id, name})
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if want := [][2]any{{int64(1), "a"}, {int64(2), "b"}}; !slices.Equal(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}

	const selectName = "SELECT name FROM dual_items WHERE id = $1"
	stmt, err := c.PrepareContext(ctx, selectName)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range []string{1: "a", 2: "b"} {
		if id == 0 {
			continue
		}
		var name string
		if err := stmt.QueryRowContext(ctx, id).Scan(&name); err != nil || name != want {
			t.Fatalf("prepared id %d: %q, %v", id, name, err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	update, err := c.PrepareContext(ctx, "UPDATE dual_items SET name = $1 WHERE id = $2")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]any{{"a", 1}, {"b", 2}} {
		if n := dualStmtExec(t, ctx, update, args...); n != 1 {
			t.Fatalf("prepared UPDATE %v affected %d", args, n)
		}
	}
	if err := update.Close(); err != nil {
		t.Fatal(err)
	}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	dualExec(t, ctx, tx, "INSERT INTO dual_items VALUES (3, 'c')")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := c.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	all := d.Log.records(t)
	for i, r := range all {
		if r.Cycle != uint64(i+1) || r.ConnectionID != all[0].ConnectionID {
			t.Errorf("record %d: connection %d cycle %d", i, r.ConnectionID, r.Cycle)
		}
		if r.ResponseDiff != nil || r.Error != "" || r.ErrorRef != "" || r.Unmirrored != "" {
			t.Errorf("record %d %s %q: diff %q error %q/%q unmirrored %q", i, r.Call, r.Query, r.ResponseDiff, r.Error, r.ErrorRef, r.Unmirrored)
		}
	}
	rs := sqlRecords(all)
	want := []string{"exec", "exec", "query", "prepare", "stmt_query", "stmt_query", "stmt_close", "prepare",
		"stmt_exec", "stmt_exec", "stmt_close", "begin", "exec", "commit", "ping"}
	if got := dualCalls(rs); !slices.Equal(got, want) {
		t.Fatalf("calls %q, want %q", got, want)
	}
	params, err := json.Marshal(rs[1].Params)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON(t, "INSERT params", params, `[
		{"index":1,"name":"","type":"int64","format":"text","null":false,"encoding":"text","value":"1"},
		{"index":2,"name":"","type":"string","format":"text","null":false,"encoding":"text","value":"a"},
		{"index":3,"name":"","type":"int64","format":"text","null":false,"encoding":"text","value":"2"},
		{"index":4,"name":"","type":"string","format":"text","null":false,"encoding":"text","value":"b"}]`)
	for _, raw := range []json.RawMessage{rs[1].Response, rs[1].ResponseRef} {
		wantJSON(t, "INSERT response", raw, `{"rows_affected":2,"last_insert_id":null}`)
	}
	const set = `{"columns":["id","name"],"types":["INT4","TEXT"],"rows":2,"partial":false}`
	for _, raw := range []json.RawMessage{rs[2].Response, rs[2].ResponseRef} {
		wantJSON(t, "SELECT response", raw, `{"sets":[`+set+`],"complete":true}`)
	}
	if rs[4].Statement != rs[3].Cycle || rs[8].Statement != rs[7].Cycle {
		t.Errorf("statement links: %d/%d, %d/%d", rs[4].Statement, rs[3].Cycle, rs[8].Statement, rs[7].Cycle)
	}
	if n := dualCount(t, ctx, d.Ref, "dual_items"); n != 3 {
		t.Errorf("reference count %d, want 3", n)
	}
	if n := dualCount(t, ctx, d.Native, "dual_items"); n != 3 {
		t.Errorf("native count %d, want 3", n)
	}
}

// dualInterfaces is what database/sql can discover on a raw driver object.
type dualInterfaces struct {
	conn  [8]bool // ConnPrepareContext, ExecerContext, QueryerContext, ConnBeginTx, Pinger, SessionResetter, Validator, NamedValueChecker
	exec  [2]bool // legacy Execer, Queryer
	rows  [6]bool // ColumnType ScanType, DatabaseTypeName, Length, Nullable, PrecisionScale; RowsNextResultSet
	stmts [4]bool // StmtExecContext, StmtQueryContext, NamedValueChecker, ColumnConverter
}

func probeDualInterfaces(t *testing.T, ctx context.Context, c *sql.Conn) dualInterfaces {
	t.Helper()
	var got dualInterfaces
	err := c.Raw(func(raw any) error {
		dc := raw.(driver.Conn)
		_, got.conn[0] = dc.(driver.ConnPrepareContext)
		_, got.conn[1] = dc.(driver.ExecerContext)
		_, got.conn[2] = dc.(driver.QueryerContext)
		_, got.conn[3] = dc.(driver.ConnBeginTx)
		_, got.conn[4] = dc.(driver.Pinger)
		_, got.conn[5] = dc.(driver.SessionResetter)
		_, got.conn[6] = dc.(driver.Validator)
		_, got.conn[7] = dc.(driver.NamedValueChecker)
		_, got.exec[0] = dc.(driver.Execer)
		_, got.exec[1] = dc.(driver.Queryer)
		r, err := dc.(driver.QueryerContext).QueryContext(ctx, "SELECT 1 AS a", nil)
		if err != nil {
			return err
		}
		_, got.rows[0] = r.(driver.RowsColumnTypeScanType)
		_, got.rows[1] = r.(driver.RowsColumnTypeDatabaseTypeName)
		_, got.rows[2] = r.(driver.RowsColumnTypeLength)
		_, got.rows[3] = r.(driver.RowsColumnTypeNullable)
		_, got.rows[4] = r.(driver.RowsColumnTypePrecisionScale)
		_, got.rows[5] = r.(driver.RowsNextResultSet)
		if err := r.Close(); err != nil {
			return err
		}
		s, err := dc.(driver.ConnPrepareContext).PrepareContext(ctx, "SELECT $1::int")
		if err != nil {
			return err
		}
		_, got.stmts[0] = s.(driver.StmtExecContext)
		_, got.stmts[1] = s.(driver.StmtQueryContext)
		_, got.stmts[2] = s.(driver.NamedValueChecker)
		_, got.stmts[3] = s.(driver.ColumnConverter)
		return s.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReferenceDualConnectorInterfaces(t *testing.T) {
	p := fixture.NewScenario(t)
	d := newDualPair(t, p, dual.Options{}, nil)
	conn := func(db *sql.DB) *sql.Conn {
		c, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	native, wrapped := conn(p.Native), conn(d.DB)
	want := dualInterfaces{
		conn:  [8]bool{true, true, true, true, true, true, true, false},
		rows:  [6]bool{true, true, true, true, true, true},
		stmts: [4]bool{true, true, false, false},
	}
	wantNative := want
	wantNative.exec = [2]bool{true, true} // legacy Execer/Queryer: native only (dual exposes the context forms)
	if got := probeDualInterfaces(t, p.Ctx, native); got != wantNative {
		t.Errorf("native interfaces %+v, want %+v", got, wantNative)
	}
	if got := probeDualInterfaces(t, p.Ctx, wrapped); got != want {
		t.Errorf("dual interfaces %+v, want %+v", got, want)
	}
}
