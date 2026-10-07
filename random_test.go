package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres"
)

func randomOne(t *testing.T, c *sql.Conn, q string, args ...any) any {
	t.Helper()
	var v any
	if err := c.QueryRowContext(context.Background(), q, args...).Scan(&v); err != nil {
		t.Fatal(q, err)
	}
	return v
}
func randomSeed(t *testing.T, c *sql.Conn, seed float64) {
	t.Helper()
	if v := randomOne(t, c, "SELECT setseed($1)", seed); v != "" {
		t.Fatalf("nonnull VOID: %T %v", v, v)
	}
}
func randomConnection(t *testing.T) (*sql.DB, *sql.Conn) {
	t.Helper()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return db, c
}

func TestRandomRangesAndMetadata(t *testing.T) {
	_, c := randomConnection(t)
	for _, tc := range []struct {
		q, typ string
		want   any
	}{
		{"SELECT random()", "FLOAT8", nil},
		{"SELECT random(7,7)", "INT4", int64(7)},
		{"SELECT random(2147483648,2147483648)", "INT8", int64(2147483648)},
		{"SELECT random(1.0,1.000)", "NUMERIC", "1.000"},
		{"SELECT random(0.00,0.000)", "NUMERIC", "0.000"},
		{"SELECT random_normal(5,0)", "FLOAT8", float64(5)},
		{"SELECT setseed(0)", "VOID", ""},
		{"SELECT setseed(NULL)", "VOID", nil},
	} {
		t.Run(tc.q, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if prepared {
					s, e := c.PrepareContext(context.Background(), tc.q)
					if e != nil {
						t.Fatal(e)
					}
					defer s.Close()
					rows, err = s.Query()
				} else {
					rows, err = c.QueryContext(context.Background(), tc.q)
				}
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != tc.typ {
					t.Fatal(types, err)
				}
				if tc.typ == "VOID" && types[0].ScanType() != reflect.TypeFor[string]() {
					t.Fatal("VOID ScanType", types[0].ScanType())
				}
				if !rows.Next() {
					t.Fatal("missing row", rows.Err())
				}
				var v any
				if err = rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
				if tc.q == "SELECT random()" {
					x, ok := v.(float64)
					if !ok || math.IsNaN(x) || x < 0 || x >= 1 {
						t.Fatal(v)
					}
				} else if !reflect.DeepEqual(v, tc.want) {
					t.Fatalf("got %T %v want %T %v", v, v, tc.want, tc.want)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("extra row or failure", rows.Err())
				}
				rows.Close()
			}
		})
	}
	for _, q := range []string{"SELECT random(-2147483648,2147483647)", "SELECT random(-9223372036854775808,9223372036854775807)"} {
		if _, ok := randomOne(t, c, q).(int64); !ok {
			t.Fatal("integer domain result")
		}
	}
	for range 32 {
		v := randomOne(t, c, "SELECT random(-7,3)").(int64)
		if v < -7 || v > 3 {
			t.Fatal(v)
		}
	}
	for range 16 {
		v := randomOne(t, c, "SELECT random(100000000000000000000.000,100000000000000000000.002)")
		if v != "100000000000000000000.000" && v != "100000000000000000000.001" && v != "100000000000000000000.002" {
			t.Fatal(v)
		}
	}
}

