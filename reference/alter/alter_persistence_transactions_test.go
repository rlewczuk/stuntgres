package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterPersistenceAtomicity(t *testing.T) {
	t.Run("inheritance",persistenceInheritanceCases)
	for _, prepared:=range []bool{false,true} {
		for _, initial:=range []string{"","UNLOGGED "} {
			t.Run(initial+map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T) {
				persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
					persistenceExec(t,p,b,c,"CREATE "+initial+"TABLE t(id serial PRIMARY KEY,note text)")
					persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO t VALUES(7,'keep')",1))
					mode,old,new:= "UNLOGGED","p","u";if initial!="" {mode,old,new="LOGGED","u","p"}
					want:=persistenceDiagnostic("23514",`check constraint "bad" of relation "t" is violated by some row`)
					want.Schema,want.Table,want.Constraint="public","t","bad"
					persistenceFailure(t,p,b,c,"ALTER TABLE t SET "+mode+", ADD COLUMN added int DEFAULT 9, ADD CONSTRAINT bad CHECK(id<0)",prepared,want)
					for _,n:=range []string{"t","t_pkey","t_id_seq"} {persistenceProperty(t,p,b,c,n,old)}
					data:=fixture.Select("SELECT * FROM t",[]string{"id","note"},[]string{"INT4","TEXT"},[][]any{{int64(7),"keep"}})
					persistenceRun(t,p,b,c,data)
					peer,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer peer.Close()
					tx,err:=c.BeginTx(p.Ctx,nil);if err!=nil {t.Fatal(err)};defer tx.Rollback()
					persistenceExec(t,p,b,tx,"ALTER TABLE t SET "+mode)
					for _,n:=range []string{"t","t_pkey","t_id_seq"} {persistenceProperty(t,p,b,tx,n,new);persistenceProperty(t,p,b,peer,n,old)}
					if err=tx.Rollback();err!=nil {t.Fatal(err)}
					persistenceProperty(t,p,b,c,"t",old)
					tx,err=c.BeginTx(p.Ctx,nil);if err!=nil {t.Fatal(err)};defer tx.Rollback()
					persistenceExec(t,p,b,tx,"ALTER TABLE t ADD COLUMN outer_col int DEFAULT 4")
					persistenceExec(t,p,b,tx,"SAVEPOINT keep")
					persistenceExec(t,p,b,tx,"ALTER TABLE t SET "+mode)
					persistenceExec(t,p,b,tx,"ROLLBACK TO keep")
					persistenceProperty(t,p,b,tx,"t",old)
					persistenceRun(t,p,b,tx,fixture.Select("SELECT outer_col FROM t",[]string{"outer_col"},[]string{"INT4"},[][]any{{int64(4)}}))
					persistenceExec(t,p,b,tx,"ALTER TABLE t SET "+mode)
					if err=tx.Commit();err!=nil {t.Fatal(err)}
					for _,n:=range []string{"t","t_pkey","t_id_seq"} {persistenceProperty(t,p,b,peer,n,new)}
					persistenceRun(t,p,b,peer,fixture.Select("SELECT * FROM t",[]string{"id","note","outer_col"},[]string{"INT4","TEXT","INT4"},[][]any{{int64(7),"keep",int64(4)}}))
				})
			})
		}
	}
}

func TestReferenceAlterPersistencePrepared(t *testing.T) {
	persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		persistenceExec(t,p,b,c,"CREATE TABLE t(id serial,note text)")
		persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO t VALUES(7,'keep')",1))
		queries:=[]string{"SELECT note FROM t WHERE id=$1","INSERT INTO t VALUES($1,$2)","SELECT nextval('t_id_seq') AS v"}
		held:=make([]*sql.Stmt,len(queries))
		for i,q:=range queries {var err error;held[i],err=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer held[i].Close()}
		for i,action:=range []string{"SET LOGGED","SET UNLOGGED","SET UNLOGGED","SET LOGGED"} {
			persistenceExec(t,p,b,c,"ALTER TABLE t "+action)
			for j,step:=range []fixture.Step{
				fixture.Select(queries[0],[]string{"note"},[]string{"TEXT"},[][]any{{"keep"}},7),
				fixture.Exec(queries[1],1,100+i,"explicit"),
				fixture.Select(queries[2],[]string{"v"},[]string{"INT8"},[][]any{{int64(i+1)}}),
			} {persistenceRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:held[j],SQL:queries[j]},step)}
		}
		want:=persistenceDiagnostic("23514",`check constraint "bad" of relation "t" is violated by some row`)
		want.Schema,want.Table,want.Constraint="public","t","bad"
		persistenceFailure(t,p,b,c,"ALTER TABLE t SET UNLOGGED, ADD CONSTRAINT bad CHECK(id<0)",true,want)
		persistenceRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:held[0],SQL:queries[0]},fixture.Select(queries[0],[]string{"note"},[]string{"TEXT"},[][]any{{"keep"}},7))
		persistenceValue(t,p,b,fixture.HeldStatement{Executor:c,Statement:held[2],SQL:queries[2]},"nextval('t_id_seq')",5)
	})
}
