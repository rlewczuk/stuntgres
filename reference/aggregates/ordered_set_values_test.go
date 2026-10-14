package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregateValues(t *testing.T) {
	orderedSetScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		for i, fraction := range []string{"0", ".25", ".5", ".75", "1"} {
			q := "SELECT percentile_cont("+fraction+") WITHIN GROUP(ORDER BY x) AS c,percentile_disc("+fraction+") WITHIN GROUP(ORDER BY x) AS d FROM os_input"
			orderedSetBoth(t,p,c,b,fixture.Select(q,[]string{"c","d"},[]string{"FLOAT8","INT4"},[][]any{{[]float64{10,17.5,25,32.5,40}[i],[]int64{10,10,20,30,40}[i]}}))
		}
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_cont(ARRAY[0,.25,.5,.75,1]::float8[]) WITHIN GROUP(ORDER BY x)::text AS v FROM os_input","TEXT","{10,17.5,25,32.5,40}"))
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_disc(ARRAY[0,.25,.5,.75,1]::float8[]) WITHIN GROUP(ORDER BY x)::text AS v FROM os_input","TEXT","{10,10,20,30,40}"))
	})
}

func TestReferenceOrderedSetAggregateSorting(t *testing.T) {
	orderedSetScenario(t, func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		for _, nulls := range []string{"FIRST","LAST"} {
			for i, fraction := range []string{"0", ".25", ".5", ".75", "1"} {
				q := "SELECT percentile_cont("+fraction+") WITHIN GROUP(ORDER BY x DESC NULLS "+nulls+") AS c,percentile_disc("+fraction+") WITHIN GROUP(ORDER BY x DESC NULLS "+nulls+") AS d FROM os_input"
				orderedSetBoth(t,p,c,b,fixture.Select(q,[]string{"c","d"},[]string{"FLOAT8","INT4"},[][]any{{[]float64{40,32.5,25,17.5,10}[i],[]int64{40,40,30,20,10}[i]}}))
			}
		}
		for _, tc := range []struct{expr,tail string; value any} {
			{"x+1","",float64(18.5)}, {"1","",float64(1)}, {"x"," FILTER(WHERE x<=20)",float64(12.5)}, {"1/(x-x)"," FILTER(WHERE FALSE)",nil}, {"1/(x-x)"," FILTER(WHERE NULL)",nil},
		} {
			orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_cont(.25) WITHIN GROUP(ORDER BY "+tc.expr+")"+tc.tail+" AS v FROM os_input","FLOAT8",tc.value))
		}
	})
}

func TestReferenceOrderedSetAggregateArrays(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		for _, tc := range []struct{family,typ,want string} {
			{"cont","_FLOAT8","[-1:0][3:5]={{40,NULL,10},{25,17.5,25}}"},
			{"disc","_INT4","[-1:0][3:5]={{40,NULL,10},{20,10,20}}"},
		} {
			q := "SELECT percentile_"+tc.family+"('[-1:0][3:5]={{1,NULL,0},{0.5,0.25,0.5}}'::float8[]) WITHIN GROUP(ORDER BY x) AS v FROM os_input"
			orderedSetBoth(t,p,c,b,orderedSetValue(q,tc.typ,tc.want))
			orderedSetBoth(t,p,c,b,orderedSetValue("SELECT v::text AS v FROM ("+q+") AS a","TEXT",tc.want))
			for _, shape := range []string{"'{}'","'{NULL,NULL}'","'[3:4]={NULL,NULL}'"} {
				want := map[string]string{"'{}'":"{}","'{NULL,NULL}'":"{NULL,NULL}","'[3:4]={NULL,NULL}'":"[3:4]={NULL,NULL}"}[shape]
				orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_"+tc.family+"("+shape+"::float8[]) WITHIN GROUP(ORDER BY x)::text AS v FROM os_input","TEXT",want))
			}
			orderedSetRuntimeError(t,p,c,b,"SELECT percentile_"+tc.family+"('{1.1,-0.1}'::float8[]) WITHIN GROUP(ORDER BY x) FROM os_input","22003","percentile value 1.1 is not between 0 and 1")
		}
	})
}

