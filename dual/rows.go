package dual

import (
	"database/sql/driver"
	"io"
	"reflect"
	"slices"
	"time"
)

// rawSet is one result set as a side delivered it; values are detached copies.
type rawSet struct {
	columns []string
	types   []string
	rows    [][]driver.Value
	partial bool // native only: the caller left the set before its end
}

// rowsOutcome is one side's result sets. complete means the end of the last
// set was reached; err is the first non-EOF error, met in set errSet.
type rowsOutcome struct {
	sets     []rawSet
	complete bool
	err      error
	errSet   int
}

// rows wraps native rows: it captures what the caller consumes and writes the
// call's record at Close. The reference rows were drained during the query.
type rows struct {
	native driver.Rows
	e      *entry
	ref    *rowsOutcome // nil when the reference did not produce rows
	out    rowsOutcome
	eof    bool // the current native set reached io.EOF
	done   bool
}

func newRows(e *entry, native driver.Rows, ref *rowsOutcome) *rows {
	r := &rows{native: native, e: e, ref: ref}
	r.startSet()
	return r
}

func (r *rows) startSet() {
	columns := r.native.Columns()
	r.out.sets = append(r.out.sets, rawSet{columns: slices.Clone(columns), types: typeNames(r.native, len(columns))})
	r.eof = false
}

func (r *rows) current() *rawSet { return &r.out.sets[len(r.out.sets)-1] }

func (r *rows) noteErr(err error) {
	if r.out.err == nil {
		r.out.err, r.out.errSet = err, len(r.out.sets)-1
	}
}

func (r *rows) Columns() []string { return r.native.Columns() }

func (r *rows) Next(dest []driver.Value) error {
	err := r.native.Next(dest)
	switch {
	case err == nil:
		if !r.done {
			set := r.current()
			set.rows = append(set.rows, copyRow(dest))
		}
	case err == io.EOF:
		r.eof = true
	default:
		r.noteErr(err)
	}
	return err
}

// HasNextResultSet is the native answer; database/sql asks it after every
// io.EOF, which is how the wrapper learns that the last set ended.
func (r *rows) HasNextResultSet() bool {
	has := false
	if n, ok := r.native.(driver.RowsNextResultSet); ok {
		has = n.HasNextResultSet()
	}
	if r.eof && !has {
		r.out.complete = true
	}
	return has
}

func (r *rows) NextResultSet() error {
	n, ok := r.native.(driver.RowsNextResultSet)
	if !ok {
		r.out.complete = true
		return io.EOF
	}
	err := n.NextResultSet()
	switch {
	case err == nil:
		r.current().partial = !r.eof
		r.startSet()
	case err == io.EOF:
		r.out.complete = true
	default:
		r.noteErr(err)
	}
	return err
}

// Close closes the native rows, then compares and writes the record once.
func (r *rows) Close() error {
	err := r.native.Close()
	if r.done {
		return err
	}
	r.done = true
	if err != nil {
		r.noteErr(err)
	}
	set := r.current()
	set.partial = set.partial || !r.eof
	e, n, ref := r.e, &r.out, r.ref
	e.end = time.Now()
	e.nerr = n.err
	e.summary = func() { e.rec.Response = summarizeRows(n) }
	if isCanceled(n.err) {
		e.uncompared = "canceled"
	}
	if ref != nil {
		e.rec.ResponseRef = summarizeRows(ref)
		e.hideRefErr = n.err == nil && !n.complete && ref.err != nil && unreached(n, ref)
		query, o := e.rec.Query, &e.c.cn.opts
		e.compare = func(d *diffLines) bool { return compareRows(d, o, query, n, ref) }
	}
	e.finish()
	return err
}

// unreached reports whether the reference error lies beyond what native
// consumed: in a later set, or in the set native left early after more rows
// than native read (the reference failed past native's last row).
func unreached(n, ref *rowsOutcome) bool {
	last, s := len(n.sets)-1, ref.errSet
	switch {
	case s > last:
		return true
	case s < last || !n.sets[last].partial || s >= len(ref.sets):
		return false
	}
	return len(ref.sets[s].rows) >= len(n.sets[s].rows)
}

func (r *rows) ColumnTypeScanType(i int) reflect.Type {
	if t, ok := r.native.(driver.RowsColumnTypeScanType); ok {
		return t.ColumnTypeScanType(i)
	}
	return reflect.TypeFor[any]()
}

func (r *rows) ColumnTypeDatabaseTypeName(i int) string {
	if t, ok := r.native.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return t.ColumnTypeDatabaseTypeName(i)
	}
	return ""
}

func (r *rows) ColumnTypeLength(i int) (int64, bool) {
	if t, ok := r.native.(driver.RowsColumnTypeLength); ok {
		return t.ColumnTypeLength(i)
	}
	return 0, false
}

func (r *rows) ColumnTypeNullable(i int) (bool, bool) {
	if t, ok := r.native.(driver.RowsColumnTypeNullable); ok {
		return t.ColumnTypeNullable(i)
	}
	return false, false
}

func (r *rows) ColumnTypePrecisionScale(i int) (int64, int64, bool) {
	if t, ok := r.native.(driver.RowsColumnTypePrecisionScale); ok {
		return t.ColumnTypePrecisionScale(i)
	}
	return 0, 0, false
}

var (
	_ driver.RowsNextResultSet              = (*rows)(nil)
	_ driver.RowsColumnTypeScanType         = (*rows)(nil)
	_ driver.RowsColumnTypeDatabaseTypeName = (*rows)(nil)
	_ driver.RowsColumnTypeLength           = (*rows)(nil)
	_ driver.RowsColumnTypeNullable         = (*rows)(nil)
	_ driver.RowsColumnTypePrecisionScale   = (*rows)(nil)
)
