package smoke_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
)

func TestCastExplicitPreflightSmoke(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	ctx := context.Background()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{"CREATE TABLE e(i INT4)", "CREATE TABLE populated(i INT4)", "INSERT INTO populated VALUES(1)"} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	failure := func(t *testing.T, rows *sql.Rows, err error, want stuntgres.Error) {
		t.Helper()
		if rows != nil {
			rows.Close()
			t.Fatal("failure exposed cursor")
		}
		if err := typeCoreDiagnostic(err, want); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, expression string
		bad, good        any
		want             stuntgres.Error
	}{
		{"text", "int4($1::text)", "oops", "42", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"narrow", "CAST($1::int8 AS int4)", int64(2147483648), int64(42), stuntgres.Error{Code: "22003", Message: "integer out of range"}},
	} {
		for _, from := range []string{"e", "populated WHERE i=0", "populated"} {
			t.Run(tc.name+"/"+from, func(t *testing.T) {
				q := "SELECT " + tc.expression + " FROM " + from
				t.Run("direct", func(t *testing.T) {
					rows, err := c.QueryContext(ctx, q, tc.bad)
					failure(t, rows, err, tc.want)
				})
				stmt, err := c.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal("expected successful Prepare", err)
				}
				defer stmt.Close()
				for index, arg := range []any{tc.bad, tc.good, nil, tc.bad, tc.good} {
					t.Run("prepared/"+fmt.Sprint(index), func(t *testing.T) {
						rows, err := stmt.QueryContext(ctx, arg)
						if index == 0 || index == 3 {
							failure(t, rows, err, tc.want)
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						count := 0
						for rows.Next() {
							var got any
							if err := rows.Scan(&got); err != nil {
								t.Fatal(err)
							}
							var want any
							if arg != nil {
								want = int64(42)
							}
							if got != want {
								t.Fatal("stale parameter frame", got, want)
							}
							count++
						}
						wantCount := 0
						if from == "populated" {
							wantCount = 1
						}
						if rows.Err() != nil || count != wantCount {
							t.Fatal(count, wantCount, rows.Err())
						}
					})
				}
			})
		}
	}
	for _, q := range []string{"CREATE ROLE cast_reader", "CREATE TABLE secret(i INT4,s TEXT,n INT8)", "INSERT INTO secret VALUES(7,'oops',2147483648)", "CREATE SCHEMA cast_private", "CREATE TABLE cast_private.secret(i INT4,s TEXT,n INT8)", "SET SESSION AUTHORIZATION cast_reader"} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, tc := range []struct {
		expression string
		want       stuntgres.Error
	}{
		{"2147483648::int4", stuntgres.Error{Code: "22003", Message: "integer out of range"}},
		{"CAST('oops'::text AS int4)", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"CAST('42'::text AS int4)", stuntgres.Error{Code: "42501", Message: "permission denied for table secret"}},
		{"7::int4", stuntgres.Error{Code: "42501", Message: "permission denied for table secret"}},
		{"s::int4", stuntgres.Error{Code: "42501", Message: "permission denied for table secret"}},
		{"CAST(n AS int4)", stuntgres.Error{Code: "42501", Message: "permission denied for table secret"}},
	} {
		for _, private := range []bool{false, true} {
			q := "SELECT " + tc.expression + " FROM public.secret"
			want := tc.want
			if private {
				q = "SELECT " + tc.expression + " FROM cast_private.secret"
				want = stuntgres.Error{Code: "42501", Message: "permission denied for schema cast_private", Position: strconv.Itoa(strings.Index(q, "cast_private") + 1)}
			}
			for _, prepared := range []bool{false, true} {
				t.Run(q+"/prepared="+fmt.Sprint(prepared), func(t *testing.T) {
					if !prepared {
						rows, err := c.QueryContext(ctx, q)
						failure(t, rows, err, want)
						return
					}
					stmt, err := c.PrepareContext(ctx, q)
					if private {
						if stmt != nil {
							stmt.Close()
							t.Fatal("namespace failure exposed statement")
						}
						if err := typeCoreDiagnostic(err, want); err != nil {
							t.Fatal(err)
						}
						return
					}
					if err != nil {
						t.Fatal("expected successful Prepare", err)
					}
					defer stmt.Close()
					rows, err := stmt.QueryContext(ctx)
					failure(t, rows, err, want)
				})
			}
		}
	}
	if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
		t.Fatal(err)
	}
	var sentinel, overflowing int64
	var malformed string
	if err := c.QueryRowContext(ctx, "SELECT i,s,n FROM secret").Scan(&sentinel, &malformed, &overflowing); err != nil || sentinel != 7 || malformed != "oops" || overflowing != 2147483648 {
		t.Fatal(sentinel, malformed, overflowing, err)
	}
}