func TestReferenceOrderedSetAggregateNullEmpty(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		for _, call := range []struct{expr,typ string} {
			{"percentile_cont(.5) WITHIN GROUP(ORDER BY x)","FLOAT8"},
			{"percentile_cont(.5) WITHIN GROUP(ORDER BY x*interval '1 day')","INTERVAL"},
			{"percentile_cont('{.5}'::float8[]) WITHIN GROUP(ORDER BY x)","_FLOAT8"},
			{"percentile_cont('{.5}'::float8[]) WITHIN GROUP(ORDER BY x*interval '1 day')","_INTERVAL"},
			{"percentile_disc(.5) WITHIN GROUP(ORDER BY x)","INT4"},
			{"percentile_disc('{.5}'::float8[]) WITHIN GROUP(ORDER BY x)","_INT4"},
			{"mode() WITHIN GROUP(ORDER BY x)","INT4"},
		} {
			for _, tail := range []string{" FROM os_input WHERE FALSE"," FROM os_input WHERE x IS NULL"} {
				orderedSetBoth(t,p,c,b,orderedSetValue("SELECT "+call.expr+" AS v"+tail,call.typ,nil))
			}
			for _, filter := range []string{"FALSE","NULL"} {
				orderedSetBoth(t,p,c,b,orderedSetValue("SELECT "+call.expr+" FILTER(WHERE "+filter+") AS v FROM os_input",call.typ,nil))
			}
			orderedSetBoth(t,p,c,b,fixture.Select("SELECT "+call.expr+" AS v FROM os_input WHERE FALSE GROUP BY p",[]string{"v"},[]string{call.typ},[][]any{}))
		}
		for _, family := range []string{"cont","disc"} {
			for _, arg := range []string{"NULL::float8","NULL::float8[]","'{}'::float8[]","'{1.1}'::float8[]"} {
				typ := "FLOAT8"; if family=="disc" {typ="INT4"}; if arg!="NULL::float8" {typ="_"+typ}
				orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_"+family+"("+arg+") WITHIN GROUP(ORDER BY x) AS v FROM os_input WHERE FALSE",typ,nil))
			}
			orderedSetRuntimeError(t,p,c,b,"SELECT percentile_"+family+"(-.1) WITHIN GROUP(ORDER BY x) FROM os_input WHERE FALSE","22003","percentile value -0.1 is not between 0 and 1")
		}
	})
}

func TestReferenceOrderedSetAggregateBoundaries(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		for _, family := range []string{"cont","disc"} {
			for _, tc := range []struct{literal,message string} {
				{"-.1","-0.1"},{"1.1","1.1"},{"'NaN'::float8","NaN"},{"'Infinity'::float8","Infinity"},{"'-Infinity'::float8","-Infinity"},{"1.123456789","1.12346"},
			} {
				t.Run(family+tc.literal,func(t *testing.T){orderedSetRuntimeError(t,p,c,b,"SELECT percentile_"+family+"("+tc.literal+") WITHIN GROUP(ORDER BY x) FROM os_input","22003","percentile value "+tc.message+" is not between 0 and 1")})
			}
		}
		for _, tc := range []struct{fraction string; want int64}{{"'-0'::float8",10},{".5",20},{".5000000001",30},{"1",40}} {
			orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_disc("+tc.fraction+") WITHIN GROUP(ORDER BY x) AS v FROM os_input","INT4",tc.want))
		}
		for _, endpoints := range []struct{a,z,want string}{{"Infinity","Infinity","NaN"},{"-Infinity","Infinity","NaN"},{"1","Infinity","Infinity"},{"Infinity","NaN","NaN"}} {
			q:=fmt.Sprintf("SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY v)::text AS v FROM (VALUES('%s'::float8),('%s'::float8)) AS a(v)",endpoints.a,endpoints.z)
			orderedSetBoth(t,p,c,b,orderedSetValue(q,"TEXT",endpoints.want))
		}
	})
}

