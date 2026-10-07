package stuntgres_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres"
)

func TestRandomFailedInsertRetainsNestedDraw(t *testing.T) {
	_, c := randomConnection(t)
	ctx := context.Background()
	for _, q := range []string{"CREATE TABLE fail_source(id INT)", "INSERT INTO fail_source VALUES(1)", "CREATE TABLE fail_sink(id INT)", "INSERT INTO fail_sink VALUES(99)"} {
		if _, e := c.ExecContext(ctx, q); e != nil {
			t.Fatal(e)
		}
	}
	randomSeed(t, c, .25)
	if n := randomOne(t, c, "SELECT random(2,3)").(int64); n < 2 || n > 3 {
		t.Fatal(n)
	}
	expected := randomOne(t, c, "SELECT random()")
	q := "INSERT INTO fail_sink SELECT random(random(2,3),1) FROM fail_source"
	s, e := c.PrepareContext(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, prepared := range []bool{false, true} {
		randomSeed(t, c, .25)
		var e error
		if prepared {
			_, e = s.ExecContext(ctx)
		} else {
			_, e = c.ExecContext(ctx, q)
		}
		var got *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "lower bound must be less than or equal to upper bound"}
		if !errors.As(e, &got) || *got != want {
			t.Fatalf("got %#v (%v) want %#v", got, e, want)
		}
		if randomOne(t, c, "SELECT random()") != expected {
			t.Fatal("failed INSERT rewound inner draw")
		}
		rows, e := c.QueryContext(ctx, "SELECT id FROM fail_sink ORDER BY id")
		queryCoreRows(t, rows, e, []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}, false)
	}
}
