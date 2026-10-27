package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreRowsValues(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRowsSeed(t, p, b, c)
		for _, name := range []string{"skeys", "svals", "each"} {
			for _, input := range []struct{ value any; rows [][]any }{
				{hstoreRowsInput, hstoreRowsValues(name)}, {"", [][]any{}}, {nil, [][]any{}},
			} {
				hstoreRowsBoth(t, p, b, c, hstoreRowsSelect("SELECT * FROM "+name+"($1::hstore)", hstoreRowsNames(name), input.rows, input.value))
			}
			if name != "each" {
				hstoreRowsBoth(t, p, b, c, hstoreRowsSelect("SELECT "+name+"($1::hstore)", []string{name}, hstoreRowsValues(name), hstoreRowsInput))
			}
		}
		checks := []fixture.Step{
			hstoreRowsSelect(`SELECT (each($1::hstore)).key,(each($1::hstore)).value`, []string{"key", "value"}, hstoreRowsPairs(), hstoreRowsInput),
			hstoreRowsSelect(`SELECT key FROM each($1::hstore) WHERE value IS NULL`, []string{"key"}, [][]any{{"b"}}, hstoreRowsInput),
			hstoreRowsSelect(`SELECT key FROM each($1::hstore) WHERE value='x'`, []string{"key"}, [][]any{{"a"}, {"d"}}, hstoreRowsInput),
			hstoreRowsSelect(`SELECT value FROM each($1::hstore) WHERE key='c'`, []string{"value"}, [][]any{{"NULL"}}, hstoreRowsInput),
			hstoreRowsSelect(`SELECT key,value FROM each($1::hstore) WHERE key='' AND value=''`, []string{"key", "value"}, [][]any{{"", ""}}, hstoreRowsInput),
			hstoreRowsSelect(`SELECT * FROM each('""=>""'::hstore)`, []string{"key", "value"}, [][]any{{"", ""}}),
			hstoreRowsSelect(`SELECT * FROM svals('a=>NULL'::hstore)`, []string{"svals"}, [][]any{{nil}}),
			hstoreRowsSelect(`SELECT * FROM each('a=>NULL'::hstore)`, []string{"key", "value"}, [][]any{{"a", nil}}),
			hstoreRowsSelect(`SELECT * FROM each($1::hstore)`, []string{"key", "value"}, [][]any{{"é\\\"", "雪\\\""}}, hstoreRowsEscaped),
			hstoreRowsSelect(`SELECT * FROM each($1::hstore) AS e(k,v)`, []string{"k", "v"}, hstoreRowsPairs(), hstoreRowsInput),
			hstoreRowsSelect(`SELECT svals('a=>x,b=>x'::hstore),skeys('z=>q'::hstore)`, []string{"svals", "skeys"}, [][]any{{"x", "z"}, {"x", nil}}),
			hstoreRowsSelect(`SELECT skeys(''::hstore),svals(NULL::hstore)`, []string{"skeys", "svals"}, [][]any{}),
			hstoreRowsSelect(`SELECT skeys(v) FROM hs_rows WHERE id=3`, []string{"skeys"}, [][]any{}),
			hstoreRowsSelect(`SELECT svals(v) FROM hs_rows WHERE id=4`, []string{"svals"}, [][]any{{nil}}),
			hstoreRowsSelect(`SELECT e.key,e.value FROM hs_rows h CROSS JOIN LATERAL each(h.v) e WHERE h.id=1`, []string{"key", "value"}, hstoreRowsPairs()),
		}
		for _, s := range checks {
			hstoreRowsBoth(t, p, b, c, s)
		}
		for _, tc := range []struct{ query string; names, types []string; want [][]any }{
			{`SELECT id,skeys(v) AS k FROM hs_rows WHERE id<5`, []string{"id","k"}, []string{"INT4","TEXT"}, [][]any{{int64(1),"a"},{int64(1),"b"},{int64(1),"c"},{int64(1),""},{int64(1),"d"},{int64(4),"a"}}},
			{`SELECT id,svals(v) AS v FROM hs_rows WHERE id<5`, []string{"id","v"}, []string{"INT4","TEXT"}, [][]any{{int64(1),"x"},{int64(1),nil},{int64(1),"NULL"},{int64(1),""},{int64(1),"x"},{int64(4),nil}}},
			{`SELECT h.id,e.key,e.value FROM hs_rows h CROSS JOIN LATERAL each(h.v) e WHERE h.id<5`, []string{"id","key","value"}, []string{"INT4","TEXT","TEXT"}, [][]any{{int64(1),"a","x"},{int64(1),"b",nil},{int64(1),"c","NULL"},{int64(1),"",""},{int64(1),"d","x"},{int64(4),"a",nil}}},
		} {
			s:=fixture.Select(tc.query,tc.names,tc.types,tc.want);s.Bag=true
			hstoreRowsBoth(t,p,b,c,s)
		}
		// Composite output is RECORD, not a text array or only its first pair.
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(`SELECT each($1::hstore) AS pair`, []string{"pair"}, []string{"RECORD"}, [][]any{{"(a,x)"}, {"(b,)"}, {"(c,NULL)"}, {`("","")`}, {"(d,x)"}}, hstoreRowsInput)
			s.Bag = true
			if prepared { s = fixture.Prepared(s) }
			hstoreRun(t, p, b, c, s)
		}
		// Ordinal values are checked independently from pair order.
		s := fixture.Select(`SELECT key,value,ordinality FROM each($1::hstore) WITH ORDINALITY`, []string{"key", "value", "ordinality"}, []string{"TEXT", "TEXT", "INT8"}, nil, hstoreRowsInput)
		// Read the unordered payload separately: no association of a key with a
		// particular ordinal is portable SQL behavior.
		rows, err := c.QueryContext(p.Ctx, s.Query, s.Args...)
		if err != nil { t.Fatal(err) }
		defer rows.Close()
		columns, err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }
		if len(columns)!=3 || columns[2].Name()!="ordinality" || columns[2].DatabaseTypeName()!="INT8" { t.Fatalf("ordinal metadata %#v", columns) }
		hstoreRowsTextMetadata(t, columns[:2], []string{"key", "value"})
		var payload [][]any
		var ordinal int64
		for rows.Next() {
			var key string; var value sql.NullString; var n int64
			if err := rows.Scan(&key,&value,&n); err != nil { t.Fatal(err) }
			ordinal++
			if n!=ordinal { t.Fatalf("ordinal %d want %d",n,ordinal) }
			var v any; if value.Valid { v=value.String }
			payload=append(payload,[]any{key,v})
		}
		if err:=rows.Err(); err!=nil { t.Fatal(err) }
		if err:=rows.Close(); err!=nil { t.Fatal(err) }
		hstoreRowsAssertBag(t,payload,hstoreRowsPairs())
		// Conflicting duplicate keys have one original survivor; never compare
		// that arbitrary choice to the other backend's choice.
		rows,err=c.QueryContext(p.Ctx,`SELECT key,value FROM each('a=>first,a=>second'::hstore)`)
		if err!=nil { t.Fatal(err) }; defer rows.Close()
		if !rows.Next() { t.Fatalf("missing duplicate survivor: %v",rows.Err()) }
		var key,value string
		if err:=rows.Scan(&key,&value); err!=nil { t.Fatal(err) }
		if key!="a" || (value!="first" && value!="second") { t.Fatalf("invalid survivor %q/%q",key,value) }
		if rows.Next() || rows.Err()!=nil { t.Fatalf("duplicate key produced extra rows: %v",rows.Err()) }
	})
}

func hstoreRowsAssertBag(t *testing.T, got,want [][]any) {
	t.Helper()
	if _,ok:=fixture.CompareRows(got,want,true);!ok {
		t.Fatalf("bag %#v want %#v",got,want)
	}
}
