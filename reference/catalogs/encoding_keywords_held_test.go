package reference_test

import (
	"context"
	"errors"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceEncodingKeywordsHeld(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name,func(t *testing.T) {
			conn,err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
			for _, c := range []struct{query,label,typ string; values []any; results []any}{
				{"SELECT pg_char_to_encoding($1) AS id","id","INT4",[]any{"UTF8",nil,"unknown","Unicode"},[]any{int64(6),nil,int64(-1),int64(6)}},
				{"SELECT pg_encoding_to_char($1) AS encoding","encoding","NAME",[]any{6,nil,42,7,6},[]any{"UTF8",nil,"","MULE_INTERNAL","UTF8"}},
			} {
				stmt,err := conn.PrepareContext(p.Ctx,c.query); if err != nil { t.Fatal(err) }
				h := fixture.HeldStatement{Executor:conn,Statement:stmt,SQL:c.query}
				for i,v := range c.values { fixture.RunStep(t,p.Ctx,h,b.Recorder,fixture.Select(c.query,[]string{c.label},[]string{c.typ},[][]any{{c.results[i]}},v)) }
				if c.typ == "NAME" {
					s := fixture.ImplicitError(c.query,fixture.Diagnostic{Code:"22P02",Message:`invalid input syntax for type integer: "bad"`,Context:`unnamed portal parameter $1 = '...'`},"bad"); s.SelectRows,s.ErrorStage = true,"execute"
					fixture.RunStep(t,p.Ctx,h,b.Recorder,s)
					fixture.RunStep(t,p.Ctx,h,b.Recorder,fixture.Select(c.query,[]string{c.label},[]string{c.typ},[][]any{{"UTF8"}},6))
				}
				if err := stmt.Close(); err != nil { t.Fatal(err) }
			}
			const query = "SELECT word,catcode,barelabel,catdesc,baredesc FROM pg_get_keywords() ORDER BY word"
			stmt,err := conn.PrepareContext(p.Ctx,query); if err != nil { t.Fatal(err) }; defer stmt.Close()
			first,err := stmt.QueryContext(p.Ctx); if err != nil { t.Fatal(err) }; defer first.Close()
			if !first.Next() { t.Fatal("first invocation empty",first.Err()) }
			second,err := b.DB.QueryContext(p.Ctx,query); if err != nil { t.Fatal(err) }; defer second.Close()
			// Both cursors remain live; complete streams must independently match494.
			read := func(rows interface{ Next() bool; Scan(...any) error; Err() error }, initial bool) [][]any {
				var got [][]any
				for present := initial || rows.Next(); present; present = rows.Next() {
					var word,code,category,bareDescription string; var bare bool
					if err := rows.Scan(&word,&code,&bare,&category,&bareDescription); err != nil { t.Fatal(err) }
					got = append(got,[]any{word,code,bare,category,bareDescription})
				}
				if rows.Err() != nil { t.Fatal(rows.Err()) }; return got
			}
			want := encodingKeywordRows(t)
			for _, got := range [][][]any{read(second,false),read(first,true)} { if _,ok := fixture.CompareRows(got,want,false); !ok { t.Fatal("independent held cursor inventory",got) } }
			second.Close(); first.Close()
			h := fixture.HeldStatement{Executor:conn,Statement:stmt,SQL:query}
			for range 2 { fixture.RunStep(t,p.Ctx,h,b.Recorder,fixture.Select(query,encodingKeywordColumns,encodingKeywordTypes,want)) }
			partial,err := stmt.QueryContext(p.Ctx); if err != nil { t.Fatal(err) }
			if !partial.Next() { t.Fatal(partial.Err()) }; if err := partial.Close(); err != nil { t.Fatal(err) }
			fixture.RunStep(t,p.Ctx,h,b.Recorder,fixture.Select(query,encodingKeywordColumns,encodingKeywordTypes,want))
			canceled,cancel := context.WithCancel(p.Ctx); cancel()
			rows,err := stmt.QueryContext(canceled)
			if rows != nil { rows.Close() }
			// pq can finish a fast command despite a pre-canceled client context.
			// Native admission must still return the ordinary context error.
			if !b.PG && !errors.Is(err,context.Canceled) { t.Fatal("pre-canceled held invocation",err) }
			if b.PG && err != nil && !errors.Is(err,context.Canceled) { t.Fatal("pre-canceled pq invocation",err) }
			fixture.RunStep(t,p.Ctx,h,b.Recorder,fixture.Select(query,encodingKeywordColumns,encodingKeywordTypes,want))
		})
	}
}
