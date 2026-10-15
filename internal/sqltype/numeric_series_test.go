package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/sqlstate"
)

func numericSeriesOpenUnit(t *testing.T, args ...string) NumericSeriesCursor {
	t.Helper()
	d := testTypes()[Numeric]
	values := make([]Value, len(args))
	for i, s := range args {
		if s == "NULL" {
			values[i] = Null(d)
		} else {
			values[i] = numericAvgInput(t, d, s)
		}
	}
	cursor, err := OpenNumericSeries(context.Background(), values[0], values[1], values[2:]...)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

func TestNumericSeriesCursor(t *testing.T) {
	for _, tc := range []struct{ args, want []string }{
		{[]string{"0.1", "0.3", "0.1"}, []string{"0.1", "0.2", "0.3"}},
		{[]string{"0.3", "0.1", "-0.1"}, []string{"0.3", "0.2", "0.1"}},
		{[]string{"0.1", "0.6", "0.2"}, []string{"0.1", "0.3", "0.5"}},
		{[]string{"0.10", "2.100"}, []string{"0.10", "1.10", "2.10"}},
		{[]string{"1", "3.000", "1.00"}, []string{"1", "2.00", "3.00"}},
		{[]string{"0.1", "0.3", "0.10"}, []string{"0.1", "0.20", "0.30"}},
		{[]string{"-0.10", "0.10", "0.1"}, []string{"-0.10", "0.00", "0.10"}},
		{[]string{"2", "1", "0.1"}, nil},
		{[]string{"NULL", "NaN", "0"}, nil},
	} {
		t.Run(strings.Join(tc.args, ","), func(t *testing.T) {
			cursor := numericSeriesOpenUnit(t, tc.args...)
			var got []string
			var retained []Value
			for {
				v, ok, err := cursor.Next(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					if !v.IsNull() {
						t.Fatal(v)
					}
					break
				}
				got = append(got, numericOutput(v))
				retained = append(retained, v)
				if len(got) > 10 {
					t.Fatal("unbounded cursor")
				}
			}
			cursor.Close()
			cursor.Close()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatal(got, tc.want)
			}
			for i, v := range retained {
				if numericOutput(v) != got[i] {
					t.Fatal("mutated publication")
				}
			}
			if len(cursor.current.Bits()) != 0 || len(cursor.stop.Bits()) != 0 || len(cursor.step.Bits()) != 0 || len(cursor.publication.coefficient.Bits()) != 0 {
				t.Fatal("retained scratch")
			}
		})
	}
	// Independently opened invocations never share mutable coefficients.
	var wg sync.WaitGroup
	for range 2 {
		cursor := numericSeriesOpenUnit(t, "-0.1", "0.1", "0.1")
		wg.Go(func() {
			defer cursor.Close()
			for _, want := range []string{"-0.1", "0.0", "0.1"} {
				v, ok, err := cursor.Next(context.Background())
				if err != nil || !ok || numericOutput(v) != want {
					t.Error(v, ok, err)
				}
			}
		})
	}
	wg.Wait()
}

func TestNumericSeriesSpecials(t *testing.T) {
	d := testTypes()[Numeric]
	for role, name := range []string{"start value", "stop value", "step size"} {
		for _, special := range []string{"NaN", "Infinity", "-Infinity", "0"} {
			if special == "0" && role != 2 {
				continue
			}
			args := []Value{numericAvgInput(t, d, "1"), numericAvgInput(t, d, "2"), numericAvgInput(t, d, "1")}
			args[role] = numericAvgInput(t, d, special)
			_, err := OpenNumericSeries(context.Background(), args[0], args[1], args[2])
			message := name + " cannot be infinity"
			if special == "NaN" {
				message = name + " cannot be NaN"
			}
			if special == "0" {
				message = "step size cannot equal zero"
			}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != message {
				t.Fatal(err, message)
			}
		}
	}
	_, err := OpenNumericSeries(context.Background(), numericAvgInput(t, d, "NaN"), numericAvgInput(t, d, "Infinity"), numericAvgInput(t, d, "0"))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Message != "start value cannot be NaN" {
		t.Fatal(err)
	}
	copyDescriptor := *d
	if _, err := OpenNumericSeries(context.Background(), Null(d), Null(&copyDescriptor)); err == nil {
		t.Fatal("descriptor mismatch")
	}
	v := numericAvgInput(t, d, "1")
	if _, err := OpenNumericSeries(context.Background(), v, v, v, v); err == nil {
		t.Fatal("invalid arity")
	}
}

func TestNumericSeriesSuccessor(t *testing.T) {
	for _, args := range [][]string{{"9e131071", "9.5e131071", "9e131071"}, {"-9e131071", "-9.5e131071", "-9e131071"}} {
		cursor := numericSeriesOpenUnit(t, args...)
		v, ok, err := cursor.Next(context.Background())
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		want := "9" + strings.Repeat("0", 131071)
		if args[0][0] == '-' {
			want = "-" + want
		}
		if numericOutput(v) != want {
			t.Fatal("incorrect extreme publication")
		}
		if _, ok, err := cursor.Next(context.Background()); err != nil || ok {
			t.Fatal(ok, err)
		}
		cursor.Close()
	}
	cursor := numericSeriesOpenUnit(t, "1e-16383", "3e-16383", "1e-16383")
	defer cursor.Close()
	for _, digit := range []string{"1", "2", "3"} {
		v, ok, err := cursor.Next(context.Background())
		if err != nil || !ok || numericOutput(v) != "0."+strings.Repeat("0", 16382)+digit {
			t.Fatal("fine value", ok, err)
		}
	}
}

func TestNumericSeriesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, args := range [][]string{{"NULL", "1", "0"}, {"1", "1", "1"}, {"2", "1", "1"}} {
		cursor := numericSeriesOpenUnit(t, args...)
		for range 2 {
			_, _, err := cursor.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := cursor.Next(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		cursor.Close()
		if _, _, err := cursor.Next(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	d := testTypes()[Numeric]
	if _, err := OpenNumericSeries(ctx, Null(d), Null(d)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var unopened NumericSeriesCursor
	if _, _, err := unopened.Next(context.Background()); err == nil {
		t.Fatal("unopened cursor")
	}
}
