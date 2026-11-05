package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarraySliceBoundaries(t *testing.T) {
	intarrayScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		intarrayInstall(t,p,b,c)
		for _,tc := range []struct{ array string; count,item,index int64 }{
			{"{}",0,22,0},{intarraySliceA,4,22,2},{intarraySliceA,4,99,0},
			{"[0:3]={11,22,33,22}",4,22,2},{"{{11,22},{33,22}}",4,33,3},
			{"{-2147483648,0,2147483647,-2147483648}",4,-2147483648,1},
			{"{-2147483648,0,2147483647}",3,2147483647,3},
		} {
			for _,expr := range []string{"ia_provider.icount($1::integer[])","OPERATOR(ia_provider.#) $1::integer[]"} {
				intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT "+expr+" AS v","INT4",tc.count,tc.array))
			}
			for _,expr := range []string{"ia_provider.idx($1::integer[],$2::integer)","$1::integer[] OPERATOR(ia_provider.#) $2::integer"} {
				intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT "+expr+" AS v","INT4",tc.index,tc.array,tc.item))
			}
		}
		for _,tc := range []struct{ array string; count int64 }{{"{NULL,1,NULL}",3},{"{NULL}",1}} {
			for _,expr := range []string{"ia_provider.icount($1::integer[])","OPERATOR(ia_provider.#) $1::integer[]"} {
				intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT "+expr+" AS v","INT4",tc.count,tc.array))
			}
		}
		for _,tc := range []struct{ start int64; length *int64; want string }{
			{2,new(int64(3)),"{22,33,22}"},{2,nil,"{22,33,22}"},{2,new(int64(0)),"{22,33,22}"},
			{0,nil,intarraySliceA},{1,nil,intarraySliceA},{-1,nil,"{22}"},{-4,nil,intarraySliceA},{-5,nil,intarraySliceA},
			{-5,new(int64(2)),"{11}"},{-6,new(int64(2)),"{}"},{2,new(int64(-1)),"{22,33}"},
			{0,new(int64(-1)),"{11,22,33}"},{2,new(int64(-3)),"{}"},{5,nil,"{}"},{3,new(int64(100)),"{33,22}"},
			{-2147483648,nil,intarraySliceA},{2147483647,nil,"{}"},{1,new(int64(-2147483648)),"{}"},
			// Pinned PostgreSQL 18.6 confirms signed-int32 overflow produces
			// empty arrays here; the adjacent nonoverflow control retains A.
			{2,new(int64(2147483647)),"{}"},{2147483647,new(int64(2147483647)),"{}"},
			{1,new(int64(2147483647)),intarraySliceA},
		} {
			args := []any{intarraySliceA,tc.start}
			params := "$1::integer[],$2::integer"
			literal := "'{11,22,33,22}'::integer[],("+strconv.FormatInt(tc.start,10)+")::integer"
			if tc.length != nil { args = append(args,*tc.length); params += ",$3::integer"; literal += ",("+strconv.FormatInt(*tc.length,10)+")::integer" }
			for _,q := range []string{"SELECT ia_provider.subarray("+params+") AS v","SELECT ia_provider.subarray("+literal+") AS v"} {
				binds := args; if q == "SELECT ia_provider.subarray("+literal+") AS v" { binds = nil }
				intarrayBoth(t,p,b,c,intarraySliceResult(b,q,"_INT4",tc.want,binds...))
			}
		}
		for _,tc := range []struct{ expression,want string; size int64 }{
			{"ia_provider.subarray('[0:3]={11,22,33,22}'::integer[],2,2)","{22,33}",2},
			{"ia_provider.subarray('{{11,22},{33,22}}'::integer[],2)","{22,33,22}",3},
			{"ia_provider.intset(-2147483648)","{-2147483648}",1},
			{"ia_provider.intset(2147483647)","{2147483647}",1},
			{"ia_provider.subarray('{}'::integer[],-5)","{}",0},
			{"ia_provider.subarray('{}'::integer[],1,2)","{}",0},
			{"ia_provider.subarray('{11,22,33,22}'::integer[],5)","{}",0},
		} {
			var ndims, dims, lower, upper any
			if tc.size > 0 { ndims,dims,lower,upper = int64(1),"[1:"+strconv.FormatInt(tc.size,10)+"]",int64(1),tc.size }
			q := "SELECT v,array_ndims(v) AS n,array_dims(v) AS d,array_lower(v,1) AS l,array_upper(v,1) AS u FROM (SELECT "+tc.expression+" AS v) shape"
			intarrayBoth(t,p,b,c,intarraySelect(b,q,[]string{"v","n","d","l","u"},[]string{"_INT4","INT4","TEXT","INT4","INT4"},nil,[][]any{{tc.want,ndims,dims,lower,upper}}))
		}
	})
}
