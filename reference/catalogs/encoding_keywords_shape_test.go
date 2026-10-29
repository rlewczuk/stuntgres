package reference_test

import (
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

var encodingKeywordColumns = []string{"word","catcode","barelabel","catdesc","baredesc"}
var encodingKeywordTypes = []string{"TEXT","CHAR","BOOL","TEXT","TEXT"}

func TestReferenceEncodingKeywordsShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name,func(t *testing.T) {
			for _, prepared := range []bool{false,true} {
				for _, suffix := range []string{" WHERE false"," LIMIT 0"} {
					s := fixture.Select("SELECT * FROM pg_catalog.pg_get_keywords()"+suffix,encodingKeywordColumns,encodingKeywordTypes,nil)
					s.Prepared = prepared
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						types,err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }
						for _, typ := range types { if _,known := typ.Nullable(); known { t.Fatal("computed source claims table nullability",typ.Name()) } }
					}
					fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
				}
				for _, c := range []struct{ from string; columns []string }{
					{"pg_get_keywords() AS k",encodingKeywordColumns},
					{"pg_get_keywords() AS k(a,b,c,d,e)",[]string{"a","b","c","d","e"}},
					{"pg_get_keywords() AS k(a)",[]string{"a","catcode","barelabel","catdesc","baredesc"}},
					{"(SELECT (pg_get_keywords()).*) AS k",encodingKeywordColumns},
				} {
					s := fixture.Select("SELECT * FROM "+c.from,c.columns,encodingKeywordTypes,encodingKeywordRows(t)); s.Prepared,s.Bag = prepared,true
					fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
				}
				for _, s := range []fixture.Step{
					fixture.Select("SELECT * FROM pg_char_to_encoding('UTF8') AS e(id)",[]string{"id"},[]string{"INT4"},[][]any{{int64(6)}}),
					fixture.Select("SELECT * FROM pg_encoding_to_char(NULL) AS e(encoding)",[]string{"encoding"},[]string{"NAME"},[][]any{{nil}}),
					fixture.Select("SELECT pg_get_keywords() AS keyword LIMIT 0",[]string{"keyword"},[]string{"RECORD"},nil),
					fixture.Select("SELECT pg_typeof(pg_char_to_encoding(NULL))::text AS id_type,pg_typeof(pg_encoding_to_char(42))::text AS name_type",[]string{"id_type","name_type"},[]string{"TEXT","TEXT"},[][]any{{"integer","name"}}),
					fixture.Select("SELECT count(*) AS total,min(ordinality) AS first,max(ordinality) AS last FROM pg_get_keywords() WITH ORDINALITY",[]string{"total","first","last"},[]string{"INT8","INT8","INT8"},[][]any{{int64(494),int64(1),int64(494)}}),
					fixture.Select("SELECT word,catcode,barelabel,catdesc,baredesc,n FROM ROWS FROM (pg_get_keywords(),generate_series(1,496)) AS z(word,catcode,barelabel,catdesc,baredesc,n) WHERE n>494 ORDER BY n",append(append([]string{},encodingKeywordColumns...),"n"),append(append([]string{},encodingKeywordTypes...),"INT4"),[][]any{{nil,nil,nil,nil,nil,int64(495)},{nil,nil,nil,nil,nil,int64(496)}}),
				} { s.Prepared = prepared; fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
				ordinals := make([][]any,494)
				for i := range ordinals { ordinals[i] = []any{int64(i+1)} }
				s := fixture.Select("SELECT ordinality FROM pg_get_keywords() WITH ORDINALITY ORDER BY ordinality",[]string{"ordinality"},[]string{"INT8"},ordinals)
				s.Prepared = prepared
				fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
			}
		})
	}
}
