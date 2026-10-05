package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func TestNumericLiteralsSmoke(t *testing.T) {
	if err := numericLiteralsRun(); err != nil {
		t.Fatal(err)
	}
}

func numericLiteralsDiagnostic(err error, code, message, position string) error {
	var got *stuntgres.Error
	want := &stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
	if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
		return fmt.Errorf("diagnostic got %#v (%v), want %#v", got, err, want)
	}
	return nil
}

func numericLiteralsCheckRows(rows *sql.Rows, names, types []string, want [][]any) error {
	defer rows.Close()
	gotNames, err := rows.Columns()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(gotNames, names) {
		return fmt.Errorf("columns %v, want %v", gotNames, names)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	for i, typ := range types {
		if metadata[i].DatabaseTypeName() != typ || metadata[i].ScanType() != reflect.TypeOf(int64(0)) {
			return fmt.Errorf("metadata %d: %s/%v, want %s/int64", i, metadata[i].DatabaseTypeName(), metadata[i].ScanType(), typ)
		}
	}
	var got [][]any
	for rows.Next() {
		values := make([]any, len(names))
		dest := make([]any, len(names))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("rows %v, want %v", got, want)
	}
	return nil
}

func numericLiteralsRun() error {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector("numeric-literals-smoke")
	if err != nil {
		return err
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(query string, count int64, prepared bool, args ...any) error {
		var result sql.Result
		var e error
		if prepared {
			stmt, err := db.PrepareContext(ctx, query)
			if err != nil {
				return err
			}
			defer stmt.Close()
			result, e = stmt.ExecContext(ctx, args...)
		} else {
			result, e = db.ExecContext(ctx, query, args...)
		}
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != count {
			return fmt.Errorf("affected %d, want %d: %s", n, count, query)
		}
		return nil
	}
	query := func(q string, prepared bool, names, types []string, want [][]any) error {
		var rows *sql.Rows
		var e error
		if prepared {
			stmt, err := db.PrepareContext(ctx, q)
			if err != nil {
				return err
			}
			defer stmt.Close()
			rows, e = stmt.QueryContext(ctx)
		} else {
			rows, e = db.QueryContext(ctx, q)
		}
		if e != nil {
			return e
		}
		return numericLiteralsCheckRows(rows, names, types, want)
	}
	for _, prepared := range []bool{false, true} {
		if err := stuntgres.Reset(ctx, db); err != nil {
			return err
		}
		if err := exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0, false); err != nil {
			return err
		}
		if err := exec("INSERT INTO t VALUES (1,0b1010,0o12),(2,0xa,1_000)", 2, prepared); err != nil {
			return err
		}
		names, types := []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}
		want := [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(1000)}}
		if err := query("SELECT id,i,b FROM t ORDER BY id", prepared, names, types, want); err != nil {
			return err
		}
		if err := exec("INSERT INTO t VALUES ($1,0B_1_010,0X_A)", 1, true, int64(3)); err != nil {
			return err
		}
		want = append(want, []any{int64(3), int64(10), int64(10)})
		if err := query("SELECT id,i,b FROM t WHERE i=0b1010 ORDER BY id", true, names, types, want); err != nil {
			return err
		}
		if err := query("SELECT id,i,b FROM t WHERE b=1_000", false, names, types, [][]any{{int64(2), int64(10), int64(1000)}}); err != nil {
			return err
		}
	}
	if err := exec("CREATE TABLE endpoints(id INT,b BIGINT)", 0, false); err != nil {
		return err
	}
	literals := []string{"9_223_372_036_854_775_807", "-9_223_372_036_854_775_808", "0x7FFF_FFFF_FFFF_FFFF", "-0x8000_0000_0000_0000", "0o777777777777777777777", "-0o1000000000000000000000", "0b" + strings.Repeat("1", 63), "-0b1" + strings.Repeat("0", 63)}
	var endpoints [][]any
	for i, literal := range literals {
		if err := exec(fmt.Sprintf("INSERT INTO endpoints VALUES (%d,%s)", i, literal), 1, i%2 == 1); err != nil {
			return err
		}
		value := int64(9223372036854775807)
		if i%2 == 1 {
			value = -9223372036854775808
		}
		endpoints = append(endpoints, []any{int64(i), value})
	}
	if err := query("SELECT id,b FROM endpoints ORDER BY id", true, []string{"id", "b"}, []string{"INT4", "INT8"}, endpoints); err != nil {
		return err
	}
	if err := exec("CREATE TABLE sentinel(x INT)", 0, false); err != nil {
		return err
	}
	if err := exec("INSERT INTO sentinel VALUES (99)", 1, false); err != nil {
		return err
	}
	unchanged := func() error {
		return query("SELECT x FROM sentinel ORDER BY x", false, []string{"x"}, []string{"INT4"}, [][]any{{int64(99)}})
	}
	for _, c := range []struct{ q, message, position string }{
		{"INSERT INTO t VALUES (1),(0b2)", `trailing junk after numeric literal at or near "0b2"`, "27"},
		{"INSERT INTO t VALUES (1),(0x_)", `invalid hexadecimal integer at or near "0x_"`, "27"},
		{"INSERT INTO t VALUES (1),(1__0)", `trailing junk after numeric literal at or near "1__0"`, "27"},
		{"INSERT INTO t VALUES (1),(1e3foo)", `trailing junk after numeric literal at or near "1e3foo"`, "27"},
		{"INSERT INTO t VALUES (1),(1e+3foo)", `trailing junk after numeric literal at or near "1e+3foo"`, "27"},
		{"INSERT INTO t VALUES (-0b2)", `trailing junk after numeric literal at or near "0b2"`, "24"},
		{"/*é*/ INSERT INTO t VALUES (0b2)", `trailing junk after numeric literal at or near "0b2"`, "29"},
	} {
		// t is intentionally used to retain the independently specified positions.
		for _, operation := range []string{"exec", "query", "prepare"} {
			var e error
			switch operation {
			case "exec":
				_, e = db.ExecContext(ctx, c.q)
			case "query":
				rows, err := db.QueryContext(ctx, c.q)
				e = err
				if rows != nil {
					rows.Close()
					return fmt.Errorf("failed Query returned cursor")
				}
			case "prepare":
				stmt, err := db.PrepareContext(ctx, c.q)
				e = err
				if stmt != nil {
					stmt.Close()
					return fmt.Errorf("failed Prepare returned statement")
				}
			}
			if err := numericLiteralsDiagnostic(e, "42601", c.message, c.position); err != nil {
				return err
			}
			if err := unchanged(); err != nil {
				return err
			}
			if err := query("SELECT id,i,b FROM t ORDER BY id", false, []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(1000)}, {int64(3), int64(10), int64(10)}}); err != nil {
				return err
			}
		}
	}
	// Actual sentinel second-row atomicity, independent of the positional table.
	_, err = db.ExecContext(ctx, "INSERT INTO sentinel VALUES (1),(0b2)")
	if err := numericLiteralsDiagnostic(err, "42601", `trailing junk after numeric literal at or near "0b2"`, "34"); err != nil {
		return err
	}
	if err := unchanged(); err != nil {
		return err
	}
	// Representable int8 constants are checked against int4 at execution.
	for _, prepared := range []bool{false, true} {
		const assignment = "INSERT INTO sentinel VALUES (1),(0x8000_0000)"
		if prepared {
			stmt, prepareErr := db.PrepareContext(ctx, assignment)
			if prepareErr != nil {
				return fmt.Errorf("int8-to-int4 assignment Prepare: %w", prepareErr)
			}
			_, err = stmt.ExecContext(ctx)
			stmt.Close()
		} else {
			_, err = db.ExecContext(ctx, assignment)
		}
		if err := numericLiteralsDiagnostic(err, "22003", "integer out of range", ""); err != nil {
			return err
		}
		if err := unchanged(); err != nil {
			return err
		}
	}
	// Native-only capability boundary, deliberately not PostgreSQL parity.
	_, err = db.ExecContext(ctx, "INSERT INTO t VALUES (0x8000000000000000)")
	if err := numericLiteralsDiagnostic(err, "22003", "integer literal out of range", "23"); err != nil {
		return err
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		return err
	}
	for _, spelling := range []string{"1000", "9223372036854775807", "-9223372036854775808"} {
		if !strings.Contains(dump.String(), spelling) {
			return fmt.Errorf("dump missing %s", spelling)
		}
	}
	if strings.Contains(dump.String(), "0x") || strings.Contains(dump.String(), "0b") || strings.Contains(dump.String(), "1_000") {
		return fmt.Errorf("dump is not canonical decimal")
	}
	held, err := db.QueryContext(ctx, "SELECT id,b FROM endpoints ORDER BY id")
	if err != nil {
		return err
	}
	defer held.Close()
	err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE staged(x INT); INSERT INTO staged VALUES (1); INSERT INTO staged VALUES (0b2)"))
	if err := numericLiteralsDiagnostic(err, "42601", `trailing junk after numeric literal at or near "0b2"`, "87"); err != nil {
		return err
	}
	if err := unchanged(); err != nil {
		return err
	}
	if err := numericLiteralsCheckRows(held, []string{"id", "b"}, []string{"INT4", "INT8"}, endpoints); err != nil {
		return err
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		return err
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		return err
	}
	if err := query("SELECT id,b FROM endpoints ORDER BY id", false, []string{"id", "b"}, []string{"INT4", "INT8"}, endpoints); err != nil {
		return err
	}
	if err := unchanged(); err != nil {
		return err
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = db.ExecContext(cancelled, "INSERT INTO sentinel VALUES (0xa)")
	if !errors.Is(err, context.Canceled) {
		return fmt.Errorf("canceled feature Exec: %v", err)
	}
	return unchanged()
}
