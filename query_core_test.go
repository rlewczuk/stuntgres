package stuntgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres"
)

func TestQueryCoreOrderPositionRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "query-core-order-position")
	execSQL(t, db, "CREATE TABLE t(id INT)")
	for _, population := range []string{"empty", "populated"} {
		if population == "populated" {
			execSQL(t, db, "INSERT INTO t VALUES(1),(2)")
		}
		t.Run(population, func(t *testing.T) {
			for _, tc := range []struct {
				name, query, position string
			}{
				{"ascii", "SELECT id FROM t ORDER BY missing", "27"},
				// PostgreSQL positions count characters, not UTF8 bytes.
				{"utf8", "SELECT /*é*/ id FROM t ORDER BY missing", "33"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					want := stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: tc.position}
					t.Run("query", func(t *testing.T) {
						rows, err := db.Query(tc.query)
						if rows != nil {
							rows.Close()
							t.Fatal("failed Query exposed a cursor")
						}
						typeCoreDiagnostic(t, err, want)
					})
					t.Run("prepare", func(t *testing.T) {
						stmt, err := db.Prepare(tc.query)
						if stmt != nil {
							stmt.Close()
							t.Fatal("failed Prepare exposed a handle")
						}
						typeCoreDiagnostic(t, err, want)
					})
				})
			}
		})
	}
}

