package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityCancellation(t *testing.T) {
	for _, call:=range []string{"pg_visibility_map($1::oid,0)","pg_visibility($1::oid,0)","pg_visibility_map($1::oid)","pg_visibility($1::oid)","pg_visibility_map_summary($1::oid)"} {
		t.Run(call,func(t *testing.T){
			pvCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
				oid:=pvOID(t,p,c,"pv_part.heap")
				q:="SELECT * FROM pv_part."+call
				st,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer st.Close()
				if !b.PG {
					ctx,cancel:=context.WithCancel(p.Ctx);cancel()
					rows,err:=st.QueryContext(ctx,oid)
					if rows!=nil {rows.Close();t.Fatal("precanceled call published cursor")}
					fixture.LexicalOrdinary(t,err,context.Canceled)
				}
				holder:=pvConn(t,p,b);defer holder.Close();defer pvRollback(holder)
				waiterPID,holderPID:=0,0
				if b.PG {waiterPID=fixture.LockPID(t,p.Ctx,c);holderPID=fixture.LockPID(t,p.Ctx,holder)}
				pvRun(t,p,b,holder,fixture.Exec("BEGIN",0))
				pvRun(t,p,b,holder,fixture.Exec("LOCK TABLE pv_part.heap IN ACCESS EXCLUSIVE MODE",0))
				fixture.BinaryConversionActiveCancelAfter(t,p,b,c,q,"(*relationRequest).wait",func(ctx context.Context){
					if b.PG {fixture.LockWaitBehind(t,ctx,waiterPID,holderPID,oid,"AccessShareLock")}
				},func(ctx context.Context)fixture.RelationOutcome{
					rows,err:=st.QueryContext(ctx,oid);return fixture.RelationOutcome{Rows:rows,Err:err}
				})
				pvRun(t,p,b,holder,fixture.Exec("ROLLBACK",0))
				pvEmpty(t,p,b,c,"pv_part.heap")
				// An ordinary read cannot expose leaked AccessShare contributions.
				// A later independent exclusive owner must acquire the exact heap.
				pvRun(t,p,b,holder,fixture.Exec("BEGIN",0))
				pvRun(t,p,b,holder,fixture.Exec("SET LOCAL lock_timeout='1s'",0))
				pvRun(t,p,b,holder,fixture.Exec("LOCK TABLE pv_part.heap IN ACCESS EXCLUSIVE MODE",0))
				pvRun(t,p,b,holder,fixture.Exec("ROLLBACK",0))
				pvEmpty(t,p,b,c,"pv_part.heap")
			})
		})
	}
	t.Run("opposite_owner_captures",func(t *testing.T){
		pvCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
			peer:=pvConn(t,p,b);defer peer.Close();defer pvRollback(peer);defer pvRollback(c)
			for i,owner:=range []*sql.Conn{c,peer} {
				pvRun(t,p,b,owner,fixture.Exec("BEGIN",0))
				pvRun(t,p,b,owner,fixture.Exec(fmt.Sprintf("INSERT INTO pv_part.heap VALUES(%d)",i+1),1))
			}
			ctx,cancel:=context.WithCancel(p.Ctx);defer cancel()
			start:=make(chan struct{});done:=make(chan fixture.RelationOutcome,2)
			remaining:=2
			defer func(){
				cancel()
				for remaining>0 {
					outcome:=fixture.LockResult(t,done)
					remaining--
					if outcome.Rows!=nil {outcome.Rows.Close()}
				}
			}()
			for _,owner:=range []*sql.Conn{c,peer} {
				go func(owner *sql.Conn){
					<-start
					rows,err:=owner.QueryContext(ctx,"SELECT * FROM pv_part.pg_visibility_map_summary('pv_part.heap'::regclass)")
					done<-fixture.RelationOutcome{Rows:rows,Err:err}
				}(owner)
			}
			close(start)
			for remaining>0 {
				outcome:=fixture.LockResult(t,done)
				remaining--
				if outcome.Err!=nil {t.Fatal("opposite capture",outcome.Err)}
				if outcome.Rows==nil {t.Fatal("opposite capture missing cursor")}
				func(){
					defer outcome.Rows.Close()
					var av,af int64
					if !outcome.Rows.Next() {t.Fatal("opposite capture missing row",outcome.Rows.Err())}
					if err:=outcome.Rows.Scan(&av,&af);err!=nil||av!=0||af!=0 {t.Fatal("opposite capture",av,af,err)}
					if outcome.Rows.Next()||outcome.Rows.Err()!=nil {t.Fatal("opposite capture tail",outcome.Rows.Err())}
				}()
			}
			pvRun(t,p,b,c,fixture.Exec("COMMIT",0));pvRun(t,p,b,peer,fixture.Exec("ROLLBACK",0))
			pvFreeze(t,p,b,c);pvOneFrozen(t,p,b,c)
		})
	})
}
