package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres"
)

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

type cancelReader struct {
	cancel context.CancelFunc
	done   bool
}

func (r *cancelReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.cancel()
	return copy(p, "CREATE TABLE replacement(v INT);"), nil
}

func TestControlRoundTripAndSharedPools(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "controls")
	other := openLocal(t, d, "controls")
	if err := other.Ping(); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, `CREATE TABLE "odd""table" ("id" INT NOT NULL, "text" TEXT, "flag" BOOL)`)
	execSQL(t, db, `CREATE TABLE "empty" ("big" BIGINT)`)
	execSQL(t, db, `INSERT INTO "odd""table" VALUES ($1,$2,$3),(1,NULL,NULL),(2,'',FALSE)`, 0, "line\nO'Brien\\end", false)
	execSQL(t, db, `CREATE TABLE public."a.b" ("q""x" INTEGER, note TEXT)`)
	execSQL(t, db, `INSERT INTO public."a.b" VALUES (9,'round trip')`)
	query := `SELECT "id","text","flag" FROM "odd""table" ORDER BY "id"`
	want := [][]any{{int64(0), "line\nO'Brien\\end", false}, {int64(1), nil, nil}, {int64(2), "", false}}
	wantTuples(t, other, `SELECT "q""x",note FROM public."a.b"`, [][]any{{int64(9), "round trip"}})
	var first, second bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &first); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(ctx, db, &second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("dump was not stable")
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	_, err := other.Query(query)
	sqlState(t, err, "42P01")
	if err = stuntgres.Restore(ctx, db, strings.NewReader(first.String())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, other, query, want)
	wantTuples(t, other, `SELECT "q""x",note FROM public."a.b"`, [][]any{{int64(9), "round trip"}})
	wantTuples(t, other, `SELECT big FROM empty`, [][]any{})
	var restored bytes.Buffer
	if err = stuntgres.Dump(ctx, other, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.String() != first.String() {
		t.Fatalf("round trip dump changed:\n%s\n%s", first.String(), restored.String())
	}
	stmt, err := other.Prepare(`SELECT id FROM "odd""table" ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if err = stuntgres.Restore(ctx, db, strings.NewReader(" -- empty\n /* nested /* yes */ */ ")); err != nil {
		t.Fatal(err)
	}
	_, err = stmt.Query()
	sqlState(t, err, "42P01")
	var empty bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &empty); err != nil {
		t.Fatal(err)
	}
	if empty.String() != "" {
		t.Fatalf("empty dump: %q", empty.String())
	}
}

func TestFailedRestorePreservesCatalog(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), "atomic")
	execSQL(t, db, "CREATE TABLE sentinel(v INT NOT NULL)")
	execSQL(t, db, "INSERT INTO sentinel VALUES(77)")
	for _, tc := range []struct{ name, script, state string }{
		{"later insert", "CREATE TABLE replacement(v INT NOT NULL); INSERT INTO replacement VALUES(1),(NULL);", "23502"},
		{"duplicate", "CREATE TABLE replacement(v INT); CREATE TABLE replacement(v INT);", "42P07"},
		{"select", "CREATE TABLE replacement(v INT); SELECT v FROM replacement;", "0A000"},
		{"parameter", "CREATE TABLE replacement(v INT); INSERT INTO replacement VALUES($1);", "42P02"},
		{"syntax", "CREATE TABLE replacement(v INT); INSERT INTO replacement VALUES(;", "42601"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := stuntgres.Restore(ctx, db, strings.NewReader(tc.script))
			sqlState(t, err, tc.state)
			wantTuples(t, db, "SELECT v FROM sentinel", [][]any{{int64(77)}})
			_, err = db.Query("SELECT v FROM replacement")
			sqlState(t, err, "42P01")
		})
	}
	failure := errors.New("reader failed")
	if err := stuntgres.Restore(ctx, db, failingReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	if err := stuntgres.Restore(canceled, db, &cancelReader{cancel: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(canceled, db); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	failure = errors.New("writer failed")
	if err := stuntgres.Dump(ctx, db, failingWriter{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT v FROM sentinel", [][]any{{int64(77)}})
}

func TestSnapshotSurvivesCatalogReplacement(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "reset", true: "restore"}[restore], func(t *testing.T) {
			d := stuntgres.NewDriver()
			db := openLocal(t, d, "snapshot")
			other := openLocal(t, d, "snapshot")
			execSQL(t, db, "CREATE TABLE public.snap(v INT)")
			execSQL(t, db, "INSERT INTO public.snap VALUES(1),(2)")
			rows, err := db.Query("SELECT v FROM public.snap ORDER BY v")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			execSQL(t, other, "INSERT INTO public.snap VALUES(3)")
			if restore {
				err = stuntgres.Restore(context.Background(), other, strings.NewReader("CREATE TABLE public.snap(replacement TEXT); INSERT INTO public.snap VALUES('99');"))
			} else {
				err = stuntgres.Reset(context.Background(), other)
			}
			if err != nil {
				t.Fatal(err)
			}
			names, err := rows.Columns()
			if err != nil || len(names) != 1 || names[0] != "v" {
				t.Fatalf("retained column names = %v, error = %v", names, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].Name() != "v" || types[0].DatabaseTypeName() != "INT4" {
				t.Fatalf("retained column types = %v, error = %v", types, err)
			}
			for _, want := range []int64{1, 2} {
				if !rows.Next() {
					t.Fatalf("missing snapshot row: %v", rows.Err())
				}
				var got int64
				if err = rows.Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Fatalf("got %d want %d", got, want)
				}
			}
			if rows.Next() {
				t.Fatal("snapshot saw later insert")
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			if restore {
				wantTuples(t, other, "SELECT replacement FROM public.snap", [][]any{{"99"}})
			} else {
				_, err = other.Query("SELECT v FROM snap")
				sqlState(t, err, "42P01")
			}
		})
	}
}

func TestInsertImplicitShortControls(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), "implicit-controls")
	execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,label TEXT,flag BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1),(2)")
	execSQL(t, db, "INSERT INTO t(label,id) VALUES('reordered',3)")
	want := [][]any{{int64(1), nil, nil}, {int64(2), nil, nil}, {int64(3), "reordered", nil}}
	wantTuples(t, db, "SELECT * FROM t ORDER BY id", want)
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM t ORDER BY id", want)
	var second bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), second.Bytes()) {
		t.Fatalf("short-row dump changed:\n%s\n%s", dump.String(), second.String())
	}
	err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE staged(id INT,label TEXT); INSERT INTO staged VALUES(7); CREATE TABLE required(id INT,flag BOOL NOT NULL); INSERT INTO required VALUES(8);"))
	var restoreError *stuntgres.Error
	if !errors.As(err, &restoreError) || restoreError.Code != "23502" ||
		restoreError.Message != `null value in column "flag" of relation "required" violates not-null constraint` ||
		restoreError.Detail != "Failing row contains (8, null)." ||
		restoreError.Schema != "public" || restoreError.Table != "required" || restoreError.Column != "flag" ||
		restoreError.Position != "" || restoreError.Hint != "" {
		t.Fatalf("short staged restore diagnostic: %+v (%v)", restoreError, err)
	}
	wantTuples(t, db, "SELECT * FROM t ORDER BY id", want)
	if _, err := db.Query("SELECT * FROM staged"); err == nil {
		t.Fatal("failed staged table was published")
	}
	var preserved bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &preserved); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), preserved.Bytes()) {
		t.Fatal("failed short staged restore changed catalog")
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE restored(id INT,label TEXT); INSERT INTO restored VALUES(8);")); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM restored", [][]any{{int64(8), nil}})
}

func TestControlInvalidInputs(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), "invalid")
	for _, err := range []error{stuntgres.Reset(ctx, nil), stuntgres.Dump(ctx, nil, io.Discard), stuntgres.Restore(ctx, nil, strings.NewReader("")), stuntgres.Dump(ctx, db, nil), stuntgres.Restore(ctx, db, nil)} {
		if err == nil {
			t.Fatal("nil control input accepted")
		}
	}
	foreign := sql.OpenDB(foreignConnector{})
	defer foreign.Close()
	for _, err := range []error{stuntgres.Reset(ctx, foreign), stuntgres.Dump(ctx, foreign, io.Discard), stuntgres.Restore(ctx, foreign, strings.NewReader(""))} {
		if err == nil {
			t.Fatal("foreign connection accepted")
		}
	}
}

type foreignConnector struct{}

func (foreignConnector) Connect(context.Context) (driver.Conn, error) { return foreignConn{}, nil }
func (foreignConnector) Driver() driver.Driver                        { return foreignDriver{} }

type foreignDriver struct{}

func (foreignDriver) Open(string) (driver.Conn, error) { return foreignConn{}, nil }

type foreignConn struct{}

func (foreignConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("foreign prepare unsupported")
}
func (foreignConn) Close() error              { return nil }
func (foreignConn) Begin() (driver.Tx, error) { return nil, errors.New("foreign begin unsupported") }
func controlValues(ctx context.Context, db *sql.DB, query string, want []int64) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var v int64
		if err = rows.Scan(&v); err != nil {
			return err
		}
		got = append(got, v)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(got) != len(want) {
		return errors.New("unexpected control query row count")
	}
	for i := range got {
		if got[i] != want[i] {
			return errors.New("unexpected control query value")
		}
	}
	return nil
}

type gatedWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	output  bytes.Buffer
}

func (w *gatedWriter) Write(p []byte) (int, error) {
	blocked := false
	w.once.Do(func() { close(w.started); blocked = true })
	if blocked {
		<-w.release
	}
	return w.output.Write(p)
}

type gatedReader struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	sent    bool
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.once.Do(func() { close(r.started); <-r.release })
	r.sent = true
	return copy(p, "CREATE TABLE public.replaced (v INT); INSERT INTO public.replaced VALUES (99);"), nil
}

func TestCatalogControlsReleaseLocksDuringIO(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "gated")
	other := openLocal(t, d, "gated")
	execSQL(t, db, "CREATE TABLE public.sentinel (v INT)")
	execSQL(t, db, "INSERT INTO public.sentinel VALUES (77)")
	writer := &gatedWriter{started: make(chan struct{}), release: make(chan struct{})}
	dumpDone := make(chan error, 1)
	go func() { dumpDone <- stuntgres.Dump(ctx, db, writer) }()
	select {
	case <-writer.started:
	case <-ctx.Done():
		t.Fatal("Dump did not reach gated writer")
	}
	operation := make(chan error, 1)
	go func() {
		if err := controlValues(ctx, other, "SELECT v FROM sentinel", []int64{77}); err != nil {
			operation <- err
			return
		}
		_, err := other.ExecContext(ctx, "INSERT INTO sentinel VALUES (88)")
		operation <- err
	}()
	select {
	case err := <-operation:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(writer.release)
		<-dumpDone
		t.Fatal("Dump held engine lock during writer I/O")
	}
	close(writer.release)
	if err := <-dumpDone; err != nil {
		t.Fatal(err)
	}
	independent := openLocal(t, stuntgres.NewDriver(), "dump_copy")
	if err := stuntgres.Restore(ctx, independent, bytes.NewReader(writer.output.Bytes())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, independent, "SELECT v FROM sentinel", [][]any{{int64(77)}})
	reader := &gatedReader{started: make(chan struct{}), release: make(chan struct{})}
	restoreDone := make(chan error, 1)
	go func() { restoreDone <- stuntgres.Restore(ctx, db, reader) }()
	select {
	case <-reader.started:
	case <-ctx.Done():
		t.Fatal("Restore did not reach gated reader")
	}
	readDone := make(chan error, 1)
	go func() { readDone <- controlValues(ctx, other, "SELECT v FROM sentinel ORDER BY v", []int64{77, 88}) }()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(reader.release)
		<-restoreDone
		t.Fatal("Restore held engine lock during reader I/O")
	}
	close(reader.release)
	if err := <-restoreDone; err != nil {
		t.Fatal(err)
	}
	wantTuples(t, other, "SELECT v FROM replaced", [][]any{{int64(99)}})
}

func TestStringLiteralControls(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db, other := openLocal(t, d, "string-controls"), openLocal(t, d, "string-controls")
	if err := other.Ping(); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE TABLE t(id INT,label TEXT)")
	execSQL(t, db, `INSERT INTO t VALUES (1,E'a\n\b\f\r\t\vO\'Brien\\tail'),(2,E'\303\251\uD83D\uDE00\uFFFF'),(3,E''),(4,NULL)`)
	want := [][]any{{int64(1), "a\n\b\f\r\t\vO'Brien\\tail"}, {int64(2), "é😀\uFFFF"}, {int64(3), ""}, {int64(4), nil}}
	execSQL(t, db, "INSERT INTO t VALUES (5,$$a;'b$$),(6,$T$first\nsecond; O'Brien\\tail $1 $other$x$other$$T$),(7,$$$$),(8,$_9$é😀$_9$)")
	want = append(want, []any{int64(5), "a;'b"}, []any{int64(6), "first\nsecond; O'Brien\\tail $1 $other$x$other$"}, []any{int64(7), ""}, []any{int64(8), "é😀"})
	held, err := other.Query("SELECT * FROM t ORDER BY id")
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
	for _, pool := range []*sql.DB{db, other} {
		wantTuples(t, pool, "SELECT * FROM t ORDER BY id", want)
		assertStringLiteralMetadata(t, pool, "SELECT * FROM t ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"})
	}
	for _, row := range want {
		var id int64
		var text any
		if !held.Next() {
			t.Fatal("snapshot lost row")
		}
		if err := held.Scan(&id, &text); err != nil || id != row[0] || text != row[1] {
			t.Fatalf("snapshot %d %q: %v", id, text, err)
		}
	}
	if held.Next() || held.Err() != nil {
		t.Fatal("snapshot extra/error")
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(ctx, other, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("dump changed")
	}
	if !strings.Contains(dump.String(), "'a;''b'") {
		t.Fatalf("dump did not use ordinary quotes: %s", dump.String())
	}
	snapshot, err := other.Query("SELECT * FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	for _, suffix := range []string{"$Tag$a$tag$);", "$$a"} {
		script := "CREATE TABLE replacement(label TEXT); INSERT INTO replacement VALUES (" + suffix
		err := stuntgres.Restore(ctx, db, strings.NewReader(script))
		wantErr := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "unterminated dollar-quoted string at or near \"" + suffix + "\"", Position: "71"}
		var got *stuntgres.Error
		if !errors.As(err, &got) || *got != wantErr || got.SQLState() != "42601" {
			t.Fatalf("Restore: %#v %v, want %#v", got, err, wantErr)
		}
		for _, pool := range []*sql.DB{db, other} {
			wantTuples(t, pool, "SELECT * FROM t ORDER BY id", want)
		}
		_, err = db.Query("SELECT * FROM replacement")
		sqlState(t, err, "42P01")
	}
	for _, row := range want {
		var id int64
		var text any
		if !snapshot.Next() {
			t.Fatal("failed Restore lost snapshot")
		}
		if err := snapshot.Scan(&id, &text); err != nil || id != row[0] || text != row[1] {
			t.Fatalf("snapshot %d %v: %v", id, text, err)
		}
	}
	if snapshot.Next() || snapshot.Err() != nil {
		t.Fatal("snapshot extra/error")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := stuntgres.Restore(canceled, db, strings.NewReader("CREATE TABLE canceled(x TEXT); INSERT INTO canceled VALUES ($$v$$)")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Restore: %v", err)
	}
	wantTuples(t, other, "SELECT * FROM t ORDER BY id", want)
	for _, source := range []string{`E'\0')`, `E'\xff')`, `E'\u061')`, `E'abc\`} {
		script := "CREATE TABLE replacement(label TEXT); INSERT INTO replacement VALUES ('valid'); INSERT INTO replacement VALUES (" + source
		if err := stuntgres.Restore(ctx, db, strings.NewReader(script)); err == nil {
			t.Fatal("bad Restore succeeded")
		}
		wantTuples(t, other, "SELECT * FROM t ORDER BY id", want)
		_, err := db.Query("SELECT * FROM replacement")
		sqlState(t, err, "42P01")
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader(`CREATE TABLE restored(label TEXT); INSERT INTO restored VALUES (E'a\nb'),(E'\xC3\xA9');`)); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, other, "SELECT * FROM restored", [][]any{{"a\nb"}, {"é"}})
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE restored(id INT,label TEXT); INSERT INTO restored VALUES (1,$$a;'b$$),(2,$T$first\nsecond; $other$x$other$$T$);")); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, other, "SELECT * FROM restored ORDER BY id", [][]any{{int64(1), "a;'b"}, {int64(2), "first\nsecond; $other$x$other$"}})
	assertStringLiteralMetadata(t, other, "SELECT * FROM restored ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"})
}

