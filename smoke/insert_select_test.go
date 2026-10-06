package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestInsertSelectSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("insert-select-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, want int64, args ...any) {
		t.Helper()
		result, err := db.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		got, err := result.RowsAffected()
		if err != nil || got != want {
			t.Fatal(q, got, err, want)
		}
	}
	check := func(pool *sql.DB, q string, names, types []string, want [][]any) {
		t.Helper()
		rows, err := pool.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		gotNames, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(gotNames, names) {
			t.Fatal(gotNames, err)
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
		got := [][]any{}
		for rows.Next() {
			row := make([]any, len(names))
			dst := make([]any, len(names))
			for i := range row {
				dst[i] = &row[i]
			}
			if err := rows.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			got = append(got, row)
		}
		if rows.Err() != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, rows.Err(), want)
		}
	}
	exec("CREATE TABLE s(i INT,v TEXT,b BOOL)", 0)
	exec("INSERT INTO s VALUES(1,'a',TRUE),(1,'a',TRUE),(2,NULL,FALSE)", 3)
	exec("CREATE TABLE t(v TEXT,i BIGINT,b BOOL)", 0)
	stmt, err := db.PrepareContext(ctx, "INSERT INTO t(i,v) SELECT i,v FROM s WHERE b=$1 ORDER BY i")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct {
		arg   any
		count int64
	}{{nil, 0}, {false, 1}, {true, 2}} {
		result, err := stmt.ExecContext(ctx, tc.arg)
		if err != nil {
			t.Fatal(err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != tc.count {
			t.Fatal(count, err)
		}
	}
	check(db, "SELECT * FROM t ORDER BY i", []string{"v", "i", "b"}, []string{"TEXT", "INT8", "BOOL"}, [][]any{{"a", int64(1), nil}, {"a", int64(1), nil}, {nil, int64(2), nil}})
	exec("INSERT INTO t(i,v) SELECT i,v FROM s WHERE FALSE", 0)
	exec("INSERT INTO t(i,v) SELECT i,v FROM s WHERE i=2", 1)
	exec("INSERT INTO t(i,v) SELECT i,v FROM s WHERE b", 2)
	exec("CREATE TABLE contextual(i INT)", 0)
	exec("INSERT INTO contextual (VALUES ('1'))", 1)
	exec("INSERT INTO contextual SELECT $1", 1, "7")
	exec("INSERT INTO contextual VALUES(2),(3) ORDER BY column1 DESC", 2)
	check(db, "SELECT i FROM contextual ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(7)}})
	command, err := db.QueryContext(ctx, "INSERT INTO contextual SELECT 8")
	if err != nil {
		t.Fatal(err)
	}
	names, err := command.Columns()
	types, te := command.ColumnTypes()
	if err != nil || te != nil || len(names) != 0 || len(types) != 0 || command.Next() || command.Err() != nil {
		t.Fatal(names, types, err, te, command.Err())
	}
	command.Close()
	exec("CREATE TABLE order_keys(i INT)", 0)
	exec("INSERT INTO order_keys VALUES(9)", 1)
	exec("INSERT INTO order_keys VALUES(1),(2) ORDER BY +2", 2)
	plus, err := db.PrepareContext(ctx, "INSERT INTO order_keys VALUES(3),(4) ORDER BY +1.5 DESC")
	if err != nil {
		t.Fatal(err)
	}
	defer plus.Close()
	result, err := plus.ExecContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 2 {
		t.Fatal(count, err)
	}
	for _, prepared := range []bool{false, true} {
		var command *sql.Rows
		if prepared {
			command, err = plus.QueryContext(ctx)
		} else {
			command, err = db.QueryContext(ctx, "INSERT INTO order_keys VALUES(5),(6) ORDER BY +2 ASC")
		}
		if err != nil {
			t.Fatal(err)
		}
		names, err := command.Columns()
		types, typeErr := command.ColumnTypes()
		if err != nil || typeErr != nil || len(names) != 0 || len(types) != 0 || command.Next() || command.Err() != nil {
			command.Close()
			t.Fatal("constant-key command metadata", names, types, err, typeErr)
		}
		if err := command.Close(); err != nil {
			t.Fatal(err)
		}
	}
	orderRows := [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(3)}, {int64(4)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(9)}}
	check(db, "SELECT i FROM order_keys ORDER BY i", []string{"i"}, []string{"INT4"}, orderRows)
	for _, tc := range []struct{ q, code, message, token string }{
		{"INSERT INTO order_keys VALUES(1) ORDER BY 2147483648", "42601", "non-integer constant in ORDER BY", "2147483648"},
		{"INSERT INTO order_keys VALUES(1) ORDER BY -2147483648", "42601", "non-integer constant in ORDER BY", "-2147483648"},
		{"INSERT INTO missing VALUES(1) ORDER BY 'x'", "42P01", `relation "missing" does not exist`, "missing"},
		{"INSERT INTO order_keys(missing) VALUES(1) ORDER BY 'x'", "42703", `column "missing" of relation "order_keys" does not exist`, "missing"},
		{"INSERT INTO order_keys VALUES(1),(TRUE) ORDER BY 'x'", "42804", "VALUES types integer and boolean cannot be matched", "TRUE"},
		{"INSERT INTO order_keys VALUES(8); INSERT INTO order_keys VALUES(1) ORDER BY 'x'; CREATE TABLE select(x INT)", "42601", `syntax error at or near "select"`, "select"},
	} {
		want := stuntgres.Error{Code: tc.code, Message: tc.message, Position: strconv.Itoa(strings.Index(tc.q, tc.token) + 1)}
		for _, prepared := range []bool{false, true} {
			if prepared {
				s, prepareErr := db.PrepareContext(ctx, tc.q)
				if s != nil {
					s.Close()
					t.Fatal("failed constant-key Prepare exposed a statement")
				}
				err = prepareErr
			} else {
				result, executeErr := db.ExecContext(ctx, tc.q)
				if result != nil {
					t.Fatal("failed constant-key Exec exposed a result")
				}
				err = executeErr
			}
			if diagnosticErr := typeCoreDiagnostic(err, want); diagnosticErr != nil {
				t.Fatal(tc.q, diagnosticErr)
			}
			check(db, "SELECT i FROM order_keys ORDER BY i", []string{"i"}, []string{"INT4"}, orderRows)
		}
	}
	exec("CREATE TABLE self(i INT)", 0)
	exec("INSERT INTO self VALUES(1),(1),(2)", 3)
	exec("INSERT INTO self SELECT * FROM self", 3)
	check(db, "SELECT i FROM self ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}})
	exec("CREATE TABLE narrowing(i INT)", 0)
	exec("INSERT INTO narrowing VALUES(9)", 1)
	exec("CREATE TABLE wide(i BIGINT)", 0)
	exec("INSERT INTO wide VALUES(1),(2147483648)", 2)
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"INSERT INTO missing SELECT 1", stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Position: "13"}},
		{"INSERT INTO t(missing) SELECT 1", stuntgres.Error{Code: "42703", Message: `column "missing" of relation "t" does not exist`, Position: "15"}},
		{"INSERT INTO t(i) SELECT 1,2", stuntgres.Error{Code: "42601", Message: "INSERT has more expressions than target columns", Position: "27"}},
		{"INSERT INTO t(i) SELECT 'bad'", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type bigint: "bad"`, Position: "25"}},
		{"INSERT INTO contextual VALUES('1') ORDER BY 1", stuntgres.Error{Code: "42804", Message: `column "i" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression."}},
		{"INSERT INTO narrowing SELECT i FROM wide ORDER BY i", stuntgres.Error{Code: "22003", Message: "integer out of range"}},
	} {
		result, err := db.ExecContext(ctx, tc.q)
		if result != nil {
			t.Fatal("failed producer returned result")
		}
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(tc.q, err)
		}
	}
	check(db, "SELECT i FROM narrowing", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}})
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	cloneConnector, err := d.OpenConnector("insert-select-smoke-clone")
	if err != nil {
		t.Fatal(err)
	}
	clone := sql.OpenDB(cloneConnector)
	defer clone.Close()
	if err := stuntgres.Restore(ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(clone, "SELECT i FROM self ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}})
	err = stuntgres.Restore(ctx, clone, strings.NewReader("CREATE TABLE self(i INT); INSERT INTO self SELECT $1"))
	if err := typeCoreDiagnostic(err, stuntgres.Error{Code: "0A000", Message: "restore INSERT query sources are unsupported"}); err != nil {
		t.Fatal(err)
	}
	check(clone, "SELECT i FROM self ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}})
}
