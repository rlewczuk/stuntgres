package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestIdentityDiscardedParameterIndex(t *testing.T){
	db:=openLocal(t,stuntgres.NewDriver(),t.Name());tableCoreExec(t,db,false,`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`,0)
	held,err:=db.Prepare(`INSERT INTO i(p,id) OVERRIDING USER VALUE VALUES($1,$2)`);if err!=nil{t.Fatal(err)};defer held.Close()
	_,err=held.Exec(7,"bad");typeCoreDiagnostic(t,err,stuntgres.Error{Code:"22P02",Message:`invalid input syntax for type integer: "bad"`,Context:"unnamed portal parameter $2 = '...'"})
	wantTuples(t,db,`SELECT last_value,is_called FROM i_id_seq`,[][]any{{int64(1),false}})
	if _,err=held.Exec(7,99);err!=nil{t.Fatal(err)};wantTuples(t,db,`SELECT * FROM i`,[][]any{{int64(1),int64(7)}})
	_,err=db.Exec(`INSERT INTO i(p,id) OVERRIDING SYSTEM VALUE VALUES($1,$2)`,8,"bad");typeCoreDiagnostic(t,err,stuntgres.Error{Code:"22P02",Message:`invalid input syntax for type integer: "bad"`,Context:"unnamed portal parameter $2 = '...'"})
	_,err=db.Exec(`INSERT INTO i OVERRIDING USER VALUE VALUES(CAST($1::text AS integer),8)`,"bad");if err!=nil{t.Fatal("discarded execution conversion ran",err)}
	wantTuples(t,db,`SELECT * FROM i ORDER BY id`,[][]any{{int64(1),int64(7)},{int64(2),int64(8)}})
	_,err=db.Exec(`INSERT INTO i(p) VALUES(CAST($1::text AS integer))`,"bad");typeCoreDiagnostic(t,err,stuntgres.Error{Code:"22P02",Message:`invalid input syntax for type integer: "bad"`})
	tableCoreExec(t,db,false,`CREATE TABLE ordinary(a INT,b INT)`,0);_,err=db.Exec(`INSERT INTO ordinary VALUES($1,$2)`,8,"bad");typeCoreDiagnostic(t,err,stuntgres.Error{Code:"22P02",Message:`invalid input syntax for type integer: "bad"`,Context:"unnamed portal parameter $2 = '...'"})
}
