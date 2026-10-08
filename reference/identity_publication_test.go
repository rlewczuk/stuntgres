//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceIdentityPublicationFailure(t *testing.T){identityReferenceCase(t,func(t *testing.T,db *sql.DB){
	identityRefExec(t,db,`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(MAXVALUE 2 CACHE 1),p INT NOT NULL)`,0)
	_,err:=db.Exec(`INSERT INTO i VALUES(DEFAULT,1),(DEFAULT,NULL)`);identityRefState(t,err,"23502")
	identityRefRows(t,db,`SELECT * FROM i`,nil)
	identityRefRows(t,db,`SELECT last_value FROM i_id_seq`,[][]int64{{2}})
	_,err=db.Exec(`INSERT INTO i(p) VALUES(3)`);identityRefState(t,err,"2200H")
	identityRefRows(t,db,`SELECT * FROM i`,nil)
})}

func TestReferenceIdentitySessionsCacheAndNoDemand(t *testing.T){identityReferenceCase(t,func(t *testing.T,db *sql.DB){
	ctx:=t.Context();a,err:=db.Conn(ctx);if err!=nil{t.Fatal(err)};defer a.Close();b,err:=db.Conn(ctx);if err!=nil{t.Fatal(err)};defer b.Close()
	sequenceProbeExec(t,ctx,a,`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(CACHE 2))`)
	held,err:=a.PrepareContext(t.Context(),`INSERT INTO i DEFAULT VALUES`);if err!=nil{t.Fatal(err)};defer held.Close()
	identityRefRows(t,db,`SELECT last_value FROM i_id_seq`,[][]int64{{1}})
	identityRefConnExec(t,ctx,a,`INSERT INTO i OVERRIDING SYSTEM VALUE VALUES(99)`,1);identityRefConnExec(t,ctx,a,`INSERT INTO i OVERRIDING SYSTEM VALUE SELECT id FROM i WHERE false`,0)
	identityRefRows(t,db,`SELECT last_value FROM i_id_seq`,[][]int64{{1}})
	if _,err=held.Exec();err!=nil{t.Fatal(err)};sequenceProbeValue(t,ctx,a,`SELECT currval('i_id_seq')`,1)
	identityRefConnExec(t,ctx,b,`INSERT INTO i DEFAULT VALUES`,1);sequenceProbeValue(t,ctx,b,`SELECT currval('i_id_seq')`,3)
	if _,err=held.Exec();err!=nil{t.Fatal(err)};sequenceProbeValue(t,ctx,a,`SELECT lastval()`,2)
	identityRefRows(t,db,`SELECT last_value FROM i_id_seq`,[][]int64{{4}})
	sequenceProbeExec(t,ctx,a,`BEGIN READ ONLY`);_,err=a.ExecContext(ctx,`INSERT INTO i DEFAULT VALUES`);identityRefState(t,err,"25006");sequenceProbeExec(t,ctx,a,`ROLLBACK`)
	identityRefRows(t,db,`SELECT last_value FROM i_id_seq`,[][]int64{{4}})
})}

func identityRefConnExec(t *testing.T,ctx context.Context,c *sql.Conn,q string,count int64){t.Helper();r,err:=c.ExecContext(ctx,q);if err!=nil{t.Fatal(q,err)};if n,err:=r.RowsAffected();err!=nil||n!=count{t.Fatal(q,n,err)}}