func queryCoreRows(t *testing.T, rows *sql.Rows, err error, names, types []string, want [][]any, bag bool) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	gotNames, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(gotNames, names) {
		t.Fatal(gotNames, err, names)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatal(metadata, err)
	}
	for i, typ := range types {
		if metadata[i].DatabaseTypeName() != typ {
			t.Fatal(metadata[i], typ)
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		values := make([]any, len(names))
		dst := make([]any, len(names))
		for i := range values {
			dst[i] = &values[i]
		}
		if err := rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if bag {
		if len(got) != len(want) {
			t.Fatal(got, want)
		}
		used := make([]bool, len(want))
		for _, row := range got {
			found := false
			for i, expected := range want {
				if !used[i] && reflect.DeepEqual(row, expected) {
					used[i] = true
					found = true
					break
				}
			}
			if !found {
				t.Fatal("bag occurrence", row, got, want)
			}
		}
	} else if !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
}

func TestQueryCoreProjectionBagsAndMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "query-core-projection")
	execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,v INT,s TEXT,f BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,1,'x',TRUE),(2,NULL,'x',NULL),(3,2,'y',FALSE),(4,1,'x',TRUE)")
	execSQL(t, db, "CREATE TABLE empty(id INT NOT NULL,v INT,s TEXT,f BOOL)")
	for _, tc := range []struct {
		q            string
		names, types []string
		want         [][]any
		args         []any
		bag          bool
	}{
		{"SELECT id,7,NULL,f FROM t ORDER BY id", []string{"id", "?column?", "?column?", "f"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, [][]any{{int64(1), int64(7), nil, true}, {int64(2), int64(7), nil, nil}, {int64(3), int64(7), nil, false}, {int64(4), int64(7), nil, true}}, nil, false},
		{"SELECT id,7,NULL,f FROM empty", []string{"id", "?column?", "?column?", "f"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, [][]any{}, nil, true},
		{"SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}, nil, true},
		{"SELECT 7 FROM t WHERE v=1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}, nil, true},
		{"SELECT v FROM t WHERE v=NULL", []string{"v"}, []string{"INT4"}, [][]any{}, nil, true},
		{"SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, [][]any{}, []any{nil}, true},
		{"SELECT id FROM t WHERE TRUE", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}, nil, true},
		{"SELECT id FROM t WHERE FALSE", []string{"id"}, []string{"INT4"}, [][]any{}, nil, true},
		{"SELECT id FROM t WHERE NULL", []string{"id"}, []string{"INT4"}, [][]any{}, nil, true},
		{"SELECT id FROM t WHERE f", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}, nil, true},
		{"SELECT $1 FROM t WHERE s=$1", []string{"?column?"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}}, []any{"x"}, true},
		{"SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {nil}}, nil, false},
		{"SELECT v FROM t ORDER BY v DESC", []string{"v"}, []string{"INT4"}, [][]any{{nil}, {int64(2)}, {int64(1)}, {int64(1)}}, nil, false},
		{"SELECT id,id FROM t ORDER BY id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}}, nil, false},
	} {
		t.Run(tc.q, func(t *testing.T) {
			rows, err := db.Query(tc.q, tc.args...)
			queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, tc.bag)
			stmt, err := db.Prepare(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err = stmt.Query(tc.args...)
			queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, tc.bag)
		})
	}
	rows, err := db.Query("SELECT id,7,NULL,f FROM t")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := rows.ColumnTypes()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if nullable, known := metadata[0].Nullable(); nullable || !known {
		t.Fatal("physical NOT NULL lost")
	}
	for _, i := range []int{1, 2} {
		if _, known := metadata[i].Nullable(); known {
			t.Fatal("scalar nullability invented")
		}
	}
	if nullable, known := metadata[3].Nullable(); !nullable || !known {
		t.Fatal("physical nullable lost")
	}
}

func TestQueryCoreOwnershipCancellationAndControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			d := stuntgres.NewDriver()
			db := openLocal(t, d, "query-core-"+replacement)
			execSQL(t, db, "CREATE TABLE t(id INT NOT NULL)")
			execSQL(t, db, "INSERT INTO t VALUES(1),(2)")
			raw, err := d.Open("query-core-" + replacement)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			query := raw.(driver.QueryerContext)
			direct, err := query.QueryContext(context.Background(), "SELECT id FROM t ORDER BY id", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer direct.Close()
			owned := []byte("before")
			computed, err := query.QueryContext(context.Background(), "SELECT id,$1 FROM t ORDER BY id", []driver.NamedValue{{Ordinal: 1, Value: owned}})
			if err != nil {
				t.Fatal(err)
			}
			defer computed.Close()
			owned[0] = 'X'
			execSQL(t, db, "INSERT INTO t VALUES(3)")
			if replacement == "reset" {
				if err := stuntgres.Reset(context.Background(), db); err != nil {
					t.Fatal(err)
				}
			} else if err := stuntgres.Restore(context.Background(), db, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('99')")); err != nil {
				t.Fatal(err)
			}
			for _, cursor := range []driver.Rows{direct, computed} {
				names := cursor.Columns()
				names[0] = "changed"
				if cursor.Columns()[0] != "id" || cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "INT4" {
					t.Fatal("metadata replaced or borrowed")
				}
				dst := make([]driver.Value, len(cursor.Columns()))
				for i := range 2 {
					if err := cursor.Next(dst); err != nil || dst[0] != int64(i+1) {
						t.Fatal(dst, err)
					}
					if len(dst) == 2 && dst[1] != "before" {
						t.Fatal(dst)
					}
				}
				if err := cursor.Next(dst); err != io.EOF {
					t.Fatal(err)
				}
				if cursor.Columns()[0] != "id" {
					t.Fatal("EOF erased schema")
				}
			}
			if replacement == "restore" {
				wantTuples(t, db, "SELECT replacement FROM t", [][]any{{"99"}})
			} else {
				rows, err := db.Query("SELECT id FROM t")
				if rows != nil {
					rows.Close()
					t.Fatal("reset source remains")
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "t" does not exist`, Position: "16"})
			}
		})
	}
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "query-core-cancel")
	execSQL(t, db, "CREATE TABLE t(id INT,v TIMESTAMP)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'infinity'),(2,'infinity')")
	raw, err := d.Open("query-core-cancel")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT id,v,v FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	dst := make([]driver.Value, 3)
	if err := cursor.Next(dst); err != nil {
		t.Fatal(err)
	}
	saved := append([]driver.Value(nil), dst...)
	first, second := saved[1].([]byte), saved[2].([]byte)
	first[0] = 'X'
	if string(second) != "infinity" {
		t.Fatal("exports alias")
	}
	cancel()
	for range 2 {
		if err := cursor.Next(dst); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation not sticky", err)
		}
	}
	if !reflect.DeepEqual(cursor.Columns(), []string{"id", "v", "v"}) || saved[0] != int64(1) || string(second) != "infinity" {
		t.Fatal("terminal cleanup invalidated saved values/schema")
	}
	if err := cursor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cursor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	other, err := raw.(driver.QueryerContext).QueryContext(context.Background(), "SELECT v FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.Next(make([]driver.Value, 0)); err == nil {
		t.Fatal("destination width accepted")
	}
	again, err := raw.(driver.QueryerContext).QueryContext(context.Background(), "SELECT v FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	one := make([]driver.Value, 1)
	if err := again.Next(one); err != nil || string(one[0].([]byte)) != "infinity" {
		t.Fatal(one, err)
	}
}

func TestQueryCoreDiagnosticsAndPreparedFrames(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "query-core-errors")
	execSQL(t, db, "CREATE TABLE t(id INT,v INT,f BOOL)")
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT missing FROM t", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}},
		{"SELECT /*é*/ missing FROM t", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "14"}},
		{"SELECT id FROM t WHERE missing=1", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "24"}},
		{"SELECT id FROM t WHERE 1", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "24"}},
		{"SELECT id FROM t WHERE id=TRUE", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "26", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE 'truth'", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "24"}},
		{"SELECT id FROM t WHERE", stuntgres.Error{Code: "42601", Message: "syntax error at end of input", Position: "23"}},
		{"SELECT $1 FROM t WHERE id=$1", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}},
		{"SELECT $1 FROM t WHERE f=$1", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "boolean versus text", Position: "8"}},
		{"SELECT $2 FROM t", stuntgres.Error{Code: "42P18", Message: "could not determine data type of parameter $1"}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			rows, err := db.Query(tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("analysis exposed rows")
			}
			typeCoreDiagnostic(t, err, tc.want)
			stmt, err := db.Prepare(tc.q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("analysis exposed handle")
			}
			typeCoreDiagnostic(t, err, tc.want)
		})
	}
	execSQL(t, db, "INSERT INTO t VALUES(1,1,TRUE),(2,NULL,NULL),(3,2,FALSE),(4,1,TRUE)")
	stmt, err := db.Prepare("SELECT id FROM t WHERE v=$1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i, arg := range []any{1, 2, nil} {
		want := [][]any{{int64(1)}, {int64(4)}}
		if i == 1 {
			want = [][]any{{int64(3)}}
		}
		if i == 2 {
			want = [][]any{}
		}
		rows, err := stmt.Query(arg)
		queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, want, false)
	}
	if _, err := stmt.Query(); err == nil {
		t.Fatal("missing actual args accepted")
	} else {
		var diagnostic *stuntgres.Error
		if errors.As(err, &diagnostic) {
			t.Fatal("ordinary adapter error gained SQLSTATE", err)
		}
	}
}

// Arm only after eager production/Finish, then cancel on the second exported
// cell. Atomics also make the context safe for the admitted operation owner.
type queryCoreExportContext struct {
	context.Context
	armed     atomic.Bool
	remaining atomic.Int64
}

func (c *queryCoreExportContext) Err() error {
	if c.armed.Load() && c.remaining.Add(-1) <= 0 {
		return context.Canceled
	}
	return c.Context.Err()
}
func TestQueryCoreExportCancellation(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "query-core-export-cancel")
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1),(2)")
	raw, err := d.Open("query-core-export-cancel")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	ctx := &queryCoreExportContext{Context: context.Background()}
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT id,id FROM t", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	ctx.remaining.Store(4)
	ctx.armed.Store(true)
	dst := make([]driver.Value, 2)
	if err := cursor.Next(dst); err != context.Canceled {
		t.Fatal("export failure not propagated", err)
	}
	ctx.armed.Store(false)
	if err := cursor.Next(dst); err != context.Canceled {
		t.Fatal("export failure not sticky", err)
	}
	if !reflect.DeepEqual(cursor.Columns(), []string{"id", "id"}) {
		t.Fatal(cursor.Columns())
	}
	if err := cursor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
}

func TestQueryCoreMissingRelationPositionRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "query-core-missing-relation-position")
	for _, tc := range []struct {
		name, query, position string
	}{
		{"ascii", "SELECT id FROM t", "16"},
		{"utf8", "SELECT /*é*/ id FROM t", "22"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := stuntgres.Error{Code: "42P01", Message: `relation "t" does not exist`, Position: tc.position}
			t.Run("query", func(t *testing.T) {
				rows, err := db.Query(tc.query)
				if rows != nil {
					rows.Close()
					t.Fatal("failed Query exposed a cursor")
				}
				typeCoreDiagnostic(t, err, want)
			})
			t.Run("prepare", func(t *testing.T) {
				stmt, err := db.Prepare(tc.query)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed Prepare exposed a handle")
				}
				typeCoreDiagnostic(t, err, want)
			})
		})
	}
}
