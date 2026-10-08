package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestBinaryBasicSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exec := func(q string, count int64) {
		t.Helper()
		result, err := conn.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		n, err := result.RowsAffected()
		if err != nil || n != count {
			t.Fatal(q, n, count, err)
		}
	}
	query := func(q string, want []any, args ...any) {
		t.Helper()
		rows, err := conn.QueryContext(ctx, q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal(q, rows.Err())
		}
		got := make([]any, len(want))
		dest := make([]any, len(want))
		for i := range got {
			dest[i] = &got[i]
		}
		if err := rows.Scan(dest...); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(q, got, want, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
	}
	diagnostic := func(err error, want stuntgres.Error) {
		t.Helper()
		var got *stuntgres.Error
		want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("diagnostic %#v want %#v (%v)", got, want, err)
		}
	}
	exec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA)", 0)
	exec(`INSERT INTO t VALUES(1,'\x00ff8041008042ff00','\xff8000'),(2,'\x010201030201','\x01010102'),(3,'',''),(4,NULL,NULL),(5,'\xc3a9f09f9880',''),(6,'\x010201','\x0201')`, 6)
	q := "SELECT v||s AS cat,bit_length(v) AS bits,octet_length(v) AS octets,length(v) AS len,btrim(v,s) AS bt,ltrim(v,s) AS lt,rtrim(v,s) AS rt FROM t WHERE id=1"
	cat := []byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 255, 128, 0}
	want := []any{cat, int64(72), int64(9), int64(9), []byte{65, 0, 128, 66}, []byte{65, 0, 128, 66, 255, 0}, []byte{0, 255, 128, 65, 0, 128, 66}}
	for _, mode := range []string{"hex", "escape"} {
		exec("SET bytea_output="+mode, 0)
		query(q, want)
	}
	query("SELECT btrim(v,s),ltrim(v,s),rtrim(v,s) FROM t WHERE id=2", []any{[]byte{3}, []byte{3, 2, 1}, []byte{1, 2, 1, 3}})
	query("SELECT v||s,btrim(v,s),length(v),bit_length(v) FROM t WHERE id=3", []any{[]byte{}, []byte{}, int64(0), int64(0)})
	full, err := conn.PrepareContext(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	preparedRows, err := full.QueryContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !preparedRows.Next() {
		t.Fatal(preparedRows.Err())
	}
	preparedGot := make([]any, 7)
	preparedDest := make([]any, 7)
	for i := range preparedGot {
		preparedDest[i] = &preparedGot[i]
	}
	if err := preparedRows.Scan(preparedDest...); err != nil || !reflect.DeepEqual(preparedGot, want) {
		t.Fatal(preparedGot, err)
	}
	if preparedRows.Next() || preparedRows.Err() != nil {
		t.Fatal(preparedRows.Err())
	}
	preparedRows.Close()
	full.Close()
	query("SELECT v||s,btrim(v,s),length(v),bit_length(v) FROM t WHERE id=4", []any{nil, nil, nil, nil})
	query("SELECT length(v),octet_length(v),bit_length(v),btrim(v,s) FROM t WHERE id=5", []any{int64(6), int64(6), int64(48), []byte{195, 169, 240, 159, 152, 128}})
	query("SELECT btrim(v,s) FROM t WHERE id=6", []any{[]byte{}})
	pq := "SELECT v||$1 AS cat,btrim(v,$1) AS bt FROM t WHERE id=1"
	stmt, err := conn.PrepareContext(ctx, pq)
	if err != nil {
		t.Fatal(err)
	}
	v := []byte{0, 255, 128, 65, 0, 128, 66, 255, 0}
	for _, tc := range []struct {
		arg  any
		want []any
	}{
		{[]byte{255, 128, 0}, []any{cat, []byte{65, 0, 128, 66}}},
		{[]byte{}, []any{v, v}}, {nil, []any{nil, nil}},
		{`\x41`, []any{[]byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 92, 120, 52, 49}, v}},
		{[]byte{255, 128, 0}, []any{cat, []byte{65, 0, 128, 66}}},
	} {
		rows, err := stmt.QueryContext(ctx, tc.arg)
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var a, b any
		if err := rows.Scan(&a, &b); err != nil || !reflect.DeepEqual([]any{a, b}, tc.want) {
			t.Fatal(a, b, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		rows.Close()
	}
	stmt.Close()
	for _, where := range []string{"id=4", "id=99"} {
		rows, err := conn.QueryContext(ctx, "SELECT btrim(v,s) AS b,length(v) AS n FROM t WHERE "+where)
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || types[0].DatabaseTypeName() != "BYTEA" || types[0].ScanType() != reflect.TypeFor[[]byte]() || types[1].DatabaseTypeName() != "INT4" || types[1].ScanType() != reflect.TypeFor[int64]() {
			t.Fatal(types, err)
		}
		for rows.Next() {
			var a, b any
			if err := rows.Scan(&a, &b); err != nil || a != nil || b != nil {
				t.Fatal(a, b, err)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT btrim(v) FROM t", stuntgres.Error{Code: "42883", Message: "function btrim(bytea) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}},
		{`SELECT btrim(v,'\q') FROM t`, stuntgres.Error{Code: "22P02", Message: "invalid input syntax for type bytea", Position: "16"}},
	} {
		_, err := conn.ExecContext(ctx, tc.q)
		diagnostic(err, tc.want)
		s, err := conn.PrepareContext(ctx, tc.q)
		if s != nil {
			s.Close()
			t.Fatal("failed Prepare published handle")
		}
		diagnostic(err, tc.want)
	}
	exec("CREATE TABLE dst(v BYTEA)", 0)
	exec("INSERT INTO dst SELECT v||s FROM t WHERE id=1", 1)
	exec("CREATE VIEW binary_view AS SELECT v||s AS cat,bit_length(v) AS bits,octet_length(v) AS octets,length(v) AS len,btrim(v,s) AS bt,ltrim(v,s) AS lt,rtrim(v,s) AS rt FROM t WHERE id=1", 0)
	exec(`CREATE VIEW binary_literal_view AS SELECT btrim(v,'\x0080ff') AS bt,v||'\x00' AS cat FROM t WHERE id=1`, 0)
	held, err := conn.QueryContext(ctx, "SELECT * FROM binary_view")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	query("SELECT * FROM binary_view", want)
	query("SELECT v FROM dst", []any{cat})
	query("SELECT * FROM binary_literal_view", []any{[]byte{65, 0, 128, 66}, []byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 0}})
	if !held.Next() {
		t.Fatal(held.Err())
	}
	got := make([]any, 7)
	dest := make([]any, 7)
	for i := range got {
		dest[i] = &got[i]
	}
	if err := held.Scan(dest...); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	got[0].([]byte)[0] = 99
	if held.Next() || held.Err() != nil {
		t.Fatal(held.Err())
	}
	held.Close()
	query("SELECT v FROM dst", []any{cat})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	write, err := conn.PrepareContext(ctx, "INSERT INTO dst SELECT v||s FROM t WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := write.ExecContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	write.Close()
	query("SELECT v FROM dst", []any{cat})
	exec("CREATE ROLE binary_smoke_reader", 0)
	exec("SET ROLE binary_smoke_reader", 0)
	rows, err := conn.QueryContext(ctx, "SELECT length(v),btrim(v,s),v||s FROM t")
	if rows != nil {
		rows.Close()
		t.Fatal("unauthorized cursor")
	}
	diagnostic(err, stuntgres.Error{Code: "42501", Message: "permission denied for table t"})
	exec("RESET ROLE", 0)
}
