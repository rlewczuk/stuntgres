package reference_test

import (
	"bytes"
	"math"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMathScalarNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE source(n NUMERIC,d DOUBLE PRECISION)", 0),
		fixture.Exec("INSERT INTO source VALUES(-17.400,'-0.5'),(NULL,NULL)", 2),
		fixture.Exec("CREATE TABLE stored(n NUMERIC,d DOUBLE PRECISION)", 0),
		fixture.Exec("INSERT INTO stored SELECT abs(n),ceil(d) FROM source", 2),
		fixture.Exec("CREATE VIEW scalar_view AS SELECT ceil(n) AS c,ceiling(n) AS cc,floor(n) AS f,div(n,4) AS q,exp(0.0) AS e,factorial(5) AS fac FROM source", 0),
	} {
		p.Step(t, step)
	}
	stored := fixture.Select("SELECT n,d FROM stored", []string{"n", "d"}, []string{"NUMERIC", "FLOAT8"}, [][]any{{"17.400", math.Copysign(0, -1)}, {nil, nil}})
	stored.Bag = true
	p.Step(t, stored)
	p.Step(t, fixture.Prepared(stored))
	view := fixture.Select("SELECT c,cc,f,q,e,fac FROM scalar_view", []string{"c", "cc", "f", "q", "e", "fac"}, []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{"-17", "-17", "-18", "-4", "1.0000000000000000", "120"}, {nil, nil, nil, nil, "1.0000000000000000", "120"}})
	view.Bag = true
	p.Step(t, view)
	p.Step(t, fixture.Prepared(view))
	held, err := p.Native.QueryContext(p.Ctx, "SELECT n,d FROM stored")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var n string
	var d float64
	if err := p.Native.QueryRowContext(p.Ctx, "SELECT n,d FROM stored").Scan(&n, &d); err != nil || n != "17.400" || d != 0 || !math.Signbit(d) {
		t.Fatal(n, d, err)
	}
	var c, cc, f, q, e, fac string
	if err := p.Native.QueryRowContext(p.Ctx, "SELECT c,cc,f,q,e,fac FROM scalar_view").Scan(&c, &cc, &f, &q, &e, &fac); err != nil || c != "-17" || cc != "-17" || f != "-18" || q != "-4" || e != "1.0000000000000000" || fac != "120" {
		t.Fatal(c, cc, f, q, e, fac, err)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	if err := held.Scan(&n, &d); err != nil || n != "17.400" || d != 0 || !math.Signbit(d) {
		t.Fatal(n, d, err)
	}
	ts, err := held.ColumnTypes()
	if err != nil || ts[0].DatabaseTypeName() != "NUMERIC" || ts[1].DatabaseTypeName() != "FLOAT8" {
		t.Fatal(ts, err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("deterministic replay", err)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var nullN, nullD any
	if err := held.Scan(&nullN, &nullD); err != nil || nullN != nil || nullD != nil {
		t.Fatal(nullN, nullD, err)
	}
	if held.Next() || held.Err() != nil {
		t.Fatal("held scalar end", held.Err())
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
}
