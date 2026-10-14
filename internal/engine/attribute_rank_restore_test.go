package engine

import (
	"context"
	"errors"
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
		name, column, rank, err := restoreAttributeRankSyntax(statements[0].(*parser.Update))
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
		_, _, _, err = restoreAttributeRankSyntax(statements[0].(*parser.Update))
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
		_, _, _, err = restoreAttributeRankSyntax(update)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal("noncanonical AST admitted", err)
		}
	}
}

func TestAttributeRankRestorePublication(t *testing.T) {
	e := New().restoreStage()
	base := e.CatalogRead()
	old := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 20000}, namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, name: "rank_restore", revision: 1, owner: e.authorizationDomain.Principal(10), columns: []ColumnDescriptor{{Name: "a", Usage: testUsage(e, sqltype.ID(1007)), DeclaredDimensions: 2, Nullable: true, NullabilityKnown: true, OriginTable: 20000, OriginAttribute: 1}}, attributes: []int32{1}, attributeHighWater: 2, index: map[string]int{"a": 0}, droppedAttributes: map[int32]attributeMetadata{2: {name: "........pg.dropped.2........", number: 2, dimensions: 2, dropped: true, local: true, facts: sqltype.PostgreSQLAttributeFacts{Length: -1, Alignment: "i", Storage: "x"}, typmod: -1}}}
	old.columns = append(old.columns, ColumnDescriptor{Name: "scalar", Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true, OriginTable: 20000, OriginAttribute: 3})
	old.attributes = append(old.attributes, 3)
	old.index["scalar"] = 1
	old.attributeHighWater = 3
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
	beforeAuthority := e.state
	if err := e.restoreAttributeRank(foreign, parse("a")); err == nil || e.state != beforeAuthority {
		t.Fatal("foreign operation admitted", err)
	}
	ordinaryState := ordinary.state
	if err := ordinary.restoreAttributeRank(foreign, parse("a")); err == nil || ordinary.state != ordinaryState {
		t.Fatal("ordinary engine admitted private repair", err)
	}
	if err := e.restoreAttributeRank(op, parse("a")); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead().state.descriptors[old.address].(*relationDescriptor)
	if current.columns[0].DeclaredDimensions != 3 || old.columns[0].DeclaredDimensions != 2 || current.revision != old.revision+1 || current.columns[0].Usage != old.columns[0].Usage || current.columns[0].Name != old.columns[0].Name || len(current.droppedAttributes) != 1 {
		t.Fatal("rank repair changed authority or old root", old, current)
	}
	before := e.state
	for _, name := range []string{"b", "........pg.dropped.2........"} {
		var diagnostic *sqlstate.Error
		err := e.restoreAttributeRank(op, parse(name))
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42703" || e.state != before {
			t.Fatal("missing/dropped repair published", name, err)
		}
	}
	var diagnostic *sqlstate.Error
	err = e.restoreAttributeRank(op, parse("scalar"))
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || e.state != before {
		t.Fatal("scalar rank repair published", err)
	}
}
