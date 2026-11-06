package dual

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"slices"
)

// The ctx* helpers call a driver object exactly as database/sql's ctxutil.go
// does: through the context interface when implemented, else through the
// legacy method after the same checks. ctxExec and ctxQuery return ErrSkip when
// neither variant exists. Both the native and the reference side use them.

func ctxPrepare(ctx context.Context, c driver.Conn, query string) (driver.Stmt, error) {
	if p, ok := c.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, query)
	}
	st, err := c.Prepare(query)
	if err == nil && ctx.Err() != nil {
		_ = st.Close()
		return nil, ctx.Err()
	}
	return st, err
}

func ctxExec(ctx context.Context, c driver.Conn, query string, args []driver.NamedValue) (driver.Result, error) {
	if x, ok := c.(driver.ExecerContext); ok {
		return x.ExecContext(ctx, query, args)
	}
	x, ok := c.(driver.Execer)
	if !ok {
		return nil, driver.ErrSkip
	}
	values, err := namedValues(args)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return x.Exec(query, values)
}

func ctxQuery(ctx context.Context, c driver.Conn, query string, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := c.(driver.QueryerContext); ok {
		return q.QueryContext(ctx, query, args)
	}
	q, ok := c.(driver.Queryer)
	if !ok {
		return nil, driver.ErrSkip
	}
	values, err := namedValues(args)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return q.Query(query, values)
}

func ctxStmtExec(ctx context.Context, s driver.Stmt, args []driver.NamedValue) (driver.Result, error) {
	if x, ok := s.(driver.StmtExecContext); ok {
		return x.ExecContext(ctx, args)
	}
	values, err := namedValues(args)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.Exec(values)
}

func ctxStmtQuery(ctx context.Context, s driver.Stmt, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := s.(driver.StmtQueryContext); ok {
		return q.QueryContext(ctx, args)
	}
	values, err := namedValues(args)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.Query(values)
}

// ctxBegin applies database/sql's rejections for drivers without ConnBeginTx.
func ctxBegin(ctx context.Context, c driver.Conn, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	if sql.IsolationLevel(opts.Isolation) != sql.LevelDefault {
		return nil, errors.New("sql: driver does not support non-default isolation level")
	}
	if opts.ReadOnly {
		return nil, errors.New("sql: driver does not support read-only transactions")
	}
	t, err := c.Begin()
	if err == nil && ctx.Err() != nil {
		_ = t.Rollback()
		return nil, ctx.Err()
	}
	return t, err
}

func namedValues(args []driver.NamedValue) ([]driver.Value, error) {
	values := make([]driver.Value, len(args))
	for i, a := range args {
		if a.Name != "" {
			return nil, errors.New("sql: driver does not support the use of Named Parameters")
		}
		values[i] = a.Value
	}
	return values, nil
}

func namedArgs(values []driver.Value) []driver.NamedValue {
	args := make([]driver.NamedValue, len(values))
	for i, v := range values {
		args[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return args
}

// refExec runs an Exec on the reference, preparing it when the connection
// answers ErrSkip, as database/sql does.
func refExec(ctx context.Context, rc driver.Conn, query string, args []driver.NamedValue) (driver.Result, error) {
	r, err := ctxExec(ctx, rc, query, args)
	if err != driver.ErrSkip {
		return r, err
	}
	st, err := ctxPrepare(ctx, rc, query)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	return ctxStmtExec(ctx, st, args)
}

// refQuery runs a Query on the reference and drains it completely. The error
// is the Query error or the first drain error.
func refQuery(ctx context.Context, rc driver.Conn, query string, args []driver.NamedValue) (*rowsOutcome, error) {
	r, err := ctxQuery(ctx, rc, query, args)
	if err == driver.ErrSkip {
		st, perr := ctxPrepare(ctx, rc, query)
		if perr != nil {
			return nil, perr
		}
		defer st.Close()
		r, err = ctxStmtQuery(ctx, st, args)
	}
	if err != nil {
		return nil, err
	}
	out := drain(r)
	return out, out.err
}

func refStmtQuery(ctx context.Context, st driver.Stmt, args []driver.NamedValue) (*rowsOutcome, error) {
	r, err := ctxStmtQuery(ctx, st, args)
	if err != nil {
		return nil, err
	}
	out := drain(r)
	return out, out.err
}

func refPing(ctx context.Context, rc driver.Conn) error {
	if p, ok := rc.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func refReset(ctx context.Context, rc driver.Conn) error {
	if r, ok := rc.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func refValid(rc driver.Conn) bool {
	if v, ok := rc.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// drain reads every set fully before asking for the next one (lib/pq only
// discovers a set after the previous one is read), stops at the first non-EOF
// error and always closes the rows.
func drain(r driver.Rows) *rowsOutcome {
	out := &rowsOutcome{}
	next, multi := r.(driver.RowsNextResultSet)
	for out.err == nil {
		columns := r.Columns()
		set := rawSet{columns: slices.Clone(columns), types: typeNames(r, len(columns))}
		dest := make([]driver.Value, len(columns))
		for {
			err := r.Next(dest)
			if err == io.EOF {
				break
			}
			if err != nil {
				out.err, out.errSet = err, len(out.sets)
				break
			}
			set.rows = append(set.rows, copyRow(dest))
		}
		out.sets = append(out.sets, set)
		if out.err != nil {
			break
		}
		if !multi || !next.HasNextResultSet() {
			out.complete = true
			break
		}
		if err := next.NextResultSet(); err != nil {
			if err == io.EOF {
				out.complete = true
			} else {
				out.err, out.errSet = err, len(out.sets)
			}
			break
		}
	}
	if err := r.Close(); err != nil && out.err == nil {
		out.err, out.errSet = err, len(out.sets)-1
		out.complete = false
	}
	return out
}

func typeNames(r driver.Rows, n int) []string {
	names := make([]string, n)
	if t, ok := r.(driver.RowsColumnTypeDatabaseTypeName); ok {
		for i := range names {
			names[i] = t.ColumnTypeDatabaseTypeName(i)
		}
	}
	return names
}

// copyRow detaches a row from driver-owned buffers.
func copyRow(dest []driver.Value) []driver.Value {
	row := make([]driver.Value, len(dest))
	for i, v := range dest {
		if b, ok := v.([]byte); ok {
			v = bytes.Clone(b)
		}
		row[i] = v
	}
	return row
}
