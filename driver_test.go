package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"stuntgres"
)

func TestErrorFieldsSyntax(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "syntax_fields")
	for _, operation := range []string{"exec", "query", "prepare"} {
		t.Run(operation, func(t *testing.T) {
			query := "INSERT INTO t VALUES ('é', )"
			var err error
			switch operation {
			case "exec":
				_, err = db.Exec(query)
			case "query":
				_, err = db.Query(query)
			case "prepare":
				_, err = db.Prepare(query)
			}
			var got *stuntgres.Error
			if !errors.As(err, &got) || got.Code != "42601" || got.SQLState() != "42601" ||
				got.Severity != "ERROR" || got.SeverityUnlocalized != "ERROR" ||
				got.Message != `syntax error at or near ")"` || got.Position != "28" {
				t.Fatalf("syntax diagnostic = %#v, %v", got, err)
			}
			if got.Detail != "" || got.Hint != "" || got.InternalPosition != "" ||
				got.InternalQuery != "" || got.Context != "" || got.Schema != "" ||
				got.Table != "" || got.Column != "" || got.DataType != "" ||
				got.Constraint != "" || got.File != "" || got.Line != "" || got.Routine != "" {
				t.Fatalf("unexpected syntax fields: %#v", got)
			}
		})
	}
	_, prefixErr := db.Exec("CREATE TABLE syntax_prefix (x INT); )")
	sqlState(t, prefixErr, "42601")
	_, missingTableErr := db.Query("SELECT * FROM syntax_prefix")
	sqlState(t, missingTableErr, "42P01")
}

func TestErrorFieldsPreservation(t *testing.T) {
	base := &stuntgres.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501",
		Message: "unrelated English primary", Detail: "detail\nline", Hint: "hint\nline",
		Position: "8", InternalPosition: "3", InternalQuery: "SELECT 1 +",
		Context: "context\nline", DataType: "only_type", Constraint: "only_constraint",
	}
	for depth := 0; depth <= 2; depth++ {
		var err error = base
		for range depth {
			err = fmt.Errorf("wrapped: %w", err)
		}
		var got *stuntgres.Error
		var state interface{ SQLState() string }
		if !errors.As(err, &got) || !errors.As(err, &state) || state.SQLState() != "42501" ||
			got.Severity != "ERROR" || got.Detail != base.Detail || got.Hint != base.Hint ||
			got.Context != base.Context || got.Position != "8" || got.InternalPosition != "3" ||
			got.InternalQuery != "SELECT 1 +" || got.DataType != "only_type" ||
			got.Constraint != "only_constraint" {
			t.Fatalf("wrapped fields at depth %d: %#v (%v)", depth, got, err)
		}
	}
	base.Message = "different message"
	if base.SQLState() != "42501" || base.Severity != "ERROR" || base.Error() != "stuntgres: different message" {
		t.Fatalf("message affected structured fields: %#v", base)
	}
	for _, tc := range []struct {
		name string
		err  stuntgres.Error
	}{
		{"schema", stuntgres.Error{Schema: "only_schema"}},
		{"table", stuntgres.Error{Table: "only_table"}},
		{"column", stuntgres.Error{Column: "only_column"}},
		{"datatype", stuntgres.Error{DataType: "only_type"}},
		{"constraint", stuntgres.Error{Constraint: "only_constraint"}},
		{"internal_query", stuntgres.Error{InternalQuery: "SELECT 1 +"}},
		{"cancellation", stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error = &tc.err
			err = fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", err))
			var got *stuntgres.Error
			if !errors.As(err, &got) || got == nil || *got != tc.err {
				t.Fatalf("error fields or omitted siblings changed: %#v", got)
			}
		})
	}
}

