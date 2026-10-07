package stuntgres_test

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"stuntgres"
)

func TestRandomGroupedKeysAndPreflight(t *testing.T) {
	_, c := randomConnection(t)
	ctx := context.Background()
	for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(1),(2),(3)", "CREATE TABLE empty(id INT)"} {
		if _, e := c.ExecContext(ctx, q); e != nil {
			t.Fatal(e)
		}
	}
	randomSeed(t, c, .25)
	counts := map[int64]int64{}
	for range 3 {
		counts[randomOne(t, c, "SELECT random(0,2147483647)").(int64)]++
	}
	continuation := randomOne(t, c, "SELECT random()")
	randomSeed(t, c, .25)
	s, e := c.PrepareContext(ctx, "SELECT random(0,2147483647),count(*) FROM t GROUP BY 1")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for range 2 {
		randomSeed(t, c, .25)
		rows, e := s.QueryContext(ctx)
		if e != nil {
			t.Fatal(e)
		}
		got := map[int64]int64{}
		for rows.Next() {
			var key, count int64
			if e = rows.Scan(&key, &count); e != nil {
				t.Fatal(e)
			}
			got[key] += count
		}
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		rows.Close()
		if len(got) != len(counts) {
			t.Fatal(got, counts)
		}
		for key, count := range counts {
			if got[key] != count {
				t.Fatal(got, counts)
			}
		}
		if randomOne(t, c, "SELECT random()") != continuation {
			t.Fatal("projected group key redrawn")
		}
	}
	randomSeed(t, c, .25)
	first := randomOne(t, c, "SELECT random()")
	randomSeed(t, c, .25)
	_, e = c.QueryContext(ctx, "SELECT random(CAST('oops' AS int4),1) FROM empty")
	var got *stuntgres.Error
	if !errors.As(e, &got) || got.Code != "22P02" || got.Message != `invalid input syntax for type integer: "oops"` {
		t.Fatal("immutable child preflight", e)
	}
	if randomOne(t, c, "SELECT random()") != first {
		t.Fatal("preflight invoked volatile parent")
	}
	if v := randomOne(t, c, "SELECT random(CASE WHEN TRUE THEN 7 ELSE 1/0 END,7) FROM t WHERE id=1"); v != int64(7) {
		t.Fatal("unchosen branch evaluated", v)
	}
	if _, e = c.ExecContext(ctx, "CREATE TABLE defaults(v INT DEFAULT random(NULL,1))"); e == nil {
		t.Fatal("strict NULL bypassed DEFAULT exclusion")
	}
}

func TestRandomPublicNumericExtremesAndNegativeScale(t *testing.T) {
	_, c := randomConnection(t)
	ctx := context.Background()
	if _, e := c.ExecContext(ctx, "CREATE TABLE extremes(id INT,lo NUMERIC,hi NUMERIC)"); e != nil {
		t.Fatal(e)
	}
	for i, value := range []string{"0." + strings.Repeat("0", 16382) + "1", "1" + strings.Repeat("0", 131071)} {
		if _, e := c.ExecContext(ctx, "INSERT INTO extremes VALUES($1,$2,$3)", i+1, value, value); e != nil {
			t.Fatal("numeric extreme input", e)
		}
		var v string
		if e := c.QueryRowContext(ctx, "SELECT random(lo,hi) FROM extremes WHERE id=$1", i+1).Scan(&v); e != nil {
			t.Fatal("numeric extreme", e)
		}
		if v != value {
			t.Fatal("numeric extreme changed")
		}
	}
	for _, q := range []string{"CREATE TABLE n(lo NUMERIC(2,-3),hi NUMERIC(2,-3))", "INSERT INTO n VALUES(1500,1500)"} {
		if _, e := c.ExecContext(ctx, q); e != nil {
			t.Fatal(e)
		}
	}
	if v := randomOne(t, c, "SELECT random(lo,hi) FROM n"); v != "2000" {
		t.Fatal("source typmod/scale", v)
	}
}

