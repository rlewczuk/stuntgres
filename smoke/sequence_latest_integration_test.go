package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestSequenceLatestIntegrationSmoke(t *testing.T) {
	for _, prepared:=range []bool{false,true} {
		t.Run(map[bool]string{false:"Direct",true:"Prepared"}[prepared],func(t *testing.T) {
			ctx:=context.Background()
			connector,err:=stuntgres.NewDriver().OpenConnector(t.Name()); if err!=nil { t.Fatal(err) }
			db:=sql.OpenDB(connector); defer db.Close()
			a,err:=db.Conn(ctx); if err!=nil { t.Fatal(err) }; defer a.Close()
			b,err:=db.Conn(ctx); if err!=nil { t.Fatal(err) }; defer b.Close()
			exec:=func(x interface{ExecContext(context.Context,string,...any)(sql.Result,error);PrepareContext(context.Context,string)(*sql.Stmt,error)},q string,count int64) {
				t.Helper()
				var r sql.Result; var e error
				if prepared { var s *sql.Stmt; s,e=x.PrepareContext(ctx,q); if e!=nil { t.Fatal(e) }; defer s.Close(); r,e=s.ExecContext(ctx) } else { r,e=x.ExecContext(ctx,q) }
				if e!=nil { t.Fatal(q,e) }; if n,e:=r.RowsAffected(); e!=nil || n!=count { t.Fatal(q,n,e) }
			}
			value:=func(x interface{QueryRowContext(context.Context,string,...any)*sql.Row},q string,want int64) { t.Helper(); var n int64; if e:=x.QueryRowContext(ctx,q).Scan(&n); e!=nil || n!=want { t.Fatal(q,n,e) } }
			diagnostic:=func(err error,code,message,position string) { t.Helper(); var got *stuntgres.Error; want:=stuntgres.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:code,Message:message,Position:position}; if !errors.As(err,&got) || *got!=want { t.Fatalf("got %#v (%v), want %#v",got,err,want) } }
			exec(a,"CREATE SEQUENCE s",0)
			exec(a,"CREATE TABLE t(id INT,n BIGINT,c CHAR(4),v VARCHAR(3) DEFAULT 'ok')",0)
			exec(a,"INSERT INTO t(id,n,c) VALUES(1,0,'é')",1)
			exec(a,"UPDATE t SET n=nextval('s'),c=CAST('é🙂xyz' AS CHAR(4)),v=CAST(nextval('s') AS VARCHAR(3))",1)
			value(a,"SELECT n FROM t",1)
			value(a,"SELECT nextval('s')",3)
			checkCharacter:=func() {
				t.Helper()
				rows,e:=a.QueryContext(ctx,"SELECT c,v FROM t"); if e!=nil { t.Fatal(e) }; defer rows.Close()
				types,e:=rows.ColumnTypes(); if e!=nil || len(types)!=2 { t.Fatal(types,e) }
				for i,want:=range []struct{name string; length int64}{{"BPCHAR",4},{"VARCHAR",3}} { n,ok:=types[i].Length(); if types[i].DatabaseTypeName()!=want.name || !ok || n!=want.length { t.Fatal(types[i],n,ok) } }
				var c,v string; if !rows.Next() { t.Fatal(rows.Err()) }; if e=rows.Scan(&c,&v); e!=nil || c!="é🙂xy" || v!="2" { t.Fatal(c,v,e) }; if rows.Next() || rows.Err()!=nil { t.Fatal("extra character row",rows.Err()) }
			}
			checkCharacter()
			for _,commit:=range []bool{false,true} {
				tx,e:=a.BeginTx(ctx,nil); if e!=nil { t.Fatal(e) }
				exec(tx,"ALTER SEQUENCE s RESTART 100",0)
				exec(tx,"UPDATE t SET n=nextval('s')",1)
				value(tx,"SELECT n FROM t",100)
				value(b,"SELECT n FROM t",1)
				if commit { e=tx.Commit() } else { e=tx.Rollback() }; if e!=nil { t.Fatal(e) }
				if commit { value(b,"SELECT n FROM t",100); value(b,"SELECT nextval('s')",101) } else { value(b,"SELECT n FROM t",1); value(b,"SELECT nextval('s')",4) }
			}
			exec(a,"CREATE TABLE copied(n BIGINT,c CHAR(4),v VARCHAR(3))",0)
			exec(a,"INSERT INTO copied SELECT nextval('s'),c,v FROM t",1)
			value(a,"SELECT n FROM copied",102)
			tx,e:=a.BeginTx(ctx,&sql.TxOptions{Isolation:sql.LevelRepeatableRead}); if e!=nil { t.Fatal(e) }
			value(tx,"SELECT n FROM t",100)
			exec(b,"UPDATE t SET n=200",1)
			value(b,"SELECT nextval('s')",103)
			value(tx,"SELECT nextval('s')",104)
			value(tx,"SELECT n FROM t",100)
			if e=tx.Rollback(); e!=nil { t.Fatal(e) }
			value(b,"SELECT n FROM t",200)
			value(b,"SELECT nextval('s')",105)
			exec(a,"CREATE SEQUENCE cached CACHE 5",0); value(a,"SELECT nextval('cached')",1)
			exec(a,"CREATE TEMP SEQUENCE local_s START 7",0)
			for _,name:=range []string{"nextval","setval"} {
				ro,e:=a.BeginTx(ctx,&sql.TxOptions{ReadOnly:true}); if e!=nil { t.Fatal(e) }
				q:="SELECT nextval('cached')"; if name=="setval" { q="SELECT setval('cached',0)" }
				var n int64; e=ro.QueryRowContext(ctx,q).Scan(&n)
				diagnostic(e,"25006","cannot execute "+name+"() in a read-only transaction","")
				if e=ro.Rollback(); e!=nil { t.Fatal(e) }
			}
			value(a,"SELECT currval('cached')",1)
			value(a,"SELECT last_value FROM cached",5)
			value(a,"SELECT nextval('cached')",2)
			ro,e:=a.BeginTx(ctx,&sql.TxOptions{ReadOnly:true}); if e!=nil { t.Fatal(e) }
			value(ro,"SELECT nextval('local_s')",7); value(ro,"SELECT setval('local_s',9,false)",9); value(ro,"SELECT nextval('local_s')",9)
			if e=ro.Commit(); e!=nil { t.Fatal(e) }
			for _,expression:=range []string{"nextval(NULL)","setval('s',nextval('s'),NULL)","nextval(NULL::regclass)"} {
				q:="CREATE TABLE bad(v BIGINT DEFAULT "+expression+")"
				_,e=a.ExecContext(ctx,q)
				diagnostic(e,"0A000","sequence functions are unsupported in DEFAULT expressions",strconv.Itoa(strings.Index(q,expression)+1))
			}
			for _,expression:=range []string{"NULL::regclass IS NULL","'s'::regclass IS NULL"} {
				q:="CREATE TABLE bad(v BOOL DEFAULT ("+expression+"))"
				_,e=a.ExecContext(ctx,q)
				diagnostic(e,"0A000","regclass is unsupported in DEFAULT expressions",strconv.Itoa(strings.Index(q,expression)+1))
			}
			var dump bytes.Buffer
			if e=stuntgres.Dump(ctx,db,&dump); e!=nil { t.Fatal(e) }
			if e=stuntgres.Reset(ctx,db); e!=nil { t.Fatal(e) }
			if e=stuntgres.Restore(ctx,db,bytes.NewReader(dump.Bytes())); e!=nil { t.Fatal(e) }
			checkCharacter()
			value(a,"SELECT n FROM copied",102)
			value(a,"SELECT nextval('s')",106)
		})
	}
}
