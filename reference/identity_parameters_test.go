//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceIdentityDiscardedParameterIndex(t *testing.T){identityReferenceCase(t,func(t *testing.T,db *sql.DB){
	identityRefExec(t,db,`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`,0)
	held,err:=db.Prepare(`INSERT INTO i(p,id) OVERRIDING USER VALUE VALUES($1,$2)`);if err!=nil{t.Fatal(err)};defer held.Close()
	_,err=held.Exec(7,"bad");want:=controlsReferenceDiagnostic("22P02",`invalid input syntax for type integer: "bad"`);want.Context="unnamed portal parameter $2 = '...'";referenceAssertDiagnostic(t,err,&want)
	identityRefRows(t,db,`SELECT last_value FROM i_id_seq`,[][]int64{{1}})
	if _,err=held.Exec(7,99);err!=nil{t.Fatal(err)};identityRefRows(t,db,`SELECT * FROM i`,[][]int64{{1,7}})
	_,err=db.Exec(`INSERT INTO i OVERRIDING USER VALUE VALUES(CAST($1::text AS integer),8)`,"bad");if err!=nil{t.Fatal("discarded execution conversion ran",err)}
	identityRefRows(t,db,`SELECT * FROM i ORDER BY id`,[][]int64{{1,7},{2,8}})
	_,err=db.Exec(`INSERT INTO i(p) VALUES(CAST($1::text AS integer))`,"bad");want=controlsReferenceDiagnostic("22P02",`invalid input syntax for type integer: "bad"`);referenceAssertDiagnostic(t,err,&want)
	identityRefExec(t,db,`CREATE TABLE ordinary(a INT,b INT)`,0);_,err=db.Exec(`INSERT INTO ordinary VALUES($1,$2)`,8,"bad");want.Context="unnamed portal parameter $2 = '...'";referenceAssertDiagnostic(t,err,&want)
})}