func TestNumericLiteralsControlsAndCancellation(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), "numeric_controls")
	script := "CREATE TABLE t(id INT,b BIGINT); INSERT INTO t VALUES (1,0b1010),(2,1_000),(3,0x7FFF_FFFF_FFFF_FFFF),(4,-0x8000_0000_0000_0000)"
	want := [][]any{{int64(1), int64(10)}, {int64(2), int64(1000)}, {int64(3), int64(9223372036854775807)}, {int64(4), int64(-9223372036854775808)}}
	if err := stuntgres.Restore(ctx, db, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		rows, err := db.Query("SELECT id,b FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		assertNumericRows(t, rows, []string{"id", "b"}, []string{"INT4", "INT8"}, want)
	}
	check()
	held, err := db.Query("SELECT id,b FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	bad := "CREATE TABLE staged(x INT); INSERT INTO staged VALUES (1); INSERT INTO staged VALUES (0b2)"
	err = stuntgres.Restore(ctx, db, strings.NewReader(bad))
	assertNumericDiagnostic(t, err, "42601", `trailing junk after numeric literal at or near "0b2"`, "87")
	check()
	_, err = db.Query("SELECT x FROM staged")
	sqlState(t, err, "42P01")
	assertNumericRows(t, held, []string{"id", "b"}, []string{"INT4", "INT8"}, want)
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{"10", "1000", "9223372036854775807", "-9223372036854775808"} {
		if !strings.Contains(dump.String(), spelling) {
			t.Fatalf("canonical dump missing %s: %s", spelling, dump.String())
		}
	}
	for _, spelling := range []string{"0b", "0x", "1_000", "7FFF"} {
		if strings.Contains(dump.String(), spelling) {
			t.Fatalf("noncanonical dump: %s", dump.String())
		}
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = db.ExecContext(cancelled, "INSERT INTO t VALUES (5,0xa)")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Exec cancellation: %v", err)
	}
	stmt, err := db.PrepareContext(cancelled, "INSERT INTO t VALUES (5,0xa)")
	if stmt != nil {
		stmt.Close()
		t.Fatal("canceled Prepare succeeded")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Prepare cancellation: %v", err)
	}
	err = stuntgres.Restore(cancelled, db, strings.NewReader(script))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Restore cancellation: %v", err)
	}
	var sqlErr *stuntgres.Error
	if errors.As(err, &sqlErr) {
		t.Fatalf("context cancellation has SQL diagnostic: %#v", sqlErr)
	}
	check()
}

