package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoReferentialConstraintsShape(t *testing.T) {
	ircScenario(t, func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,db string) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		run(ircProjection(b.PG,"constraint_schema='irc_scope'",nil))
		var attributes, columns [][]any
		for i,name := range ircColumns {
			domain,base,data := "sql_identifier",int64(19),"name"
			if i>=6 { domain,base,data="character_data",1043,"character varying" }
			attributes=append(attributes,[]any{int64(i+1),name,"information_schema",domain,"d",base,int64(-1),false,int64(950),int64(0),false})
			columns=append(columns,[]any{int64(i+1),name,"information_schema",domain,data,"YES"})
		}
		fixture.PgDescriptionExecutorRows(t,p,b,c,fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.referential_constraints'::regclass AND a.attnum>0 ORDER BY a.attnum`,[]string{"attnum","attname","nspname","typname","typtype","typbasetype","atttypmod","attnotnull","attcollation","attndims","typnotnull"},[]string{"INT2","NAME","NAME","NAME","CHAR","OID","INT4","BOOL","OID","INT2","BOOL"},attributes))
		run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.referential_constraints'::regclass`,[]string{"same_type","same_relation","array_element","array_link"},[]string{"BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true}}))
		run(fixture.Select(`SELECT ordinal_position,column_name,domain_schema,domain_name,data_type,is_nullable FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='referential_constraints' ORDER BY ordinal_position`,[]string{"ordinal_position","column_name","domain_schema","domain_name","data_type","is_nullable"},[]string{"INT4","NAME","NAME","NAME","VARCHAR","VARCHAR"},columns))
		ircSeed(run)
		want:=[][]any{ircRow(db,"irc_scope","child_fk","irc_scope","parent_pk","NONE","NO ACTION","NO ACTION")}
		for _,where:=range []string{"constraint_schema='irc_scope'","FALSE","constraint_schema='irc_absent'","constraint_schema='irc_scope' AND constraint_name='absent'"} {
			rows:=want; if where!="constraint_schema='irc_scope'" { rows=nil }
			for _,prepared:=range []bool{false,true} { for _,star:=range []bool{false,true} {
				s:=ircProjection(b.PG,where,rows); s.Prepared=prepared
				if star { s.Query=strings.Replace(s.Query,strings.Join(ircColumns,","),"*",1) }
				run(s)
				cursor,closeQuery,err:=fixture.TransactionQuery(p.Ctx,c,s.Query,prepared); if err!=nil { t.Fatal(err) }
				desc,err:=cursor.ColumnTypes(); if err!=nil { t.Fatal(err) }; ircMetadata(t,desc,b.PG)
				for cursor.Next() {}; if err:=cursor.Err(); err!=nil { t.Fatal(err) }
				ircMetadata(t,desc,b.PG); if err:=closeQuery(); err!=nil { t.Fatal(err) }; ircMetadata(t,desc,b.PG)
			} }
		}
		s:=ircProjection(b.PG,"constraint_schema='irc_scope' AND constraint_name=$1",nil)
		held:=ircHold(t,p,c,s.Query)
		for _,arg:=range []any{"child_fk","absent",nil} {
			rows:=[][]any(nil); if arg=="child_fk" { rows=want }
			ircCheck(t,p,c,r,held,ircProjection(b.PG,"constraint_schema='irc_scope' AND constraint_name=$1",rows,arg))
		}
	})
}
