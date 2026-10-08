package stuntgres_test

import (
	"bytes"
	"strings"
	"testing"
	"stuntgres"
)

func TestQueryLimitViewBounds(t *testing.T) {
	for _,prepared:=range []bool{false,true} {
		t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T){
			db:=queryLimitFixture(t)
			for _,q:=range []string{
				"CREATE SCHEMA bounds_schema","CREATE TABLE bounds_schema.src(id INT)",
				"INSERT INTO bounds_schema.src VALUES(1),(2),(3),(4)","CREATE SEQUENCE bounds_schema.s START WITH 1",
				"CREATE VIEW bounds_schema.dynamic AS SELECT id FROM bounds_schema.src ORDER BY id OFFSET NULL LIMIT (nextval('bounds_schema.s')+1)",
				"CREATE VIEW bounds_schema.unlimited AS SELECT id FROM bounds_schema.src LIMIT ALL OFFSET NULL",
				"CREATE VIEW bounds_schema.empty AS SELECT id FROM bounds_schema.src LIMIT (1-1)",
				"ALTER SCHEMA bounds_schema RENAME TO renamed_bounds","ALTER TABLE renamed_bounds.src RENAME COLUMN id TO renamed",
				"ALTER TABLE renamed_bounds.src RENAME TO renamed_src",
				"ALTER SEQUENCE renamed_bounds.s RENAME TO renamed_s",
			} {execSQL(t,db,q)}
			rows,closeQuery,err:=transactionQuery(t.Context(),db,"SELECT id FROM renamed_bounds.dynamic ORDER BY id",prepared)
			if closeQuery!=nil{defer closeQuery()}
			queryCoreRows(t,rows,err,[]string{"id"},[]string{"INT4"},queryLimitIDs(1,2),false)
			queryLimitSelect(t,db,"SELECT last_value FROM renamed_bounds.renamed_s","last_value","INT8",queryLimitIDs(1))
			queryLimitSelect(t,db,"SELECT id FROM renamed_bounds.unlimited ORDER BY id","id","INT4",queryLimitIDs(1,2,3,4))
			queryLimitSelect(t,db,"SELECT id FROM renamed_bounds.empty","id","INT4",queryLimitIDs())
			_,err=db.Exec("DROP SEQUENCE renamed_bounds.renamed_s RESTRICT")
			typeCoreDiagnostic(t,err,stuntgres.Error{Code:"2BP01",Message:"cannot drop sequence renamed_s because other objects depend on it",Detail:"view dynamic depends on sequence renamed_s",Hint:"Use DROP ... CASCADE to drop the dependent objects too."})
			held,err:=db.Query("SELECT id FROM renamed_bounds.unlimited ORDER BY id LIMIT 2")
			if err!=nil{t.Fatal(err)}
			var dump bytes.Buffer
			if err:=stuntgres.Dump(t.Context(),db,&dump);err!=nil{held.Close();t.Fatal(err)}
			if err:=stuntgres.Restore(t.Context(),db,strings.NewReader(dump.String()+"\nSELECT id FROM missing LIMIT 1;"));err==nil{held.Close();t.Fatal("invalid restore accepted")}
			queryLimitSelect(t,db,"SELECT id FROM renamed_bounds.unlimited ORDER BY id","id","INT4",queryLimitIDs(1,2,3,4))
			if err:=stuntgres.Restore(t.Context(),db,bytes.NewReader(dump.Bytes()));err!=nil{held.Close();t.Fatal(err)}
			queryCoreRows(t,held,nil,[]string{"id"},[]string{"INT4"},queryLimitIDs(1,2),false)
			rows,closeQuery,err=transactionQuery(t.Context(),db,"SELECT id FROM renamed_bounds.dynamic ORDER BY id",prepared)
			if closeQuery!=nil{defer closeQuery()}
			queryCoreRows(t,rows,err,[]string{"id"},[]string{"INT4"},queryLimitIDs(1,2,3),false)
		})
	}
}
