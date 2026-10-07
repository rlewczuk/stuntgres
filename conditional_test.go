package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"stuntgres"
	"sync"
	"testing"
)

func TestConditionalPreparedReuseAndMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "conditional-prepared")
	execSQL(t, db, "CREATE TABLE t(id INT,v INT,b BOOL,a VARCHAR(3),c VARCHAR(3),d VARCHAR(4))")
	execSQL(t, db, "INSERT INTO t VALUES(1,0,TRUE,'x','y','z'),(2,NULL,FALSE,NULL,'y','z')")
	for _, tc := range []struct {
		q, name, typ string
		length       int64
		known        bool
	}{
		{"COALESCE(a,c)", "coalesce", "VARCHAR", 3, true}, {"GREATEST(a,c)", "greatest", "VARCHAR", 3, true},
		{"COALESCE(a,d)", "coalesce", "VARCHAR", math.MaxInt64, true}, {"CASE WHEN b THEN a ELSE c END", "c", "VARCHAR", 3, true},
		{"CASE WHEN b THEN a END", "case", "VARCHAR", math.MaxInt64, true}, {"NULLIF(a,c)", "nullif", "TEXT", math.MaxInt64, true},
	} {
		q := "SELECT " + tc.q + " FROM t"
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		for _, prepared := range []bool{false, true} {
			rows, err := db.Query(q)
			if prepared {
				if rows != nil {
					rows.Close()
				}
				rows, err = stmt.Query()
			}
			if err != nil {
				t.Fatal(err)
			}
			names, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(names, []string{tc.name}) {
				t.Fatal(names, err)
			}
			types, err := rows.ColumnTypes()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			if types[0].DatabaseTypeName() != tc.typ || types[0].ScanType() != reflect.TypeFor[string]() {
				t.Fatal(types[0])
			}
			length, known := types[0].Length()
			if known != tc.known || known && length != tc.length {
				t.Fatal(tc, length, known)
			}
			if _, known := types[0].Nullable(); known {
				t.Fatal("invented scalar nullability")
			}
		}
		stmt.Close()
	}
	stmt, err := db.Prepare("SELECT COALESCE($1,1),NULLIF($1,1) FROM t WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct {
		arg  any
		want []any
	}{{2, []any{int64(2), int64(2)}}, {nil, []any{int64(1), nil}}, {1, []any{int64(1), nil}}, {3, []any{int64(3), int64(3)}}} {
		rows, err := stmt.Query(tc.arg)
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "INT4" {
			t.Fatal(types, err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		got := make([]any, 2)
		if err := rows.Scan(&got[0], &got[1]); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatal(got, tc.want)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		rows.Close()
	}
	rows, err := stmt.Query("bad")
	if rows != nil {
		rows.Close()
	}
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
	rows, err = stmt.Query(4)
	if err != nil {
		t.Fatal("failed execution poisoned prepared statement", err)
	}
	rows.Close()
	held, err := db.Prepare("SELECT CASE WHEN b THEN 1 ELSE CAST(CAST($1 AS TEXT) AS INTEGER) END FROM t WHERE id=$2")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, id := range []int{1, 2, 1} {
		rows, err := held.Query("bad", id)
		if err == nil && rows != nil {
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
		}
		if id == 2 {
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
		} else if err != nil {
			t.Fatal("skipped cast", err)
		}
	}
	wantTuples(t, db, "SELECT COALESCE(v,CAST(CAST($1 AS TEXT) AS INTEGER)) FROM t WHERE id=1", [][]any{{int64(0)}}, "bad")
	for _, q := range []string{"SELECT GREATEST(100,10/v) FROM t WHERE id=1", "SELECT LEAST(0,10/v) FROM t WHERE id=1", "SELECT NULLIF(NULL,10/v) FROM t WHERE id=1"} {
		rows, err := db.Query(q)
		if err == nil && rows != nil {
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
	}
}
func TestConditionalSequenceDemandAndConsumers(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "conditional-consumers")
	execSQL(t, db, "CREATE TABLE t(id INT,v INT,b BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,0,TRUE),(2,2,FALSE),(3,NULL,NULL)")
	wantTuples(t, db, "SELECT id FROM t WHERE CASE WHEN b THEN TRUE ELSE FALSE END", [][]any{{int64(1)}})
	execSQL(t, db, "CREATE SEQUENCE seq")
	stmt, err := db.Prepare("SELECT CASE nextval('seq') WHEN 1 THEN 10 WHEN 2 THEN 20 ELSE 30 END FROM t WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	var value int64
	if err := stmt.QueryRow().Scan(&value); err != nil || value != 10 {
		t.Fatal(value, err)
	}
	stmt.Close()
	wantTuples(t, db, "SELECT nextval('seq')", [][]any{{int64(2)}})
	wantTuples(t, db, "SELECT CASE WHEN b THEN 7 ELSE nextval('seq') END,COALESCE(v,nextval('seq')) FROM t WHERE id=1", [][]any{{int64(7), int64(0)}})
	wantTuples(t, db, "SELECT nextval('seq')", [][]any{{int64(3)}})
	wantTuples(t, db, "SELECT CASE nextval('seq') WHEN 4 THEN CASE nextval('seq') WHEN 5 THEN 9 ELSE 0 END ELSE 0 END FROM t WHERE id=1", [][]any{{int64(9)}})
	wantTuples(t, db, "SELECT nextval('seq')", [][]any{{int64(6)}})
	execSQL(t, db, "CREATE TABLE out(v INT)")
	execSQL(t, db, "CREATE TABLE decorated(v BIGINT)")
	execSQL(t, db, "INSERT INTO decorated VALUES(CASE WHEN TRUE THEN 1 ELSE 2147483648 END),(COALESCE(NULL,2147483648)) ORDER BY column1")
	execSQL(t, db, "INSERT INTO decorated SELECT GREATEST(v,9) FROM t")
	wantTuples(t, db, "SELECT v FROM decorated ORDER BY v", [][]any{{int64(1)}, {int64(9)}, {int64(9)}, {int64(9)}, {int64(2147483648)}})
	execSQL(t, db, "INSERT INTO out VALUES(COALESCE(NULL,1)),(NULLIF(2,3)),(GREATEST(NULL,4)),(LEAST(NULL,5)),(CASE WHEN FALSE THEN 1/0 ELSE 6 END)")
	execSQL(t, db, "INSERT INTO out SELECT CASE WHEN b THEN 10 ELSE 20 END FROM t")
	wantTuples(t, db, "SELECT v FROM out ORDER BY v", [][]any{{int64(1)}, {int64(2)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(10)}, {int64(20)}, {int64(20)}})
	for _, q := range []string{"UPDATE t SET v=CASE WHEN id=1 THEN 9 ELSE 10/(id-2) END", "DELETE FROM t WHERE CASE WHEN id=1 THEN TRUE ELSE 10/(id-2)>0 END", "INSERT INTO out SELECT CASE WHEN id=1 THEN 9 ELSE 10/(id-2) END FROM t"} {
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
		wantTuples(t, db, "SELECT v FROM t ORDER BY id", [][]any{{int64(0)}, {int64(2)}, {nil}})
	}
	wantTuples(t, db, "SELECT v FROM out ORDER BY v", [][]any{{int64(1)}, {int64(2)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(10)}, {int64(20)}, {int64(20)}})
	execSQL(t, db, "UPDATE t SET v=CASE WHEN b THEN 10 WHEN id=2 THEN 20 ELSE 30 END WHERE COALESCE(TRUE,FALSE)")
	wantTuples(t, db, "SELECT v FROM t ORDER BY id", [][]any{{int64(10)}, {int64(20)}, {int64(30)}})
	execSQL(t, db, "DELETE FROM t WHERE CASE t.b WHEN TRUE THEN TRUE ELSE FALSE END")
	wantTuples(t, db, "SELECT id FROM t ORDER BY id", [][]any{{int64(2)}, {int64(3)}})
	wantTuples(t, db, "SELECT COALESCE(t.v,1) FROM t ORDER BY id", [][]any{{int64(20)}, {int64(30)}})
	for _, q := range []string{"UPDATE t SET v=COALESCE(t.v,1)", "DELETE FROM t WHERE CAST(COALESCE(t.v,1) AS int4)=1"} {
		_, err := db.Exec(q)
		var d *stuntgres.Error
		if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "qualified column references are unsupported" || d.Position == "" {
			t.Fatal(q, err)
		}
	}
	_, err = db.Exec("INSERT INTO out VALUES(COALESCE(NULL,NULL))")
	var d *stuntgres.Error
	if !errors.As(err, &d) || d.Code != "42804" || d.Hint != "You will need to rewrite or cast the expression." {
		t.Fatal(err)
	}
}
func TestConditionalNativeDefaultsAndSnapshots(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "conditional-native")
	execSQL(t, db, "CREATE TABLE t(id INT,v INT,b BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,0,TRUE),(2,NULL,FALSE)")
	execSQL(t, db, "CREATE TABLE defaults(a INT DEFAULT CASE WHEN TRUE THEN COALESCE(NULL,7) ELSE 1/0 END,b INT DEFAULT NULLIF(2,3),c INT DEFAULT GREATEST(NULL,4),d INT DEFAULT LEAST(NULL,5))")
	execSQL(t, db, "CREATE TABLE deferred(v INT DEFAULT CASE WHEN FALSE THEN 1 ELSE 1/0 END)")
	execSQL(t, db, "CREATE TABLE wide(v BIGINT DEFAULT CASE 2147483648 WHEN 2147483648 THEN NULLIF(2147483649,2147483650) ELSE 2147483651 END)")
	execSQL(t, db, "INSERT INTO deferred VALUES(9)")
	execSQL(t, db, "INSERT INTO defaults VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)")
	// A retained TEMP sequence forces Int8/Bool default provider retagging when
	// Restore installs its new permanent catalog.
	execSQL(t, db, "CREATE TEMP SEQUENCE held_seq")
	execSQL(t, db, "INSERT INTO defaults VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)")
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal("Dump evaluated a dormant default", err)
	}
	raw, err := d.Open("conditional-native")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	owned := []byte("owned")
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT CASE WHEN b THEN v ELSE COALESCE(v,3) END,COALESCE($1,'fallback') FROM t ORDER BY id", []driver.NamedValue{{Ordinal: 1, Value: owned}})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	owned[0] = 'X'
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("native default serialization changed on replay")
	}
	execSQL(t, db, "INSERT INTO defaults VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)")
	wantTuples(t, db, "SELECT a,b,c,d FROM defaults", [][]any{{int64(7), int64(2), int64(4), int64(5)}, {int64(7), int64(2), int64(4), int64(5)}, {int64(7), int64(2), int64(4), int64(5)}})
	execSQL(t, db, "INSERT INTO wide VALUES(DEFAULT)")
	wantTuples(t, db, "SELECT v FROM wide", [][]any{{int64(2147483649)}})
	_, err = db.Exec("INSERT INTO deferred VALUES(DEFAULT)")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
	wantTuples(t, db, "SELECT v FROM deferred", [][]any{{int64(9)}})
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(v INT); INSERT INTO replacement VALUES(CASE WHEN TRUE THEN 1 ELSE $1 END)")); err == nil {
		t.Fatal("hidden restore parameter accepted")
	}
	wantTuples(t, db, "SELECT v FROM deferred", [][]any{{int64(9)}})
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(v TEXT); INSERT INTO replacement VALUES('new')")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cursor.Columns(), []string{"coalesce", "coalesce"}) {
		t.Fatal(cursor.Columns())
	}
	dst := make([]driver.Value, 2)
	for _, want := range [][]driver.Value{{int64(0), "owned"}, {int64(3), "owned"}} {
		if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
			t.Fatal(dst, want, err)
		}
	}
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
}

