package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestRoutineDeparseMetadataCapture(t *testing.T) {
	e := engine.New()
	enginetest.Run(t,e,"CREATE FUNCTION rd_leaf() RETURNS integer LANGUAGE SQL AS 'SELECT 1'")
	enginetest.Run(t,e,"CREATE FUNCTION rd_caller() RETURNS integer RETURN rd_leaf()")
	old := e.CatalogRead()
	public := old.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:"public"})
	address := old.State().ProceduresNamed(public,"rd_caller")[0]
	d,ok := old.Procedure(address)
	if !ok { t.Fatal("missing caller") }
	program,err := stored.RoutineProgramOf(d)
	if err != nil { t.Fatal(err) }
	path := []ids.ObjectAddress{{ClassID:ids.NamespaceClassID,ObjectID:ids.PGCatalogNamespaceOID},public}
	p := stored.Presentation{Deparse:true,Path:path}
	ctx := context.Background()
	text,err := program.DefinitionSQL(ctx,old,d,p)
	if err != nil || text != "RETURN rd_leaf()\n" { t.Fatal(text,err) }
	enginetest.Run(t,e,"ALTER FUNCTION rd_leaf() RENAME TO rd_renamed")
	enginetest.Run(t,e,"CREATE OR REPLACE FUNCTION rd_renamed() RETURNS integer LANGUAGE SQL AS 'SELECT 2'")
	read := e.CatalogRead()
	current,_ := read.Procedure(address)
	text,err = program.DefinitionSQL(ctx,read,current,p)
	if err != nil || text != "RETURN rd_renamed()\n" { t.Fatal("metadata-only replacement display",text,err) }
	p.Output = sqltype.OutputEnvironment{QuoteAllIdentifiers:true}
	text,err = program.DefinitionSQL(ctx,read,current,p)
	if err != nil || text != "RETURN \"rd_renamed\"()\n" { t.Fatal(text,err) }
	p.Output = sqltype.OutputEnvironment{}
	text,err = program.DefinitionSQL(ctx,old,d,p)
	if err != nil || text != "RETURN rd_leaf()\n" { t.Fatal("retained catalog changed",text,err) }
	copy := *current
	if _,err := program.DefinitionSQL(ctx,read,&copy,p);err == nil { t.Fatal("detached descriptor accepted") }
	if _,err := program.DefinitionSQL(ctx,engine.New().CatalogRead(),current,p);err == nil { t.Fatal("foreign domain accepted") }
	ctx,cancel := context.WithCancel(context.Background());cancel()
	if _,err := program.DefinitionSQL(ctx,read,current,p);err == nil { t.Fatal("canceled presentation accepted") }
}