func TestBooleanDiagnosticsControls(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "boolean_controls")
	ctx := context.Background()
	execSQL(t, db, "CREATE TABLE b(id INT,flag BOOL)")
	execSQL(t, db, "INSERT INTO b VALUES (99,TRUE),(100,FALSE),(101,NULL)")
	want := [][]any{{int64(99), true}, {int64(100), false}, {int64(101), nil}}
	cursor, err := db.Query("SELECT id,flag FROM b ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(flag BOOL); INSERT INTO replacement VALUES (TRUE),('o');"))
	wantBooleanDiagnostic(t, err, `invalid input syntax for type boolean: "o"`, "77")
	wantTuples(t, db, "SELECT id,flag FROM b ORDER BY id", want)
	wantBooleanMetadata(t, db, "SELECT flag FROM b")
	_, err = db.Query("SELECT flag FROM replacement")
	sqlState(t, err, "42P01")
	var before bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &before); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader(before.String())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT id,flag FROM b ORDER BY id", want)
	wantBooleanMetadata(t, db, "SELECT flag FROM b")
	var after bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &after); err != nil {
		t.Fatal(err)
	}
	if before.String() != after.String() {
		t.Fatal("boolean dump changed after round trip")
	}
	var old [][]any
	for cursor.Next() {
		var id, flag any
		if err := cursor.Scan(&id, &flag); err != nil {
			t.Fatal(err)
		}
		old = append(old, []any{id, flag})
	}
	if err := cursor.Err(); err != nil {
		t.Fatal(err)
	}
	if len(old) != len(want) {
		t.Fatalf("retained cursor = %#v", old)
	}
	for i, row := range want {
		if old[i][0] != row[0] || old[i][1] != row[1] {
			t.Fatalf("retained cursor = %#v", old)
		}
	}
	wantBooleanDiagnostic(t, booleanFailure(t, db, "SELECT id FROM b WHERE flag='o'", "query"),
		`invalid input syntax for type boolean: "o"`, "29")
}