func openLocal(t *testing.T, d *stuntgres.Driver, name string) *sql.DB {
	t.Helper()
	c, err := d.OpenConnector(name)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	t.Cleanup(func() { db.Close() })
	return db
}
func execSQL(t *testing.T, db *sql.DB, query string, args ...any) sql.Result {
	t.Helper()
	r, err := db.Exec(query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return r
}
func sqlState(t *testing.T, err error, want string) {
	t.Helper()
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != want {
		t.Fatalf("got %v, want SQLSTATE %s", err, want)
	}
}
func readTuples(t *testing.T, db *sql.DB, query string, args ...any) [][]any {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	result := make([][]any, 0)
	for rows.Next() {
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range values {
			dest[i] = &values[i]
		}
		if err = rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
func wantTuples(t *testing.T, db *sql.DB, query string, want [][]any, args ...any) {
	t.Helper()
	got := readTuples(t, db, query, args...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", query, got, want)
	}
}

func TestDatabaseIdentity(t *testing.T) {
	d := stuntgres.NewDriver()
	first := openLocal(t, d, "")
	same := openLocal(t, d, "")
	other := openLocal(t, d, " ")
	isolated := openLocal(t, stuntgres.NewDriver(), "")
	execSQL(t, first, "CREATE TABLE public.identity (v INT)")
	execSQL(t, first, "INSERT INTO public.identity VALUES (7)")
	first.Close()
	wantTuples(t, same, "SELECT v FROM public.identity", [][]any{{int64(7)}})
	wantTuples(t, same, "SELECT v FROM identity", [][]any{{int64(7)}})
	for _, db := range []*sql.DB{other, isolated} {
		_, err := db.Query("SELECT v FROM public.identity")
		sqlState(t, err, "42P01")
	}
}

func TestDirectAndPreparedParameters(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), "params")
			execSQL(t, db, "CREATE TABLE p (a INT, b INT, text TEXT)")
			query := "INSERT INTO p (a,b) VALUES ($1,$1),($2,$2)"
			var r sql.Result
			var err error
			if prepared {
				stmt, e := db.Prepare(query)
				if e != nil {
					t.Fatal(e)
				}
				defer stmt.Close()
				r, err = stmt.Exec(2, "3")
			} else {
				r, err = db.Exec(query, 2, "3")
			}
			if err != nil {
				t.Fatal(err)
			}
			n, err := r.RowsAffected()
			if err != nil || n != 2 {
				t.Fatalf("affected: %d %v", n, err)
			}
			wantTuples(t, db, "SELECT a,b,text FROM p ORDER BY a", [][]any{{int64(2), int64(2), nil}, {int64(3), int64(3), nil}})
			if _, err = db.Exec("INSERT INTO p(a,b) VALUES ($1,$1)"); err == nil {
				t.Fatal("missing argument accepted")
			}
			if _, err = db.Exec("INSERT INTO p(a) VALUES ($1)", 1, 2); err == nil {
				t.Fatal("extra argument accepted")
			}
			if _, err = db.Exec("INSERT INTO p(a) VALUES ($1)", sql.Named("a", 1)); err == nil {
				t.Fatal("named argument accepted")
			}
			_, err = db.Prepare("INSERT INTO p(a) VALUES ($2)")
			sqlState(t, err, "42P18")
			_, err = db.Prepare("INSERT INTO p(a,text) VALUES ($1,$1)")
			sqlState(t, err, "0A000")
		})
	}
}

func TestTypedValuesAndAtomicInsert(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "values")
	execSQL(t, db, "CREATE TABLE typed (i INT NOT NULL, big BIGINT, text TEXT, flag BOOL)")
	for _, tc := range []struct {
		query, state string
		args         []any
	}{
		{"INSERT INTO typed(i) VALUES (2147483648)", "22003", nil},
		{"INSERT INTO typed(i) VALUES ('not an int')", "22P02", nil},
		{"INSERT INTO typed(i) VALUES (TRUE)", "42804", nil},
		{"INSERT INTO typed(i) VALUES (NULL)", "23502", nil},
		{"INSERT INTO typed(i) VALUES (1),(2147483648)", "22003", nil},
		{"INSERT INTO typed(i,flag) VALUES (1,'o')", "22P02", nil},
		{"INSERT INTO typed(i) VALUES ($1)", "0A000", []any{1.5}},
	} {
		_, err := db.Exec(tc.query, tc.args...)
		sqlState(t, err, tc.state)
		wantTuples(t, db, "SELECT i FROM typed", [][]any{})
	}
	execSQL(t, db, "INSERT INTO typed VALUES (-2147483648,-9223372036854775808,TRUE,' YeS '),(2147483647,9223372036854775807,42,'of')")
	execSQL(t, db, "INSERT INTO typed(i,flag,text) VALUES ($1,$2,$3)", " +0 ", []byte("on"), []byte("owned"))
	wantTuples(t, db, "SELECT i,big,text,flag FROM typed ORDER BY i", [][]any{{int64(-2147483648), int64(-9223372036854775808), "true", true}, {int64(0), nil, "owned", true}, {int64(2147483647), int64(9223372036854775807), "42", false}})
	_, err := db.Query("SELECT i FROM typed WHERE flag = 1")
	sqlState(t, err, "42883")
}

