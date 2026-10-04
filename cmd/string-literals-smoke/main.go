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
	_, err = stmt.ExecContext(ctx, 8, `a\nb`)
	stmt.Close()
	if err != nil {
		return err
	}
	want = append(want, []any{int64(8), `a\nb`, true})
	if _, err = first.ExecContext(ctx, `INSERT INTO t VALUES (E'\061\060',E'',E'\164')`); err != nil {
		return err
	}
	want = append(want, []any{int64(10), "", true})
	verify := func(db *sql.DB) error {
		rows, e := db.QueryContext(ctx, "SELECT * FROM t ORDER BY id")
		if e != nil {
			return e
		}
		defer rows.Close()
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
	check := func(q string, want stuntgres.Error) error {
		for _, prepared := range []bool{false, true} {
			var e error
			if prepared {
				s, err := first.PrepareContext(ctx, q)
				e = err
				if s != nil {
					s.Close()
					return fmt.Errorf("invalid SQL prepared: %q", q)
				}
			} else {
				_, e = first.ExecContext(ctx, q)
			}
			var got *stuntgres.Error
			if !errors.As(e, &got) || *got != want || got.SQLState() != want.Code {
				return fmt.Errorf("%q prepared=%v: %#v %v; want %#v", q, prepared, got, e, want)
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
	for _, line := range []string{
		"strings: direct/prepared escape round trips",
		"strings: diagnostics and positions",
		"strings: statement atomicity",
		"strings: dump/reset/restore shared pools",
		"strings: raw SQL boundary preserved",
	} {
		fmt.Println(line)
	}
	return nil
}
