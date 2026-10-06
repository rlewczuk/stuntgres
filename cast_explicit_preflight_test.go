package stuntgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
)

// These expectations describe conversion after successful source-type import,
// not parameter input failures. In particular Context and Position stay empty.
func TestCastExplicitParameterPreflight(t *testing.T) {
	_, db := castExplicitSetup(t)
	execSQL(t, db, "CREATE TABLE e(i INT4)")
	execSQL(t, db, "CREATE TABLE populated(i INT4)")
	execSQL(t, db, "INSERT INTO populated VALUES(1)")
	for _, tc := range []struct {
		name, expression string
		bad, good, again any
		want             stuntgres.Error
	}{
		{"text", "int4($1::text)", "oops", "42", "7", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"narrow", "CAST($1::int8 AS int4)", int64(2147483648), int64(42), int64(7), stuntgres.Error{Code: "22003", Message: "integer out of range"}},
	} {
		for _, source := range []struct {
			name, from string
			count      int
		}{
			{"empty", "e", 0}, {"filtered", "populated WHERE i=0", 0}, {"populated", "populated", 1},
		} {
			t.Run(tc.name+"/"+source.name, func(t *testing.T) {
				q := "SELECT " + tc.expression + " FROM " + source.from
				t.Run("direct", func(t *testing.T) {
					rows, err := db.Query(q, tc.bad)
					if rows != nil {
						rows.Close()
						t.Fatal("failed conversion exposed cursor")
					}
					typeCoreDiagnostic(t, err, tc.want)
				})
				t.Run("fresh-prepared-and-private-frames", func(t *testing.T) {
					stmt, err := db.Prepare(q)
					if err != nil {
						t.Fatalf("conversion must not fail Prepare: %v", err)
					}
					defer stmt.Close()
					for index, arg := range []any{tc.bad, tc.good, nil, tc.bad, tc.again} {
						t.Run(fmt.Sprint(index), func(t *testing.T) {
							rows, err := stmt.Query(arg)
							if index == 0 || index == 3 {
								if rows != nil {
									rows.Close()
									t.Fatal("failed conversion exposed cursor")
								}
								typeCoreDiagnostic(t, err, tc.want)
								return
							}
							if err != nil {
								t.Fatal(err)
							}
							defer rows.Close()
							castExplicitMetadata(t, rows, []string{"int4"}, []string{"INT4"})
							count := 0
							for rows.Next() {
								var got any
								if err := rows.Scan(&got); err != nil {
									t.Fatal(err)
								}
								var want any
								if index == 1 {
									want = int64(42)
								}
								if index == 4 {
									want = int64(7)
								}
								if got != want {
									t.Fatalf("frame value %v want %v", got, want)
								}
								count++
							}
							if rows.Err() != nil || count != source.count {
								t.Fatal(count, source.count, rows.Err())
							}
						})
					}
				})
			})
		}
		// The independent RHS conversion also precedes an empty source scan.
		q := "SELECT i FROM e WHERE i=" + tc.expression
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+"/rhs/"+fmt.Sprint(prepared), func(t *testing.T) {
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, prepareErr := db.Prepare(q)
					if prepareErr != nil {
						t.Fatal("Prepare", prepareErr)
					}
					defer stmt.Close()
					rows, err = stmt.Query(tc.bad)
				} else {
					rows, err = db.Query(q, tc.bad)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("failed RHS conversion exposed cursor")
				}
				typeCoreDiagnostic(t, err, tc.want)
			})
		}
	}
}

func TestCastExplicitSelectACLPreflight(t *testing.T) {
	_, db := castExplicitSetup(t)
	ctx := context.Background()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
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
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, prepareErr := c.PrepareContext(ctx, q)
						if private {
							if stmt != nil {
								stmt.Close()
								t.Fatal("namespace denial exposed statement")
							}
							typeCoreDiagnostic(t, prepareErr, want)
							return
						}
						if prepareErr != nil {
							t.Fatal("expected successful Prepare", prepareErr)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(ctx)
					} else {
						rows, err = c.QueryContext(ctx, q)
					}
					if rows != nil {
						rows.Close()
						t.Fatal("denied query exposed cursor")
					}
					typeCoreDiagnostic(t, err, want)
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
