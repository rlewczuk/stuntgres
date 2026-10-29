package reference_test

import (
	"fmt"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceEncodingKeywordsRoutines(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, prepared := range []bool{false,true} {
		s := fixture.Select("SELECT oid,proname,proargtypes::text,prorettype,proretset,proisstrict,provolatile,proparallel,procost,prorows,prosrc,prolang,provariadic,pronargdefaults,proargnames,proallargtypes,proargmodes FROM pg_proc WHERE oid IN (1264,1597,1686) ORDER BY oid",
			[]string{"oid","proname","proargtypes","prorettype","proretset","proisstrict","provolatile","proparallel","procost","prorows","prosrc","prolang","provariadic","pronargdefaults","proargnames","proallargtypes","proargmodes"},
			[]string{"OID","NAME","TEXT","OID","BOOL","BOOL","CHAR","CHAR","FLOAT4","FLOAT4","TEXT","OID","OID","INT2","_TEXT","_OID","_CHAR"},[][]any{
				{int64(1264),"pg_char_to_encoding","19",int64(23),false,true,"s","s",float64(1),float64(0),"PG_char_to_encoding",int64(12),int64(0),int64(0),nil,nil,nil},
				{int64(1597),"pg_encoding_to_char","23",int64(19),false,true,"s","s",float64(1),float64(0),"PG_encoding_to_char",int64(12),int64(0),int64(0),nil,nil,nil},
				{int64(1686),"pg_get_keywords","",int64(2249),true,true,"s","s",float64(10),float64(500),"pg_get_keywords",int64(12),int64(0),int64(0),"{word,catcode,barelabel,catdesc,baredesc}","{25,18,16,25,25}","{o,o,o,o,o}"},
			})
		s.Prepared = prepared
		for _, b := range p.Backends() {
			t.Run(b.Name,func(t *testing.T) {
				step := s
				if b.PG {
					// pq transports OID cells as strings; retain the OID metadata
					// assertions and independently normalize only expected cells.
					step.Rows = make([][]any,len(s.Rows))
					for i,row := range s.Rows {
						step.Rows[i] = append([]any(nil),row...)
						for _,column := range []int{0,3,11,12} {
							step.Rows[i][column] = fmt.Sprint(row[column])
						}
					}
				}
				fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,step)
			})
		}
		s = fixture.Select("SELECT pg_get_function_arguments(1264) AS forward,pg_get_function_arguments(1597) AS reverse,pg_get_function_result(1686) AS result",[]string{"forward","reverse","result"},[]string{"TEXT","TEXT","TEXT"},[][]any{{"name","integer","SETOF record"}})
		s.Prepared = prepared; p.Step(t,s)
	}
}
