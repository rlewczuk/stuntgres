package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"

	"stuntgres"
)

func TestCompareOpsProjectionCrossWidth(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "compare-ops-projection")
	execSQL(t, db, "CREATE TABLE t(id INTEGER,i INTEGER,j BIGINT,s TEXT,b BOOLEAN)")
	execSQL(t, db, "INSERT INTO t VALUES(1,2147483647,2147483648,'a',FALSE),(2,-2147483648,-2147483649,'z',TRUE),(3,NULL,NULL,NULL,NULL),(4,7,7,'a',FALSE)")
	const query = "SELECT i<j,i<>j,i!=j FROM t ORDER BY id"
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"?column?", "?column?", "?column?"}) {
		t.Fatalf("column names = %v, error = %v", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "BOOL" || typ.ScanType() != reflect.TypeFor[bool]() {
			t.Fatalf("comparison metadata = %s/%v, want BOOL/bool", typ.DatabaseTypeName(), typ.ScanType())
		}
	}
	got := [][]any{}
	for rows.Next() {
		var less, unequal, alias any
		if err := rows.Scan(&less, &unequal, &alias); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{less, unequal, alias})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := [][]any{{true, true, true}, {false, true, true}, {nil, nil, nil}, {false, false, false}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("comparison rows = %v, want %v", got, want)
	}
}

func compareOpsSetup(t *testing.T) (*stuntgres.Driver, *sql.DB) {
	t.Helper()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "compare-ops")
	execSQL(t, db, "CREATE TABLE t(id INTEGER,i INTEGER,j BIGINT,s TEXT,b BOOLEAN)")
	execSQL(t, db, "INSERT INTO t VALUES(1,2147483647,2147483648,'a',FALSE),(2,-2147483648,-2147483649,'z',TRUE),(3,NULL,NULL,NULL,NULL),(4,7,7,'a',FALSE)")
	return d, db
}

func compareOpsRows(t *testing.T, rows *sql.Rows, err error, types []string, want [][]any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(types) {
		t.Fatal(meta, err)
	}
	for i, typ := range types {
		if meta[i].DatabaseTypeName() != typ {
			t.Fatal(i, meta[i])
		}
		if typ == "BOOL" && (meta[i].Name() != "?column?" || meta[i].ScanType() != reflect.TypeFor[bool]()) {
			t.Fatal(meta[i])
		}
	}
	got := [][]any{}
	for rows.Next() {
		v, dst := make([]any, len(types)), make([]any, len(types))
		for i := range v {
			dst[i] = &v[i]
		}
		if err := rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
}

func compareOpsDirectPrepared(t *testing.T, db *sql.DB, query string, types []string, want [][]any, args ...any) {
	t.Helper()
	rows, err := db.Query(query, args...)
	compareOpsRows(t, rows, err, types, want)
	stmt, err := db.Prepare(query)
	if err != nil {
		t.Fatal(query, err)
	}
	defer stmt.Close()
	rows, err = stmt.Query(args...)
	compareOpsRows(t, rows, err, types, want)
}

func TestCompareOpsMatrix(t *testing.T) {
	_, db := compareOpsSetup(t)
	for _, tc := range []struct {
		op               string
		forward, reverse []any
		ids, reverseIDs  []int64
	}{
		{"=", []any{false, false, nil, true}, []any{false, false, nil, true}, []int64{4}, []int64{4}},
		{"<>", []any{true, true, nil, false}, []any{true, true, nil, false}, []int64{1, 2}, []int64{1, 2}},
		{"!=", []any{true, true, nil, false}, []any{true, true, nil, false}, []int64{1, 2}, []int64{1, 2}},
		{"<", []any{true, false, nil, false}, []any{false, true, nil, false}, []int64{1}, []int64{2}},
		{"<=", []any{true, false, nil, true}, []any{false, true, nil, true}, []int64{1, 4}, []int64{2, 4}},
		{">", []any{false, true, nil, false}, []any{true, false, nil, false}, []int64{2}, []int64{1}},
		{">=", []any{false, true, nil, true}, []any{true, false, nil, true}, []int64{2, 4}, []int64{1, 4}},
	} {
		for direction, operands := range []string{"i" + tc.op + "j", "j" + tc.op + "i"} {
			values, ids := tc.forward, tc.ids
			if direction == 1 {
				values, ids = tc.reverse, tc.reverseIDs
			}
			want := [][]any{}
			for _, value := range values {
				want = append(want, []any{value})
			}
			compareOpsDirectPrepared(t, db, "SELECT "+operands+" FROM t ORDER BY id", []string{"BOOL"}, want)
			want = [][]any{}
			for _, id := range ids {
				want = append(want, []any{id})
			}
			compareOpsDirectPrepared(t, db, "SELECT id FROM t WHERE "+operands+" ORDER BY id", []string{"INT4"}, want)
		}
		for _, operands := range []string{"NULL" + tc.op + "i", "i" + tc.op + "NULL", "NULL" + tc.op + "NULL"} {
			compareOpsDirectPrepared(t, db, "SELECT "+operands+" FROM t", []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}, {nil}})
			compareOpsDirectPrepared(t, db, "SELECT "+operands+" FROM t WHERE "+operands, []string{"BOOL"}, [][]any{})
		}
	}
	execSQL(t, db, "CREATE TABLE empty(i INT,j BIGINT)")
	compareOpsDirectPrepared(t, db, "SELECT i<j FROM empty", []string{"BOOL"}, [][]any{})
}

