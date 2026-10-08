package stuntgres_test

import (
	"bytes"
	"context"
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

func TestFloatDeclarationsMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "float-aliases")
	execSQL(t, db, `CREATE TABLE aliases(a REAL NOT NULL,b FLOAT4,c DOUBLE PRECISION,d FLOAT8,e FLOAT,f FLOAT(1),g FLOAT(24),h FLOAT(25),i FLOAT(53),j "float4",k "float8")`)
	for _, prepared := range []bool{false, true} {
		q := "SELECT * FROM aliases"
		if prepared {
			s, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			rows, err := s.Query()
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range []string{"FLOAT4", "FLOAT4", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT4", "FLOAT4", "FLOAT8", "FLOAT8", "FLOAT4", "FLOAT8"} {
				nullable, known := types[i].Nullable()
				if types[i].DatabaseTypeName() != want || types[i].ScanType() != reflect.TypeFor[float64]() || !known || nullable != (i != 0) {
					t.Fatal(types[i])
				}
				if _, _, ok := types[i].DecimalSize(); ok {
					t.Fatal("fake precision")
				}
				if _, ok := types[i].Length(); ok {
					t.Fatal("fake length")
				}
			}
			if rows.Next() {
				t.Fatal("empty rows")
			}
			rows.Close()
		} else {
			rows, err := db.Query(q)
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || types[0].DatabaseTypeName() != "FLOAT4" {
				t.Fatal(types, err)
			}
			rows.Close()
		}
	}
	for _, tc := range []struct{ q, code, message, position string }{{"CREATE TABLE f(x FLOAT(0))", "22023", "precision for type float must be at least 1 bit", "24"}, {"CREATE TABLE f(x FLOAT(54))", "22023", "precision for type float must be less than 54 bits", "24"}, {"CREATE TABLE f(x FLOAT(-1))", "42601", `syntax error at or near "-"`, "24"}, {`CREATE TABLE f(x "real")`, "42704", `type "real" does not exist`, "18"}} {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				s, e := db.Prepare(tc.q)
				err = e
				if tc.code == "42704" {
					// CREATE utility preparation validates raw syntax only;
					// catalog type resolution occurs when the utility executes.
					if e != nil || s == nil {
						t.Fatal("utility preparation", s, e)
					}
					_, err = s.Exec()
					s.Close()
				} else if s != nil {
					s.Close()
					t.Fatal("failed raw precision prepare returned handle")
				}
			} else {
				_, err = db.Exec(tc.q)
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.position})
		}
	}
	_, err := db.Exec("CREATE TABLE IF NOT EXISTS aliases(x FLOAT(0))")
	sqlState(t, err, "22023")
}

