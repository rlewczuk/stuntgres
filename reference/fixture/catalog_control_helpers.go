package fixture

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"stuntgres"
	"sync"
	"testing"
)

type CatalogControlFailingWriter struct{ Err error }

func (w CatalogControlFailingWriter) Write([]byte) (int, error) { return 0, w.Err }

type CatalogControlFailingReader struct{ Err error }

func (r CatalogControlFailingReader) Read([]byte) (int, error) { return 0, r.Err }

type CatalogControlCancelReader struct {
	Cancel context.CancelFunc
	done   bool
}

func (r *CatalogControlCancelReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.Cancel()
	return copy(p, "CREATE TABLE replacement(v INT);"), nil
}
func CatalogControlValues(ctx context.Context, db *sql.DB, query string, want []int64) error {
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

type CatalogControlGatedWriter struct {
	Started chan struct{}
	Release chan struct{}
	once    sync.Once
	Output  bytes.Buffer
}

func (w *CatalogControlGatedWriter) Write(p []byte) (int, error) {
	blocked := false
	w.once.Do(func() { close(w.Started); blocked = true })
	if blocked {
		<-w.Release
	}
	return w.Output.Write(p)
}

type CatalogControlGatedReader struct {
	Started chan struct{}
	Release chan struct{}
	once    sync.Once
	sent    bool
}

func (r *CatalogControlGatedReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.once.Do(func() { close(r.Started); <-r.Release })
	r.sent = true
	return copy(p, "CREATE TABLE public.replaced (v INT); INSERT INTO public.replaced VALUES (99);"), nil
}

// Additional pools are only public sharing/isolation/clone witnesses; the
// scenario owns the primary configured engine and backend lifecycle.
func CatalogControlPool(t *testing.T, d *stuntgres.Driver, dsn string) *sql.DB {
	t.Helper()
	c, err := d.OpenConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
func CatalogControlExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(q, err)
	}
}
func CatalogControlTuples(t *testing.T, db *sql.DB, q string, want [][]any) {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][]any
	for rows.Next() {
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		cells := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		got = append(got, cells)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if len(got) != len(want) {
		t.Fatalf("rows %#v want %#v", got, want)
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("rows %#v want %#v", got, want)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
func CatalogControlAffected(t *testing.T, ctx context.Context, x Executor, q string, prepared bool, want int64, args ...any) {
	t.Helper()
	s := Exec(q, want, args...)
	s.Prepared = prepared
	RunStep(t, ctx, x, &Recorder{RawBackend: RawNative}, s)
}
func CatalogControlSelect(t *testing.T, ctx context.Context, x Executor, q string, prepared bool, want ...int64) {
	t.Helper()
	rows, closeQuery, err := TransactionQuery(ctx, x, q, prepared)
	if err != nil {
		t.Fatal(q, err)
	}
	defer closeQuery()
	CatalogControlNativeValues(t, rows, want...)
	if err := closeQuery(); err != nil {
		t.Fatal(err)
	}
}
func CatalogControlConnections(t *testing.T, p *Pair, create bool) (*sql.DB, *sql.Conn, *sql.Conn) {
	t.Helper()
	_, a, b := IsolationReferenceConnections(t, p, false)
	if create {
		CatalogControlAffected(t, p.Ctx, b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
	}
	return p.Native, a, b
}

func CatalogControlNativeValues(t *testing.T, rows *sql.Rows, want ...int64) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"v"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" || types[0].ScanType() != reflect.TypeFor[int64]() {
		t.Fatal("metadata", types, err)
	}
	TransactionRowValues(t, rows, want...)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