func TestReferencesNullsAndOrdering(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "ordering")
	execSQL(t, db, "CREATE TABLE ord (v INT, label TEXT)")
	for _, q := range []string{"SELECT missing FROM ord", "SELECT v FROM ord WHERE missing = 1", "SELECT v FROM ord ORDER BY missing"} {
		_, err := db.Query(q)
		sqlState(t, err, "42703")
	}
	execSQL(t, db, "INSERT INTO ord VALUES (NULL,'n'),(2,'b'),(1,'a')")
	wantTuples(t, db, "SELECT v FROM ord ORDER BY v", [][]any{{int64(1)}, {int64(2)}, {nil}})
	wantTuples(t, db, "SELECT v FROM ord ORDER BY v DESC", [][]any{{nil}, {int64(2)}, {int64(1)}})
	wantTuples(t, db, "SELECT v FROM ord WHERE v = NULL", [][]any{})
	wantTuples(t, db, "SELECT v FROM ord WHERE v = $1", [][]any{}, nil)
	_, err := db.Exec("CREATE TABLE ord (v INT)")
	sqlState(t, err, "42P07")
	_, err = db.Exec("CREATE TABLE duplicate (v INT,v TEXT)")
	sqlState(t, err, "42701")
	_, err = db.Exec("INSERT INTO ord(v,v) VALUES(1,2)")
	sqlState(t, err, "42701")
}

func TestRowsMetadataAndCommandSemantics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "metadata")
	rows, err := db.Query("CREATE TABLE metadata (i INTEGER NOT NULL,b BIGINT,t TEXT,f BOOLEAN)")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("command produced row")
	}
	rows.Close()
	execSQL(t, db, "INSERT INTO metadata VALUES (1,2,'x',FALSE)")
	rows, err = db.Query("SELECT * FROM metadata")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cols, []string{"i", "b", "t", "f"}) {
		t.Fatal(cols)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"INT4", "INT8", "TEXT", "BOOL"} {
		if types[i].DatabaseTypeName() != want {
			t.Fatal(types[i].DatabaseTypeName())
		}
		nullable, ok := types[i].Nullable()
		if !ok || nullable != (i != 0) {
			t.Fatalf("nullable %d: %v %v", i, nullable, ok)
		}
		scan := []reflect.Type{reflect.TypeOf(int64(0)), reflect.TypeOf(int64(0)), reflect.TypeOf(""), reflect.TypeOf(false)}
		if types[i].ScanType() != scan[i] {
			t.Fatal(types[i].ScanType())
		}
	}
	rows.Close()
	if rows.Next() {
		t.Fatal("closed cursor produced row")
	}
	r := execSQL(t, db, "SELECT * FROM metadata")
	n, err := r.RowsAffected()
	if err != nil || n != 1 {
		t.Fatalf("select affected: %d %v", n, err)
	}
	_, err = r.LastInsertId()
	if err == nil || err.Error() != "LastInsertId is not supported by this driver" {
		t.Fatal(err)
	}
	r = execSQL(t, db, "-- empty\n")
	n, err = r.RowsAffected()
	if err != nil || n != 0 {
		t.Fatalf("empty affected: %d %v", n, err)
	}
	rows, err = db.Query("")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("empty query produced row")
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	_, err = db.Exec("INSERT INTO metadata(i) VALUES(9); INSERT INTO metadata(i) VALUES(10)")
	sqlState(t, err, "0A000")
	wantTuples(t, db, "SELECT i FROM metadata", [][]any{{int64(1)}})
}

