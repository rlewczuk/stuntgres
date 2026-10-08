package engine

import "testing"

func TestQueryLimitLockedCache(t *testing.T) {
	for _,prepared:=range []bool{false,true} {
		for _,source:=range []string{"t","own","stored_rows"} {
			t.Run(source+map[bool]string{false:"/direct",true:"/prepared"}[prepared],func(t *testing.T){
				e,a,b,c:=rowLockEngineFixture(t)
				sessionSQL(t,c,"CREATE SEQUENCE s")
				sessionSQL(t,c,"CREATE VIEW own AS SELECT id,nextval('s') AS n FROM t WHERE id=1 ORDER BY n,n LIMIT 1 FOR UPDATE")
				sessionSQL(t,c,"CREATE VIEW stored_rows AS SELECT id,v FROM t WHERE id=1 FOR UPDATE")
				q:="SELECT nextval('s') AS n FROM t WHERE id=1 ORDER BY n,n LIMIT 1 FOR UPDATE"
				want:=int64(2)
				if source=="own"{q="SELECT n FROM own"}
				if source=="stored_rows"{q="SELECT nextval('s') AS n FROM stored_rows ORDER BY n,n LIMIT 1";want=1}
				ar:=transactionBegin(t,a)
				transactionRows(t,a,"SELECT v FROM t WHERE id=1 FOR UPDATE",10)
				sessionSQL(t,a,"UPDATE t SET v=30 WHERE id=1")
				done:=queryLimitAsync(t,e,b,q,prepared)
				rowLockQueued(t,e,a.transactions.ticket,rowUpdate)
				transactionEnd(t,a,ar,true)
				rowLockAwait(t,done,want)
				transactionRows(t,b,"SELECT currval('s')",want)
				lockEmpty(t,e)
			})
		}
	}
}

func TestQueryLimitLocalOuterSort(t *testing.T) {
	for _,prepared:=range []bool{false,true} {
		for _,source:=range []string{"stored","stored_rows"} {
			t.Run(source+map[bool]string{false:"/direct",true:"/prepared"}[prepared],func(t *testing.T){
				e,a,b,c:=rowLockEngineFixture(t)
				sessionSQL(t,c,"CREATE SEQUENCE s")
				sessionSQL(t,c,"CREATE VIEW stored AS SELECT id,v FROM t ORDER BY v+1,id FOR UPDATE")
				sessionSQL(t,c,"CREATE VIEW stored_rows AS SELECT id,v FROM t WHERE id=1 FOR UPDATE")
				q:="SELECT v+1 AS k FROM stored ORDER BY k,id"
				want:=[]int64{21,31}
				if source=="stored_rows"{q="SELECT nextval('s') AS n FROM stored_rows ORDER BY n,n";want=[]int64{1}}
				ar:=transactionBegin(t,a)
				transactionRows(t,a,"SELECT v FROM t WHERE id=1 FOR UPDATE",10)
				sessionSQL(t,a,"UPDATE t SET v=30 WHERE id=1")
				done:=queryLimitAsync(t,e,b,q,prepared)
				rowLockQueued(t,e,a.transactions.ticket,rowUpdate)
				transactionEnd(t,a,ar,true)
				rowLockAwait(t,done,want...)
				lockEmpty(t,e)
			})
		}
	}
}
