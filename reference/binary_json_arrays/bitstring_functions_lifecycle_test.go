package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func bitStringFunctionsMetadata(t *testing.T, cols []*sql.ColumnType, pg bool) {
	t.Helper()
	names := []string{"f", "v", "s", "o", "c", "b", "l", "n", "p"}
	types := []string{"BIT", "VARBIT", "BIT", "BIT", "INT8", "INT4", "INT4", "INT4", "INT4"}
	if len(cols) != len(names) {
		t.Fatal(cols)
	}
	for i, col := range cols {
		scan := reflect.TypeFor[string]()
		if i >= 4 {
			scan = reflect.TypeFor[int64]()
			if pg && i >= 5 {
				scan = reflect.TypeFor[int32]()
			}
		}
		if col.Name() != names[i] || col.DatabaseTypeName() != types[i] || col.ScanType() != scan {
			t.Errorf("metadata %d: %s %s %v want %s %s %v", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), names[i], types[i], scan)
		}
		width, known := col.Length()
		wantWidth := int64(0)
		wantKnown := i < 4
		if i < 4 {
			wantWidth = []int64{3, 5, -1, -1}[i]
		}
		if width != wantWidth || known != wantKnown {
			t.Errorf("Length %d: %d/%t want %d/%t", i, width, known, wantWidth, wantKnown)
		}
		precision, scale, decimal := col.DecimalSize()
		if precision != 0 || scale != 0 || decimal {
			t.Errorf("DecimalSize %d: %d/%d/%t", i, precision, scale, decimal)
		}
		nullable, nknown := col.Nullable()
		wantNullable := !pg && i < 2
		if nullable != wantNullable || nknown != wantNullable {
			t.Errorf("Nullable %d: %t/%t want %t/%t", i, nullable, nknown, wantNullable, wantNullable)
		}
	}
}

