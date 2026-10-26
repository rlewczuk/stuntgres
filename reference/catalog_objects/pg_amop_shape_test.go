package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func pgAmopShapes(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor) {
	t.Helper()
	for _, name := range []string{"pg_catalog.pg_amop","pg_amop"} {
		for _, prepared := range []bool{false,true} {
			pgAmopRows(t,p,b,e,"SELECT * FROM "+name+" WHERE oid=10015",pgAmopFacts()[1:2],prepared)
			for _, predicate := range []string{"oid=0","FALSE"} { pgAmopRows(t,p,b,e,"SELECT * FROM "+name+" WHERE "+predicate,nil,prepared) }
		}
	}
}

func TestReferencePGAmopShape(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"amop_shape_reader"})
	pgOpclassScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		pgAmopShapes(t,p,b,b.DB)
		// Capture metadata while Rows is open; retained ColumnType values remain
		// useful after EOF without calling Rows.ColumnTypes after auto-close.
		rows, err := b.DB.QueryContext(p.Ctx,"SELECT * FROM pg_amop WHERE oid=10015")
		if err != nil { t.Fatal(err) }
		pgAmopMetadata(t,rows,b.PG)
		types, err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }
		pgAmopConsume(t,rows)
		if err := rows.Close(); err != nil { t.Fatal(err) }
		for i,c := range types {
			if c.Name() != pgAmopColumns[i] || c.DatabaseTypeName() != pgAmopTypes[i] { t.Fatal("retained metadata",i,c) }
		}
		c := fixture.SequenceCoreConn(t,p.Ctx,b.DB)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE ROLE amop_shape_reader",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("SET ROLE amop_shape_reader",0))
		defer c.ExecContext(context.Background(),"RESET ROLE")
		pgAmopShapes(t,p,b,c)
	})
}

func TestReferencePGAmopMetadata(t *testing.T) {
	pgOpclassScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		var want [][]any
		for i,name := range pgAmopColumns {
			typ := int64(26); if i == 4 { typ = 21 }; if i == 5 { typ = 18 }
			want = append(want,[]any{int64(i+1),name,typ,true,int64(0),int64(-1)})
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT oid,reltype,relnatts,relkind,relhasindex FROM pg_class WHERE oid=2602",[]string{"oid","reltype","relnatts","relkind","relhasindex"},[]string{"OID","OID","INT2","CHAR","BOOL"},[][]any{{int64(2602),int64(10017),int64(9),"r",true}}),
			fixture.Select("SELECT attnum,attname,atttypid,attnotnull,attcollation,atttypmod FROM pg_attribute WHERE attrelid=2602 AND attnum>0 ORDER BY attnum",[]string{"attnum","attname","atttypid","attnotnull","attcollation","atttypmod"},[]string{"INT2","NAME","OID","BOOL","OID","INT4"},want),
		} { fixture.PgDescriptionRows(t,p,b,s); fixture.PgDescriptionRows(t,p,b,fixture.Prepared(s)) }
	})
}
