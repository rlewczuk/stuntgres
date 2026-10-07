package stuntgres_test

import (
 "context"
 "errors"
 "stuntgres"
 "testing"
)

func TestViewJoinedSourcesAndFrozenLayouts(t *testing.T) {
 ctx := context.Background()
 db := openLocal(t, stuntgres.NewDriver(), t.Name())
 for _, q := range []string{
  "CREATE TABLE a(unused INT,id INT,x TEXT)",
  "CREATE TABLE b(unused INT,id INT,y TEXT)",
  "INSERT INTO a VALUES(0,1,'a'),(0,2,'b'),(0,3,'c')",
  "INSERT INTO b VALUES(0,1,'u'),(0,1,'v'),(0,3,NULL)",
  "CREATE VIEW v AS SELECT a.id,a.x,b.y FROM a JOIN b ON a.id=b.id",
  "CREATE VIEW nested AS SELECT id,x,y FROM v",
  "CREATE VIEW mixed AS SELECT a.*,DATE '2000-01-02' AS d,b.y FROM a JOIN b ON a.id=b.id",
  "CREATE VIEW hidden AS SELECT a.id FROM a JOIN b ON a.id=b.id",
  "CREATE TABLE copied(id INT,x TEXT,y TEXT)",
  "INSERT INTO copied SELECT id,x,y FROM nested",
 } { execSQL(t, db, q) }
 want := [][]any{{int64(1),"a","u"},{int64(1),"a","v"},{int64(3),"c",nil}}
 check := func(q string) {
  t.Helper()
  rows, err := db.QueryContext(ctx,q)
  queryCoreRows(t,rows,err,[]string{"id","x","y"},[]string{"INT4","TEXT","TEXT"},want,true)
  stmt, err := db.PrepareContext(ctx,q)
  if err != nil { t.Fatal(err) }
  defer stmt.Close()
  rows, err = stmt.QueryContext(ctx)
  queryCoreRows(t,rows,err,[]string{"id","x","y"},[]string{"INT4","TEXT","TEXT"},want,true)
 }
 check("SELECT id,x,y FROM v")
 check("SELECT id,x,y FROM nested")
 check("SELECT id,x,y FROM copied")
 rows, err := db.QueryContext(ctx,"SELECT unused,id,x,d=DATE '2000-01-02',y FROM mixed")
 queryCoreRows(t,rows,err,[]string{"unused","id","x","?column?","y"},[]string{"INT4","INT4","TEXT","BOOL","TEXT"},[][]any{{int64(0),int64(1),"a",true,"u"},{int64(0),int64(1),"a",true,"v"},{int64(0),int64(3),"c",true,nil}},true)
 for _, q := range []string{
  "ALTER TABLE a RENAME TO aa", "ALTER TABLE b RENAME TO bb",
  "ALTER TABLE aa RENAME COLUMN id TO aid", "ALTER TABLE bb RENAME COLUMN id TO bid",
  "ALTER TABLE bb DROP COLUMN unused",
  "CREATE TABLE a(id INT,x TEXT)", "CREATE TABLE b(id INT,y TEXT)",
  "INSERT INTO a VALUES(99,'wrong')", "INSERT INTO b VALUES(99,'wrong')",
 } { execSQL(t,db,q) }
 check("SELECT id,x,y FROM v")
 check("SELECT id,x,y FROM nested")
 rows,err = db.QueryContext(ctx,"SELECT unused,id,x,d=DATE '2000-01-02',y FROM mixed")
 queryCoreRows(t,rows,err,[]string{"unused","id","x","?column?" ,"y"},[]string{"INT4","INT4","TEXT","BOOL","TEXT"},[][]any{{int64(0),int64(1),"a",true,"u"},{int64(0),int64(1),"a",true,"v"},{int64(0),int64(3),"c",true,nil}},true)
 rows,err = db.QueryContext(ctx,"SELECT * FROM mixed")
 if err != nil { t.Fatal(err) }
 columns,err := rows.ColumnTypes()
 if err != nil { rows.Close(); t.Fatal(err) }
 names,types := []string{"unused","id","x","d","y"},[]string{"INT4","INT4","TEXT","DATE","TEXT"}
 if len(columns)!=len(names) { rows.Close(); t.Fatal("frozen star output width changed",columns) }
 for i,column := range columns {
  if column.Name()!=names[i] || column.DatabaseTypeName()!=types[i] { rows.Close(); t.Fatal(i,column) }
 }
 if err=rows.Close(); err!=nil { t.Fatal(err) }
 _,err = db.ExecContext(ctx,"ALTER TABLE bb DROP COLUMN bid")
 if err == nil { t.Fatal("hidden second-source join dependency was dropped") }
 var state interface{ SQLState() string }
 if !errors.As(err,&state) || state.SQLState() != "2BP01" { t.Fatal(err) }
 // Drop an unreferenced leading attribute on both leaves in a separate view.
 compact := openLocal(t,stuntgres.NewDriver(),t.Name()+"-compact")
 for _,q := range []string{"CREATE TABLE a(unused INT,id INT)","CREATE TABLE b(unused INT,id INT)","INSERT INTO a VALUES(0,7)","INSERT INTO b VALUES(0,7)","CREATE VIEW v AS SELECT a.id FROM a JOIN b ON a.id=b.id","ALTER TABLE a DROP COLUMN unused","ALTER TABLE b DROP COLUMN unused"} { execSQL(t,compact,q) }
 rows,err = compact.QueryContext(ctx,"SELECT id FROM v")
 queryCoreRows(t,rows,err,[]string{"id"},[]string{"INT4"},[][]any{{int64(7)}},false)
 _,err = compact.ExecContext(ctx,"ALTER TABLE b DROP COLUMN id")
 typeCoreDiagnostic(t,err,stuntgres.Error{Code:"2BP01",Message:"cannot drop column id of table b because other objects depend on it",Detail:"view v depends on column id of table b",Hint:"Use DROP ... CASCADE to drop the dependent objects too."})
}

func TestViewJoinedRepeatedOccurrences(t *testing.T) {
 db := openLocal(t,stuntgres.NewDriver(),t.Name())
 for _,q := range []string{"CREATE TABLE t(unused INT,id INT)","INSERT INTO t VALUES(0,1),(0,2),(0,3)","CREATE VIEW pairs AS SELECT l.id AS left_id,r.id AS right_id FROM t AS l JOIN t AS r ON l.id<r.id","ALTER TABLE t DROP COLUMN unused"} { execSQL(t,db,q) }
 want := [][]any{{int64(1),int64(2)},{int64(1),int64(3)},{int64(2),int64(3)}}
 rows,err := db.Query("SELECT left_id,right_id FROM pairs")
 queryCoreRows(t,rows,err,[]string{"left_id","right_id"},[]string{"INT4","INT4"},want,true)
 stmt,err := db.Prepare("SELECT left_id,right_id FROM pairs")
 if err != nil { t.Fatal(err) }; defer stmt.Close()
 rows,err = stmt.Query()
 queryCoreRows(t,rows,err,[]string{"left_id","right_id"},[]string{"INT4","INT4"},want,true)
}