func TestRandomSignatureDiagnostics(t *testing.T) {
	_, c := randomConnection(t)
	ctx := context.Background()
	if _, e := c.ExecContext(ctx, "CREATE TABLE signatures(f DOUBLE PRECISION,s SMALLINT)"); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ q, code, message, hint, pos string }{
		{"SELECT random(f,f) FROM signatures", "42883", "function random(double precision, double precision) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "8"},
		{"SELECT random('1','1')", "42725", "function random(unknown, unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts.", "8"},
		{"SELECT random(s,s) FROM signatures", "42725", "function random(smallint, smallint) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts.", "8"},
		{"SELECT random($1,$2)", "42725", "function random(unknown, unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts.", "8"},
		{"/*é*/ SELECT random(1)", "42883", "function random(integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "14"},
		{"SELECT random_normal(1,TRUE)", "42883", "function random_normal(integer, boolean) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "8"},
	} {
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.pos}
		for _, prepare := range []bool{false, true} {
			var e error
			if prepare {
				s, err := c.PrepareContext(ctx, tc.q)
				if s != nil {
					s.Close()
				}
				e = err
			} else {
				_, e = c.ExecContext(ctx, tc.q)
			}
			var got *stuntgres.Error
			if !errors.As(e, &got) || *got != want {
				t.Fatalf("%s got %#v (%v) want %#v", tc.q, got, e, want)
			}
		}
	}
}

func TestRandomVoidExecAndPreparedStream(t *testing.T) {
	_, c := randomConnection(t)
	ctx := context.Background()
	randomSeed(t, c, .25)
	expected := randomOne(t, c, "SELECT random()")
	s, e := c.PrepareContext(ctx, "SELECT setseed($1)")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	draw, e := c.PrepareContext(ctx, "SELECT random()")
	if e != nil {
		t.Fatal(e)
	}
	defer draw.Close()
	for range 2 {
		result, e := s.ExecContext(ctx, .25)
		if e != nil {
			t.Fatal(e)
		}
		if n, e := result.RowsAffected(); e != nil || n != 1 {
			t.Fatal(n, e)
		}
		var value any
		if e = draw.QueryRowContext(ctx).Scan(&value); e != nil || value != expected {
			t.Fatal("prepared Exec seed effect", value, expected, e)
		}
	}
}

func TestRandomNumericNegativeRangeScale(t *testing.T) {
	_, c := randomConnection(t)
	for range 16 {
		text := randomOne(t, c, "SELECT random(-0.499,0.499)").(string)
		value, ok := new(big.Rat).SetString(text)
		dot := strings.IndexByte(text, '.')
		if !ok || dot < 0 || len(text)-dot-1 != 3 || value.Cmp(big.NewRat(-499, 1000)) < 0 || value.Cmp(big.NewRat(499, 1000)) > 0 {
			t.Fatal("exact negative/cross-zero lattice", text)
		}
	}
}

func TestRandomCountStageEmptyAndGroups(t *testing.T) {
	_, c := randomConnection(t)
	ctx := context.Background()
	for _, q := range []string{"CREATE TABLE counts(id INT)", "INSERT INTO counts VALUES(1),(2),(2)", "CREATE TABLE no_counts(id INT)"} {
		if _, e := c.ExecContext(ctx, q); e != nil {
			t.Fatal(q, e)
		}
	}
	randomSeed(t, c, .25)
	first := randomOne(t, c, "SELECT random()")
	for _, tc := range []struct {
		q            string
		names, types []string
		want         [][]any
	}{
		{"SELECT random(count(*),count(*)) FROM counts", []string{"random"}, []string{"INT8"}, [][]any{{int64(3)}}},
		{"SELECT random(count(*),count(*)) FROM no_counts", []string{"random"}, []string{"INT8"}, [][]any{{int64(0)}}},
		{"SELECT id,random(count(*),count(*)) FROM counts GROUP BY id ORDER BY id", []string{"id", "random"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}},
	} {
		for _, prepared := range []bool{false, true} {
			randomSeed(t, c, .25)
			if prepared {
				s, e := c.PrepareContext(ctx, tc.q)
				if e != nil {
					t.Fatal(e)
				}
				rows, e := s.QueryContext(ctx)
				queryCoreRows(t, rows, e, tc.names, tc.types, tc.want, false)
				s.Close()
			} else {
				rows, e := c.QueryContext(ctx, tc.q)
				queryCoreRows(t, rows, e, tc.names, tc.types, tc.want, false)
			}
			if randomOne(t, c, "SELECT random()") != first {
				t.Fatal("equal aggregate bounds consumed draw")
			}
		}
	}
}
