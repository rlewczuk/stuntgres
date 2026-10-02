package stuntgres_test

import (
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
	execSQL(t, first, "CREATE TABLE identity (v INT)")
	execSQL(t, first, "INSERT INTO identity VALUES (7)")
	first.Close()
	wantTuples(t, same, "SELECT v FROM identity", [][]any{{int64(7)}})
	for _, db := range []*sql.DB{other, isolated} {
		_, err := db.Query("SELECT v FROM identity")
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

func TestParameterConversionErrorMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "error_metadata")
	execSQL(t, db, "CREATE TABLE diagnostics (id INT, flag BOOLEAN)")
	stmt, err := db.Prepare("INSERT INTO diagnostics (flag,id) VALUES ($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	_, insertErr := stmt.Exec("ambiguous", 1)
	_, queryErr := db.Query("SELECT id FROM diagnostics WHERE id = $1", "not an integer")
	for _, tc := range []struct {
		err    error
		column string
	}{{insertErr, "flag"}, {queryErr, "id"}} {
		var sqlErr *stuntgres.Error
		if !errors.As(tc.err, &sqlErr) || sqlErr.Code != "22P02" ||
			sqlErr.Schema != "public" || sqlErr.Table != "diagnostics" || sqlErr.Column != tc.column {
			t.Fatalf("parameter diagnostic for %s: %#v (%v)", tc.column, sqlErr, tc.err)
		}
	}
	wantTuples(t, db, "SELECT * FROM diagnostics", [][]any{})
}
