package tablecmds_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestAlterStorageCandidate(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	e.Mu.Lock()
	auth := enginetest.CatalogAuthorization(e, view)
	e.Mu.Unlock()
	for _, tc := range []struct { typ sqltype.ID; action parser.AlterTableActionKind; value, storage, compression, code, message string }{
		{sqltype.Text, parser.SetStorage, "eXtErNaL", "e", "", "", ""},
		{sqltype.Text, parser.SetStorage, "DEFAULT", "x", "", "", ""},
		{sqltype.Numeric, parser.SetStorage, "default", "m", "", "", ""},
		{sqltype.Int4, parser.SetStorage, "plain", "p", "", "", ""},
		{sqltype.Int4, parser.SetCompression, "default", "p", "", "", ""},
		{sqltype.Text, parser.SetCompression, "pglz", "x", "p", "", ""},
		{sqltype.Numeric, parser.SetCompression, "lz4", "m", "l", "", ""},
		{sqltype.Int4, parser.SetStorage, "bogus", "", "", "22023", `invalid storage type "bogus"`},
		{sqltype.Int4, parser.SetCompression, "bogus", "", "", "0A000", "column data type integer does not support compression"},
		{sqltype.Int4, parser.SetStorage, "external", "", "", "0A000", "column data type integer can only have storage PLAIN"},
		{3615, parser.SetCompression, "pglz", "", "", "0A000", "column data type tsquery does not support compression"},
		{3615, parser.SetStorage, "extended", "", "", "0A000", "column data type tsquery can only have storage PLAIN"},
		{sqltype.Text, parser.SetCompression, "PGLZ", "", "", "22023", `invalid compression method "PGLZ"`},
		{sqltype.Text, parser.SetCompression, "DEFAULT", "", "", "22023", `invalid compression method "DEFAULT"`},
	} {
		old := &catalog.Relation{ObjectName:"t", Columns: []catalog.Column{{Name:"a",Usage:enginetest.Usage(e,tc.typ)}}, Attributes:[]int32{7}, Index:map[string]int{"a":0}}
		e.Mu.Lock()
		next, slot, err := table.ColumnStorageCandidate(view, auth, old, parser.AlterTableAction{Action:tc.action, Column:parser.Identifier{Text:"a"},Property:parser.Identifier{Text:tc.value}})
		e.Mu.Unlock()
		if tc.code != "" {
			var got *sqlstate.Error
			want := sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:tc.code,Message:tc.message}
			if !errors.As(err,&got) || *got != want || next != nil { t.Fatal(tc, next,err) }
			continue
		}
		if err != nil || slot != 0 || next.ColumnStorage(0) != (catalog.ColumnStorage{Storage:tc.storage,Compression:tc.compression}) || len(old.ColumnMeta.Storage)!=0 || &next.Columns[0] != &old.Columns[0] { t.Fatal(tc,next,slot,err) }
	}
	old := &catalog.Relation{ObjectName:"t",ObjectRevision:5,Columns:[]catalog.Column{{Name:"a",Usage:enginetest.Usage(e,sqltype.Text)},{Name:"b",Usage:enginetest.Usage(e,sqltype.Numeric)}},Attributes:[]int32{3,9},Index:map[string]int{"a":0,"b":1},ColumnMeta:catalog.ColumnMetadata{Storage:map[int32]catalog.ColumnStorage{3:{Storage:"e",Compression:"p"},9:{Compression:"l"}}}}
	saved := old.ColumnMeta.Storage[3]
	e.Mu.Lock()
	next, _, err := table.ColumnStorageCandidate(view,auth,old,parser.AlterTableAction{Action:parser.SetStorage,Column:parser.Identifier{Text:"a"},Property:parser.Identifier{Text:"default"}})
	e.Mu.Unlock()
	if err != nil || next == old || old.ColumnMeta.Storage[3] != saved || next.ColumnMeta.Storage[3].Compression!="p" || next.ColumnMeta.Storage[3].Storage!="" || next.ColumnMeta.Storage[9]!=old.ColumnMeta.Storage[9] || !reflect.DeepEqual(next.Attributes,old.Attributes) { t.Fatal(next,err) }
	if next.Revision()!=5 { t.Fatal("leaf published a revision") }
	for _, kind := range []parser.AlterTableActionKind{parser.SetStorage,parser.SetCompression} {
		if table.AlterActionPhase(kind)!=10 { t.Fatal("wrong pass") }
		for _, relationKind := range []catalog.RelationKind{catalog.TableRelation,catalog.PartitionedRelation,catalog.MaterializedViewRelation,catalog.ForeignRelation,catalog.ViewRelation,catalog.IndexRelation} {
			r := &catalog.Relation{RelationKind:relationKind,ObjectName:"r",ObjectAddress:ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:90000}}
			allowed := relationKind==catalog.TableRelation || relationKind==catalog.PartitionedRelation || relationKind==catalog.MaterializedViewRelation || relationKind==catalog.ForeignRelation && kind==parser.SetStorage
			if err:=table.AlterTableActionKind(r,kind); (err==nil)!=allowed { t.Fatal(kind,relationKind,err) }
		}
	}
}
