package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSParserMetadata(t *testing.T) {
	for _, tc := range []struct { name, query string; columns, types []string; rows [][]any }{
		{"parse", "SELECT * FROM ts_parse('default','Word')", []string{"tokid","token"}, []string{"INT4","TEXT"}, [][]any{{int64(1),"Word"}}},
		{"empty", "SELECT * FROM ts_parse('default','')", []string{"tokid","token"}, []string{"INT4","TEXT"}, nil},
		{"null", "SELECT * FROM ts_parse(NULL::text,'Word')", []string{"tokid","token"}, []string{"INT4","TEXT"}, nil},
		{"descriptors", "SELECT * FROM ts_token_type('default') ORDER BY tokid", []string{"tokid","alias","description"}, []string{"INT4","TEXT","TEXT"}, ftsParserDescriptors()},
		{"null_descriptors", "SELECT * FROM ts_token_type(NULL::oid)", []string{"tokid","alias","description"}, []string{"INT4","TEXT","TEXT"}, nil},
		{"aliases", "SELECT * FROM ts_parse('default','Word') WITH ORDINALITY AS p(id,lexeme,n)", []string{"id","lexeme","n"}, []string{"INT4","TEXT","INT8"}, [][]any{{int64(1),"Word",int64(1)}}},
		{"partial_alias", "SELECT * FROM ts_parse('default','Word') AS p(id)", []string{"id","token"}, []string{"INT4","TEXT"}, [][]any{{int64(1),"Word"}}},
		{"target", "SELECT ts_parse('default','Word')", []string{"ts_parse"}, []string{"RECORD"}, [][]any{{"(1,Word)"}}},
		{"join", "SELECT p.tokid,p.token,d.alias,d.description FROM ts_parse('default','Word 123') p JOIN ts_token_type('default') d ON d.tokid=p.tokid WHERE p.tokid=22", []string{"tokid","token","alias","description"}, []string{"INT4","TEXT","TEXT","TEXT"}, [][]any{{int64(22),"123","uint","Unsigned integer"}}},
		{"rows_from", "SELECT * FROM ROWS FROM(ts_parse('default',''),ts_parse('default','Word')) WITH ORDINALITY AS p(a,b,c,d,n)", []string{"a","b","c","d","n"}, []string{"INT4","TEXT","INT4","TEXT","INT8"}, [][]any{{nil,nil,int64(1),"Word",int64(1)}}},
		{"cartesian", "SELECT p.token,q.token FROM ts_parse('default','One') p,ts_parse('default','Two') q", []string{"token","token"}, []string{"TEXT","TEXT"}, [][]any{{"One","Two"}}},
		{"implicit_lateral", "SELECT v.id,p.tokid,p.token FROM (VALUES(1,'Word'),(2,'')) v(id,txt),ts_parse('default',v.txt) p ORDER BY v.id", []string{"id","tokid","token"}, []string{"INT4","INT4","TEXT"}, [][]any{{int64(1),int64(1),"Word"}}},
		{"explicit_lateral", "SELECT v.id,p.tokid,p.token FROM (VALUES(1,'Word'),(2,'')) v(id,txt) LEFT JOIN LATERAL ts_parse('default',v.txt) p ON TRUE ORDER BY v.id", []string{"id","tokid","token"}, []string{"INT4","INT4","TEXT"}, [][]any{{int64(1),int64(1),"Word"},{int64(2),nil,nil}}},
	} {
		t.Run(tc.name,func(t *testing.T) {
			ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				indices := make([]int,len(tc.columns))
				for i := range indices { indices[i] = i }
				if tc.name == "explicit_lateral" && !b.PG {
					indices = []int{0} // Only the left VALUES column remains unknown.
				}
				for _, prepared := range []bool{false,true} {
					s := fixture.Select(tc.query,tc.columns,tc.types,tc.rows)
					s.Prepared = prepared
					if tc.name == "explicit_lateral" {
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
							columns, err := rows.ColumnTypes()
							if err != nil { t.Fatal(err) }
							for i, column := range columns {
								nullable, known := column.Nullable()
								wantKnown := !b.PG && i > 0
								if nullable != wantKnown || known != wantKnown {
									t.Fatalf("outer column %d Nullable=(%t,%t), want (%t,%t)",i,nullable,known,wantKnown,wantKnown)
								}
							}
						}
					}
					fixture.ScalarRunStep(t,p.Ctx,b.DB,b.Recorder,s,indices...)
				}
			})
		})
	}
	t.Run("parser_catalog",func(t *testing.T) {
		ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
			ftsParserSelect(t,p,b,"SELECT oid,prsname,prsnamespace,prsstart,prstoken,prsend,prsheadline,prslextype FROM pg_catalog.pg_ts_parser WHERE FALSE",[]string{"oid","prsname","prsnamespace","prsstart","prstoken","prsend","prsheadline","prslextype"},[]string{"OID","NAME","OID","REGPROC","REGPROC","REGPROC","REGPROC","REGPROC"},nil)
			ftsParserSelect(t,p,b,"SELECT oid::int4,prsname,prsnamespace::int4,prsstart,prstoken,prsend,prsheadline,prslextype FROM pg_catalog.pg_ts_parser ORDER BY oid",[]string{"oid","prsname","prsnamespace","prsstart","prstoken","prsend","prsheadline","prslextype"},[]string{"INT4","NAME","INT4","REGPROC","REGPROC","REGPROC","REGPROC","REGPROC"},[][]any{{int64(3722),"default",int64(11),"prsd_start","prsd_nexttoken","prsd_end","prsd_headline","prsd_lextype"}})
			ftsParserSelect(t,p,b,"SELECT prsstart::oid::bigint,prstoken::oid::bigint,prsend::oid::bigint,prsheadline::oid::bigint,prslextype::oid::bigint FROM pg_catalog.pg_ts_parser WHERE oid=3722",[]string{"prsstart","prstoken","prsend","prsheadline","prslextype"},[]string{"INT8","INT8","INT8","INT8","INT8"},[][]any{{int64(3717),int64(3718),int64(3719),int64(3720),int64(3721)}})
			ftsParserSelect(t,p,b,"SELECT attname,atttypid::bigint FROM pg_attribute WHERE attrelid='pg_catalog.pg_ts_parser'::regclass AND attnum>0 ORDER BY attnum",[]string{"attname","atttypid"},[]string{"NAME","INT8"},[][]any{{"oid",int64(26)},{"prsname",int64(19)},{"prsnamespace",int64(26)},{"prsstart",int64(24)},{"prstoken",int64(24)},{"prsend",int64(24)},{"prsheadline",int64(24)},{"prslextype",int64(24)}})
		})
	})
	for _, tc := range []struct { oid,result int; name,signature,volatility string; set bool }{
		{3713,2249,"ts_token_type","oid","i",true}, {3714,2249,"ts_token_type","text","s",true},
		{3715,2249,"ts_parse","oid,text","i",true}, {3716,2249,"ts_parse","text,text","s",true},
		{3717,2281,"prsd_start","internal,integer","i",false}, {3718,2281,"prsd_nexttoken","internal,internal,internal","i",false},
		{3719,2278,"prsd_end","internal","i",false}, {3720,2281,"prsd_headline","internal,internal,tsquery","i",false}, {3721,2281,"prsd_lextype","internal","i",false},
	} {
		t.Run(fmt.Sprint(tc.oid),func(t *testing.T) {
			ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				query := "SELECT oid::bigint,proname,oid="+ftsTypesQuote("pg_catalog."+tc.name+"("+tc.signature+")")+"::regprocedure::oid AS signature,prorettype::bigint AS result,proisstrict,provolatile,proparallel,proretset,prolang::bigint AS language FROM pg_proc WHERE oid="+fmt.Sprint(tc.oid)
				ftsParserSelect(t,p,b,query,[]string{"oid","proname","signature","result","proisstrict","provolatile","proparallel","proretset","language"},[]string{"INT8","NAME","BOOL","INT8","BOOL","CHAR","CHAR","BOOL","INT8"},[][]any{{int64(tc.oid),tc.name,true,int64(tc.result),true,tc.volatility,"s",tc.set,int64(12)}})
			})
		})
	}
	t.Run("argument_labels",func(t *testing.T) {
		ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
			ftsParserSelect(t,p,b,"SELECT oid::bigint,proargnames::text FROM pg_proc WHERE oid IN (3713,3714,3715,3716) ORDER BY oid",[]string{"oid","proargnames"},[]string{"INT8","TEXT"},[][]any{
				{int64(3713),"{parser_oid,tokid,alias,description}"},
				{int64(3714),"{parser_name,tokid,alias,description}"},
				{int64(3715),"{parser_oid,txt,tokid,token}"},
				{int64(3716),"{parser_name,txt,tokid,token}"},
			})
		})
	})
	t.Run("handler_arguments",func(t *testing.T) {
		ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
			// Released pg_proc.dat input arities; all five omit proargnames.
			ftsParserSelect(t,p,b,"SELECT oid::bigint,pronargs,proargnames IS NULL AS unnamed FROM pg_proc WHERE oid IN (3717,3718,3719,3720,3721) ORDER BY oid",[]string{"oid","pronargs","unnamed"},[]string{"INT8","INT2","BOOL"},[][]any{
				{int64(3717),int64(2),true},
				{int64(3718),int64(3),true},
				{int64(3719),int64(1),true},
				{int64(3720),int64(3),true},
				{int64(3721),int64(1),true},
			})
		})
	})
}
