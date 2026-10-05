package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"

	"stuntgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("string-literals-smoke")
	if err != nil {
		return err
	}
	first, second := sql.OpenDB(connector), sql.OpenDB(connector)
	defer first.Close()
	defer second.Close()
	if err := second.PingContext(ctx); err != nil {
		return err
	}
	if _, err := first.ExecContext(ctx, "CREATE TABLE t(id INT,label TEXT,flag BOOL)"); err != nil {
		return err
	}
	want := [][]any{}
	for _, tc := range []struct {
		source, text string
		prepared     bool
	}{
		{`E'a\nb'`, "a\nb", false},
		{`E'O\'Brien\\tail'`, "O'Brien\\tail", true},
		{`E'\303\251'`, "é", false},
		{`E'\xC3\xA9'`, "é", true},
		{`E'\uD83D\uDE00'`, "😀", true},
		{"E'\\xC3'\n'\\xA9'", "é", false},
		{"E'a'\n'\\n'", "a\n", true},
		{`$$a;'b$$`, "a;'b", false},
		{"$T$first\nsecond; O'Brien\\tail $1 $other$x$other$$T$", "first\nsecond; O'Brien\\tail $1 $other$x$other$", true},
		{`$tag$a$junk$tag$`, "a$junk", false},
		{`$$$$`, "", true},
		{`$_9$é😀$_9$`, "é😀", false},
	} {
		id := len(want) + 1
		q := fmt.Sprintf("INSERT INTO t VALUES (%d,%s,TRUE)", id, tc.source)
		var result sql.Result
		if tc.prepared {
			s, e := first.PrepareContext(ctx, q)
			if e != nil {
				return e
			}
			result, err = s.ExecContext(ctx)
			s.Close()
		} else {
			result, err = first.ExecContext(ctx, q)
		}
		if err != nil {
			return err
		}
		if n, e := result.RowsAffected(); e != nil || n != 1 {
			return fmt.Errorf("affected %d: %v", n, e)
		}
		want = append(want, []any{int64(id), tc.text, true})
	}
	stmt, err := first.PrepareContext(ctx, `INSERT INTO t VALUES ($1,$2,E'\164')`)
	if err != nil {
		return err
	}
	id := len(want) + 1
	_, err = stmt.ExecContext(ctx, id, `a\nb`)
	stmt.Close()
	if err != nil {
		return err
	}
	want = append(want, []any{int64(id), `a\nb`, true})
	id = len(want) + 1
	if _, err = first.ExecContext(ctx, fmt.Sprintf(`INSERT INTO t VALUES (%d,E'',E'\164')`, id)); err != nil {
		return err
	}
	want = append(want, []any{int64(id), "", true})
	for _, prepared := range []bool{false, true} {
		id = len(want) + 1
		q := `INSERT INTO t VALUES ($1,$tag$$2;$tag$,TRUE)`
		var result sql.Result
		if prepared {
			s, e := first.PrepareContext(ctx, q)
			if e != nil {
				return e
			}
			result, err = s.ExecContext(ctx, id)
			s.Close()
		} else {
			result, err = first.ExecContext(ctx, q, id)
		}
		if err != nil {
			return err
		}
		if n, e := result.RowsAffected(); e != nil || n != 1 {
			return fmt.Errorf("parameter affected %d: %v", n, e)
		}
		want = append(want, []any{int64(id), "$2;", true})
	}
	bodyOnly, err := first.PrepareContext(ctx, `SELECT id FROM t WHERE label=$$ $1 $$ ORDER BY id`)
	if err != nil {
		return err
	}
	bodyRows, err := bodyOnly.QueryContext(ctx)
	if err != nil {
		bodyOnly.Close()
		return err
	}
	if bodyRows.Next() || bodyRows.Err() != nil {
		bodyRows.Close()
		bodyOnly.Close()
		return fmt.Errorf("body-only parameter query returned rows/error")
	}
	bodyRows.Close()
	bodyOnly.Close()
	verifyRows := func(rows *sql.Rows) error {
		defer rows.Close()
		columns, e := rows.Columns()
		if e != nil || !reflect.DeepEqual(columns, []string{"id", "label", "flag"}) {
			return fmt.Errorf("columns %v: %v", columns, e)
		}
		types, e := rows.ColumnTypes()
		if e != nil {
			return e
		}
		if len(types) != 3 {
			return fmt.Errorf("metadata width %d", len(types))
		}
		for i, name := range []string{"INT4", "TEXT", "BOOL"} {
			if types[i].DatabaseTypeName() != name {
				return fmt.Errorf("type %d: %s", i, types[i].DatabaseTypeName())
			}
		}
		got := [][]any{}
		for rows.Next() {
			var id int64
			var text string
			var flag bool
			if e := rows.Scan(&id, &text, &flag); e != nil {
				return e
			}
			got = append(got, []any{id, text, flag})
		}
		if e := rows.Err(); e != nil {
			return e
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("rows %#v, want %#v", got, want)
		}
		return nil
	}
	verify := func(db *sql.DB) error {
		rows, e := db.QueryContext(ctx, "SELECT * FROM t ORDER BY id")
		if e != nil {
			return e
		}
		return verifyRows(rows)
	}
	check := func(q string, want stuntgres.Error) error {
		for _, operation := range []string{"exec", "prepare", "query"} {
			var e error
			if operation == "prepare" {
				s, err := first.PrepareContext(ctx, q)
				e = err
				if s != nil {
					s.Close()
					return fmt.Errorf("invalid SQL prepared: %q", q)
				}
			} else if operation == "query" {
				r, err := first.QueryContext(ctx, q)
				e = err
				if r != nil {
					r.Close()
					return fmt.Errorf("invalid SQL yielded cursor: %q", q)
				}
			} else {
				result, err := first.ExecContext(ctx, q)
				e = err
				if result != nil {
					return fmt.Errorf("invalid SQL yielded result: %q", q)
				}
			}
			var got *stuntgres.Error
			if !errors.As(e, &got) || *got != want || got.SQLState() != want.Code {
				return fmt.Errorf("%q operation=%s: %#v %v; want %#v", q, operation, got, e, want)
			}
		}
		return nil
	}
	for _, tc := range []struct{ q, code, message, position, hint string }{
		{`INSERT INTO t VALUES (11,'valid',TRUE),(12,E'\0',TRUE)`, "22021", `invalid byte sequence for encoding "UTF8": 0x00`, "", ""},
		{`INSERT INTO t VALUES (11,'valid',TRUE),(12,E'\xff',TRUE)`, "22021", `invalid byte sequence for encoding "UTF8": 0xff`, "", ""},
		{`/*é*/ INSERT INTO t VALUES (E'\u061')`, "22025", "invalid Unicode escape", "31", `Unicode escapes must be \uXXXX or \UXXXXXXXX.`},
		{`INSERT INTO t VALUES (E'abc\`, "42601", `unterminated quoted string at or near "E'abc\"`, "23", ""},
		{"INSERT INTO t VALUES (E'\xff')", "42601", "invalid UTF-8 in SQL", "25", ""},
		{"INSERT INTO t VALUES (E'\x00')", "42601", "NUL in SQL", "25", ""},
		{"INSERT INTO t VALUES ($$a)", "42601", `unterminated dollar-quoted string at or near "$$a)"`, "23", ""},
		{"/*é*/ INSERT INTO t VALUES ($Tag$a$tag$)", "42601", `unterminated dollar-quoted string at or near "$Tag$a$tag$)"`, "29", ""},
		{"INSERT INTO t VALUES ($tag)", "42601", `syntax error at or near "$"`, "23", ""},
		{"INSERT INTO t VALUES ($tag$first\nsecond)", "42601", "unterminated dollar-quoted string at or near \"$tag$first\nsecond)\"", "23", ""},
		{"INSERT INTO t VALUES (99,'valid',TRUE),(100,$Tag$a$tag$)", "42601", `unterminated dollar-quoted string at or near "$Tag$a$tag$)"`, "45", ""},
	} {
		wantErr := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint}
		if err := check(tc.q, wantErr); err != nil {
			return err
		}
		if err := verify(first); err != nil {
			return err
		}
		if err := verify(second); err != nil {
			return err
		}
	}
	held, err := second.QueryContext(ctx, "SELECT * FROM t ORDER BY id")
	if err != nil {
		return err
	}
	defer held.Close()
	for _, suffix := range []string{"$Tag$a$tag$);", "$$a"} {
		script := "CREATE TABLE replacement(label TEXT); INSERT INTO replacement VALUES (" + suffix
		err := stuntgres.Restore(ctx, first, bytes.NewBufferString(script))
		wantErr := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "unterminated dollar-quoted string at or near \"" + suffix + "\"", Position: "71"}
		var got *stuntgres.Error
		if !errors.As(err, &got) || *got != wantErr || got.SQLState() != "42601" {
			return fmt.Errorf("Restore: %#v %v; want %#v", got, err, wantErr)
		}
		if err := verify(second); err != nil {
			return err
		}
		r, err := first.QueryContext(ctx, "SELECT * FROM replacement")
		if r != nil {
			r.Close()
			return fmt.Errorf("failed Restore published replacement")
		}
		if !errors.As(err, &got) || got.Code != "42P01" {
			return fmt.Errorf("replacement lookup: %v", err)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, first, &dump); err != nil {
		return err
	}
	if err := stuntgres.Reset(ctx, first); err != nil {
		return err
	}
	if err := stuntgres.Restore(ctx, first, bytes.NewReader(dump.Bytes())); err != nil {
		return err
	}
	if err := verify(first); err != nil {
		return err
	}
	if err := verify(second); err != nil {
		return err
	}
	if err := verifyRows(held); err != nil {
		return err
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(ctx, second, &again); err != nil {
		return err
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		return fmt.Errorf("dump changed after Restore")
	}
	if err := stuntgres.Restore(ctx, first, bytes.NewBufferString("CREATE TABLE restored(id INT,label TEXT); INSERT INTO restored VALUES (1,$$a;'b$$),(2,$T$first\nsecond; $other$x$other$$T$);")); err != nil {
		return err
	}
	for _, pool := range []*sql.DB{first, second} {
		rows, err := pool.QueryContext(ctx, "SELECT * FROM restored ORDER BY id")
		if err != nil {
			return err
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
			rows.Close()
			return fmt.Errorf("Restore metadata: %v", err)
		}
		for i, text := range []string{"a;'b", "first\nsecond; $other$x$other$"} {
			var id int64
			var got string
			if !rows.Next() {
				rows.Close()
				return fmt.Errorf("missing restored row")
			}
			if err := rows.Scan(&id, &got); err != nil || id != int64(i+1) || got != text {
				rows.Close()
				return fmt.Errorf("restored row %d %q: %v", id, got, err)
			}
		}
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			return fmt.Errorf("restored extra rows/error")
		}
		rows.Close()
	}
	for _, line := range []string{
		"strings: direct/prepared escape round trips",
		"strings: diagnostics and positions",
		"strings: statement atomicity",
		"strings: dump/reset/restore shared pools",
		"strings: raw SQL boundary preserved",
		"strings: dollar quotes, tags and parameter separation",
	} {
		fmt.Println(line)
	}
	return nil
}
