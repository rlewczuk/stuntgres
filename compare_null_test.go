package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func compareNullSetup(t *testing.T) (*stuntgres.Driver, *sql.DB) {
	t.Helper()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v INTEGER,b BIGINT,s TEXT,f BOOLEAN)")
	execSQL(t, db, "INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'x',NULL),(3,2,3,'y',FALSE),(4,1,4,'x',TRUE)")
	return d, db
}

func compareNullRows(t *testing.T, rows *sql.Rows, err error, names, types []string, want [][]any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	gotNames, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(gotNames, names) {
		t.Fatal(gotNames, names, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range types {
		if metadata[i].DatabaseTypeName() != typ {
			t.Fatal(metadata[i], typ)
		}
		if typ == "BOOL" {
			if metadata[i].ScanType() != reflect.TypeFor[bool]() {
				t.Fatal(metadata[i])
			}
			if _, known := metadata[i].Nullable(); known {
				t.Fatal("invented computed nullability")
			}
		}
	}
	got := [][]any{}
	for rows.Next() {
		values := make([]any, len(names))
		destinations := make([]any, len(names))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
}

func TestCompareNullMatrices(t *testing.T) {
	_, db := compareNullSetup(t)
	check := func(q string, names, types []string, want [][]any, args ...any) {
		t.Helper()
		rows, err := db.Query(q, args...)
		compareNullRows(t, rows, err, names, types, want)
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(q, err)
		}
		defer stmt.Close()
		rows, err = stmt.Query(args...)
		compareNullRows(t, rows, err, names, types, want)
	}
	check("SELECT id,v IS NULL,v IS NOT NULL,v ISNULL,v NOTNULL FROM t ORDER BY id", []string{"id", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), false, true, false, true}, {int64(2), true, false, true, false}, {int64(3), false, true, false, true}, {int64(4), false, true, false, true}})
	check("SELECT id,f IS TRUE,f IS NOT TRUE,f IS FALSE,f IS NOT FALSE,f IS UNKNOWN,f IS NOT UNKNOWN FROM t ORDER BY id", []string{"id", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, false, false, true, false, true}, {int64(2), false, true, false, true, true, false}, {int64(3), false, true, true, false, false, true}, {int64(4), true, false, false, true, false, true}})
	check("SELECT v=NULL FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}, {nil}})
	check("SELECT num_nulls(v),v IS NULL FROM t ORDER BY id", []string{"num_nulls", "?column?"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), false}, {int64(1), true}, {int64(0), false}, {int64(0), false}})
	variants := []string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"}
	truth := [][]bool{{true, false, false, true, false, true}, {false, true, true, false, false, true}, {false, true, false, true, true, false}}
	for i, literal := range []string{"TRUE", "FALSE", "NULL"} {
		for j, variant := range variants {
			want := [][]any{}
			for range 4 {
				want = append(want, []any{truth[i][j]})
			}
			check("SELECT "+literal+" "+variant+" FROM t", []string{"?column?"}, []string{"BOOL"}, want)
		}
	}
	for i, predicate := range append([]string{"v IS NULL", "v IS NOT NULL", "v ISNULL", "v NOTNULL"}, []string{"f IS TRUE", "f IS NOT TRUE", "f IS FALSE", "f IS NOT FALSE", "f IS UNKNOWN", "f IS NOT UNKNOWN"}...) {
		ids := [][]int64{{2}, {1, 3, 4}, {2}, {1, 3, 4}, {1, 4}, {2, 3}, {3}, {1, 2, 4}, {2}, {1, 3, 4}}[i]
		want := [][]any{}
		for _, id := range ids {
			want = append(want, []any{id})
		}
		check("SELECT id FROM t WHERE "+predicate+" ORDER BY id", []string{"id"}, []string{"INT4"}, want)
	}
	for _, tc := range []struct {
		left, right string
		distinct    bool
	}{
		{"NULL", "NULL", false}, {"NULL", "1", true}, {"1", "NULL", true}, {"1", "1", false}, {"1", "2", true},
		{"'x'", "'x'", false}, {"'x'", "'y'", true}, {"TRUE", "FALSE", true}, {"TRUE", "TRUE", false},
		{"1", "'1'", false}, {"'1'", "1", false}, {"(NULL)", "NULL", false}, {"v", "v", false}, {"v", "b", true}, {"b", "v", true},
	} {
		want := [][]any{}
		for range 4 {
			want = append(want, []any{tc.distinct, !tc.distinct})
		}
		check("SELECT "+tc.left+" IS DISTINCT FROM "+tc.right+","+tc.left+" IS NOT DISTINCT FROM "+tc.right+" FROM t", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, want)
	}
	for _, tc := range []struct {
		q   string
		ids []int64
	}{
		{"v IS DISTINCT FROM 1", []int64{2, 3}}, {"1 IS DISTINCT FROM v", []int64{2, 3}},
		{"v IS NOT DISTINCT FROM 1", []int64{1, 4}}, {"1 IS NOT DISTINCT FROM v", []int64{1, 4}},
		{"v IS DISTINCT FROM NULL", []int64{1, 3, 4}}, {"NULL IS DISTINCT FROM v", []int64{1, 3, 4}},
		{"v IS NOT DISTINCT FROM NULL", []int64{2}}, {"NULL IS NOT DISTINCT FROM v", []int64{2}},
		{"v=NULL", nil}, {"(v=NULL) IS UNKNOWN", []int64{1, 2, 3, 4}}, {"v=1 IS TRUE", []int64{1, 4}}, {"(v=1) IS NOT TRUE", []int64{2, 3}},
		{"TRUE IS NULL IS TRUE", nil}, {"v IS NULL ISNULL", nil},
		{"v IS NULL AND TRUE", []int64{2}}, {"NOT v IS NULL", []int64{1, 3, 4}},
		{"v IS NULL OR f IS TRUE", []int64{1, 2, 4}}, {"TRUE OR FALSE", []int64{1, 2, 3, 4}},
		{"v<2 IS TRUE OR NOT f IS UNKNOWN AND v IS NOT NULL", []int64{1, 3, 4}},
		{"(f AND TRUE) IS UNKNOWN", []int64{2}},
		{"(f OR FALSE) IS NOT TRUE", []int64{2, 3}},
		{"(NOT f) IS DISTINCT FROM FALSE", []int64{2, 3}},
	} {
		want := [][]any{}
		for _, id := range tc.ids {
			want = append(want, []any{id})
		}
		check("SELECT id FROM t WHERE "+tc.q+" ORDER BY id", []string{"id"}, []string{"INT4"}, want)
	}
	for _, tc := range []struct {
		expr   string
		values []bool
	}{
		{"(v IS NULL) IS TRUE", []bool{false, true, false, false}},
		{"(v IS DISTINCT FROM 1) IS FALSE", []bool{true, false, false, true}},
		{"TRUE IS DISTINCT FROM (v IS NULL)", []bool{true, false, true, true}},
		{"(TRUE IS NULL) IS TRUE", []bool{false, false, false, false}},
		{"TRUE IS NULL IS TRUE", []bool{false, false, false, false}},
		{"v IS NULL ISNULL", []bool{false, false, false, false}},
		{"'yes' IS TRUE", []bool{true, true, true, true}},
		{"'null' IS NULL", []bool{false, false, false, false}},
		{"(v=1) IS TRUE", []bool{true, false, false, true}},
		{"(v=NULL) IS UNKNOWN", []bool{true, true, true, true}},
		{"(v=v) IS TRUE", []bool{true, false, true, true}},
		{"v IS NULL AND TRUE", []bool{false, true, false, false}},
		{"NOT v IS NULL", []bool{true, false, true, true}},
		{"v IS NULL OR f IS TRUE", []bool{true, true, false, true}},
		{"v<2 IS TRUE OR NOT f IS UNKNOWN AND v IS NOT NULL", []bool{true, false, true, true}},
		{"(f AND TRUE) IS UNKNOWN", []bool{false, true, false, false}},
		{"(f OR FALSE) IS NOT TRUE", []bool{false, true, true, false}},
		{"(NOT f) IS DISTINCT FROM FALSE", []bool{false, true, true, false}},
	} {
		want := [][]any{}
		for _, v := range tc.values {
			want = append(want, []any{v})
		}
		check("SELECT "+tc.expr+" FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, want)
	}
	execSQL(t, db, "CREATE TABLE empty(v INT)")
	check("SELECT v IS NULL FROM empty", []string{"?column?"}, []string{"BOOL"}, [][]any{})
}

