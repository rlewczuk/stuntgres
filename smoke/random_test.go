package smoke_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
)

func TestRandomSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector("random-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	one := func(c *sql.Conn, q string, args ...any) any {
		t.Helper()
		var v any
		if err := c.QueryRowContext(ctx, q, args...).Scan(&v); err != nil {
			t.Fatal(q, err)
		}
		return v
	}
	seed := func(c *sql.Conn) {
		t.Helper()
		if v := one(c, "SELECT setseed(0.25)"); v != "" {
			t.Fatal("nonnull VOID", v)
		}
	}
	for _, tc := range []struct {
		q, typ string
		want   any
	}{
		{"SELECT random()", "FLOAT8", nil},
		{"SELECT random(7,7)", "INT4", int64(7)},
		{"SELECT random(2147483648,2147483648)", "INT8", int64(2147483648)},
		{"SELECT random(1.0,1.000)", "NUMERIC", "1.000"},
		{"SELECT random_normal(5,0)", "FLOAT8", float64(5)},
		{"SELECT setseed(0)", "VOID", ""},
		{"SELECT setseed(NULL)", "VOID", nil},
	} {
		s, err := a.PrepareContext(ctx, tc.q)
		if err != nil {
			t.Fatal(tc.q, err)
		}
		rows, err := s.Query()
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != tc.typ {
			t.Fatal(types, err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var v any
		if err = rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		if tc.q == "SELECT random()" {
			x, ok := v.(float64)
			if !ok || !(x >= 0 && x < 1) {
				t.Fatal(v)
			}
		} else if !reflect.DeepEqual(v, tc.want) {
			t.Fatal(v, tc.want)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		rows.Close()
		s.Close()
	}
	seed(a)
	first := one(a, "SELECT random()")
	second := one(a, "SELECT random()")
	seed(a)
	seed(b)
	if one(a, "SELECT random()") != first || one(b, "SELECT random()") != first {
		t.Fatal("seed repeat/isolation")
	}
	seed(a)
	if one(b, "SELECT random()") != second {
		t.Fatal("seed leaked")
	}
	seed(a)
	implicit := one(a, "SELECT random_normal()")
	seed(a)
	if one(a, "SELECT random_normal(0,1)") != implicit {
		t.Fatal("defaults")
	}
	seed(a)
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var d float64
	if err = tx.QueryRow("SELECT random()").Scan(&d); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if one(a, "SELECT random()") != second {
		t.Fatal("rollback rewound PRNG")
	}
	_, err = a.ExecContext(ctx, "SELECT setseed(1.23456789)")
	var got *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "setseed parameter 1.23457 is out of allowed range [-1,1]"}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic %#v (%v)", got, err)
	}
	result, err := a.ExecContext(ctx, "SELECT setseed(0)")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for range 8 {
		if n := one(a, "SELECT random(-7,3)").(int64); n < -7 || n > 3 {
			t.Fatal(n)
		}
		if n := one(a, "SELECT random(2147483648,2147483650)").(int64); n < 2147483648 || n > 2147483650 {
			t.Fatal(n)
		}
	}
	if one(a, "SELECT random_normal(NULL,1)") != nil {
		t.Fatal("strict NULL")
	}
	// Hold exactly one physical connection when checking borrower reuse.
	db.SetMaxIdleConns(0)
	b.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	identity := func(c *sql.Conn) any {
		t.Helper()
		var id any
		if e := c.Raw(func(raw any) error { id = raw; return nil }); e != nil {
			t.Fatal(e)
		}
		return id
	}
	before := identity(a)
	seed(a)
	one(a, "SELECT random()")
	a.Close()
	reused, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reused.Close()
	if identity(reused) != before {
		t.Fatal("pool replaced seeded physical connection")
	}
	if one(reused, "SELECT random()") != second {
		t.Fatal("pool return reset seeded stream")
	}
}