func TestCompareOpsBoundariesAndUnknowns(t *testing.T) {
	_, db := compareOpsSetup(t)
	for _, expr := range []string{
		"-9223372036854775808<-2147483648", "-2147483648>-9223372036854775808",
		"9223372036854775807>2147483647", "2147483647<9223372036854775807",
		"9007199254740993>9007199254740992", "9223372036854775807=9223372036854775807",
		"''<'a'", "'a'<'z'", "'z'<'é'", "FALSE<TRUE", "FALSE<=FALSE", "TRUE>=TRUE",
		"'a'<'b'",
	} {
		compareOpsDirectPrepared(t, db, "SELECT "+expr+" FROM t", []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}})
	}
	compareOpsDirectPrepared(t, db, "SELECT TRUE<FALSE FROM t", []string{"BOOL"}, [][]any{{false}, {false}, {false}, {false}})
	compareOpsDirectPrepared(t, db, "SELECT id FROM t WHERE i<2147483648 ORDER BY id", []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(4)}})
	// Identical duplicates pin multiplicity without promising source ordering.
	compareOpsDirectPrepared(t, db, "SELECT s='a' FROM t WHERE s='a'", []string{"BOOL"}, [][]any{{true}, {true}})
	compareOpsDirectPrepared(t, db, "SELECT s<'z',b<TRUE FROM t ORDER BY id", []string{"BOOL", "BOOL"}, [][]any{{true, true}, {false, false}, {nil, nil}, {true, true}})
	compareOpsDirectPrepared(t, db, "SELECT NULL<'x' FROM t", []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}, {nil}})
}

