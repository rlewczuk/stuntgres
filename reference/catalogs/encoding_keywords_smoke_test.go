package reference_test

import (
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceEncodingKeywordsFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, prepared := range []bool{false,true} {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT pg_char_to_encoding('UTF8') AS id,pg_encoding_to_char(pg_char_to_encoding('UTF8')) AS encoding,pg_char_to_encoding('not-an-encoding') AS missing,pg_encoding_to_char(42) AS invalid,pg_char_to_encoding(NULL::name) AS null_id",[]string{"id","encoding","missing","invalid","null_id"},[]string{"INT4","NAME","INT4","NAME","INT4"},[][]any{{int64(6),"UTF8",int64(-1),"",nil}}),
			fixture.Select("SELECT word,catcode,barelabel,catdesc,baredesc FROM pg_get_keywords() WHERE word IN ('char','from','select','virtual') ORDER BY word",encodingKeywordColumns,encodingKeywordTypes,[][]any{
				{"char","C",false,"unreserved (cannot be function or type name)","requires AS"},
				{"from","R",false,"reserved","requires AS"},
				{"select","R",true,"reserved","can be bare label"},
				{"virtual","U",true,"unreserved","can be bare label"},
			}),
			fixture.Select("SELECT * FROM pg_get_keywords() WHERE false",encodingKeywordColumns,encodingKeywordTypes,nil),
		} { s.Prepared = prepared; p.Step(t,s) }
	}
	for _, b := range p.Backends() {
		t.Run(b.Name,func(t *testing.T) {
			const query = "SELECT count(*) AS total,min(ordinality) AS first,max(ordinality) AS last FROM pg_get_keywords() WITH ORDINALITY"
			stmt,err := b.DB.PrepareContext(p.Ctx,query); if err != nil { t.Fatal(err) }; defer stmt.Close()
			h := fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:query}
			for range 2 { fixture.RunStep(t,p.Ctx,h,b.Recorder,fixture.Select(query,[]string{"total","first","last"},[]string{"INT8","INT8","INT8"},[][]any{{int64(494),int64(1),int64(494)}})) }
			s := fixture.ImplicitError("SELECT pg_encoding_to_char(true)",fixture.Diagnostic{Code:"42883",Message:"function pg_encoding_to_char(boolean) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(8)}); s.SelectRows = true
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
			fixture.RunStep(t,p.Ctx,h,b.Recorder,fixture.Select(query,[]string{"total","first","last"},[]string{"INT8","INT8","INT8"},[][]any{{int64(494),int64(1),int64(494)}}))
		})
	}
}