func TestCancellationTransactionsAndClosedObjects(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "cancel")
	execSQL(t, db, "CREATE TABLE c (v INT)")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.ExecContext(ctx, "INSERT INTO c VALUES (1)"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT v FROM c", [][]any{})
	_, err := db.BeginTx(context.Background(), nil)
	sqlState(t, err, "0A000")
	for _, q := range []string{"BEGIN", "START TRANSACTION", "COMMIT", "ROLLBACK", "SAVEPOINT x", "RELEASE x", "UPDATE c SET v=1", "SELECT v FROM c LIMIT 1"} {
		_, err = db.Exec(q)
		sqlState(t, err, "0A000")
	}
	stmt, err := db.Prepare("INSERT INTO c VALUES ($1)")
	if err != nil {
		t.Fatal(err)
	}
	stmt.Close()
	stmt.Close()
	if _, err = stmt.Exec(1); err == nil {
		t.Fatal("closed statement accepted execution")
	}
	conn, err := stuntgres.NewDriver().Open("native")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	conn.Close()
	if _, err = conn.Prepare("SELECT v FROM c"); err == nil {
		t.Fatal("closed connection prepared")
	}
	if err = conn.(driver.Pinger).Ping(context.Background()); err == nil {
		t.Fatal("closed connection pinged")
	}
}