func TestCompareOpsParameters(t *testing.T) {
	_, db := compareOpsSetup(t)
	for _, tc := range []struct {
		q     string
		types []string
		want  [][]any
		args  []any
	}{
		{"SELECT $1<$2 FROM t", []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}}, []any{"10", "2"}},
		{"SELECT $1=$1 FROM t", []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}}, []any{"x"}},
		{"SELECT $1=$1 FROM t", []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}, {nil}}, []any{nil}},
		{"SELECT $1<i FROM t ORDER BY id", []string{"BOOL"}, [][]any{{true}, {false}, {nil}, {false}}, []any{7}},
		{"SELECT i>$1 FROM t ORDER BY id", []string{"BOOL"}, [][]any{{true}, {false}, {nil}, {false}}, []any{7}},
		{"SELECT $1<2147483648 FROM t", []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}}, []any{int64(2147483647)}},
		{"SELECT $1<2147483648 FROM t", []string{"BOOL"}, [][]any{{false}, {false}, {false}, {false}}, []any{int64(2147483648)}},
		{"SELECT i=$1,$1 FROM t ORDER BY id", []string{"BOOL", "INT4"}, [][]any{{false, int64(7)}, {false, int64(7)}, {nil, int64(7)}, {true, int64(7)}}, []any{7}},
		{"SELECT i=$1,j=$1 FROM t ORDER BY id", []string{"BOOL", "BOOL"}, [][]any{{false, false}, {false, false}, {nil, nil}, {true, true}}, []any{7}},
		{"SELECT j=$1,i=$1 FROM t ORDER BY id", []string{"BOOL", "BOOL"}, [][]any{{false, false}, {false, false}, {nil, nil}, {true, true}}, []any{7}},
	} {
		compareOpsDirectPrepared(t, db, tc.q, tc.types, tc.want, tc.args...)
	}
	held, err := db.Prepare("SELECT $1<i FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, arg := range []any{7, nil, 7} {
		want := [][]any{{true}, {false}, {nil}, {false}}
		if arg == nil {
			want = [][]any{{nil}, {nil}, {nil}, {nil}}
		}
		rows, err := held.Query(arg)
		compareOpsRows(t, rows, err, []string{"BOOL"}, want)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			rows, err := held.Query(7)
			compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{true}, {false}, {nil}, {false}})
		})
	}
	wg.Wait()
}

func TestCompareOpsDiagnostics(t *testing.T) {
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "populated", true: "empty"}[empty], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), "compare-errors")
			execSQL(t, db, "CREATE TABLE t(id INT,i INT,j BIGINT,s TEXT,b BOOL)")
			if !empty {
				execSQL(t, db, "INSERT INTO t VALUES(1,NULL,NULL,NULL,NULL)")
			}
			for _, tc := range []struct {
				q    string
				want stuntgres.Error
				args []any
			}{
				{"SELECT i<TRUE FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer < boolean", Position: "9", Hint: hint}, nil},
				{"SELECT s!=b FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: text <> boolean", Position: "9", Hint: hint}, nil},
				{"SELECT id FROM t WHERE i<s", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer < text", Position: "25", Hint: hint}, nil},
				{"SELECT /*é*/ i<TRUE FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer < boolean", Position: "15", Hint: hint}, nil},
				{"SELECT i<'oops' FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "10"}, nil},
				{"SELECT i<'2147483648' FROM t", stuntgres.Error{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "10"}, nil},
				{"SELECT $1,i=$1 FROM t", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}, []any{7}},
				{"SELECT i=$1,b=$1 FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: boolean = integer", Position: "14", Hint: hint}, []any{7}},
				{"SELECT $1=$2 FROM t WHERE i=$1", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = text", Position: "28", Hint: hint}, []any{"7", "7"}},
			} {
				rows, err := db.Query(tc.q, tc.args...)
				if rows != nil {
					rows.Close()
					t.Fatal("failed query exposed cursor")
				}
				typeCoreDiagnostic(t, err, tc.want)
				stmt, err := db.Prepare(tc.q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed prepare exposed handle")
				}
				typeCoreDiagnostic(t, err, tc.want)
			}
			stmt, err := db.Prepare("SELECT $1<i FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, direct := range []bool{false, true} {
				var rows *sql.Rows
				if direct {
					rows, err = db.Query("SELECT $1<i FROM t", "oops")
				} else {
					rows, err = stmt.Query("oops")
				}
				if rows != nil {
					rows.Close()
					t.Fatal("bad parameter exposed cursor")
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
			}
		})
	}
}

