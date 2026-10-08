package stuntgres_test

import (
	"testing"
	"stuntgres"
)

func TestQueryLimitTypingProvider(t *testing.T) {
	db:=queryLimitFixture(t)
	for _,tc:=range []struct{bound string;ids []int64}{
		{"CAST(2 AS SMALLINT)",[]int64{1,2}},
		{"CAST(2 AS INTEGER)",[]int64{1,2}},
		{"CAST(2 AS BIGINT)",[]int64{1,2}},
		{"CAST(2 AS OID)",[]int64{1,2}},
		{"CAST(2.5 AS NUMERIC)",[]int64{1,2,3}},
		{"CAST(2.5 AS REAL)",[]int64{1,2}},
		{"CAST(2.5 AS DOUBLE PRECISION)",[]int64{1,2}},
		{"CAST(NULL AS NUMERIC)",[]int64{1,2,3,4}},
		{"CAST(NULL AS DOUBLE PRECISION)",[]int64{1,2,3,4}},
	} {
		t.Run(tc.bound,func(t *testing.T){queryLimitSelect(t,db,"SELECT id FROM t ORDER BY id LIMIT "+tc.bound,"id","INT4",queryLimitIDs(tc.ids...))})
	}
	queryLimitSelect(t,db,"SELECT id FROM t ORDER BY id FETCH FIRST CAST(2 AS BIGINT) ROWS ONLY","id","INT4",queryLimitIDs(1,2))
	for _,tc:=range []struct{bound,code,message string}{
		{"CAST('NaN' AS NUMERIC)","0A000","cannot convert NaN to bigint"},
		{"CAST('Infinity' AS NUMERIC)","0A000","cannot convert infinity to bigint"},
		{"CAST('-Infinity' AS NUMERIC)","0A000","cannot convert infinity to bigint"},
		{"CAST('NaN' AS REAL)","22003","bigint out of range"},
		{"CAST('Infinity' AS DOUBLE PRECISION)","22003","bigint out of range"},
		{"CAST('-Infinity' AS DOUBLE PRECISION)","22003","bigint out of range"},
		{"9223372036854775808","22003","bigint out of range"},
		{"CAST('9223372036854775807' AS DOUBLE PRECISION)","22003","bigint out of range"},
	} {
		t.Run(tc.bound,func(t *testing.T){
			q:="SELECT id FROM t LIMIT "+tc.bound
			want:=stuntgres.Error{Code:tc.code,Message:tc.message}
			rows,err:=db.Query(q)
			if rows!=nil{rows.Close();t.Fatal("failed bound exposed rows")}
			typeCoreDiagnostic(t,err,want)
			stmt,err:=db.Prepare(q)
			if err!=nil{t.Fatal("runtime conversion failed Prepare",err)}
			defer stmt.Close()
			rows,err=stmt.Query()
			if rows!=nil{rows.Close();t.Fatal("failed bound exposed rows")}
			typeCoreDiagnostic(t,err,want)
		})
	}
}
