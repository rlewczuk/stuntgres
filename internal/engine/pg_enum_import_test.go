package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGEnumImportMetadata(t *testing.T) {
	ctx := context.Background()
	source, target := New(), New()
	op, done := roleOperation(t, source, "postgres")
	domainUnitPublish(t, source, domainUnitStage(t, source, op, "CREATE TYPE mood AS ENUM ('z','q')"))
	old := domainUnitType(t, source.CatalogRead(), "mood")
	members := old.SQLType.EnumMembers()
	members[0].SortOrder, members[1].SortOrder = -1, 1.5
	typ, err := sqltype.EnumWithMembers(old.SQLType.ID(), old.Name(), old.SQLType.SQLName(), members)
	if err != nil {
		t.Fatal(err)
	}
	next := *old
	next.SQLType, next.revision = typ, old.revision+1
	stage := source.BeginCatalogStage(source.CatalogRead())
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	// Replacing the enum codec must reown its associated array generation
	// before validating the candidate, just as the real ALTER path does.
	source.mu.Lock()
	err = source.reownStagedTypeGenerationsLocked(stage)
	source.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, source, stage)
	for _, q := range []string{"CREATE DOMAIN wrapped AS mood", "CREATE TYPE box AS (v mood)"} {
		domainUnitPublish(t, source, domainUnitStage(t, source, op, q))
	}
	// Tables/defaults need the real analyzer, not the schema-utility helper.
	source.mu.Lock()
	auth, err := source.authorizationLocked(op)
	if err == nil {
		stage = source.BeginCatalogStage(auth.catalog)
		err = source.schemaTableLocked(op, auth, stage, statement(t, "CREATE TABLE saved(v public.mood DEFAULT 'q'::public.mood,d public.wrapped,c public.box)").(*parser.CreateTable), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.state.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	source.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, source, stage)
	done()
	original := source.CatalogRead()
	mood := domainUnitType(t, original, "mood").SQLType
	domain := domainUnitType(t, original, "wrapped").SQLType
	box := domainUnitType(t, original, "box").SQLType
	v, err := mood.Input(ctx, "q", -1)
	if err != nil {
		t.Fatal(err)
	}
	d, err := domain.Input(ctx, "q", -1)
	if err != nil {
		t.Fatal(err)
	}
	c, err := box.Input(ctx, "(q)", -1)
	if err != nil {
		t.Fatal(err)
	}
	ref := catalogRef(t, source, original, "saved")
	source.commitSeq = 1
	source.storage[ref.Address] = &relationStorage{versions: []rowVersion{{row: 7, version: 11, born: 1, values: []sqltype.Value{v, d, c}}}}
	targetOp, targetDone := roleOperation(t, target, "postgres")
	domainUnitPublish(t, target, domainUnitStage(t, target, targetOp, "CREATE TYPE reserved AS ENUM ('a','b','c')"))
	targetDone()
	if _, err := target.ReplaceCatalog(ctx, source, original); err != nil {
		t.Fatal(err)
	}
	imported := domainUnitType(t, target.CatalogRead(), "mood").SQLType
	for i, oldMember := range members {
		newMember, ok := imported.EnumMemberAt(i)
		if !ok || newMember.ID == oldMember.ID || newMember.Label != oldMember.Label || newMember.SortOrder != oldMember.SortOrder {
			t.Fatal("import metadata", oldMember, newMember, ok)
		}
	}
	if !reflect.DeepEqual(mood.EnumMembers(), members) || source.CatalogRead().state != original.state {
		t.Fatal("import mutated source")
	}
	newRef := catalogRef(t, target, target.CatalogRead(), "saved")
	rows := target.storage[newRef.Address].versions
	// Foreign replacement remints engine-local row/version identities;
	// unlike ALTER publication, import preserves logical values, not these IDs.
	if len(rows) != 1 {
		t.Fatal("import row count", rows)
	}
	sourceRows := source.storage[ref.Address].versions
	if len(sourceRows) != 1 || sourceRows[0].row != 7 || sourceRows[0].version != 11 || sourceRows[0].born != 1 || !reflect.DeepEqual(sourceRows[0].values, []sqltype.Value{v, d, c}) {
		t.Fatal("import mutated source storage", sourceRows)
	}
	newRelation := target.catalog.state.MustRelation(newRef.Address)
	for i, want := range []string{"q", "q", "(q)"} {
		value := rows[0].values[i]
		if value.Type() != newRelation.Columns[i].Usage.Type {
			t.Fatal("foreign imported codec", i, value)
		}
		if text, err := value.Type().Output(ctx, value); err != nil || text != want {
			t.Fatal("nested imported value", i, text, err)
		}
	}
	newID, _ := rows[0].values[0].EnumMemberID()
	oldID, _ := v.EnumMemberID()
	if newID == oldID {
		t.Fatal("datum identity not reminted", newID)
	}
	definition := newRelation.Defaults[newRelation.Attributes[0]]
	literal, err := definition.Expression.Eval(&EvalContext{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	literalID, _ := literal.EnumMemberID()
	if literal.Type() != imported || literalID != newID {
		t.Fatal("stored literal translation", literal, newID)
	}
	if text, err := mood.Output(ctx, v); err != nil || text != "q" {
		t.Fatal("source datum changed", text, err)
	}
}