func TestConditionalConcurrentPreparedFrames(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "conditional-concurrent")
	execSQL(t, db, "CREATE TABLE t(v INT)")
	execSQL(t, db, "INSERT INTO t VALUES(0)")
	stmt, err := db.Prepare("SELECT CASE WHEN $1 THEN COALESCE($2,7) ELSE NULLIF($2,9) END FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	var wg sync.WaitGroup
	for n := range 16 {
		wg.Go(func() {
			var arg any = int64(n)
			var want any = int64(n)
			if n%4 == 0 {
				arg = nil
				want = int64(7)
			} else if n == 9 {
				want = nil
			}
			var got any
			if err := stmt.QueryRow(n%2 == 0, arg).Scan(&got); err != nil || !reflect.DeepEqual(got, want) {
				t.Error(n, got, want, err)
			}
		})
	}
	wg.Wait()
}

func TestConditionalDefaultBooleanDemand(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), "conditional-default-boolean")
	execSQL(t, db, "CREATE TABLE d(a BOOL DEFAULT CASE WHEN TRUE THEN FALSE AND CAST(CAST('oops' AS TEXT) AS INTEGER)=1 ELSE FALSE END,b BOOL DEFAULT CASE WHEN TRUE THEN TRUE OR CAST(CAST('oops' AS TEXT) AS INTEGER)=1 ELSE FALSE END,c BOOL DEFAULT COALESCE(FALSE AND CAST(CAST('oops' AS TEXT) AS INTEGER)=1,FALSE),e BOOL DEFAULT COALESCE(TRUE OR CAST(CAST('oops' AS TEXT) AS INTEGER)=1,FALSE))")
	dump := func() []byte {
		t.Helper()
		var out bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &out); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	definitions := func(data []byte) []string {
		var lines []string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "DEFAULT") {
				lines = append(lines, line)
			}
		}
		return lines
	}
	before := definitions(dump())
	if len(before) == 0 || !strings.Contains(strings.Join(before, "\n"), "oops") {
		t.Fatal("stored defaults missing")
	}
	execSQL(t, db, "INSERT INTO d VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)")
	wantTuples(t, db, "SELECT a,b,c,e FROM d", [][]any{{false, true, false, true}})
	snapshot := dump()
	if !reflect.DeepEqual(before, definitions(snapshot)) {
		t.Fatal("consumption rewrote stored defaults")
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(snapshot)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snapshot, dump()) {
		t.Fatal("default roundtrip changed snapshot")
	}
	execSQL(t, db, "INSERT INTO d VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)")
	wantTuples(t, db, "SELECT a,b,c,e FROM d", [][]any{{false, true, false, true}, {false, true, false, true}})
	if !reflect.DeepEqual(before, definitions(dump())) {
		t.Fatal("restored consumption rewrote stored defaults")
	}
}

func TestConditionalCoalescePreparedDemand(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "conditional-coalesce-demand")
	execSQL(t, db, "CREATE TABLE t(id INT,v INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,0),(2,NULL)")
	q := "SELECT COALESCE(v,CAST(CAST($1 AS TEXT) AS INTEGER)) FROM t WHERE id=$2"
	drain := func(rows *sql.Rows, err error) error {
		if rows != nil {
			if err == nil {
				for rows.Next() {
				}
				err = rows.Err()
			}
			rows.Close()
		}
		return err
	}
	rows, err := db.Query(q, "bad", 2)
	if rows != nil {
		err = drain(rows, err)
	}
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
	wantTuples(t, db, q, [][]any{{int64(7)}}, "7", 2)
	stmt, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err = stmt.Query("bad", 2)
	if rows != nil {
		err = drain(rows, err)
	}
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
	for _, tc := range []struct {
		arg  string
		id   int
		want int64
	}{{"7", 2, 7}, {"bad", 1, 0}} {
		rows, err = stmt.Query(tc.arg, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		var value int64
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != tc.want || rows.Next() {
			t.Fatal(value, tc.want)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
