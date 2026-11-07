package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"slices"
	"testing"

	"stuntgres"
)

// TestReferenceEnumAlterFillReownDriver runs the exact replacement repro through
// the public native driver and a single database/sql transaction, the same
// driver.Tx path coder's migration runner uses. The prepared route prepares each
// statement with tx.PrepareContext. Completing the binary is the witness that the
// transaction no longer aborts the process; the transaction-private read after
// RENAME and the committed read after COMMIT prove the fill followed the column.
func TestReferenceEnumAlterFillReownDriver(t *testing.T) {
	for _, route := range []struct {
		name     string
		prepared bool
	}{{"direct", false}, {"prepared", true}} {
		t.Run(route.name, func(t *testing.T) {
			ctx := context.Background()
			connector, err := stuntgres.NewDriver().OpenConnector("enum-fill-reown/" + t.Name())
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Rollback reports sql.ErrTxDone after a successful commit.
			defer tx.Rollback()
			inside := &enumFillDriverRoute{t: t, conn: tx, prepared: route.prepared, statements: map[string]*sql.Stmt{}}
			for _, step := range enumFillDriverRepro {
				inside.exec(ctx, step.query, step.affected)
			}
			inside.selectRows(ctx, "SELECT id, x::text AS v, x IS NULL AS missing FROM grin_pu ORDER BY id", []string{"id", "v", "missing"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), nil, true}})
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			after := &enumFillDriverRoute{t: t, conn: db}
			after.selectRows(ctx, "SELECT id, x::text AS v, x IS NULL AS missing FROM grin_pu ORDER BY id", []string{"id", "v", "missing"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), nil, true}})
			after.selectRows(ctx, "SELECT pg_typeof(x)::text AS v FROM grin_pu", []string{"v"}, []string{"TEXT"}, [][]any{{"grin_lt"}})
		})
	}
}

// enumFillDriverRepro is the A case without transaction control: BeginTx and
// Commit own BEGIN/COMMIT. Counts are the pinned 18.6 values.
var enumFillDriverRepro = []struct {
	query    string
	affected int64
}{
	{"CREATE TYPE grin_lt AS ENUM ('a','b')", 0},
	{"CREATE TABLE grin_pu (id int PRIMARY KEY)", 0},
	{"INSERT INTO grin_pu VALUES (1)", 1},
	{"ALTER TABLE grin_pu ADD COLUMN x grin_lt", 0},
	{"CREATE TYPE grin_new_lt AS ENUM ('a','b','c')", 0},
	{"ALTER TABLE grin_pu ALTER COLUMN x TYPE grin_new_lt USING (x::text::grin_new_lt)", 0},
	{"DROP TYPE grin_lt", 0},
	{"ALTER TYPE grin_new_lt RENAME TO grin_lt", 0},
}

type enumFillDriverConn interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
}

// enumFillDriverRoute executes either directly or through statements prepared on
// the same connection or transaction.
type enumFillDriverRoute struct {
	t          *testing.T
	conn       enumFillDriverConn
	prepared   bool
	statements map[string]*sql.Stmt
}

func (r *enumFillDriverRoute) statement(ctx context.Context, query string) *sql.Stmt {
	r.t.Helper()
	if stmt := r.statements[query]; stmt != nil {
		return stmt
	}
	stmt, err := r.conn.PrepareContext(ctx, query)
	if err != nil {
		r.t.Fatalf("prepare %q: %v", query, err)
	}
	r.statements[query] = stmt
	r.t.Cleanup(func() { stmt.Close() })
	return stmt
}

func (r *enumFillDriverRoute) exec(ctx context.Context, query string, affected int64) {
	r.t.Helper()
	var result sql.Result
	var err error
	if r.prepared {
		result, err = r.statement(ctx, query).ExecContext(ctx)
	} else {
		result, err = r.conn.ExecContext(ctx, query)
	}
	if err != nil {
		r.t.Fatalf("exec %q: %v", query, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		r.t.Fatalf("exec %q rows affected: %v", query, err)
	}
	if count != affected {
		r.t.Fatalf("exec %q affected %d, want %d", query, count, affected)
	}
}

func (r *enumFillDriverRoute) selectRows(ctx context.Context, query string, columns, types []string, want [][]any) {
	r.t.Helper()
	var rows *sql.Rows
	var err error
	if r.prepared {
		rows, err = r.statement(ctx, query).QueryContext(ctx)
	} else {
		rows, err = r.conn.QueryContext(ctx, query)
	}
	if err != nil {
		r.t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	gotColumns, err := rows.Columns()
	if err != nil {
		r.t.Fatal(err)
	}
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		r.t.Fatal(err)
	}
	typeNames := make([]string, len(columnTypes))
	for i, typ := range columnTypes {
		typeNames[i] = typ.DatabaseTypeName()
	}
	if !slices.Equal(gotColumns, columns) || !slices.Equal(typeNames, types) {
		r.t.Fatalf("query %q columns %#v types %#v, want %#v %#v", query, gotColumns, typeNames, columns, types)
	}
	got := make([][]any, 0, len(want))
	for rows.Next() {
		row := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			r.t.Fatalf("query %q scan: %v", query, err)
		}
		got = append(got, enumFillDriverRow(row))
	}
	if err := rows.Err(); err != nil {
		r.t.Fatalf("query %q rows: %v", query, err)
	}
	if !reflect.DeepEqual(got, want) {
		r.t.Fatalf("query %q rows %#v, want %#v", query, got, want)
	}
}

func enumFillDriverRow(row []any) []any {
	values := make([]any, len(row))
	for i, value := range row {
		switch typed := value.(type) {
		case []byte:
			values[i] = string(typed)
		case int32:
			values[i] = int64(typed)
		default:
			values[i] = typed
		}
	}
	return values
}
