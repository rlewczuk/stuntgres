package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
)

func TestQueryAliasesSmoke(t *testing.T) {
	ctx := context.Background()
	open := func(name string) *sql.DB {
		t.Helper()
		connector, err := stuntgres.NewDriver().OpenConnector(name)
		if err != nil {
			t.Fatal(err)
		}
		db := sql.OpenDB(connector)
		t.Cleanup(func() { db.Close() })
		return db
	}
	db := open(t.Name())
	exec := func(db *sql.DB, q string, want int64) {
		t.Helper()
		result, err := db.Exec(q)
		if err != nil {
			t.Fatal(q, err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != want {
			t.Fatal(q, count, want, err)
		}
	}
	exec(db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0)
	exec(db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4)
	for _, tc := range []struct {
		q            string
		names, types []string
		want         [][]any
	}{
		{"SELECT x.iid AS shown,x.a,x.b FROM t AS x(iid) ORDER BY shown", []string{"shown", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(1)}, {int64(2), int64(1), int64(5)}, {int64(3), nil, int64(2)}, {int64(4), int64(2), int64(0)}}},
		{"SELECT x.iid AS shown,x.a,x.b FROM t x(iid) ORDER BY shown", []string{"shown", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(1)}, {int64(2), int64(1), int64(5)}, {int64(3), nil, int64(2)}, {int64(4), int64(2), int64(0)}}},
		{"SELECT a AS b,id FROM t WHERE b=1 ORDER BY b", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}}},
		{"SELECT a AS b,id FROM t ORDER BY b,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(2), int64(4)}, {nil, int64(3)}}},
		{"SELECT a AS b,id FROM t ORDER BY b+0,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(2), int64(1)}, {nil, int64(3)}, {int64(1), int64(2)}}},
	} {
		rows, err := db.Query(tc.q)
		orderSmokeRows(t, rows, err, tc.names, tc.types, tc.want)
		stmt, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.Query()
		orderSmokeRows(t, rows, err, tc.names, tc.types, tc.want)
		stmt.Close()
	}
	q := "SELECT x.iid+$1 AS sum FROM t x(iid) WHERE x.iid=$2 ORDER BY sum"
	held, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, tc := range []struct {
		args []any
		want [][]any
	}{
		{[]any{10, 1}, [][]any{{int64(11)}}},
		{[]any{20, 2}, [][]any{{int64(22)}}},
		{[]any{nil, 2}, [][]any{{nil}}},
		{[]any{10, nil}, [][]any{}},
	} {
		rows, err := held.Query(tc.args...)
		orderSmokeRows(t, rows, err, []string{"sum"}, []string{"INT4"}, tc.want)
	}
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT id FROM t AS x(iid)", stuntgres.Error{Code: "42703", Message: `column "id" does not exist`, Position: "8", Hint: `Perhaps you meant to reference the column "x.iid".`}},
		{"SELECT x.id FROM t AS x(iid)", stuntgres.Error{Code: "42703", Message: "column x.id does not exist", Position: "8", Hint: `Perhaps you meant to reference the column "x.iid".`}},
		{"SELECT /*é*/ x.id FROM t AS x(iid)", stuntgres.Error{Code: "42703", Message: "column x.id does not exist", Position: "14", Hint: `Perhaps you meant to reference the column "x.iid".`}},
		{"SELECT id AS shown, shown+1 FROM t", stuntgres.Error{Code: "42703", Message: `column "shown" does not exist`, Position: "21"}},
		{"SELECT t.id FROM t AS x", stuntgres.Error{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "8", Hint: `Perhaps you meant to reference the table alias "x".`}},
		// F1: exact inaccessible matches precede fuzzy spelling suggestions.
		{"SELECT id FROM (t AS l CROSS JOIN t AS r(other,c,d)) AS j(iid,a,b,other,c,d)", stuntgres.Error{Code: "42703", Message: `column "id" does not exist`, Position: "8", Detail: `There is a column named "id" in table "l", but it cannot be referenced from this part of the query.`}},
		{"SELECT id FROM (t AS l CROSS JOIN t AS r) AS j(iid,a,b,other,c,d)", stuntgres.Error{Code: "42703", Message: `column "id" does not exist`, Position: "8", Detail: `There are columns named "id", but they are in tables that cannot be referenced from this part of the query.`, Hint: `Try using a table-qualified name.`}},
		// F2: hidden within-entry duplicates still raise positioned ambiguity,
		// even after earlier entries establish plural exact matches.
		{"SELECT id FROM (t AS x(id,id) CROSS JOIN t AS v(k,c,d)) AS j(iid,a,b,k,c,d)", stuntgres.Error{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"SELECT id FROM (t CROSS JOIN t AS u CROSS JOIN t AS x(id,id)) AS j(iid,a,b,k,c,d,l,e,f)", stuntgres.Error{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
		{"SELECT j.id FROM (t AS x(id,id) CROSS JOIN t AS v(k,c,d)) AS j(iid,a,b,k,c,d)", stuntgres.Error{Code: "42702", Message: `column reference "id" is ambiguous`, Position: "8"}},
	} {
		want := tc.want
		want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				stmt, prepareErr := db.Prepare(tc.q)
				err = prepareErr
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed Prepare exposed statement")
				}
			} else {
				rows, queryErr := db.Query(tc.q)
				err = queryErr
				if rows != nil {
					rows.Close()
					t.Fatal("failed Query exposed rows")
				}
			}
			var got *stuntgres.Error
			if !errors.As(err, &got) || *got != want {
				t.Fatal(tc.q, got, err, want)
			}
		}
	}
	division := "SELECT 1/(x.iid-2) AS quotient FROM t x(iid) ORDER BY x.iid"
	divisionStmt, err := db.Prepare(division)
	if err != nil {
		t.Fatal(err)
	}
	defer divisionStmt.Close()
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		if prepared {
			rows, err = divisionStmt.Query()
		} else {
			rows, err = db.Query(division)
		}
		if rows != nil {
			rows.Close()
			t.Fatal("later failure exposed partial rows")
		}
		var got *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(got, err, want)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	rows, err := held.QueryContext(canceled, 10, 1)
	if rows != nil {
		rows.Close()
		t.Fatal("canceled held execution exposed rows")
	}
	var diagnostic *stuntgres.Error
	if !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) {
		t.Fatal("context failure must remain ordinary", err)
	}
	exec(db, "CREATE VIEW alias_view AS SELECT x.iid AS shown,x.a AS amount FROM t x(iid) WHERE x.b=1 ORDER BY shown", 0)
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	destination := open(t.Name() + "_destination")
	exec(destination, "CREATE TABLE sentinel(id INT)", 0)
	exec(destination, "INSERT INTO sentinel VALUES(17)", 1)
	old, err := destination.Query("SELECT x.id AS retained FROM sentinel x")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	suffix := "CREATE VIEW invalid_alias AS SELECT t.id FROM t x;\n"
	invalid := append(append([]byte(nil), dump.Bytes()...), suffix...)
	err = stuntgres.Restore(ctx, destination, bytes.NewReader(invalid))
	// Independently count characters in the complete generated input; the
	// suffix's diagnostic must not be mistaken for a standalone SQL position.
	position := utf8.RuneCount(dump.Bytes()) + utf8.RuneCountInString(suffix[:strings.Index(suffix, "t.id")]) + 1
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Hint: `Perhaps you meant to reference the table alias "x".`, Position: strconv.Itoa(position)}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(diagnostic, err, want)
	}
	checkSentinel := func() {
		t.Helper()
		rows, err := destination.Query("SELECT id FROM sentinel")
		orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}})
	}
	checkSentinel()
	if err := stuntgres.Restore(canceled, destination, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled Restore", err)
	}
	checkSentinel()
	if err := stuntgres.Restore(ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	orderSmokeRows(t, old, nil, []string{"retained"}, []string{"INT4"}, [][]any{{int64(17)}})
	viewQuery := "SELECT shown,amount FROM alias_view ORDER BY shown"
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		if prepared {
			stmt, err := destination.Prepare(viewQuery)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = stmt.Query()
			orderSmokeRows(t, rows, err, []string{"shown", "amount"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
			stmt.Close()
		} else {
			rows, err = destination.Query(viewQuery)
			orderSmokeRows(t, rows, err, []string{"shown", "amount"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
		}
	}
	retainedView, err := destination.Query(viewQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer retainedView.Close()
	if err := stuntgres.Reset(ctx, destination); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	orderSmokeRows(t, retainedView, nil, []string{"shown", "amount"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
	rows, err = destination.Query(viewQuery)
	orderSmokeRows(t, rows, err, []string{"shown", "amount"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
}
