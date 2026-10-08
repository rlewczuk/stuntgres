package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestSerialControls(t *testing.T) {
	db := openLocal(t,stuntgres.NewDriver(),t.Name())
	ctx := context.Background()
	for _,q := range []string{`CREATE TABLE called(id SERIAL,v TEXT)`,`CREATE TABLE untouched(a SMALLSERIAL,b BIGSERIAL)`,`INSERT INTO called VALUES(100,'duplicate'),(100,'duplicate'),(0,'zero'),(-1,'negative')`,`SELECT setval('called_id_seq',7,true)`,`ALTER SEQUENCE called_id_seq RENAME TO renamed`,`ALTER SEQUENCE renamed MAXVALUE 10000 CACHE 1`,`COMMENT ON SEQUENCE renamed IS 'serial generator'`} {
		if _,err := db.Exec(q); err != nil { t.Fatal(q,err) }
	}
	held,err := db.Prepare(`INSERT INTO called(v) VALUES('future')`)
	if err != nil { t.Fatal(err) }; defer held.Close()
	old,err := db.Query(`SELECT id,v FROM called ORDER BY id`)
	if err != nil { t.Fatal(err) }; defer old.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx,db,&dump); err != nil { t.Fatal(err) }
	wantTuples(t,db,`SELECT last_value,is_called FROM renamed`,[][]any{{int64(7),true}})
	if err := stuntgres.Reset(ctx,db); err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(ctx,db,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx,db,&redump); err != nil { t.Fatal(err) }
	if !bytes.Equal(dump.Bytes(),redump.Bytes()) { t.Fatal("control definitions/state changed",dump.String(),redump.String()) }
	if !old.Next() { t.Fatal(old.Err()) }
	var id int64; var value string
	if err := old.Scan(&id,&value); err != nil || id != -1 || value != "negative" { t.Fatal(id,value,err) }
	old.Close()
	if r,err := held.Exec(); err != nil { t.Fatal(err) } else if n,err := r.RowsAffected(); err != nil || n != 1 { t.Fatal(n,err) }
	tableCoreExec(t,db,false,`INSERT INTO untouched DEFAULT VALUES`,1)
	wantTuples(t,db,`SELECT * FROM untouched`,[][]any{{int64(1),int64(1)}})
	wantTuples(t,db,`SELECT id,v FROM called ORDER BY id`,[][]any{{int64(-1),"negative"},{int64(0),"zero"},{int64(8),"future"},{int64(100),"duplicate"},{int64(100),"duplicate"}})
	for _,script := range []string{`CREATE TABLE broken(id INT DEFAULT nextval('missing'));`,`CREATE TABLE notseq(id INT); CREATE TABLE broken(id INT DEFAULT nextval('notseq'));`,`CREATE TABLE broken(id SERIAL); SELECT setval('broken_id_seq',0,true);`} {
		if err := stuntgres.Restore(ctx,db,strings.NewReader(script)); err == nil { t.Fatal("invalid Restore succeeded",script) }
		wantTuples(t,db,`SELECT * FROM untouched`,[][]any{{int64(1),int64(1)}})
	}
	if _,err := held.Exec(); err != nil { t.Fatal(err) }
	wantTuples(t,db,`SELECT id FROM called WHERE id<100 AND id>0 ORDER BY id`,[][]any{{int64(8)},{int64(9)}})
}
