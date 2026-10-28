package tablecmds_test

import (
	"strings"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func TestAlterStorageRestoreSyntax(t *testing.T) {
	const valid = `UPDATE pg_catalog.pg_attribute SET attstorage='e',attcompression='p' WHERE attrelid='"S"."I"'::regclass AND attnum=1`
	parse := func(query string)*parser.Update { t.Helper();statements,err:=parser.Parse(query);if err!=nil {t.Fatal(query,err)};return statements[0].(*parser.Update) }
	for _, compression:=range []string{"", "p", "l"} {
		raw:=parse(strings.Replace(valid,"attcompression='p'","attcompression='"+compression+"'",1))
		name,number,value,err:=table.RestoreIndexStorageSyntax(raw)
		if err!=nil || name.Parts[0].Text!="S" || name.Parts[1].Text!="I" || number!=1 || value.Storage!="e" || value.Compression!=compression {t.Fatal(name,number,value,err)}
	}
	for _, assignments := range []string{
		"attname='old'",
		"attstorage='e',attcompression='p',attname='old'",
		"attstorage='e',attcompression='p',attname='Mixed '' name'",
	} {
		raw := parse(strings.Replace(valid, "attstorage='e',attcompression='p'", assignments, 1))
		_, _, value, err := table.RestoreIndexStorageSyntax(raw)
		if err != nil || !value.Rename || value.Name == "" || value.SetStorage != strings.HasPrefix(assignments, "attstorage=") {
			t.Fatal("canonical name repair rejected", value, err)
		}
	}
	for _, query:=range []string{
		strings.Replace(valid,"pg_catalog.pg_attribute","pg_attribute",1),
		strings.Replace(valid,"pg_attribute SET","pg_attribute AS a SET",1),
		strings.Replace(valid,"'e'","'bogus'",1),
		strings.Replace(valid,"attcompression='p'","attcompression='bogus'",1),
		strings.Replace(valid,"attcompression='p'","attcompression=$1",1),
		strings.Replace(valid,",attcompression='p'","",1),
		strings.Replace(valid,"attcompression='p'","attcompression='p',attacl=NULL",1),
		strings.Replace(valid,"attcompression='p'","attcompression='p',attname=$1",1),
		strings.Replace(valid,"attcompression='p'","attcompression='p',attname=''",1),
		strings.Replace(valid,"attcompression='p'","attcompression='p',attname='"+strings.Repeat("n",64)+"'",1),
		strings.Replace(valid,"attcompression='p'","attcompression='p',attname='old',attacl=NULL",1),
		strings.Replace(valid,"attstorage='e',attcompression='p'","attname='old',attstorage='e',attcompression='p'",1),
		strings.Replace(valid,"attstorage='e',attcompression='p'","attname='old',attname='other'",1),
		strings.Replace(valid," AND attnum=1","",1),
		strings.Replace(valid," AND attnum=1"," OR attnum=1",1),
		strings.Replace(valid,"attnum=1","attnum=0",1),
		strings.Replace(valid,"attnum=1","attnum=$1",1),
		strings.Replace(valid,`'"S"."I"'`,`'I'`,1),
		strings.Replace(valid," WHERE"," FROM pg_catalog.pg_class WHERE",1),
		valid+" AND attname='a'",
		valid+" RETURNING attnum",
	} {
		if _,_,_,err:=table.RestoreIndexStorageSyntax(parse(query));err==nil {t.Fatal("admitted noncanonical repair",query)}
	}
	for _,mutation:=range []func(*parser.Update){
		func(raw *parser.Update){raw.With=[]parser.CommonTableExpression{{}}},
		func(raw *parser.Update){raw.WithRecursive=true},
		func(raw *parser.Update){raw.CurrentOf=&parser.Identifier{Text:"cursor"}},
		func(raw *parser.Update){raw.Assignments[0].Steps=[]parser.TargetStep{{}}},
	} {
		raw:=parse(valid);mutation(raw)
		if _,_,_,err:=table.RestoreIndexStorageSyntax(raw);err==nil {t.Fatal("admitted noncanonical AST")}
	}
}

func TestAlterStorageRestoreIndexNames(t *testing.T) {
	e := engine.New().RestoreStage()
	enginetest.Run(t, e, "CREATE TABLE storage_names(a text,b text)")
	enginetest.Run(t, e, "CREATE INDEX storage_names_idx ON storage_names(a,b)")
	ref := enginetest.RoleTableRef(t, e, "storage_names_idx")
	read := e.CatalogRead()
	old := read.MustRelation(ref.Address)
	root := e.Storage.IndexRoots[ref.Address]
	op, done := roleOperation(t, e, "postgres")
	defer done()
	parse := func(assignments string) *parser.Update {
		t.Helper()
		raw, err := parser.Parse("UPDATE pg_catalog.pg_attribute SET " + assignments + " WHERE attrelid='public.storage_names_idx'::regclass AND attnum=1")
		if err != nil { t.Fatal(err) }
		return raw[0].(*parser.Update)
	}
	before := e.Catalog.State
	if err := tablecmds.RestoreIndexStorage(e, op, parse("attname='b'")); err == nil || e.Catalog.State != before {
		t.Fatal("duplicate name repair published", err)
	}
	if err := tablecmds.RestoreIndexStorage(e, op, parse("attname='saved'")); err != nil { t.Fatal(err) }
	next := e.CatalogRead().MustRelation(ref.Address)
	if next == old || next.Columns[0].Name != "saved" || next.Index["saved"] != 0 || next.Revision() != old.Revision()+1 {
		t.Fatal("name repair failed to replace immutable descriptor")
	}
	if _, exists := next.Index["a"]; exists { t.Fatal("old lookup retained") }
	if old.Columns[0].Name != "a" || old.Index["a"] != 0 || read.MustRelation(ref.Address) != old || next.SQLIndex != old.SQLIndex || next.ColumnStorage(0) != old.ColumnStorage(0) || e.Storage.IndexRoots[ref.Address] != root {
		t.Fatal("name repair changed predecessor, properties or executable index")
	}
	if err := tablecmds.RestoreIndexStorage(e, op, parse("attstorage='e',attcompression='p',attname='final'")); err != nil { t.Fatal(err) }
	final := e.CatalogRead().MustRelation(ref.Address)
	if final.Columns[0].Name != "final" || final.ColumnStorage(0).Storage != "e" || final.ColumnStorage(0).Compression != "p" || next.Columns[0].Name != "saved" || next.ColumnStorage(0) != old.ColumnStorage(0) || final.SQLIndex != old.SQLIndex || e.Storage.IndexRoots[ref.Address] != root {
		t.Fatal("combined name/policy repair mutated prior state or index")
	}
	ordinary := engine.New()
	ordinaryOp, closeOrdinary := roleOperation(t, ordinary, "postgres")
	defer closeOrdinary()
	state := ordinary.Catalog.State
	if err := tablecmds.RestoreIndexStorage(ordinary, ordinaryOp, parse("attname='saved'")); err == nil || ordinary.Catalog.State != state {
		t.Fatal("ordinary engine admitted private repair", err)
	}
}
