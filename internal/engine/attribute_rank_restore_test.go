package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAttributeRankRestoreSyntax(t *testing.T) {
	const prefix = "UPDATE pg_catalog.pg_attribute SET attndims="
	const suffix = " WHERE attrelid='\"S\".\"T\"'::regclass AND attname='a'"
	for _, query := range []string{prefix + "0" + suffix, prefix + "32767" + suffix, prefix + "3 WHERE attrelid=CAST('\"S\".\"T\"' AS regclass) AND attname='a'"} {
		statements, err := parser.Parse(query)
		if err != nil {
			t.Fatal(err)
		}
		name, column, rank, err := table.RestoreAttributeRankSyntax(statements[0].(*parser.Update))
		if err != nil || len(name.Parts) != 2 || name.Parts[0].Text != "S" || name.Parts[1].Text != "T" || column != "a" || rank < 0 {
			t.Fatal("canonical syntax", name, column, rank, err)
		}
	}
	for _, tc := range []struct{ query, code string }{
		{prefix + "-1" + suffix, "22003"},
		{prefix + "32768" + suffix, "22003"},
		{prefix + "NULL" + suffix, "0A000"},
		{prefix + "$1" + suffix, "0A000"},
		{prefix + "1+2" + suffix, "0A000"},
		{prefix + "3,attnotnull=true" + suffix, "0A000"},
		{"UPDATE pg_attribute SET attndims=3" + suffix, "0A000"},
		{"UPDATE pg_catalog.pg_attribute AS a SET attndims=3" + suffix, "0A000"},
		{prefix + "3 FROM pg_class" + suffix, "0A000"},
		{prefix + "3" + suffix + " RETURNING attndims", "0A000"},
		{prefix + "3" + suffix + " AND attnum=1", "0A000"},
		{prefix + "3 WHERE attrelid='\"S\".\"T\"'::regclass OR attname='a'", "0A000"},
		{prefix + "3 WHERE attname='a' AND attrelid='\"S\".\"T\"'::regclass", "0A000"},
		{prefix + "3 WHERE attrelid='T'::regclass AND attname='a'", "0A000"},
		{prefix + "3 WHERE attrelid='12'::regclass AND attname='a'", "0A000"},
		{prefix + "3 WHERE attrelid='\"S\".\"T\"'::regclass AND attname=$1", "0A000"},
	} {
		statements, err := parser.Parse(tc.query)
		if err != nil {
			t.Fatal("syntax fixture", tc.query, err)
		}
		_, _, _, err = table.RestoreAttributeRankSyntax(statements[0].(*parser.Update))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal("noncanonical syntax admitted", tc.query, err)
		}
	}
	for _, mutation := range []func(*parser.Update){
		func(raw *parser.Update) { raw.With = []parser.CommonTableExpression{{}} },
		func(raw *parser.Update) { raw.CurrentOf = &parser.Identifier{Text: "cursor"} },
		func(raw *parser.Update) { raw.Assignments[0].Steps = []parser.TargetStep{{}} },
	} {
		statements, err := parser.Parse(prefix + "3" + suffix)
		if err != nil {
			t.Fatal(err)
		}
		update := statements[0].(*parser.Update)
		mutation(update)
		_, _, _, err = table.RestoreAttributeRankSyntax(update)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal("noncanonical AST admitted", err)
		}
	}
}

func TestAttributeRankRestorePublication(t *testing.T) {
	e := New().restoreStage()
	base := e.CatalogRead()
	old := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "rank_restore", ObjectRevision: 1, Owner: e.roles.domain.Principal(10), Columns: []catalog.Column{{Name: "a", Usage: testUsage(e, sqltype.ID(1007)), DeclaredDimensions: 2, Nullable: true, NullabilityKnown: true, OriginTable: 20000, OriginAttribute: 1}}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2, Dropped: map[int32]catalog.AttributeMetadata{2: {Name: "........pg.dropped.2........", Number: 2, Dimensions: 2, Dropped: true, Local: true, Facts: sqltype.PostgreSQLAttributeFacts{Length: -1, Alignment: "i", Storage: "x"}, Typmod: -1}}}, Index: map[string]int{"a": 0}}
	old.Columns = append(old.Columns, catalog.Column{Name: "scalar", Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true, OriginTable: 20000, OriginAttribute: 3})
	old.Attributes = append(old.Attributes, 3)
	old.Index["scalar"] = 1
	old.ColumnMeta.HighWater = 3
	staged := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(staged)
	if err := staged.Create(old); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), staged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	parse := func(column string) *parser.Update {
		raw, err := parser.Parse("UPDATE pg_catalog.pg_attribute SET attndims=3 WHERE attrelid='public.rank_restore'::regclass AND attname='" + column + "'")
		if err != nil {
			t.Fatal(err)
		}
		return raw[0].(*parser.Update)
	}
	ordinary := New()
	foreign, closeForeign := roleOperation(t, ordinary, "postgres")
	defer closeForeign()
	beforeAuthority := e.catalog.state
	if err := e.restoreAttributeRank(foreign, parse("a")); err == nil || e.catalog.state != beforeAuthority {
		t.Fatal("foreign operation admitted", err)
	}
	ordinaryState := ordinary.catalog.state
	if err := ordinary.restoreAttributeRank(foreign, parse("a")); err == nil || ordinary.catalog.state != ordinaryState {
		t.Fatal("ordinary engine admitted private repair", err)
	}
	if err := e.restoreAttributeRank(op, parse("a")); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead().MustRelation(old.Address())
	if current.Columns[0].DeclaredDimensions != 3 || old.Columns[0].DeclaredDimensions != 2 || current.Revision() != old.Revision()+1 || current.Columns[0].Usage != old.Columns[0].Usage || current.Columns[0].Name != old.Columns[0].Name || len(current.ColumnMeta.Dropped) != 1 {
		t.Fatal("rank repair changed authority or old root", old, current)
	}
	before := e.catalog.state
	for _, name := range []string{"b", "........pg.dropped.2........"} {
		var diagnostic *sqlstate.Error
		err := e.restoreAttributeRank(op, parse(name))
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42703" || e.catalog.state != before {
			t.Fatal("missing/dropped repair published", name, err)
		}
	}
	var diagnostic *sqlstate.Error
	err = e.restoreAttributeRank(op, parse("scalar"))
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || e.catalog.state != before {
		t.Fatal("scalar rank repair published", err)
	}
}