func TestReferenceBitStringFunctionsNullMetadata(t *testing.T) {
	bitStringFunctionsScenario(t, bitStringFunctionsNullMetadata)
}
func bitStringFunctionsNullMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE bsf_meta(id INTEGER,f BIT(3),v VARBIT(5),r "bit",i INTEGER,n INTEGER)`, 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO bsf_meta VALUES(1,B'101',B'10110',B'00',2,2),(2,NULL,NULL,NULL,NULL,NULL),(3,NULL,NULL,B'00',0,-1)", 3))
	q := "SELECT f,v,substring(v FROM i FOR n) AS s,overlay(v PLACING r FROM i FOR n) AS o,bit_count(v) AS c,bit_length(v) AS b,length(v) AS l,octet_length(v) AS n,position(r IN v) AS p FROM bsf_meta WHERE id=$1"
	for _, prepared := range []bool{false, true} {
		for _, id := range []int{1, 2, 3, 99} {
			want := [][]any(nil)
			if id == 1 {
				want = [][]any{{"101", "10110", "01", "10010", int64(3), int64(5), int64(5), int64(1), int64(0)}}
			}
			if id == 2 || id == 3 {
				want = [][]any{{nil, nil, nil, nil, nil, nil, nil, nil, nil}}
			}
			s := fixture.Select(q, []string{"f", "v", "s", "o", "c", "b", "l", "n", "p"}, []string{"BIT", "VARBIT", "BIT", "BIT", "INT8", "INT4", "INT4", "INT4", "INT4"}, want, id)
			s.Prepared = prepared
			var saved []*sql.ColumnType
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				var err error
				saved, err = r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				bitStringFunctionsMetadata(t, saved, b.PG)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			bitStringFunctionsMetadata(t, saved, b.PG)
		}
	}
	for _, id := range []int{1, 2, 99} {
		want := [][]any(nil)
		if id == 1 {
			want = [][]any{{"01", "10010"}}
		}
		if id == 2 {
			want = [][]any{{nil, nil}}
		}
		s := fixture.Select("SELECT substring(v FROM i FOR n) AS s,overlay(v PLACING r FROM i FOR n) AS o FROM bsf_meta WHERE id=$1", []string{"s", "o"}, []string{"BIT", "BIT"}, want, id)
		s.CheckMetadata = bitstringReferenceMetadata(b.PG, []int64{-1, -1}, nil)
		bitStringFunctionsBoth(t, p, b, c, s)
	}
	for _, expr := range []string{
		"bit_count(v)", "bit_length(v)", "length(v)", "octet_length(v)",
		"substring(v FROM 1 FOR -1)", "substring(B'101' FROM NULL)", "substring(B'101' FROM 1 FOR NULL)",
		"overlay(v PLACING B'1' FROM 0)", "overlay(B'1' PLACING v FROM 0)", "overlay(B'1' PLACING B'0' FROM NULL)", "overlay(B'1' PLACING B'0' FROM 1 FOR NULL)", "overlay(v PLACING B'1' FROM 2147483647 FOR 1)",
		"position(v IN B'1')", "position(B'1' IN v)",
	} {
		typ := "BIT"
		switch {
		case len(expr) >= 9 && expr[:9] == "bit_count":
			typ = "INT8"
		case len(expr) >= 8 && expr[:8] == "position", len(expr) >= 6 && expr[:6] == "length", len(expr) >= 10 && expr[:10] == "bit_length", len(expr) >= 12 && expr[:12] == "octet_length":
			typ = "INT4"
		}
		bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT "+expr+" AS result FROM bsf_meta WHERE id=2", []string{"result"}, []string{typ}, [][]any{{nil}}))
	}
}

func TestReferenceBitStringFunctionsHeldStatements(t *testing.T) {
	bitStringFunctionsScenario(t, bitStringFunctionsHeld)
}
func bitStringFunctionsHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE bsf_held(id INTEGER,v VARBIT)", 0))
	run(fixture.Exec("INSERT INTO bsf_held VALUES(1,B'10110')", 1))
	q := "SELECT substring(v FROM $1 FOR $2) AS s,overlay(v PLACING $3 FROM $1 FOR $2) AS o,bit_count(v) AS c FROM bsf_held ORDER BY id"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	check := func(start, count, replacement any, want []any) {
		s := fixture.Select(q, []string{"s", "o", "c"}, []string{"BIT", "BIT", "INT8"}, [][]any{want}, start, count, replacement)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	var retained *sql.Rows
	var descriptors []*sql.ColumnType
	initial := []byte("00")
	if !b.PG {
		retained, err = stmt.QueryContext(p.Ctx, 2, 2, initial)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		descriptors, err = retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
	} else {
		check(2, 2, initial, []any{"01", "10010", int64(3)})
	}
	initial[0] = '1'
	replacement := []byte("11")
	check(1, 1, replacement, []any{"1", "110110", int64(3)})
	replacement[0] = '0'
	check(3, 0, "00", []any{"", "1000110", int64(3)})
	check(nil, 2, "00", []any{nil, nil, int64(3)})
	fixture.RunStep(t, p.Ctx, held, b.Recorder, bitStringFunctionsError(q, "22011", "negative substring length not allowed", 0, false, false, 1, -1, "00"))
	check(2, 2, "00", []any{"01", "10010", int64(3)})
	wq := "UPDATE bsf_held SET v=overlay(v PLACING $1 FROM $2 FOR $3) WHERE id=1"
	write, err := c.PrepareContext(p.Ctx, wq)
	if err != nil {
		t.Fatal(err)
	}
	defer write.Close()
	wh := fixture.HeldStatement{Executor: c, Statement: write, SQL: wq}
	fixture.RunStep(t, p.Ctx, wh, b.Recorder, fixture.Exec(wq, 1, "00", 3, 2))
	bad := bitStringFunctionsError(wq, "22011", "negative substring length not allowed", 0, false, false, "11", 0, 1)
	bad.SelectRows = false
	fixture.RunStep(t, p.Ctx, wh, b.Recorder, bad)
	run(fixture.Select("SELECT v FROM bsf_held", []string{"v"}, []string{"VARBIT"}, [][]any{{"10000"}}))
	fixture.RunStep(t, p.Ctx, wh, b.Recorder, fixture.Exec(wq, 1, "11", 2, 2))
	check(2, 2, "00", []any{"11", "10000", int64(3)})
	fixture.RunStep(t, p.Ctx, wh, b.Recorder, fixture.Exec(wq, 1, nil, 1, 1))
	check(2, 2, "00", []any{nil, nil, nil})
	fixture.RunStep(t, p.Ctx, wh, b.Recorder, fixture.Exec("INSERT INTO bsf_held VALUES(2,B'1')", 1))
	if retained != nil {
		if !retained.Next() {
			t.Fatal(retained.Err())
		}
		var s, o, count any
		if err := retained.Scan(&s, &o, &count); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, false, "BIT", s)
		fixture.AssertRawValue(t, false, "BIT", o)
		if !reflect.DeepEqual([]any{s, o, count}, []any{"01", "10010", int64(3)}) {
			t.Fatal("retained result changed", s, o, count)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained snapshot changed", retained.Err())
		}
		for i, typ := range descriptors {
			scan := reflect.TypeFor[string]()
			if i == 2 {
				scan = reflect.TypeFor[int64]()
			}
			if typ.Name() != []string{"s", "o", "c"}[i] || typ.DatabaseTypeName() != []string{"BIT", "BIT", "INT8"}[i] || typ.ScanType() != scan {
				t.Fatal(typ)
			}
			n, known := typ.Length()
			if i < 2 {
				if !known || n != -1 {
					t.Fatal(n, known)
				}
			} else if known || n != 0 {
				t.Fatal(n, known)
			}
			if n, known := typ.Nullable(); n || known {
				t.Fatal(n, known)
			}
			if p, s, known := typ.DecimalSize(); p != 0 || s != 0 || known {
				t.Fatal(p, s, known)
			}
		}
	}
}
