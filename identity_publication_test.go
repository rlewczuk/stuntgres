package stuntgres_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestIdentityPublicationFailureConsumesDemandedValues(t *testing.T){
	db:=openLocal(t,stuntgres.NewDriver(),t.Name())
	tableCoreExec(t,db,false,`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(MAXVALUE 2 CACHE 1),p INT NOT NULL)`,0)
	_,err:=db.Exec(`INSERT INTO i VALUES(DEFAULT,1),(DEFAULT,NULL)`);identityState(t,err,"23502")
	wantTuples(t,db,`SELECT * FROM i`,[][]any{})
	wantTuples(t,db,`SELECT last_value,is_called FROM i_id_seq`,[][]any{{int64(2),true}})
	_,err=db.Exec(`INSERT INTO i(p) VALUES(3)`);identityState(t,err,"2200H")
	wantTuples(t,db,`SELECT * FROM i`,[][]any{})
}

func TestIdentitySessionsCacheAndNoDemand(t *testing.T){
	db:=openLocal(t,stuntgres.NewDriver(),t.Name());a,b:=sequenceConn(t,db),sequenceConn(t,db);ctx:=context.Background()
	sequenceExec(t,a,`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(CACHE 2))`)
	held,err:=a.PrepareContext(ctx,`INSERT INTO i DEFAULT VALUES`);if err!=nil{t.Fatal(err)};defer held.Close()
	wantTuples(t,db,`SELECT last_value,is_called FROM i_id_seq`,[][]any{{int64(1),false}})
	identityConnExec(t,ctx,a,`INSERT INTO i OVERRIDING SYSTEM VALUE VALUES(99)`,1)
	identityConnExec(t,ctx,a,`INSERT INTO i OVERRIDING SYSTEM VALUE SELECT id FROM i WHERE false`,0)
	wantTuples(t,db,`SELECT last_value,is_called FROM i_id_seq`,[][]any{{int64(1),false}})
	if _,err=held.ExecContext(ctx);err!=nil{t.Fatal(err)};sequenceValue(t,a,`SELECT currval('i_id_seq')`,1)
	identityConnExec(t,ctx,b,`INSERT INTO i DEFAULT VALUES`,1);sequenceValue(t,b,`SELECT currval('i_id_seq')`,3)
	if _,err=held.ExecContext(ctx);err!=nil{t.Fatal(err)};sequenceValue(t,a,`SELECT lastval()`,2)
	wantTuples(t,db,`SELECT last_value,is_called FROM i_id_seq`,[][]any{{int64(4),true}})
	sequenceExec(t,a,`BEGIN READ ONLY`);_,err=a.ExecContext(ctx,`INSERT INTO i DEFAULT VALUES`);identityState(t,err,"25006");sequenceExec(t,a,`ROLLBACK`)
	wantTuples(t,db,`SELECT last_value,is_called FROM i_id_seq`,[][]any{{int64(4),true}})
}

func identityConnExec(t *testing.T,ctx context.Context,c *sql.Conn,q string,count int64){t.Helper();r,err:=c.ExecContext(ctx,q);if err!=nil{t.Fatal(q,err)};if n,err:=r.RowsAffected();err!=nil||n!=count{t.Fatal(q,n,err)}}