func TestCompareNullHeldParameters(t *testing.T) {
	_, db := compareNullSetup(t)
	for _, q := range []string{"SELECT id FROM t WHERE v IS NOT DISTINCT FROM $1 ORDER BY id", "SELECT id FROM t WHERE $1 IS NOT DISTINCT FROM v ORDER BY id", "SELECT id FROM t WHERE v IS NOT DISTINCT FROM $1 AND (v=$1) IS NOT FALSE ORDER BY id"} {
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		for i, arg := range []any{1, 2, nil} {
			want := [][][]any{{{int64(1)}, {int64(4)}}, {{int64(3)}}, {{int64(2)}}}[i]
			rows, err := stmt.Query(arg)
			compareNullRows(t, rows, err, []string{"id"}, []string{"INT4"}, want)
		}
		stmt.Close()
	}
	for _, q := range []string{"SELECT $1 IS TRUE,$1 IS UNKNOWN FROM t", "SELECT $1 IS TRUE,$1 IS NULL FROM t"} {
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		for i, arg := range []any{true, false, nil} {
			pair := [][]any{{true, false}, {false, false}, {false, true}}[i]
			want := [][]any{pair, pair, pair, pair}
			rows, err := stmt.Query(arg)
			compareNullRows(t, rows, err, []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, want)
		}
		rows, err := stmt.Query("truth")
		if rows != nil {
			rows.Close()
			t.Fatal("failed bind exposed cursor")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`})
		stmt.Close()
	}
	for _, q := range []string{"SELECT $1 IS DISTINCT FROM $2 FROM t", "SELECT $1 IS DISTINCT FROM $1 FROM t"} {
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		args := []any{"x", "y"}
		want := true
		if strings.Contains(q, "FROM $1") {
			args = []any{"x"}
			want = false
		}
		rows, err := stmt.Query(args...)
		compareNullRows(t, rows, err, []string{"?column?"}, []string{"BOOL"}, [][]any{{want}, {want}, {want}, {want}})
		stmt.Close()
	}
	wantTuples(t, db, "SELECT id FROM t WHERE v IS DISTINCT FROM $1 ORDER BY id", [][]any{{int64(1)}, {int64(3)}, {int64(4)}}, nil)
}

func TestCompareNullAnyInteraction(t *testing.T) {
	_, db := compareNullSetup(t)
	q := "SELECT $1 IS TRUE,num_nulls($1),$1 IS NULL FROM t"
	stmt, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i, arg := range []any{true, false, nil} {
		row := [][]any{{true, int64(0), false}, {false, int64(0), false}, {false, int64(1), true}}[i]
		want := [][]any{row, row, row, row}
		rows, err := db.Query(q, arg)
		compareNullRows(t, rows, err, []string{"?column?", "num_nulls", "?column?"}, []string{"BOOL", "INT4", "BOOL"}, want)
		rows, err = stmt.Query(arg)
		compareNullRows(t, rows, err, []string{"?column?", "num_nulls", "?column?"}, []string{"BOOL", "INT4", "BOOL"}, want)
	}
}

func TestCompareNullDiagnostics(t *testing.T) {
	_, db := compareNullSetup(t)
	execSQL(t, db, "CREATE TABLE empty(v INT,s TEXT,b BIGINT)")
	cases := []struct {
		q    string
		want stuntgres.Error
		args []any
	}{
		{"SELECT /*é*/ 1 IS NOT UNKNOWN FROM t", stuntgres.Error{Code: "42804", Message: "argument of IS NOT UNKNOWN must be type boolean, not type integer", Position: "14"}, nil},
		{"SELECT v IS NULL AND 1 FROM t", stuntgres.Error{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "22"}, nil},
		{"SELECT TRUE OR 1 IS TRUE FROM t", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "16"}, nil},
		{"SELECT FALSE AND missing IS NULL FROM t", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "18"}, nil},
		{"SELECT v IS DISTINCT FROM TRUE FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, nil},
		{"SELECT v IS NOT DISTINCT FROM TRUE FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, nil},
		{"SELECT TRUE IS DISTINCT FROM v FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: boolean = integer", Position: "13", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, nil},
		{"SELECT 1 IS DISTINCT FROM 'oops' FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "27"}, nil},
		{"SELECT 'oops' IS DISTINCT FROM 1 FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "8"}, nil},
		{"SELECT 'truth' IS TRUE FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "8"}, nil},
		{"SELECT missing IS NULL FROM t", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}, nil},
		{"SELECT 1 IS DISTINCT FROM 1 IS TRUE FROM t", stuntgres.Error{Code: "42601", Message: `syntax error at or near "IS"`, Position: "29"}, nil},
		{"SELECT $1 IS NULL,$1 IS TRUE FROM t", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "8"}, []any{true}},
		{"SELECT num_nulls($1),$1 IS NULL,$1 IS TRUE FROM t", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "18"}, []any{true}},
		{"SELECT $1 IS NULL,num_nulls($1),$1 IS TRUE FROM t", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "8"}, []any{true}},
		{"SELECT num_nulls($2),$1 IS NULL,$1 IS TRUE,$2 IS TRUE FROM t", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $2", Position: "18"}, []any{true, true}},
		{"SELECT $1 IS NULL,num_nulls($2),$1 IS TRUE,$2 IS TRUE FROM t", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "8"}, []any{true, true}},
		{"SELECT num_nulls($2),$1 IS NULL,$1 IS TRUE FROM t", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "22"}, []any{true, nil}},
		{"SELECT $1 IS NULL,$1 FROM t", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "8"}, []any{"x"}},
		{"SELECT $1,$1 IS NULL FROM t", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "11"}, []any{"x"}},
		{"SELECT $1 FROM t WHERE v IS DISTINCT FROM $1", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}, []any{1}},
		{"SELECT $1 IS DISTINCT FROM v,$1 IS TRUE FROM t", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "30"}, []any{1}},
	}
	for _, variant := range []string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"} {
		for _, table := range []string{"t", "empty"} {
			for _, input := range []struct{ expr, typ string }{{"1", "integer"}, {"s", "text"}, {"b", "bigint"}} {
				cases = append(cases, struct {
					q    string
					want stuntgres.Error
					args []any
				}{"SELECT " + input.expr + " " + variant + " FROM " + table, stuntgres.Error{Code: "42804", Message: "argument of " + variant + " must be type boolean, not type " + input.typ, Position: "8"}, nil})
			}
		}
	}
	for _, expr := range []string{"$1 IS NULL", "$1 IS NOT NULL", "$1 ISNULL", "$1 NOTNULL", "$1 IS DISTINCT FROM NULL", "$1 IS NOT DISTINCT FROM NULL", "NULL IS DISTINCT FROM $1", "NULL IS NOT DISTINCT FROM $1"} {
		cases = append(cases, struct {
			q    string
			want stuntgres.Error
			args []any
		}{"SELECT " + expr + " FROM t", stuntgres.Error{Code: "42P18", Message: "could not determine data type of parameter $1"}, []any{true}})
	}
	for _, tc := range cases {
		rows, err := db.Query(tc.q, tc.args...)
		if rows != nil {
			rows.Close()
			t.Fatal("failed query exposed cursor", tc.q)
		}
		typeCoreDiagnostic(t, err, tc.want)
		stmt, err := db.Prepare(tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failed prepare exposed handle", tc.q)
		}
		typeCoreDiagnostic(t, err, tc.want)
	}
	compareOpsDirectPrepared(t, db, "SELECT extra.v IS NULL FROM t extra ORDER BY id", []string{"BOOL"}, [][]any{{false}, {true}, {false}, {false}})
	compareOpsDirectPrepared(t, db, "SELECT extra.v IS NULL FROM t extra JOIN t rhs USING(id) ORDER BY id", []string{"BOOL"}, [][]any{{false}, {true}, {false}, {false}})
}

func TestCompareNullCapturedMetadataControlsAndCancellation(t *testing.T) {
	d, db := compareNullSetup(t)
	ctx := context.Background()
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v IS NULL,f IS UNKNOWN,v IS DISTINCT FROM b FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	execSQL(t, db, "INSERT INTO t VALUES(5,5,5,'later',TRUE)")
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT id FROM t WHERE v IS NULL", [][]any{{int64(2)}})
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('new')")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cursor.Columns(), []string{"?column?", "?column?", "?column?"}) {
		t.Fatal(cursor.Columns())
	}
	meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
	for i := range 3 {
		if meta.ColumnTypeDatabaseTypeName(i) != "BOOL" {
			t.Fatal("retained metadata changed")
		}
	}
	dst := make([]driver.Value, 3)
	for _, want := range [][]driver.Value{{false, false, true}, {true, true, true}, {false, false, true}, {false, false, true}} {
		if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
			t.Fatal(dst, want, err)
		}
	}
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT replacement IS NOT NULL FROM t", [][]any{{true}})
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES($1)")); err == nil {
		t.Fatal("parameter Restore accepted")
	}
	wantTuples(t, db, "SELECT replacement FROM t", [][]any{{"new"}})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.QueryContext(canceled, "SELECT replacement IS NULL FROM t"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	iteration, cancel := context.WithCancel(ctx)
	rows, err := raw.(driver.QueryerContext).QueryContext(iteration, "SELECT replacement IS NULL FROM t", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cancel()
	if err := rows.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCompareNullFunctionBoundaries(t *testing.T) {
	_, db := compareNullSetup(t)
	for _, q := range []string{
		"SELECT num_nulls(v) IS NULL FROM t", "SELECT num_nulls(v IS NULL) FROM t",
		"SELECT (num_nulls(v)) IS NULL FROM t", "SELECT num_nulls((v IS NULL)) FROM t",
		"SELECT num_nulls(v)=1 FROM t", "SELECT 1=num_nulls(v) FROM t",
		"SELECT num_nulls(v) AND TRUE FROM t", "SELECT TRUE OR num_nulls(v) FROM t",
		"SELECT id FROM t WHERE num_nulls(v) IS NULL",
	} {
		rows, err := db.Query(q)
		if rows != nil {
			rows.Close()
			t.Fatal("excluded function/predicate composition exposed cursor", q)
		}
		if err == nil {
			t.Fatal("excluded function/predicate composition succeeded", q)
		}
		stmt, err := db.Prepare(q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("excluded function/predicate composition exposed handle", q)
		}
		if err == nil {
			t.Fatal("excluded function/predicate composition prepared", q)
		}
	}
}

func TestCompareNullFunctionInsertSelectDiagnostics(t *testing.T) {
	_, db := compareNullSetup(t)
	execSQL(t, db, "CREATE TABLE counts(n INT)")
	for _, prepared := range []bool{false, true} {
		query := "INSERT INTO counts SELECT num_nulls(v) FROM t"
		var result sql.Result
		var err error
		if prepared {
			stmt, prepareErr := db.Prepare(query)
			if prepareErr != nil {
				t.Fatal(prepareErr)
			}
			result, err = stmt.Exec()
			stmt.Close()
		} else {
			result, err = db.Exec(query)
		}
		insertSelectCount(t, result, err, 4)
	}
	wantTuples(t, db, "SELECT n FROM counts ORDER BY n", [][]any{{int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}, {int64(0)}, {int64(1)}, {int64(1)}})
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"INSERT INTO t SELECT num_nulls(v) FROM t VALUES (1)", stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "1"`, Position: "50"}},
	} {
		rows, err := db.Query(tc.q)
		if rows != nil {
			rows.Close()
			t.Fatal("failed INSERT SELECT exposed cursor", tc.q)
		}
		typeCoreDiagnostic(t, err, tc.want)
		stmt, err := db.Prepare(tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failed INSERT SELECT exposed handle", tc.q)
		}
		typeCoreDiagnostic(t, err, tc.want)
	}
}

