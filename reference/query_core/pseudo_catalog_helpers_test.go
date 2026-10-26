package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func pseudoCatalogFacts(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,id int64) {
	t.Helper()
	facts := map[int64]struct{input,output,receive,send,length,array int64; byval bool; alignment,storage string}{
		2276:{2294,2295,0,0,4,0,true,"i","p"}, 2283:{2312,2313,0,0,4,0,true,"i","p"},
		2277:{2296,2297,2502,2503,-1,0,false,"d","x"}, 2776:{2777,2778,0,0,4,0,true,"i","p"}, 3500:{3504,3505,0,0,4,0,true,"i","p"},
		3831:{3832,3833,0,0,-1,0,false,"d","x"}, 4537:{4229,4230,0,0,-1,0,false,"d","x"},
		5077:{5086,5087,0,0,4,0,true,"i","p"}, 5078:{5088,5089,5090,5091,-1,0,false,"d","x"}, 5079:{5092,5093,0,0,4,0,true,"i","p"},
		5080:{5094,5095,0,0,-1,0,false,"d","x"}, 4538:{4226,4227,0,0,-1,0,false,"d","x"},
		2275:{2292,2293,2500,2501,-2,1263,false,"c","p"}, 2281:{2304,2305,0,0,8,0,true,"d","p"},
		2280:{2302,2303,0,0,4,0,true,"i","p"}, 3115:{3116,3117,0,0,4,0,true,"i","p"}, 269:{267,268,0,0,4,0,true,"i","p"},
		325:{326,327,0,0,4,0,true,"i","p"}, 3310:{3311,3312,0,0,4,0,true,"i","p"},
		2249:{2290,2291,2402,2403,-1,2287,false,"d","x"}, 2279:{2300,2301,0,0,4,0,true,"i","p"}, 3838:{3594,3595,0,0,4,0,true,"i","p"},
		32:{86,87,88,90,8,0,true,"d","p"}, 2278:{2298,2299,3120,3121,4,0,true,"i","p"}, 705:{109,110,2416,2417,-2,0,false,"c","p"},
	}
	f,ok := facts[id]; if !ok { t.Fatal("missing independent catalog expectation",id) }
	query := fmt.Sprintf("SELECT typinput::oid::integer,typoutput::oid::integer,typreceive::oid::integer,typsend::oid::integer,typlen::integer,typbyval,typalign::text,typstorage::text,typarray::integer FROM pg_type WHERE oid=%d",id)
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select(query,[]string{"typinput","typoutput","typreceive","typsend","typlen","typbyval","typalign","typstorage","typarray"},[]string{"INT4","INT4","INT4","INT4","INT4","BOOL","TEXT","TEXT","INT4"},[][]any{{f.input,f.output,f.receive,f.send,f.length,f.byval,f.alignment,f.storage,f.array}}))
}
