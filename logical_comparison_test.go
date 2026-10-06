package stuntgres_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
)

// The integrated comparison capability remains a scalar atom above logical
// operators; this witnesses composition without broadening comparison operands.
func TestLogicalComparisonComposition(t *testing.T) {
	db := openLocal(t,stuntgres.NewDriver(),"logical-comparisons")
	execSQL(t,db,"CREATE TABLE t(id INT,a BOOL,b BOOL)")
	execSQL(t,db,"INSERT INTO t VALUES(1,TRUE,TRUE),(2,TRUE,FALSE),(3,TRUE,NULL),(4,FALSE,TRUE),(5,FALSE,FALSE),(6,FALSE,NULL),(7,NULL,TRUE),(8,NULL,FALSE),(9,NULL,NULL)")
	q := "SELECT id,id>=2 AND a=b,NOT id<4 OR a<>b FROM t ORDER BY id"
	want := [][]any{{int64(1),false,false},{int64(2),false,true},{int64(3),nil,nil},{int64(4),false,true},{int64(5),true,true},{int64(6),nil,true},{int64(7),nil,true},{int64(8),nil,true},{int64(9),nil,true}}
	for _,prepared := range []bool{false,true} {
		var rows *sql.Rows
		var err error
		if prepared {
			stmt,e := db.Prepare(q); if e != nil { t.Fatal(e) }; defer stmt.Close(); rows,err = stmt.Query()
		} else { rows,err = db.Query(q) }
		if err != nil { t.Fatal(err) }
		meta,err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }
		for i := 1; i < 3; i++ { if meta[i].Name() != "?column?" || meta[i].DatabaseTypeName() != "BOOL" || meta[i].ScanType() != reflect.TypeFor[bool]() { t.Fatal(meta[i]) } }
		got := [][]any{}
		for rows.Next() { var id,a,b any; if err := rows.Scan(&id,&a,&b); err != nil { t.Fatal(err) }; got = append(got,[]any{id,a,b}) }
		if err := rows.Err(); err != nil { t.Fatal(err) }; rows.Close()
		if !reflect.DeepEqual(got,want) { t.Fatal(got,want) }
	}
	wantTuples(t,db,"SELECT id FROM t WHERE id>=2 AND a=b ORDER BY id",[][]any{{int64(5)}})
	wantTuples(t,db,"SELECT id FROM t WHERE NOT id<4 OR a<>b ORDER BY id",[][]any{{int64(2)},{int64(4)},{int64(5)},{int64(6)},{int64(7)},{int64(8)},{int64(9)}})
}
