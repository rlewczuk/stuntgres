package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func publicationRelRoundtrip(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	if b.PG { return }
	before:=publicationRelRead(t,p,b,b.DB,publicationRelRaw,1,false)
	old,err:=p.Native.QueryContext(p.Ctx,publicationRelRaw)
	if err!=nil { t.Fatal(err) }
	defer old.Close()
	metadata,err:=old.ColumnTypes()
	if err!=nil { t.Fatal(err) }
	publicationRelMetadata(t,false,metadata)
	stmt,err:=b.DB.PrepareContext(p.Ctx,publicationRelEdges)
	if err!=nil { t.Fatal(err) }
	defer stmt.Close()
	held:=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:publicationRelEdges}
	var dump bytes.Buffer
	if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil { t.Fatal(err) }
	if dump.Len()==0 { t.Fatal("empty actual dump") }
	if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil { t.Fatal(err) }
	publicationRun(t,p,b,held,publicationRelState())
	publicationRelRead(t,p,b,b.DB,publicationRelRaw,0,false)
	if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil { t.Fatal(err) }
	publicationRun(t,p,b,held,publicationRelState([]any{"publication_rt_p","publication_rt_t","1 3","(v > 0)"}))
	publicationRelRead(t,p,b,b.DB,publicationRelRaw,1,true)
	publicationRelSame(t,publicationRelConsume(t,b,old,1),before)
	publicationRelMetadata(t,false,metadata)
}

func TestReferencePGPublicationRelLifecycle(t *testing.T) {
	publicationScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		c:=publicationConn(t,p,b)
		peer:=publicationConn(t,p,b)
		run:=func(s ...fixture.Step){publicationRun(t,p,b,c,s...)}
		publicationRelPayloadSetup(t,p,b,c)
		run(fixture.Exec("CREATE TABLE publication_rt_u(id integer,v integer)",0),fixture.Exec("INSERT INTO publication_rt_u VALUES(9,7)",1),fixture.Exec("CREATE TABLE publication_rt_fresh(id integer)",0))
		stmt,err:=c.PrepareContext(p.Ctx,publicationRelEdges)
		if err!=nil { t.Fatal(err) }
		defer stmt.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:publicationRelEdges}
		trow:=[]any{"publication_rt_p","publication_rt_t","1 3","(v > 0)"}
		urow:=[]any{"publication_rt_p","publication_rt_u",nil,nil}
		state:=func(e fixture.Executor,rows ...[]any){publicationRun(t,p,b,e,publicationRelState(rows...))}
		initial:=publicationRelRead(t,p,b,c,publicationRelRaw,1,false)
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_u",0))
		state(held,trow,urow)
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p DROP TABLE publication_rt_u",0))
		state(held,trow)
		publicationRelSame(t,publicationRelRead(t,p,b,c,publicationRelRaw,1,false),initial)
		retained, err := peer.QueryContext(p.Ctx, publicationRelRaw)
		if err != nil { t.Fatal(err) }
		defer retained.Close()
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_u",0))
		state(held,urow)
		publicationRelSame(t, publicationRelConsume(t,b,retained,1), initial)
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t(v,id) WHERE(v>0)",0))
		committed:=publicationRelRead(t,p,b,c,publicationRelRaw,1,false)
		run(fixture.Exec("BEGIN",0),fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_u(v) WHERE(v>1)",0))
		provisional:=[]any{"publication_rt_p","publication_rt_u","2","(v > 1)"}
		state(held,provisional)
		state(peer,trow)
		prior:=publicationRelRead(t,p,b,c,publicationRelRaw,1,false)
		run(fixture.Exec("SAVEPOINT mapping_before",0),fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t(id)",0))
		state(held,[]any{"publication_rt_p","publication_rt_t","1",nil})
		run(fixture.Exec("ROLLBACK TO mapping_before",0))
		state(held,provisional)
		publicationRelSame(t,publicationRelRead(t,p,b,c,publicationRelRaw,1,false),prior)
		run(fixture.Exec("ROLLBACK",0))
		state(held,trow)
		publicationRelSame(t,publicationRelRead(t,p,b,c,publicationRelRaw,1,false),committed)
		run(fixture.Exec("BEGIN",0),fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_u",0))
		state(held,trow,urow)
		state(peer,trow)
		run(fixture.Exec("COMMIT",0))
		state(peer,trow,urow)
		unchanged:=publicationRelRead(t,p,b,c,publicationRelRaw,2,false)
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_fresh,publication_rt_t","42710",`relation "publication_rt_t" is already member of publication "publication_rt_p"`,"",""))
		state(held,trow,urow)
		publicationRelSame(t,publicationRelRead(t,p,b,c,publicationRelRaw,2,false),unchanged)
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p DROP TABLE publication_rt_fresh","42704",`relation "publication_rt_fresh" is not part of the publication`,"",""))
		publicationRelSame(t,publicationRelRead(t,p,b,c,publicationRelRaw,2,false),unchanged)
		run(fixture.Exec("CREATE PUBLICATION publication_rt_q FOR TABLE publication_rt_t,publication_rt_u WITH(publish='insert')",0),fixture.Exec("DROP TABLE publication_rt_t",0))
		state(held,urow,[]any{"publication_rt_q","publication_rt_u",nil,nil})
		run(fixture.Exec("DROP PUBLICATION publication_rt_p",0))
		state(held,[]any{"publication_rt_q","publication_rt_u",nil,nil})
		run(fixture.Select("SELECT id,v FROM publication_rt_u",[]string{"id","v"},[]string{"INT4","INT4"},[][]any{{int64(9),int64(7)}}),fixture.Exec("DROP PUBLICATION publication_rt_q",0),fixture.Exec("CREATE TABLE publication_rt_ordinary(id integer)",0))
		state(held)
		publicationRelPayloadSetup(t,p,b,c)
		state(held,trow)
		publicationRelRoundtrip(t,p,b)
	})
}