func TestCompareOpsCapturedCursorAndCancellation(t *testing.T) {
	d, db := compareOpsSetup(t)
	ctx := context.Background()
	raw, err := d.Open("compare-ops")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT i<j,$1<i FROM t ORDER BY id", []driver.NamedValue{{Ordinal: 1, Value: int64(7)}})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	execSQL(t, db, "INSERT INTO t VALUES(5,9,10,'later',TRUE)")
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
	wantTuples(t, db, "SELECT id FROM t WHERE i<j ORDER BY id", [][]any{{int64(1)}, {int64(5)}})
	if !reflect.DeepEqual(cursor.Columns(), []string{"?column?", "?column?"}) {
		t.Fatal(cursor.Columns())
	}
	meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
	for i := range 2 {
		if meta.ColumnTypeDatabaseTypeName(i) != "BOOL" {
			t.Fatal("captured metadata replaced")
		}
	}
	dst := make([]driver.Value, 2)
	for _, want := range [][]driver.Value{{true, true}, {false, false}, {nil, nil}, {false, false}} {
		if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
			t.Fatal(dst, want, err)
		}
	}
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.QueryContext(canceled, "SELECT i<j FROM t"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCompareOpsExtendedProvidersAndNativeBoundary(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "compare-extended")
	execSQL(t, db, "CREATE TABLE t(a SMALLINT,z BIGINT,f REAL,g DOUBLE PRECISION,n NUMERIC,m NUMERIC,u UUID,v UUID,c TIME,d TIME,x TIMESTAMP,y TIMESTAMP)")
	execSQL(t, db, "INSERT INTO t VALUES(-32768,2147483648,'NaN','Infinity',9007199254740993,9007199254740992,'7fffffffffffffffffffffffffffffff','80000000000000000000000000000000','01:00','02:00','2000-01-01','2000-01-02')")
	compareOpsDirectPrepared(t, db, "SELECT a<z,f>g,n>m,u<v,c<d,x<y FROM t", []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true}})
	compareOpsDirectPrepared(t, db, "SELECT f=f,n=n,u=u,c=c,x=x FROM t", []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true}})
	compareOpsDirectPrepared(t, db, "SELECT c=NULL,NULL<x FROM t", []string{"BOOL", "BOOL"}, [][]any{{nil, nil}})
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	rows, err := db.Query("SELECT c<x FROM t")
	if rows != nil {
		rows.Close()
		t.Fatal("native cross-temporal cast admitted")
	}
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42883", Message: "operator does not exist: time without time zone < timestamp without time zone", Position: "9", Hint: hint})
	stmt, err := db.Prepare("SELECT c<x FROM t")
	if stmt != nil {
		stmt.Close()
		t.Fatal("native cross-temporal prepare admitted")
	}
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42883", Message: "operator does not exist: time without time zone < timestamp without time zone", Position: "9", Hint: hint})
}

func TestCompareOpsRightOnlyPublicAuthorization(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "compare-authorization")
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{"CREATE USER owner", "CREATE USER reader", "SET SESSION AUTHORIZATION owner", "CREATE TABLE secret(i INT)", "INSERT INTO secret VALUES(1)"} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	stmt, err := conn.PrepareContext(ctx, "SELECT 0<i FROM secret")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err := conn.ExecContext(ctx, "SET SESSION AUTHORIZATION reader"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"SELECT 0<i FROM secret", "SELECT 7 FROM secret WHERE 0<i"} {
		rows, err := conn.QueryContext(ctx, q)
		if rows != nil {
			rows.Close()
			t.Fatal("right-only column escaped authorization")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42501", Message: "permission denied for table secret"})
	}
	rows, err := stmt.QueryContext(ctx)
	if rows != nil {
		rows.Close()
		t.Fatal("held comparison escaped authorization")
	}
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42501", Message: "permission denied for table secret"})
}

func TestCompareOpsTemporalPreparedCaptures(t *testing.T) {
	_, db := compareOpsSetup(t)
	for _, query := range []string{
		"SELECT '04:05:06.123'=TIME(3) '04:05:06.123' FROM t",
		"SELECT '2000-01-01 04:05:06.123'=TIMESTAMP(3) '2000-01-01 04:05:06.123' FROM t",
	} {
		stmt, err := db.Prepare(query)
		if err != nil {
			t.Fatal(query, err)
		}
		for range 3 {
			rows, err := stmt.Query()
			compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}})
		}
		stmt.Close()
	}
}