func TestNativeCursorEOFAndCancellation(t *testing.T) {
	conn, err := stuntgres.NewDriver().Open("cursor")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ex := conn.(driver.ExecerContext)
	q := conn.(driver.QueryerContext)
	if _, err = ex.ExecContext(context.Background(), "CREATE TABLE c(v INT)", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = ex.ExecContext(context.Background(), "INSERT INTO c VALUES(1)", nil); err != nil {
		t.Fatal(err)
	}
	rows, err := q.QueryContext(context.Background(), "SELECT v FROM c", nil)
	if err != nil {
		t.Fatal(err)
	}
	dest := make([]driver.Value, 1)
	if err = rows.Next(dest); err != nil || dest[0] != int64(1) {
		t.Fatalf("row: %v %v", dest, err)
	}
	if err = rows.Next(dest); err != io.EOF {
		t.Fatal(err)
	}
	rows.Close()
	if err = rows.Next(dest); err != io.EOF {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rows, err = q.QueryContext(ctx, "SELECT v FROM c", nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	defer rows.Close()
	if err = rows.Next(dest); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type textValuer string

func (v textValuer) Value() (driver.Value, error) { return string(v), nil }

func TestPreparedQueryRevalidatesReplacedCatalog(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "revalidate")
	execSQL(t, db, "CREATE TABLE p(v INT)")
	execSQL(t, db, "INSERT INTO p VALUES($1)", textValuer("12"))
	stmt, err := db.Prepare("SELECT v FROM p WHERE v=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err := stmt.Query("12")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing prepared query row")
	}
	var v int64
	if err = rows.Scan(&v); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if v != 12 {
		t.Fatal(v)
	}
	if rows.Next() {
		t.Fatal("unexpected row")
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if err = stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE TABLE p(other TEXT)")
	_, err = stmt.Query(12)
	sqlState(t, err, "42703")
}

func TestConcurrentInsertsPreserveSnapshot(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "concurrent")
	execSQL(t, db, "CREATE TABLE concurrent_rows (id INTEGER NOT NULL)")
	execSQL(t, db, "INSERT INTO concurrent_rows VALUES (0)")
	rows, err := db.Query("SELECT id FROM concurrent_rows")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	// Keep the snapshot's pooled connection occupied while other connections
	// append enough rows to exercise both backing-array growth and reuse.
	const workers, perWorker = 8, 32
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for worker := range workers {
		wg.Go(func() {
			for i := range perWorker {
				id := 1 + worker*perWorker + i
				if _, err := db.Exec("INSERT INTO concurrent_rows VALUES ($1)", id); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatalf("snapshot lost original row: %v", rows.Err())
	}
	var id int64
	if err := rows.Scan(&id); err != nil || id != 0 {
		t.Fatalf("original snapshot row: id=%d err=%v", id, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("snapshot saw later inserts: %v", rows.Err())
	}
	want := make([][]any, 1+workers*perWorker)
	for i := range want {
		want[i] = []any{int64(i)}
	}
	wantTuples(t, db, "SELECT id FROM concurrent_rows ORDER BY id", want)
}

func TestInsertImplicitShortTargets(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), "implicit-short")
			execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,big BIGINT,label TEXT,flag BOOL)")
			execSQL(t, db, "CREATE TABLE repeats(a INT,b INT,tail TEXT)")
			exec := func(query string, args ...any) (sql.Result, error) {
				if !prepared {
					return db.Exec(query, args...)
				}
				stmt, err := db.Prepare(query)
				if err != nil {
					return nil, err
				}
				defer stmt.Close()
				return stmt.Exec(args...)
			}
			for _, tc := range []struct {
				query string
				args  []any
				count int64
			}{
				{"INSERT INTO t VALUES (1)", nil, 1},
				{"INSERT INTO t VALUES ($1,$2)", []any{2, "20"}, 1},
				{"INSERT INTO t VALUES (3,30,'three')", nil, 1},
				{"INSERT INTO t VALUES ($1)", []any{"5"}, 1},
				{"INSERT INTO t VALUES ($1)", []any{[]byte("6")}, 1},
				{"INSERT INTO t(label,id) VALUES ('reordered',4)", nil, 1},
				{"INSERT INTO repeats VALUES ($1,$1),($2,$2)", []any{11, 12}, 2},
			} {
				result, err := exec(tc.query, tc.args...)
				if err != nil {
					t.Fatal(err)
				}
				count, err := result.RowsAffected()
				if err != nil || count != tc.count {
					t.Fatalf("affected=%d err=%v want=%d", count, err, tc.count)
				}
				if _, err = result.LastInsertId(); err == nil {
					t.Fatal("LastInsertId succeeded")
				}
			}
			wantTuples(t, db, "SELECT * FROM t ORDER BY id", [][]any{{int64(1), nil, nil, nil}, {int64(2), int64(20), nil, nil}, {int64(3), int64(30), "three", nil}, {int64(4), nil, "reordered", nil}, {int64(5), nil, nil, nil}, {int64(6), nil, nil, nil}})
			wantTuples(t, db, "SELECT * FROM repeats ORDER BY a", [][]any{{int64(11), int64(11), nil}, {int64(12), int64(12), nil}})
			execSQL(t, db, `CREATE TABLE "Odd"("Id" INT NOT NULL,label TEXT)`)
			if _, err := exec(`INSERT INTO "Odd" VALUES ($1)`, "7"); err != nil {
				t.Fatal(err)
			}
			wantTuples(t, db, `SELECT * FROM "Odd" ORDER BY "Id"`, [][]any{{int64(7), nil}})
			_, err := exec("INSERT INTO t VALUES ($2)", 10)
			sqlState(t, err, "42P18")
			_, err = exec("INSERT INTO t VALUES ($1,$1)", 10)
			sqlState(t, err, "0A000")
			query := "SELECT * FROM t ORDER BY id"
			var rows *sql.Rows
			if prepared {
				stmt, err := db.Prepare(query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err = stmt.Query()
				if err != nil {
					t.Fatal(err)
				}
			} else {
				rows, err = db.Query(query)
				if err != nil {
					t.Fatal(err)
				}
			}
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for i, expected := range []struct {
				name, database string
				scan           reflect.Type
				nullable       bool
			}{
				{"id", "INT4", reflect.TypeOf(int64(0)), false}, {"big", "INT8", reflect.TypeOf(int64(0)), true}, {"label", "TEXT", reflect.TypeOf(""), true}, {"flag", "BOOL", reflect.TypeOf(false), true},
			} {
				name, _ := rows.Columns()
				nullable, known := types[i].Nullable()
				if name[i] != expected.name || types[i].DatabaseTypeName() != expected.database || types[i].ScanType() != expected.scan || !known || nullable != expected.nullable {
					t.Fatalf("metadata %d: name=%s type=%s scan=%v nullable=%v/%v", i, name[i], types[i].DatabaseTypeName(), types[i].ScanType(), nullable, known)
				}
			}
		})
	}
}

func TestInsertImplicitShortDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "implicit-diagnostics")
	execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,big BIGINT,label TEXT,flag BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(99)")
	check := func(query, code, message string, position int, detail, hint, schema, table, column string) {
		t.Helper()
		_, err := db.Exec(query)
		var got *stuntgres.Error
		if !errors.As(err, &got) {
			t.Fatalf("%s: expected SQL diagnostic, got %v", query, err)
		}
		wantPosition := ""
		if position > 0 {
			wantPosition = fmt.Sprint(position)
		}
		if got.Code != code || got.Message != message || got.Position != wantPosition || got.Detail != detail || got.Hint != hint || got.Schema != schema || got.Table != table || got.Column != column {
			t.Fatalf("%s: diagnostic=%+v", query, got)
		}
		wantTuples(t, db, "SELECT id FROM t ORDER BY id", [][]any{{int64(99)}})
	}
	q := "INSERT INTO t VALUES (20),('oops')"
	check(q, "22P02", `invalid input syntax for type integer: "oops"`, strings.Index(q, "'oops'")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (20),('2147483648')"
	check(q, "22003", `value "2147483648" is out of range for type integer`, strings.Index(q, "'2147483648'")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (20,'oops')"
	check(q, "22P02", `invalid input syntax for type bigint: "oops"`, strings.Index(q, "'oops'")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (20,20,NULL,'maybe')"
	check(q, "22P02", `invalid input syntax for type boolean: "maybe"`, strings.Index(q, "'maybe'")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (2147483648)"
	check(q, "22003", "integer out of range", 0, "", "", "", "", "")
	q = "INSERT INTO t VALUES (20),(21,22)"
	check(q, "42601", "VALUES lists must all be the same length", strings.Index(q, "21,22")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (20,30),(21)"
	check(q, "42601", "VALUES lists must all be the same length", strings.Index(q, "21)")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (20,30,40,TRUE,50)"
	check(q, "42601", "INSERT has more expressions than target columns", strings.Index(q, "50)")+1, "", "", "", "", "")
	q = "INSERT INTO t (id,label) VALUES (20)"
	check(q, "42601", "INSERT has more target columns than expressions", strings.Index(q, "label")+1, "", "", "", "", "")
	q = "INSERT INTO t (id,label) VALUES ('oops')"
	check(q, "42601", "INSERT has more target columns than expressions", strings.Index(q, "label")+1, "", "", "", "", "")
	preparedUnderflow, prepareErr := db.Prepare(q)
	err := prepareErr
	if prepareErr == nil {
		_, err = preparedUnderflow.Exec()
		preparedUnderflow.Close()
	}
	var underflow *stuntgres.Error
	if !errors.As(err, &underflow) || underflow.Code != "42601" || underflow.Message != "INSERT has more target columns than expressions" || underflow.Position != fmt.Sprint(strings.Index(q, "label")+1) || underflow.Detail != "" || underflow.Hint != "" || underflow.Schema != "" || underflow.Table != "" || underflow.Column != "" {
		t.Fatalf("prepared explicit underflow diagnostic: %+v (%v)", underflow, err)
	}
	wantTuples(t, db, "SELECT id FROM t ORDER BY id", [][]any{{int64(99)}})
	q = "INSERT INTO t (id,id) VALUES (20,21)"
	check(q, "42701", `column "id" specified more than once`, strings.LastIndex(q, "id)")+1, "", "", "", "", "")
	q = "INSERT INTO t (missing) VALUES (20)"
	check(q, "42703", `column "missing" of relation "t" does not exist`, strings.Index(q, "missing")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (TRUE)"
	check(q, "42804", `column "id" is of type integer but expression is of type boolean`, strings.Index(q, "TRUE")+1, "", "You will need to rewrite or cast the expression.", "", "", "")
	q = "INSERT INTO t VALUES (20),(NULL)"
	check(q, "23502", `null value in column "id" of relation "t" violates not-null constraint`, 0, "Failing row contains (null, null, null, null).", "", "public", "t", "id")
	execSQL(t, db, "CREATE TABLE required(id INT,flag BOOL NOT NULL)")
	q = "INSERT INTO required VALUES (1)"
	_, err = db.Exec(q)
	var got *stuntgres.Error
	if !errors.As(err, &got) || got.Code != "23502" || got.Detail != "Failing row contains (1, null)." || got.Schema != "public" || got.Table != "required" || got.Column != "flag" {
		t.Fatalf("NOT NULL diagnostic: %+v (%v)", got, err)
	}
	prepared, err := db.Prepare("INSERT INTO t VALUES ($1)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepared.Exec("oops")
	prepared.Close()
	if !errors.As(err, &got) || got.Code != "22P02" || got.Message != `invalid input syntax for type integer: "oops"` || got.Position != "" || got.Schema != "" || got.Table != "" || got.Column != "" {
		t.Fatalf("prepared parameter diagnostic: %+v (%v)", got, err)
	}
	wantTuples(t, db, "SELECT id FROM t ORDER BY id", [][]any{{int64(99)}})
	execSQL(t, db, "CREATE TABLE clip(label TEXT,flag BOOL,required INT NOT NULL)")
	long := strings.Repeat("é", 33)
	_, err = db.Exec("INSERT INTO clip VALUES ($1,$2)", long, true)
	if !errors.As(err, &got) || got.Code != "23502" || got.Detail != "Failing row contains ("+strings.Repeat("é", 32)+"..., t, null)." || got.Column != "required" {
		t.Fatalf("UTF-8 clipped row detail: %#v detail=%q (%v)", got, got.Detail, err)
	}
}
func TestInsertImplicitShortUnicodePosition(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "implicit-unicode")
	execSQL(t, db, "CREATE TABLE t(id INT,label TEXT)")
	query := "/* é */ INSERT INTO t VALUES(1),('bad')"
	_, err := db.Exec(query)
	var got *stuntgres.Error
	if !errors.As(err, &got) || got.Code != "22P02" || got.Position != fmt.Sprint(utf8.RuneCountInString(query[:strings.Index(query, "'bad'")])+1) {
		t.Fatalf("UTF-8 diagnostic position: %+v (%v)", got, err)
	}
	if got.Schema != "" || got.Table != "" || got.Column != "" || got.Detail != "" || got.Hint != "" {
		t.Fatalf("unexpected optional fields: %+v", got)
	}
}
func TestInsertImplicitShortBoundariesAndCancellation(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "implicit-boundaries")
	execSQL(t, db, "CREATE TABLE t(id INT,label TEXT)")
	for _, tc := range []struct{ query, state string }{
		{"CREATE TABLE defaults(id INT DEFAULT 1)", "0A000"},
		{"INSERT INTO t VALUES(DEFAULT)", "0A000"},
		{"INSERT INTO t DEFAULT VALUES", "0A000"},
		{"INSERT INTO t SELECT 1", "0A000"},
		{"INSERT INTO t VALUES(1) RETURNING id", "0A000"},
		{"INSERT INTO t VALUES()", "42601"},
		{"INSERT INTO t VALUES($2)", "42P18"},
	} {
		_, err := db.Exec(tc.query)
		sqlState(t, err, tc.state)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.ExecContext(ctx, "INSERT INTO t VALUES(1)"); !errors.Is(err, context.Canceled) {
		t.Fatalf("direct cancellation: %v", err)
	}
	stmt, err := db.Prepare("INSERT INTO t VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.ExecContext(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("prepared cancellation: %v", err)
	}
	stmt.Close()
	wantTuples(t, db, "SELECT * FROM t", [][]any{})
}

func TestParameterConversionErrorMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "error_metadata")
	execSQL(t, db, "CREATE TABLE diagnostics (id INT, flag BOOLEAN)")
	stmt, err := db.Prepare("INSERT INTO diagnostics (flag,id) VALUES ($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	_, insertErr := stmt.Exec("ambiguous", 1)
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	_, queryErr := db.Query("SELECT id FROM diagnostics WHERE id = $1", "not an integer")
	for _, tc := range []struct {
		err      error
		schema   string
		table    string
		column   string
	}{{insertErr, "", "", ""}, {queryErr, "public", "diagnostics", "id"}} {
		var sqlErr *stuntgres.Error
		if !errors.As(tc.err, &sqlErr) || sqlErr.Code != "22P02" ||
			sqlErr.Severity != "ERROR" || sqlErr.SeverityUnlocalized != "ERROR" ||
			sqlErr.Schema != tc.schema || sqlErr.Table != tc.table || sqlErr.Column != tc.column {
			t.Fatalf("parameter diagnostic: %#v (%v)", sqlErr, tc.err)
		}
	}
	wantTuples(t, db, "SELECT * FROM diagnostics", [][]any{})
}

func TestErrorFieldsNotNull(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "not_null_fields")
	execSQL(t, db, "CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT)")
	wantError := func(err error) {
		t.Helper()
		var got *stuntgres.Error
		if !errors.As(err, &got) || got.Code != "23502" || got.SQLState() != "23502" ||
			got.Severity != "ERROR" || got.SeverityUnlocalized != "ERROR" ||
			got.Message != `null value in column "id" of relation "diagnostic_rows" violates not-null constraint` ||
			got.Detail != "Failing row contains (null, bad)." || got.Schema != "public" ||
			got.Table != "diagnostic_rows" || got.Column != "id" || got.Hint != "" ||
			got.Position != "" || got.InternalPosition != "" || got.InternalQuery != "" ||
			got.Context != "" || got.DataType != "" || got.Constraint != "" ||
			got.File != "" || got.Line != "" || got.Routine != "" {
			t.Fatalf("NOT NULL error = %#v (%v)", got, err)
		}
	}
	_, err := db.Exec("INSERT INTO diagnostic_rows VALUES (NULL,'bad')")
	wantError(err)
	stmt, err := db.Prepare("INSERT INTO diagnostic_rows VALUES ($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = stmt.Exec(nil, "bad")
	if closeErr := stmt.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	wantError(err)
	_, err = db.Exec("INSERT INTO diagnostic_rows VALUES (1,'good'),(NULL,'bad')")
	wantError(err)
	wantTuples(t, db, "SELECT id,note FROM diagnostic_rows ORDER BY id", [][]any{})
	rows, err := db.Query("SELECT id,note FROM diagnostic_rows")
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
		rows.Close()
		t.Fatalf("types = %#v", types)
	}
	rows.Close()
}

func TestErrorFieldsRestore(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "error_fields_restore")
	execSQL(t, db, "CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT)")
	execSQL(t, db, "INSERT INTO diagnostic_rows VALUES (7,'kept')")
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec("INSERT INTO diagnostic_rows VALUES (NULL,'bad')")
	var got *stuntgres.Error
	if !errors.As(err, &got) || got.Code != "23502" || got.Table != "diagnostic_rows" || got.Column != "id" || got.Detail != "Failing row contains (null, bad)." {
		t.Fatalf("restored NOT NULL error = %#v (%v)", got, err)
	}
	before := readTuples(t, db, "SELECT id,note FROM diagnostic_rows ORDER BY id")
	err = stuntgres.Restore(context.Background(), db, bytes.NewBufferString("CREATE TABLE replacement (x TEXT); INSERT INTO replacement VALUES ('é', )"))
	if !errors.As(err, &got) || got.Code != "42601" || got.Message != `syntax error at or near ")"` || got.Position != "73" {
		t.Fatalf("malformed Restore diagnostic = %#v (%v)", got, err)
	}
	after := readTuples(t, db, "SELECT id,note FROM diagnostic_rows ORDER BY id")
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(after, [][]any{{int64(7), "kept"}}) {
		t.Fatalf("failed Restore changed data: before=%#v after=%#v", before, after)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewBufferString("CREATE TABLE staged (id INT NOT NULL); INSERT INTO staged VALUES (NULL)")); err == nil {
		t.Fatal("NOT NULL Restore succeeded")
	}
	if rows := readTuples(t, db, "SELECT id,note FROM diagnostic_rows ORDER BY id"); !reflect.DeepEqual(rows, before) {
		t.Fatalf("failed NOT NULL Restore changed data: %#v", rows)
	}
}
