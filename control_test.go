package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

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
	query := `SELECT "id","text","flag" FROM "odd""table" ORDER BY "id"`
	want := [][]any{{int64(0), "line\nO'Brien\\end", false}, {int64(1), nil, nil}, {int64(2), "", false}}
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
			execSQL(t, db, "CREATE TABLE snap(v INT)")
			execSQL(t, db, "INSERT INTO snap VALUES(1),(2)")
			rows, err := db.Query("SELECT v FROM snap ORDER BY v")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			execSQL(t, other, "INSERT INTO snap VALUES(3)")
			if restore {
				err = stuntgres.Restore(context.Background(), other, strings.NewReader("CREATE TABLE snap(v INT); INSERT INTO snap VALUES(99);"))
			} else {
				err = stuntgres.Reset(context.Background(), other)
			}
			if err != nil {
				t.Fatal(err)
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
				wantTuples(t, other, "SELECT v FROM snap", [][]any{{int64(99)}})
			} else {
				_, err = other.Query("SELECT v FROM snap")
				sqlState(t, err, "42P01")
			}
		})
	}
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