func TestReferenceOrderedSetAggregateIntervals(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		for _, tc := range []struct{end,fraction,direction,want string} {
			{"2 days",".25","","12:00:00"},{"2 days",".5","","1 day"},{"2 days",".75","","1 day 12:00:00"},
			{"2 months",".25","","15 days"},{"2 months",".5","","1 mon"},{"2 months",".75","","1 mon 15 days"},{"2 months",".25","DESC","2 mons -15 days"},
			{"1 microsecond",".5","","00:00:00"},
		} {
			q:="SELECT percentile_cont("+tc.fraction+") WITHIN GROUP(ORDER BY v "+tc.direction+") AS v FROM (VALUES(interval '0'),(interval '"+tc.end+"')) AS a(v)"
			orderedSetBoth(t,p,c,b,orderedSetValue(q,"INTERVAL",tc.want))
		}
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_cont('{0,.25,.5,.75,1}'::float8[]) WITHIN GROUP(ORDER BY v) AS v FROM (VALUES(interval '0'),(interval '2 days')) AS a(v)","_INTERVAL","{00:00:00,12:00:00,\"1 day\",\"1 day 12:00:00\",\"2 days\"}"))
		for _, fraction:=range []string{"0","1"} {
			want:="-Infinity";if fraction=="1" {want="Infinity"}
			orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_cont("+fraction+") WITHIN GROUP(ORDER BY v)::text AS v FROM (VALUES('-Infinity'::float8),('Infinity'::float8)) AS a(v)","TEXT",want))
			want="-infinity";if fraction=="1" {want="infinity"}
			orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_cont("+fraction+") WITHIN GROUP(ORDER BY v)::text AS v FROM (VALUES(interval '-infinity'),(interval 'infinity')) AS a(v)","TEXT",want))
		}
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_cont('{0,.25,.5,.75,1}'::float8[]) WITHIN GROUP(ORDER BY v)::text AS v FROM (VALUES(interval '0'),(interval '2 days')) AS a(v)","TEXT","{00:00:00,12:00:00,\"1 day\",\"1 day 12:00:00\",\"2 days\"}"))
		orderedSetRuntimeError(t,p,c,b,"SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY v) FROM (VALUES(interval '-2147483647 months'),(interval '2147483647 months')) AS a(v)","22008","interval out of range")
		orderedSetRuntimeError(t,p,c,b,"SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY v) FROM (VALUES(interval '-infinity'),(interval 'infinity')) AS a(v)","22008","interval out of range")
	})
}

func TestReferenceOrderedSetAggregateMode(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		for _, tc := range []struct{values,typ string; asc,desc any} {
			{"(2),(1),(2),(1),(NULL)","INT4",int64(1),int64(2)},
			{"(1),(1),(1),(2),(2),(NULL)","INT4",int64(1),int64(1)},
			{"('b'::text),('a'),('b'),('a'),(NULL)","TEXT","a","b"},
			{"('NaN'::float8),('NaN'),(1)","TEXT","NaN","NaN"},
			{"('10.0.0.2'::inet),('10.0.0.1'),('10.0.0.2'),('10.0.0.1')","TEXT","10.0.0.1/32","10.0.0.2/32"},
		} {
			cast:="";if tc.typ=="TEXT"&&tc.asc!="a" {cast="::text"}
			for _, direction := range []string{"ASC","DESC"} {
				want:=tc.asc;if direction=="DESC" {want=tc.desc}
				q:="SELECT mode() WITHIN GROUP(ORDER BY v "+direction+")"+cast+" AS v FROM (VALUES"+tc.values+") AS a(v)"
				orderedSetBoth(t,p,c,b,orderedSetValue(q,tc.typ,want))
			}
		}
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT mode() WITHIN GROUP(ORDER BY v)=0 AS v FROM (VALUES('0'::float8),('-0'),(1)) AS a(v)","BOOL",true))
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT mode(*) WITHIN GROUP(ORDER BY 1) AS v","INT4",int64(1)))
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_cont(ALL .5) WITHIN GROUP(ORDER BY 1) AS v","FLOAT8",float64(1)))
	})
}
