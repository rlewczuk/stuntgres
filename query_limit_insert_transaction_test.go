package stuntgres_test

import "testing"

func TestQueryLimitInsertPrivateWrites(t *testing.T) {
	for _,prepared:=range []bool{false,true}{
		for _,commit:=range []bool{false,true}{
			t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared]+map[bool]string{false:"/rollback",true:"/commit"}[commit],func(t *testing.T){
				db:=queryLimitFixture(t)
				tx,err:=db.BeginTx(t.Context(),nil);if err!=nil{t.Fatal(err)};defer tx.Rollback()
				if _,err:=tx.Exec("INSERT INTO t VALUES(8,8)");err!=nil{t.Fatal(err)}
				q:="INSERT INTO t SELECT id,v FROM t ORDER BY id DESC LIMIT 1"
				var count int64
				if prepared{
					stmt,err:=tx.Prepare(q);if err!=nil{t.Fatal(err)}
					result,err:=stmt.Exec();stmt.Close();if err!=nil{t.Fatal(err)};count,err=result.RowsAffected();if err!=nil{t.Fatal(err)}
				}else{result,err:=tx.Exec(q);if err!=nil{t.Fatal(err)};count,err=result.RowsAffected();if err!=nil{t.Fatal(err)}}
				if count!=1{t.Fatal(count)}
				rows,err:=tx.Query("SELECT id FROM t ORDER BY id")
				queryCoreRows(t,rows,err,[]string{"id"},[]string{"INT4"},queryLimitIDs(1,2,3,4,8,8),false)
				queryLimitSelect(t,db,"SELECT id FROM t ORDER BY id","id","INT4",queryLimitIDs(1,2,3,4))
				want:=queryLimitIDs(1,2,3,4)
				if commit{err=tx.Commit();want=queryLimitIDs(1,2,3,4,8,8)}else{err=tx.Rollback()}
				if err!=nil{t.Fatal(err)}
				queryLimitSelect(t,db,"SELECT id FROM t ORDER BY id","id","INT4",want)
			})
		}
	}
}
