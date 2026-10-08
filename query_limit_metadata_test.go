package stuntgres_test

import (
	"testing"
	"stuntgres"
)

func TestQueryLimitMetadata(t *testing.T) {
	db:=openLocal(t,stuntgres.NewDriver(),t.Name())
	execSQL(t,db,"CREATE TABLE meta(id INT NOT NULL,n NUMERIC(6,2),s VARCHAR(5))")
	execSQL(t,db,"INSERT INTO meta VALUES(1,12.30,'abc')")
	for _,prepared:=range []bool{false,true} {
		for _,suffix:=range []string{"LIMIT 1","LIMIT 0","OFFSET 99"} {
			t.Run(suffix+map[bool]string{false:"/direct",true:"/prepared"}[prepared],func(t *testing.T){
				rows,closeQuery,err:=transactionQuery(t.Context(),db,"SELECT id AS ident,n,s,id+1 AS computed FROM meta ORDER BY id "+suffix,prepared)
				if closeQuery!=nil{defer closeQuery()}
				if err!=nil{t.Fatal(err)}
				defer rows.Close()
				metadata,err:=rows.ColumnTypes()
				if err!=nil||len(metadata)!=4{t.Fatal(metadata,err)}
				for i,want:=range []struct{name,typ string}{{"ident","INT4"},{"n","NUMERIC"},{"s","VARCHAR"},{"computed","INT4"}} {if metadata[i].Name()!=want.name||metadata[i].DatabaseTypeName()!=want.typ{t.Fatal(metadata[i],want)}}
				if nullable,known:=metadata[0].Nullable();nullable||!known{t.Fatal("lost physical NOT NULL")}
				if nullable,known:=metadata[1].Nullable();!nullable||!known{t.Fatal("lost physical nullable column")}
				if _,known:=metadata[3].Nullable();known{t.Fatal("computed output invented physical nullability")}
				if precision,scale,known:=metadata[1].DecimalSize();!known||precision!=6||scale!=2{t.Fatal(precision,scale,known)}
				if length,known:=metadata[2].Length();!known||length!=5{t.Fatal(length,known)}
				count:=0
				for rows.Next(){count++;var id,n,s,k any;if err:=rows.Scan(&id,&n,&s,&k);err!=nil{t.Fatal(err)}}
				want:=0
				if suffix=="LIMIT 1"{want=1}
				if rows.Err()!=nil||count!=want{t.Fatal(count,want,rows.Err())}
			})
		}
	}
	execSQL(t,db,"INSERT INTO meta VALUES(2,10.00,'def')")
	for _,tc:=range []struct{suffix string;rows [][]any}{{"LIMIT 2",[][]any{{},{}}},{"LIMIT 0",[][]any{}},{"OFFSET 99",[][]any{}}} {
		for _,prepared:=range []bool{false,true} {
			rows,closeQuery,err:=transactionQuery(t.Context(),db,"SELECT FROM meta "+tc.suffix,prepared)
			queryCoreRows(t,rows,err,[]string{},[]string{},tc.rows,false)
			if closeQuery!=nil{closeQuery()}
		}
	}
}