func TestCompareNullParameterErrorPrecedence(t *testing.T) {
	_, db := compareNullSetup(t)
	for _, tc := range []struct {
		name, query, parameter, code, position string
		args                                   []any
	}{
		{"unresolved competitor", "SELECT $1 IS NULL,$1 IS TRUE,$2 IS NULL FROM t", "1", "42P08", "8", []any{true, nil}},
		{"gap competitor", "SELECT $2 IS NULL,$2 IS TRUE FROM t", "2", "42P08", "8", []any{nil, true}},
		{"raw NULL rewrite competitor", "SELECT $1 IS DISTINCT FROM NULL,$1 IS TRUE,$2 IS NULL FROM t", "1", "42P08", "8", []any{true, nil}},
		{"unresolved control", "SELECT $1 IS TRUE,$2 IS NULL FROM t", "2", "42P18", "", []any{true, nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := stuntgres.Error{Code: tc.code, Message: "could not determine data type of parameter $" + tc.parameter, Position: tc.position}
			rows, err := db.Query(tc.query, tc.args...)
			if rows != nil {
				rows.Close()
				t.Fatal("failed query exposed rows")
			}
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(tc.query)
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed prepare exposed handle")
			}
			typeCoreDiagnostic(t, err, want)
		})
	}
}
