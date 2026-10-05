package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
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
func exec(ctx context.Context, c *sql.Conn, q string, prepared bool) error {
	var r sql.Result
	var err error
	if prepared {
		var s *sql.Stmt
		s, err = c.PrepareContext(ctx, q)
		if err != nil {
			return err
		}
		defer s.Close()
		r, err = s.ExecContext(ctx)
	} else {
		r, err = c.ExecContext(ctx, q)
	}
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf("%s affected=%d, want 0", q, n)
	}
	return nil
}
func show(ctx context.Context, c *sql.Conn, want string, prepared bool) error {
	if err := exec(ctx, c, "SHOW application_name", prepared); err != nil {
		return err
	}
	var rows *sql.Rows
	var err error
	if prepared {
		var s *sql.Stmt
		s, err = c.PrepareContext(ctx, "SHOW application_name")
		if err != nil {
			return err
		}
		defer s.Close()
		rows, err = s.QueryContext(ctx)
	} else {
		rows, err = c.QueryContext(ctx, "SHOW application_name")
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(cols, []string{"application_name"}) || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" {
		return fmt.Errorf("SHOW metadata %v %v", cols, types)
	}
	if !rows.Next() {
		return fmt.Errorf("SHOW missing row: %v", rows.Err())
	}
	var value any
	if err = rows.Scan(&value); err != nil {
		return err
	}
	if value != want {
		return fmt.Errorf("SHOW got %#v want %q", value, want)
	}
	if rows.Next() {
		return errors.New("SHOW extra row")
	}
	return rows.Err()
}
func sentinel(ctx context.Context, c *sql.Conn) error {
	var v int64
	if err := c.QueryRowContext(ctx, "SELECT v FROM sentinel").Scan(&v); err != nil {
		return err
	}
	if v != 7 {
		return fmt.Errorf("sentinel=%d", v)
	}
	return nil
}
func identity(c *sql.Conn) (any, error) {
	var object any
	err := c.Raw(func(raw any) error { object = raw; return nil })
	return object, err
}
func diagnostic(err error, code, message, position string) error {
	var got *stuntgres.Error
	if !errors.As(err, &got) {
		return fmt.Errorf("missing diagnostic: %v", err)
	}
	want := &stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("diagnostic got %#v want %#v", got, want)
	}
	return nil
}
func run() error {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("session-smoke")
	if err != nil {
		return err
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	a, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer b.Close()
	if err = show(ctx, a, "", false); err != nil {
		return err
	}
	if err = show(ctx, b, "", true); err != nil {
		return err
	}
	if _, err = a.ExecContext(ctx, "CREATE TABLE sentinel(v INT)"); err != nil {
		return err
	}
	if _, err = a.ExecContext(ctx, "INSERT INTO sentinel VALUES(7)"); err != nil {
		return err
	}
	if err = exec(ctx, a, "SET SESSION application_name TO 'session-a'", false); err != nil {
		return err
	}
	if err = exec(ctx, b, "SET application_name = 'session-b'", true); err != nil {
		return err
	}
	if err = show(ctx, a, "session-a", true); err != nil {
		return err
	}
	if err = show(ctx, b, "session-b", false); err != nil {
		return err
	}
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ input, want string }{{"+0002147483647", "2147483647"}, {"+0002147483648", "0002147483648"}, {"-0002147483648", "-0002147483648"}, {`E'a\nb'`, `a\x0ab`}} {
			if err = exec(ctx, a, "SET application_name TO "+tc.input, prepared); err != nil {
				return err
			}
			if err = show(ctx, a, tc.want, prepared); err != nil {
				return err
			}
			if err = show(ctx, b, "session-b", prepared); err != nil {
				return err
			}
		}
	}
	if err = exec(ctx, a, "SET application_name TO 'session-a'", false); err != nil {
		return err
	}
	fmt.Println("session isolation: session-a session-b")
	fmt.Println("prepared settings: TEXT affected=0")
	pool := sql.OpenDB(connector)
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	defer pool.Close()
	p, err := pool.Conn(ctx)
	if err != nil {
		return err
	}
	old, err := identity(p)
	if err != nil {
		return err
	}
	if err = exec(ctx, p, "SET application_name TO pooled", false); err != nil {
		return err
	}
	if err = p.Close(); err != nil {
		return err
	}
	p, err = pool.Conn(ctx)
	if err != nil {
		return err
	}
	same, err := identity(p)
	if err != nil {
		return err
	}
	if old != same {
		return errors.New("reuse replaced physical identity")
	}
	if err = show(ctx, p, "pooled", true); err != nil {
		return err
	}
	fmt.Println("pool reuse: preserved")
	if err = p.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		return fmt.Errorf("invalidation: %v", err)
	}
	_ = p.Close()
	p, err = pool.Conn(ctx)
	if err != nil {
		return err
	}
	fresh, err := identity(p)
	if err != nil {
		return err
	}
	if fresh == old {
		return errors.New("replacement retained identity")
	}
	if err = show(ctx, p, "", false); err != nil {
		return err
	}
	if err = sentinel(ctx, p); err != nil {
		return err
	}
	if err = show(ctx, b, "session-b", true); err != nil {
		return err
	}
	fmt.Println("physical replacement: default restored, data=7")
	for _, tc := range []struct{ query, code, message, position string }{
		{"SET absent_setting TO x", "42704", `unrecognized configuration parameter "absent_setting"`, ""},
		{"SHOW absent_setting", "42704", `unrecognized configuration parameter "absent_setting"`, ""},
		{"SET application_name TO 'a','b'", "22023", "SET application_name takes only one argument", ""},
		{"SET absent_setting TO 'a','b'", "22023", "SET absent_setting takes only one argument", ""},
		{`SET "APPLICATION_NAME" TO 'a','b'`, "22023", "SET APPLICATION_NAME takes only one argument", ""},
		{"SET application_name TO NULL", "42601", `syntax error at or near "NULL"`, "25"},
		{"SET application_name TO $1", "42601", `syntax error at or near "$1"`, "25"},
		{"SET application_name TO TO", "42601", `syntax error at or near "TO"`, "25"},
		{"CREATE TABLE invalid_to(to BOOL)", "42601", `syntax error at or near "to"`, "25"},
	} {
		_, failure := a.ExecContext(ctx, tc.query)
		if err = diagnostic(failure, tc.code, tc.message, tc.position); err != nil {
			return err
		}
		if err = show(ctx, a, "session-a", false); err != nil {
			return err
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = a.ExecContext(canceled, "SET application_name TO lost"); !errors.Is(err, context.Canceled) {
		return fmt.Errorf("cancellation: %v", err)
	}
	if err = show(ctx, a, "session-a", true); err != nil {
		return err
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		return err
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		return err
	}
	if err = show(ctx, a, "session-a", false); err != nil {
		return err
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		return err
	}
	if err = show(ctx, b, "session-b", true); err != nil {
		return err
	}
	if err = sentinel(ctx, b); err != nil {
		return err
	}
	if err = p.Close(); err != nil {
		return err
	}
	if err = pool.Close(); err != nil {
		return err
	}
	if err = a.Close(); err != nil {
		return err
	}
	if err = b.Close(); err != nil {
		return err
	}
	if err = db.Close(); err != nil {
		return err
	}
	reopened := sql.OpenDB(connector)
	defer reopened.Close()
	p, err = reopened.Conn(ctx)
	if err != nil {
		return err
	}
	defer p.Close()
	if err = sentinel(ctx, p); err != nil {
		return err
	}
	if err = show(ctx, p, "", false); err != nil {
		return err
	}
	fmt.Println("pool reopen: data=7")
	fmt.Println("session failures: 42704 22023 42601")
	fmt.Println("session cancellation and controls: preserved")
	return nil
}
