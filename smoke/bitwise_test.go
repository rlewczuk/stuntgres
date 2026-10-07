package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestBitwiseSmoke(t *testing.T) {
	ctx := context.Background()
	c, err := stuntgres.NewDriver().OpenConnector("bitwise-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	defer db.Close()
	exec := func(q string, want int64) {
		t.Helper()
		r, err := db.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != want {
			t.Fatal(q, n, err)
		}
	}
	check := func(q string, types []string, want [][]any, args ...any) {
		t.Helper()
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var err error
			if prepared {
				s, e := db.PrepareContext(ctx, q)
				if e != nil {
					t.Fatal(q, e)
				}
				rows, err = s.QueryContext(ctx, args...)
				s.Close()
			} else {
				rows, err = db.QueryContext(ctx, q, args...)
			}
			if err != nil {
				t.Fatal(q, err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != len(types) {
				t.Fatal(meta, err)
			}
			for i, typ := range types {
				if meta[i].DatabaseTypeName() != typ || meta[i].ScanType() != reflect.TypeOf(int64(0)) {
					t.Fatal(q, meta[i])
				}
				if strings.Contains(q, " FROM t ") {
					if meta[i].Name() != "?column?" {
						t.Fatal(q, meta[i])
					}
					if _, known := meta[i].Nullable(); known {
						t.Fatal("computed nullability became physical")
					}
				}
			}
			got := make([][]any, 0)
			for rows.Next() {
				values := make([]any, len(types))
				targets := make([]any, len(types))
				for i := range values {
					targets[i] = &values[i]
				}
				if err := rows.Scan(targets...); err != nil {
					t.Fatal(err)
				}
				got = append(got, values)
			}
			err = rows.Err()
			rows.Close()
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal(q, got, want, err)
			}
		}
	}
	exec("CREATE TABLE t(id INTEGER,s SMALLINT,i INTEGER,b BIGINT)", 0)
	exec("INSERT INTO t VALUES(1,1,1,1),(2,-8,-8,-8),(3,-32768,-2147483648,-9223372036854775808),(4,32767,2147483647,9223372036854775807),(5,NULL,NULL,NULL)", 5)
	for _, tc := range []struct{ cast, typ string }{{"smallint", "INT2"}, {"integer", "INT4"}, {"bigint", "INT8"}} {
		q := "SELECT 91::" + tc.cast + " & 15::" + tc.cast + ",32::" + tc.cast + " | 3::" + tc.cast + ",17::" + tc.cast + " # 5::" + tc.cast + ",~1::" + tc.cast + ",1::" + tc.cast + " << 4,8::" + tc.cast + " >> 2 FROM t WHERE id=1"
		check(q, []string{tc.typ, tc.typ, tc.typ, tc.typ, tc.typ, tc.typ}, [][]any{{int64(11), int64(35), int64(20), int64(-2), int64(16), int64(2)}})
	}
	check("SELECT s & 15::smallint,i | 3,b # 5,~s,s << 1,b >> 2 FROM t ORDER BY id", []string{"INT2", "INT4", "INT8", "INT2", "INT2", "INT8"}, [][]any{
		{int64(1), int64(3), int64(4), int64(-2), int64(2), int64(0)},
		{int64(8), int64(-5), int64(-3), int64(7), int64(-16), int64(-2)},
		{int64(0), int64(-2147483645), int64(-9223372036854775803), int64(32767), int64(0), int64(-2305843009213693952)},
		{int64(15), int64(2147483647), int64(9223372036854775802), int64(-32768), int64(-2), int64(2305843009213693951)},
		{nil, nil, nil, nil, nil, nil},
	})
	check("SELECT s << 32,i << -1,b << 64 FROM t WHERE id=1", []string{"INT2", "INT4", "INT8"}, [][]any{{int64(1), int64(-2147483648), int64(1)}})
	check("SELECT ~s,i & 1,b >> 1 FROM t WHERE FALSE", []string{"INT2", "INT4", "INT8"}, [][]any{})
	held, err := db.PrepareContext(ctx, "SELECT s & $1,b << $2 FROM t WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, a := range []any{3, nil, 1} {
		var x, y any
		if err := held.QueryRowContext(ctx, a, 1).Scan(&x, &y); err != nil {
			t.Fatal(err)
		}
		want := any(int64(1))
		if a == nil {
			want = nil
		}
		if x != want || y != int64(2) {
			t.Fatal(x, y)
		}
	}
	exec("UPDATE t SET i=i | 4 WHERE i & 1=1", 2)
	exec("DELETE FROM t WHERE id=2 AND i & 1=0", 1)
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT '1' & '2' FROM t", stuntgres.Error{Code: "42725", Message: "operator is not unique: unknown & unknown", Position: "12", Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}},
		{"SELECT ~true FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: ~ boolean", Position: "8", Hint: "No operator matches the given name and argument type. You might need to add an explicit type cast."}},
		{"SELECT 1::bigint << 1::bigint FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: bigint << bigint", Position: "18", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT NULL::integer & 'bad' FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "24"}},
	} {
		rows, err := db.QueryContext(ctx, tc.q)
		if rows != nil {
			rows.Close()
		}
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(tc.q, err)
		}
		s, err := db.PrepareContext(ctx, tc.q)
		if s != nil {
			s.Close()
		}
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(tc.q, err)
		}
	}
	exec("CREATE TABLE atomic(id INTEGER,s SMALLINT)", 0)
	exec("INSERT INTO atomic VALUES(99,99)", 1)
	exec("CREATE TABLE source(id INTEGER,x TEXT)", 0)
	exec("INSERT INTO source VALUES(1,'1'),(2,'bad')", 2)
	if _, err := db.ExecContext(ctx, "INSERT INTO atomic SELECT id,CAST(x AS INTEGER) & 1 FROM source ORDER BY id"); err == nil {
		t.Fatal("later decoding published")
	}
	check("SELECT id,s FROM atomic", []string{"INT4", "INT2"}, [][]any{{int64(99), int64(99)}})
	exec("INSERT INTO atomic VALUES(1,1),(2,2)", 2)
	if _, err := db.ExecContext(ctx, "UPDATE atomic SET s=CASE WHEN id=2 THEN 1::bigint << 16 ELSE s::bigint & 1 END"); err == nil {
		t.Fatal("later conversion published")
	}
	check("SELECT id,s FROM atomic ORDER BY id", []string{"INT4", "INT2"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(99), int64(99)}})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := held.QueryContext(canceled, 1, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(canceled, "INSERT INTO atomic VALUES(4,1::smallint<<2)"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	exec("CREATE TABLE defaults(id INTEGER,s SMALLINT DEFAULT (1::smallint << 15),i INTEGER DEFAULT (~0),b BIGINT DEFAULT (1::bigint << 63),n SMALLINT DEFAULT (~NULL::smallint),w BIGINT DEFAULT (32767::smallint << 1::smallint))", 0)
	exec("INSERT INTO defaults(id) VALUES(1)", 1)
	exec("INSERT INTO defaults VALUES(2,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
	exec("CREATE VIEW masks AS SELECT id,~s AS ns,s << 32 AS ss,s & b AS mixed FROM defaults", 0)
	check("SELECT s,i,b,n,w FROM defaults ORDER BY id", []string{"INT2", "INT4", "INT8", "INT2", "INT8"}, [][]any{{int64(-32768), int64(-1), int64(-9223372036854775808), nil, int64(-2)}, {int64(-32768), int64(-1), int64(-9223372036854775808), nil, int64(-2)}})
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(canceled, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO defaults(id) VALUES(3)", 1)
	check("SELECT ns,ss,mixed FROM masks ORDER BY id", []string{"INT2", "INT2", "INT8"}, [][]any{{int64(32767), int64(-32768), int64(-9223372036854775808)}, {int64(32767), int64(-32768), int64(-9223372036854775808)}, {int64(32767), int64(-32768), int64(-9223372036854775808)}})
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(i INTEGER); INSERT INTO replacement VALUES(1 & 1); INSERT INTO replacement VALUES(~true)")); err == nil {
		t.Fatal("invalid replacement installed")
	}
	check("SELECT s FROM defaults WHERE id=3", []string{"INT2"}, [][]any{{int64(-32768)}})
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(1::smallint << 15)")); err != nil {
		t.Fatal(err)
	}
	check("SELECT s FROM replacement", []string{"INT2"}, [][]any{{int64(-32768)}})
}