func TestRandomSeedDefaultsAndStrictness(t *testing.T) {
	_, c := randomConnection(t)
	series := func() []any {
		return []any{randomOne(t, c, "SELECT random()"), randomOne(t, c, "SELECT random(-7,3)"), randomOne(t, c, "SELECT random(2147483648,2147483650)"), randomOne(t, c, "SELECT random(0.000,0.999)"), randomOne(t, c, "SELECT random_normal()"), randomOne(t, c, "SELECT random_normal(5)"), randomOne(t, c, "SELECT random_normal(5,2)")}
	}
	randomSeed(t, c, .25)
	a := series()
	randomSeed(t, c, .25)
	b := series()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("reseed mixed stream", a, b)
	}
	for _, pair := range [][2]string{{"SELECT random_normal()", "SELECT random_normal(0,1)"}, {"SELECT random_normal(5)", "SELECT random_normal(5,1)"}} {
		randomSeed(t, c, .25)
		x := randomOne(t, c, pair[0])
		randomSeed(t, c, .25)
		y := randomOne(t, c, pair[1])
		if x != y {
			t.Fatal("defaults", x, y)
		}
	}
	randomSeed(t, c, .25)
	first := randomOne(t, c, "SELECT random()")
	second := randomOne(t, c, "SELECT random()")
	randomSeed(t, c, .25)
	if v := randomOne(t, c, "SELECT random_normal(NULL,random())"); v != nil {
		t.Fatal(v)
	}
	if v := randomOne(t, c, "SELECT random()"); v != first {
		t.Fatal("literal NULL consumed sibling", v, first)
	}
	randomSeed(t, c, .25)
	if v := randomOne(t, c, "SELECT random_normal(NULL::int4,random())"); v != nil {
		t.Fatal(v)
	}
	if v := randomOne(t, c, "SELECT random()"); v != first {
		t.Fatal("typed literal NULL consumed sibling", v, first)
	}
	if _, e := c.ExecContext(context.Background(), "CREATE TABLE null_inputs(id INT,mean DOUBLE PRECISION)"); e != nil {
		t.Fatal(e)
	}
	if _, e := c.ExecContext(context.Background(), "INSERT INTO null_inputs VALUES(1,NULL),(2,1)"); e != nil {
		t.Fatal(e)
	}
	randomSeed(t, c, .25)
	if v := randomOne(t, c, "SELECT random_normal(mean,random()) FROM null_inputs WHERE id=1"); v != nil {
		t.Fatal(v)
	}
	if v := randomOne(t, c, "SELECT random()"); v != second {
		t.Fatal("runtime NULL skipped sibling", v, second)
	}
}

func TestRandomSessionIsolationAndRollback(t *testing.T) {
	db, a := randomConnection(t)
	b, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	randomSeed(t, a, .25)
	first := randomOne(t, a, "SELECT random()")
	second := randomOne(t, a, "SELECT random()")
	randomSeed(t, a, .25)
	randomSeed(t, b, .25)
	if randomOne(t, b, "SELECT random()") != first {
		t.Fatal("session seed")
	}
	randomOne(t, a, "SELECT random()")
	randomSeed(t, a, -1)
	if randomOne(t, b, "SELECT random()") != second {
		t.Fatal("cross-session mutation")
	}
	randomSeed(t, a, .25)
	tx, err := a.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var draw float64
	if err = tx.QueryRow("SELECT random()").Scan(&draw); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if randomOne(t, a, "SELECT random()") != second {
		t.Fatal("rollback rewound generator")
	}
}

func TestRandomRuntimeDiagnostics(t *testing.T) {
	_, c := randomConnection(t)
	for _, tc := range []struct{ q, message string }{
		{"SELECT setseed(1.23456789)", "setseed parameter 1.23457 is out of allowed range [-1,1]"},
		{"SELECT setseed(1e20)", "setseed parameter 1e+20 is out of allowed range [-1,1]"},
		{"SELECT setseed(-1.0000000000000002)", "setseed parameter -1 is out of allowed range [-1,1]"},
		{"SELECT setseed(0.00000123456789e20)", "setseed parameter 1.23457e+14 is out of allowed range [-1,1]"},
		{"SELECT setseed('NaN')", "setseed parameter NaN is out of allowed range [-1,1]"},
		{"SELECT setseed('Infinity')", "setseed parameter Infinity is out of allowed range [-1,1]"},
		{"SELECT setseed('-Infinity')", "setseed parameter -Infinity is out of allowed range [-1,1]"},
		{"SELECT random(2,1)", "lower bound must be less than or equal to upper bound"},
		{"SELECT random('NaN',1.0)", "lower bound cannot be NaN"},
		{"SELECT random(0.0,'Infinity')", "upper bound cannot be infinity"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				randomSeed(t, c, .25)
				wantDraw := randomOne(t, c, "SELECT random()")
				randomSeed(t, c, .25)
				var err error
				if prepared {
					s, e := c.PrepareContext(context.Background(), tc.q)
					if e != nil {
						t.Fatal("volatile runtime validation ran during prepare", e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = c.ExecContext(context.Background(), tc.q)
				}
				var got *stuntgres.Error
				want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: tc.message}
				if !errors.As(err, &got) || *got != want {
					t.Fatalf("got %#v (%v) want %#v", got, err, want)
				}
				if v := randomOne(t, c, "SELECT random()"); v != wantDraw {
					t.Fatal("invalid call consumed state", v, wantDraw)
				}
			}
		})
	}
}
