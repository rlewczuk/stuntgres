//go:build reference

package reference_test

import (
	"testing"
)

func TestReferenceIdentityCatalogAndSerialBoundary(t *testing.T){
	p:=newReferencePair(t)
	identityRefExec(t,p.postgres,`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY)`,0)
	identityRefExec(t,p.postgres,`CREATE TABLE serial_witness(id SERIAL)`,0)
	for _,tc:=range []struct{table,mode,dependency string}{{"i","a","i"},{"serial_witness","","a"}}{
		var mode string;var nullable bool
		if err:=p.postgres.QueryRow(`SELECT attidentity::text,attnotnull FROM pg_attribute WHERE attrelid=$1::regclass AND attname='id'`,tc.table).Scan(&mode,&nullable);err!=nil||mode!=tc.mode||!nullable{t.Fatal(tc.table,mode,nullable,err)}
		var dependency string
		if err:=p.postgres.QueryRow(`SELECT d.deptype::text FROM pg_depend d JOIN pg_class s ON s.oid=d.objid WHERE s.relkind='S' AND d.refobjid=$1::regclass AND d.refobjsubid=1`,tc.table).Scan(&dependency);err!=nil||dependency!=tc.dependency{t.Fatal(tc.table,dependency,err)}
	}
	var isIdentity,generation string
	if err:=p.postgres.QueryRow(`SELECT is_identity,identity_generation FROM information_schema.columns WHERE table_schema='public' AND table_name='i' AND column_name='id'`).Scan(&isIdentity,&generation);err!=nil||isIdentity!="YES"||generation!="ALWAYS"{t.Fatal(isIdentity,generation,err)}
	if _,err:=p.memory.Exec(`CREATE TABLE serial_witness(id SERIAL)`);err==nil{t.Fatal("native SERIAL boundary disappeared")}
	if _,err:=p.memory.Exec(`CREATE TABLE expression_generated(id INT,p INT GENERATED ALWAYS AS (id+1) STORED)`);err==nil{t.Fatal("native expression-generated boundary disappeared")}
	identityRefExec(t,p.memory,`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY)`,0)
	if _,err:=p.memory.Exec(`UPDATE i SET id=DEFAULT`);err==nil{t.Fatal("native UPDATE DEFAULT boundary disappeared")}
	if _,err:=p.memory.Exec(`CREATE TABLE clone(LIKE i INCLUDING IDENTITY)`);err==nil{t.Fatal("native LIKE INCLUDING IDENTITY boundary disappeared")}
}
