package stuntgres_test

import "testing"

// Public successful writes prove direct/join tail freedom and zero-limit freedom.
// Stored marks below an outer sort hold the tail: native held/refill ownership
// is proved internally; the reference suite observes actual PostgreSQL waiters.
func TestQueryLimitLocks(t *testing.T) {
	for _, prepared := range []bool{false,true} {
		for _, source := range []string{"t", "marked", "t JOIN u USING(id)"} {
			for _, zero := range []bool{false,true} {
				t.Run(source+map[bool]string{false:"/one",true:"/zero"}[zero]+map[bool]string{false:"/direct",true:"/prepared"}[prepared],func(t *testing.T) {
					_,a,b,ctx := rowLockPublicViewFixture(t)
					transactionAffected(t,ctx,a,"INSERT INTO t VALUES(4,40)",false,1)
					transactionAffected(t,ctx,a,"CREATE TABLE u(id INT)",false,0)
					transactionAffected(t,ctx,a,"INSERT INTO u VALUES(1),(2),(3),(4)",false,4)
					transactionAffected(t,ctx,a,"CREATE VIEW marked AS SELECT id,v FROM t FOR UPDATE",false,0)
					holder,err := a.BeginTx(ctx,nil)
					if err != nil { t.Fatal(err) }
					defer holder.Rollback()
					query := "SELECT v FROM "+source+" ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE"
					want := []int64{20}
					if zero { query = "SELECT v FROM "+source+" ORDER BY id LIMIT 0 FOR UPDATE"; want = nil }
					rowLockPublicSelect(t,ctx,holder,query,prepared,"v","INT4",want...)
					if source == "marked" && !zero {
						// The inner mark supplies every row to the outer blocking sort.
						// Do not demand a successful tail write until the holder ends.
						if err := holder.Rollback(); err != nil { t.Fatal(err) }
					}
					first := 3
					if zero { first = 1 }
					for id := first; id <= 4; id++ {
						query := "UPDATE t SET v=v+1 WHERE id=$1"
						result,err := b.ExecContext(ctx,query,id)
						if err != nil { t.Fatal("unconsumed row was locked",id,err) }
						count,err := result.RowsAffected()
						if err != nil || count != 1 { t.Fatal(count,err) }
					}
					if source == "t JOIN u USING(id)" {
						transactionAffected(t,ctx,b,"DELETE FROM u WHERE id=4",false,1)
					}
				})
			}
		}
	}
}