func TestFloatQueriesBindsAndOrdering(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "float-queries")
	execSQL(t, db, "CREATE TABLE f(id INTEGER,r REAL,d DOUBLE PRECISION)")
	execSQL(t, db, "INSERT INTO f VALUES(1,'16777217','0.1'),(2,'7.038531e-26','5e-324'),(3,'0.1','1.2345678901234567')")
	wantTuples(t, db, "SELECT r,d FROM f WHERE id=1", [][]any{{float64(16777216), 0.1}})
	wantTuples(t, db, "SELECT r FROM f WHERE r=16777217", [][]any{})
	wantTuples(t, db, "SELECT id FROM f WHERE r='16777217'", [][]any{{int64(1)}})
	held, err := db.Prepare("SELECT id FROM f WHERE r=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, arg := range []any{"16777217", int64(16777217), float64(16777217)} {
		rows, err := held.Query(arg)
		if err != nil {
			t.Fatal(err)
		}
		var id int64
		if !rows.Next() || rows.Scan(&id) != nil || id != 1 || rows.Next() {
			t.Fatal("contextual match", arg, id)
		}
		rows.Close()
	}
	var r, d float64
	if err = db.QueryRow("SELECT r,d FROM f WHERE id=2").Scan(&r, &d); err != nil || math.Float32bits(float32(r)) != 0x15ae43fd || math.Float64bits(d) != 1 {
		t.Fatal(r, d, err)
	}
	if err = db.QueryRow("SELECT r,d FROM f WHERE id=3").Scan(&r, &d); err != nil || r != float64(float32(0.1)) || d != 1.2345678901234567 {
		t.Fatal(r, d, err)
	}
	execSQL(t, db, "CREATE TABLE ordered(id INTEGER,r REAL)")
	insert, err := db.Prepare("INSERT INTO ordered VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer insert.Close()
	values := []any{math.Inf(-1), float64(-1), math.Copysign(0, -1), float32(0), float64(1), math.Inf(1), math.NaN(), "nan", nil}
	for i, v := range values {
		result, err := insert.Exec(i+1, v)
		if err != nil {
			t.Fatal(err)
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	for _, descending := range []bool{false, true} {
		q := "SELECT id,r FROM ordered ORDER BY r"
		if descending {
			q += " DESC"
		}
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		groups := []int{0, 1, 2, 2, 3, 4, 5, 5, 6}
		if descending {
			groups = []int{6, 5, 5, 4, 3, 2, 2, 1, 0}
		}
		seen := map[int64]bool{}
		index := 0
		for rows.Next() {
			var id int64
			var value any
			if err = rows.Scan(&id, &value); err != nil {
				t.Fatal(err)
			}
			if seen[id] || index >= len(groups) {
				t.Fatal(id)
			}
			seen[id] = true
			group := map[int64]int{1: 0, 2: 1, 3: 2, 4: 2, 5: 3, 6: 4, 7: 5, 8: 5, 9: 6}[id]
			if group != groups[index] {
				t.Fatal(q, index, id, value)
			}
			if id == 9 {
				if value != nil {
					t.Fatal(value)
				}
			} else {
				n := value.(float64)
				if id == 7 || id == 8 {
					if !math.IsNaN(n) {
						t.Fatal(n)
					}
				}
			}
			index++
		}
		if rows.Err() != nil || index != 9 {
			t.Fatal(index, rows.Err())
		}
		rows.Close()
	}
	wantTuples(t, db, "SELECT id FROM ordered WHERE r='NaN' ORDER BY id", [][]any{{int64(7)}, {int64(8)}})
	wantTuples(t, db, "SELECT r FROM ordered WHERE r=NULL", [][]any{})
	wantTuples(t, db, "SELECT r FROM ordered WHERE r=$1", [][]any{}, nil)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			if _, err := insert.Exec(i+20, float64(i)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func TestFloatDiagnosticsAndAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "float-errors")
	execSQL(t, db, "CREATE TABLE f(r REAL,d DOUBLE PRECISION)")
	for _, tc := range []struct{ q, code, message, position string }{
		{"INSERT INTO f VALUES('oops','1')", "22P02", `invalid input syntax for type real: "oops"`, "22"},
		{"INSERT INTO f VALUES('1','oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "26"},
		{"INSERT INTO f VALUES('1e999','1')", "22003", `"1e999" is out of range for type real`, "22"},
		{"INSERT INTO f VALUES('1','1e-999')", "22003", `"1e-999" is out of range for type double precision`, "26"},
		{"SELECT r FROM f WHERE r='oops'", "22P02", `invalid input syntax for type real: "oops"`, "25"},
		{"/*é*/ SELECT r FROM f WHERE r='oops'", "22P02", `invalid input syntax for type real: "oops"`, "31"},
		{"SELECT r FROM f WHERE r=TRUE", "42883", "operator does not exist: real = boolean", "24"},
	} {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				s, e := db.Prepare(tc.q)
				err = e
				if s != nil {
					s.Close()
					t.Fatal("eager failure returned statement")
				}
			} else {
				_, err = db.Exec(tc.q)
			}
			want := stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.position}
			if tc.code == "42883" {
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			}
			typeCoreDiagnostic(t, err, want)
		}
	}
	stmt, err := db.Prepare("INSERT INTO f VALUES($1,'1')")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct{ arg, code, message string }{{"oops", "22P02", `invalid input syntax for type real: "oops"`}, {"1e999", "22003", `"1e999" is out of range for type real`}, {"1e-46", "22003", `"1e-46" is out of range for type real`}} {
		_, err = stmt.Exec(tc.arg)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message,Context:"unnamed portal parameter $1 = '...'"})
	}
	execSQL(t, db, "INSERT INTO f VALUES('99','99')")
	for _, q := range []string{"INSERT INTO f VALUES('1','1'),('oops','2')", "INSERT INTO f VALUES('1','1'),('2','1e999')"} {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				s, e := db.Prepare(q)
				err = e
				if s != nil {
					_, err = s.Exec()
					s.Close()
				}
			} else {
				_, err = db.Exec(q)
			}
			if err == nil {
				t.Fatal("bad later tuple accepted")
			}
			wantTuples(t, db, "SELECT * FROM f", [][]any{{float64(99), float64(99)}})
		}
	}
	execSQL(t, db, "CREATE TABLE nn(r REAL NOT NULL)")
	_, err = db.Exec("INSERT INTO nn VALUES('1'),(NULL)")
	sqlState(t, err, "23502")
	wantTuples(t, db, "SELECT * FROM nn", [][]any{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = db.ExecContext(ctx, "INSERT INTO f VALUES('1','2')")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, "SELECT * FROM f")
	if rows != nil {
		rows.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestFloatControlsRetainedAndCancellation(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "float-controls")
	execSQL(t, db, "CREATE TABLE f(id INTEGER,r REAL,d DOUBLE PRECISION)")
	execSQL(t, db, "INSERT INTO f VALUES(1,'33554448','-0'),(2,'1e-45','5e-324'),(3,'NaN','Infinity'),(4,'-Infinity','NaN'),(5,'7.038531e-26','0.1')")
	raw, err := d.Open("float-controls")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT * FROM f ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	execSQL(t, db, "INSERT INTO f VALUES(6,NULL,NULL)")
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"REAL", "DOUBLE PRECISION", "'3.3554448e+07'", "'-0'", "'1e-45'", "'5e-324'", "'NaN'"} {
		if !strings.Contains(dump.String(), fragment) {
			t.Fatal(fragment, dump.String())
		}
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &again); err != nil || again.String() != dump.String() {
		t.Fatal(again.String(), err)
	}
	for _, script := range []string{"CREATE TABLE replacement(r REAL); INSERT INTO replacement VALUES('1'),('oops')", "CREATE TABLE replacement(r DOUBLE PRECISION); INSERT INTO replacement VALUES('1'),('1e999')"} {
		if err = stuntgres.Restore(ctx, db, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore")
		}
		again.Reset()
		if err = stuntgres.Dump(ctx, db, &again); err != nil || again.String() != dump.String() {
			t.Fatal("restore changed state", err)
		}
	}
	if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(t TEXT); INSERT INTO replacement VALUES('new')")); err != nil {
		t.Fatal(err)
	}
	metadata := held.(driver.RowsColumnTypeDatabaseTypeName)
	if metadata.ColumnTypeDatabaseTypeName(1) != "FLOAT4" || metadata.ColumnTypeDatabaseTypeName(2) != "FLOAT8" {
		t.Fatal("old metadata changed")
	}
	dst := make([]driver.Value, 3)
	for i := 1; i <= 5; i++ {
		if err = held.Next(dst); err != nil || dst[0] != int64(i) {
			t.Fatal(dst, err)
		}
		if i == 1 {
			if dst[1] != float64(33554448) || !math.Signbit(dst[2].(float64)) {
				t.Fatal(dst)
			}
		}
		if i == 2 {
			if math.Float32bits(float32(dst[1].(float64))) != 1 || math.Float64bits(dst[2].(float64)) != 1 {
				t.Fatal(dst)
			}
		}
		if i == 3 && !math.IsNaN(dst[1].(float64)) {
			t.Fatal(dst)
		}
	}
	if err = held.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = stuntgres.Restore(canceled, db, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(canceled, db); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	iteration, cancelIteration := context.WithCancel(ctx)
	cursor, err := raw.(driver.QueryerContext).QueryContext(iteration, "SELECT * FROM replacement", nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelIteration()
	defer cursor.Close()
	if err = cursor.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM replacement", [][]any{{"new"}})
}

func TestFloatConstraintDetailAndAssignments(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "float-constraints")
	execSQL(t, db, "CREATE TABLE nn(r REAL NOT NULL,d REAL)")
	for _, prepared := range []bool{false, true} {
		q := "INSERT INTO nn VALUES('1','1'),(NULL,'33554448')"
		var err error
		if prepared {
			s, e := db.Prepare(q)
			if e != nil {
				t.Fatal(e)
			}
			_, err = s.Exec()
			s.Close()
		} else {
			_, err = db.Exec(q)
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "r" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (null, 3.3554448e+07).", Schema: "public", Table: "nn", Column: "r"})
		wantTuples(t, db, "SELECT * FROM nn", [][]any{})
	}
	execSQL(t, db, "INSERT INTO nn VALUES(16777217,9007199254740993)")
	wantTuples(t, db, "SELECT r FROM nn", [][]any{{float64(16777216)}})
}
