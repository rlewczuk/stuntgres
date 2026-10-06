package stuntgres_test

import (
	"context"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestUpdateCoreRecursivePreflightRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, table := range []string{"empty", "rejected"} {
		execSQL(t, db, "CREATE TABLE "+table+"(a INT,f BOOL,n BIGINT,s TEXT)")
	}
	execSQL(t, db, "INSERT INTO rejected VALUES(1,TRUE,2147483648,'oops')")
	cases := []struct {
		name, clause  string
		bad, good     any
		parameter     bool
		code, message string
	}{
		{name: "where", clause: "SET a=1 WHERE a=CAST(2147483648 AS INTEGER)"},
		{name: "where_rejected_comparison", clause: "SET a=1 WHERE f IS FALSE AND a=CAST(2147483648 AS INTEGER)"},
		{name: "where_rejected_null", clause: "SET a=1 WHERE f IS FALSE AND CAST(2147483648 AS INTEGER) IS NULL"},
		{name: "where_rejected_truth", clause: "SET a=1 WHERE f IS FALSE AND bool(CAST(2147483648 AS INTEGER)) IS TRUE"},
		{name: "where_rejected_distinct", clause: "SET a=1 WHERE f IS FALSE AND a IS DISTINCT FROM CAST(2147483648 AS INTEGER)"},
		{name: "frame_where_rejected", clause: "SET a=1 WHERE f IS FALSE AND a=CAST($1::BIGINT AS INTEGER)", bad: int64(2147483648), good: int64(7), parameter: true},
		{name: "rhs_comparison", clause: "SET f=a=CAST(2147483648 AS INTEGER)"},
		{name: "rhs_second_assignment", clause: "SET a=1,f=a=CAST(2147483648 AS INTEGER)"},
		{name: "rhs_nested_cast", clause: "SET f=a=CAST(CAST(2147483648 AS BIGINT) AS INTEGER)"},
		{name: "rhs_logical", clause: "SET f=(a=CAST(2147483648 AS INTEGER)) AND TRUE"},
		{name: "rhs_null", clause: "SET f=CAST(2147483648 AS INTEGER) IS NULL"},
		{name: "rhs_truth", clause: "SET f=bool(CAST(2147483648 AS INTEGER)) IS TRUE"},
		{name: "rhs_distinct", clause: "SET f=a IS DISTINCT FROM CAST(2147483648 AS INTEGER)"},
		{name: "rhs_null_count", clause: "SET a=num_nulls(CAST(2147483648 AS INTEGER))"},
		{name: "frame_rhs", clause: "SET a=CAST($1::BIGINT AS INTEGER)", bad: int64(2147483648), good: int64(7), parameter: true},
		{name: "frame_where", clause: "SET a=1 WHERE a=CAST($1::BIGINT AS INTEGER)", bad: int64(2147483648), good: int64(7), parameter: true},
		{name: "frame_text", clause: "SET f=int4($1::TEXT) IS NOT NULL", bad: "oops", good: "7", parameter: true, code: "22P02", message: `invalid input syntax for type integer: "oops"`},
	}
	for _, table := range []string{"empty", "rejected"} {
		for _, tc := range cases {
			t.Run(table+"/"+tc.name, func(t *testing.T) {
				q := "UPDATE " + table + " " + tc.clause
				if table == "rejected" && !strings.Contains(tc.clause, " WHERE ") {
					q += " WHERE FALSE"
				}
				want := stuntgres.Error{Code: "22003", Message: "integer out of range"}
				if tc.code != "" {
					want.Code, want.Message = tc.code, tc.message
				}
				args := []any{}
				if tc.parameter {
					args = []any{tc.bad}
				}
				t.Run("direct", func(t *testing.T) {
					r, err := db.Exec(q, args...)
					if r != nil {
						t.Fatal("conversion exposed successful result", r, err)
					}
					typeCoreDiagnostic(t, err, want)
				})
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal("execution conversion failed Prepare", err)
				}
				defer stmt.Close()
				frames := [][]any{args}
				if tc.parameter {
					frames = [][]any{{tc.bad}, {tc.good}, {nil}, {tc.bad}, {tc.good}}
				}
				for i, frame := range frames {
					t.Run(fmt.Sprintf("retained_frame_%d", i), func(t *testing.T) {
						r, err := stmt.Exec(frame...)
						bad := !tc.parameter || i == 0 || i == 3
						if bad {
							if r != nil {
								t.Fatal("conversion exposed successful result", r, err)
							}
							typeCoreDiagnostic(t, err, want)
						} else {
							if err != nil {
								t.Fatal(err)
							}
							if n, err := r.RowsAffected(); err != nil || n != 0 {
								t.Fatal(n, err)
							}
						}
					})
				}
			})
		}
	}
	// UPDATE rejects the row before evaluating RHS conversions. Logical children
	// themselves retain the existing eager evaluation contract, independently
	// from PostgreSQL's simplification of constant FALSE predicates.
	for _, table := range []string{"empty", "rejected"} {
		for _, clause := range []string{"SET a=CAST(n AS INTEGER) WHERE FALSE", "SET f=int4(s) IS NULL WHERE FALSE", "SET a=1 WHERE FALSE AND CAST(n AS INTEGER)=0"} {
			q := "UPDATE " + table + " " + clause
			for _, prepared := range []bool{false, true} {
				var err error
				if prepared {
					stmt, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					r, e := stmt.Exec()
					err = e
					stmt.Close()
					if err == nil {
						if n, e := r.RowsAffected(); e != nil || n != 0 {
							t.Fatal(n, e)
						}
					}
				} else {
					r, e := db.Exec(q)
					err = e
					if err == nil {
						if n, e := r.RowsAffected(); e != nil || n != 0 {
							t.Fatal(n, e)
						}
					}
				}
				if table == "rejected" && strings.Contains(clause, "FALSE AND") {
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "integer out of range"})
				} else if err != nil {
					t.Fatal("row-dependent RHS evaluated rejected row", q, err)
				}
			}
		}
	}
	wantTuples(t, db, "SELECT a,f,n,s FROM rejected", [][]any{{int64(1), true, int64(2147483648), "oops"}})
	t.Run("acl_precedence", func(t *testing.T) {
		execSQL(t, db, "CREATE ROLE update_preflight_reader")
		c, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		if _, err = c.ExecContext(context.Background(), "SET SESSION AUTHORIZATION update_preflight_reader"); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			clause    string
			arg       any
			parameter bool
			want      stuntgres.Error
		}{
			{clause: "SET a=1 WHERE a=CAST(2147483648 AS INTEGER)", want: stuntgres.Error{Code: "22003", Message: "integer out of range"}},
			{clause: "SET f=a=CAST(2147483648 AS INTEGER)", want: stuntgres.Error{Code: "22003", Message: "integer out of range"}},
			{clause: "SET a=CAST($1::BIGINT AS INTEGER)", arg: int64(2147483648), parameter: true, want: stuntgres.Error{Code: "22003", Message: "integer out of range"}},
			{clause: "SET a=CAST($1::BIGINT AS INTEGER)", arg: int64(7), parameter: true, want: stuntgres.Error{Code: "42501", Message: "permission denied for table rejected"}},
			{clause: "SET a=CAST(n AS INTEGER)", want: stuntgres.Error{Code: "42501", Message: "permission denied for table rejected"}},
		} {
			q := "UPDATE rejected " + tc.clause
			args := []any{}
			if tc.parameter {
				args = []any{tc.arg}
			}
			for _, prepared := range []bool{false, true} {
				t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared]+tc.want.Code, func(t *testing.T) {
					var err error
					if prepared {
						stmt, e := c.PrepareContext(context.Background(), q)
						if e != nil {
							t.Fatal(e)
						}
						_, err = stmt.Exec(args...)
						stmt.Close()
					} else {
						_, err = c.ExecContext(context.Background(), q, args...)
					}
					typeCoreDiagnostic(t, err, tc.want)
				})
			}
		}
	})
}
