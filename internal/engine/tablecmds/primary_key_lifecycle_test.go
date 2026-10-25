package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// rebuildPrimaryKeyTypeLocked rebuilds the unique backings and then the NOT
// NULL constraints of an ALTER COLUMN TYPE, in the ALTER TABLE order.
func rebuildPrimaryKeyTypeLocked(e *core.Engine, stage *core.CatalogStage, parent *catalog.Relation, transformations []index.ColumnTransformation) (*catalog.Relation, error) {
	next, err := index.RebuildAlterUniques(engine.TableHost{DDLHost: core.DDLHost{E: e, CatalogStage: stage}}, parent, transformations)
	if err != nil {
		return nil, err
	}
	return table.RebuildAlterNotNulls(core.DDLHost{E: e, CatalogStage: stage}, next, transformations)
}

func TestPrimaryKeyTypeCandidateIdentity(t *testing.T) {
	e, _, parent, idx := primaryKeyUnitFixture(t)
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	candidate := *parent
	candidate.Columns = slices.Clone(parent.Columns)
	candidate.Columns[0].Usage = indexClausesTypes()[sqltype.Int8].Usage()
	e.Mu.Lock()
	next, err := rebuildPrimaryKeyTypeLocked(e, stage, &candidate, []index.ColumnTransformation{{Attribute: parent.Attributes[0]}})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	old, rebuilt := parent.Constraints.Unique[0], next.Constraints.Unique[0]
	if next.Address() != parent.Address() || !rebuilt.Primary || rebuilt.Name != old.Name || rebuilt.OID == old.OID || rebuilt.Index == old.Index || next.Constraints.NotNull[0].OID == parent.Constraints.NotNull[0].OID || rebuilt.OID >= next.Constraints.NotNull[0].OID || next.Constraints.NotNull[1] != parent.Constraints.NotNull[1] {
		t.Fatal("affected p/index/n remint and order", parent, next)
	}
	private := e.StageRead(stage)
	backing := private.MustRelation(rebuilt.Index)
	if backing.SQLIndex.Constraint == nil || !backing.SQLIndex.Constraint.Primary || backing.SQLIndex.Constraint.OID != rebuilt.OID || backing.SQLIndex.Constraint.Index != backing.Address() || backing.Columns[0].Usage.Type.ID() != sqltype.Int8 {
		t.Fatal(backing)
	}
	if _, exists := private.Find(idx.Address()); exists {
		t.Fatal("old backing retained in candidate")
	}
	if e.CatalogRead().State() != base.State() || !reflect.DeepEqual(parent.Constraints.Unique[0], old) || idx.SQLIndex.Constraint.OID != old.OID || parent.Constraints.NotNull[0].OID == next.Constraints.NotNull[0].OID || parent.Columns[0].Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("candidate changed retained source/savepoint state")
	}
	for _, transformations := range [][]index.ColumnTransformation{nil, {{Attribute: parent.Attributes[2]}}} {
		e.Mu.Lock()
		unchanged, err := rebuildPrimaryKeyTypeLocked(e, e.BeginCatalogStage(base), parent, transformations)
		e.Mu.Unlock()
		if err != nil || unchanged != parent {
			t.Fatal("unaffected candidate must not remint", unchanged, err)
		}
	}
}

func TestPrimaryKeyLikeActualProviderAndComment(t *testing.T) {
	e, owner, parent, index := primaryKeyUnitFixture(t)
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	annotated := *index
	definition := *index.SQLIndex
	definition.Keys = slices.Clone(index.SQLIndex.Keys)
	class, ok := sqltype.LookupIndexClass("text_pattern_ops")
	if !ok {
		t.Fatal("missing real text provider")
	}
	definition.Keys[1].Class, definition.Keys[1].ExplicitClass = class, true
	annotated.SQLIndex, annotated.Comment = &definition, "native descriptor comment"
	if err := stage.Replace(&annotated); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	source := primaryKeyUnitRelation(t, e, "pk")
	sourceRoot := e.Storage.IndexRoots[source.Address()]
	ddl := &parser.CreateTable{Name: primaryKeyUnitName("clone"), Elements: []parser.TableElement{
		{Column: &parser.Column{Name: "lead", TypeName: parser.TypeName{Name: "boolean"}}},
		{Like: &parser.TableLike{Name: primaryKeyUnitName("t"), Options: []parser.LikeOption{{Name: "indexes", Including: true}, {Name: "comments", Including: true}}}},
		{Unique: &parser.UniqueConstraint{Name: parser.Identifier{Text: "chosen"}, Columns: []parser.Identifier{{Text: "a"}, {Text: "b"}}}},
	}}
	withPublicSchemaUnitOwner(t, e, "pk_owner", func() {
		if _, err := e.Execute(owner, ddl, 0, nil); err != nil {
			t.Fatal(err)
		}
	})
	clone := primaryKeyUnitRelation(t, e, "clone")
	if len(clone.Constraints.Unique) != 2 || !clone.Constraints.Unique[0].Primary || clone.Constraints.Unique[0].Name != "clone_pkey" || clone.Constraints.Unique[1].Primary || clone.Constraints.Unique[1].Name != "chosen" || !reflect.DeepEqual(clone.Constraints.Unique[0].Attributes, []int32{2, 3}) {
		t.Fatal("copied PK is not an explicit UNIQUE dedup candidate", clone.Constraints.Unique)
	}
	c := clone.Constraints.Unique[0]
	copied := e.Catalog.State.MustRelation(c.Index)
	if c.OID == parent.Constraints.Unique[0].OID || c.Index == source.Address() || copied.Comment != "native descriptor comment" || copied.SQLIndex.Keys[1].Class != class || !copied.SQLIndex.Keys[1].ExplicitClass || copied.SQLIndex.Keys[0].Attribute != 2 || copied.SQLIndex.Keys[1].Attribute != 3 || copied.SQLIndex.Parent != clone.Address() || !copied.SQLIndex.Constraint.Primary {
		t.Fatal("LIKE lost real provider/comment/attachment", copied)
	}
	if e.Storage.IndexRoots[c.Index] == sourceRoot || len(e.Storage.IndexRoots[c.Index].Entries) != 0 || len(sourceRoot.Entries) != 2 || index.Comment != "" || index.SQLIndex.Keys[1].ExplicitClass {
		t.Fatal("LIKE transplanted root or mutated retained source")
	}
	bound, err := analyze.BindCreateUniques(ddl, clone.Index, []analyze.LikeInput{{Source: parent, Offset: 1, Options: analyze.LikeIndexes | analyze.LikeComments}}, e.CatalogRead())
	if err != nil || len(bound) != 2 || bound[0].Source != source || bound[0].Like == nil || !bound[0].CopyComments || bound[1].Source != nil {
		t.Fatal("bound source provenance", bound, err)
	}
}

func TestPrimaryKeyCreateGroupFailureFence(t *testing.T) {
	e, owner, parent, index := primaryKeyUnitFixture(t)
	withPublicSchemaUnitOwner(t, e, "pk_owner", func() {
		before, store, root := e.Catalog.State, e.Storage.Relations[parent.Address()], e.Storage.IndexRoots[index.Address()]
		result, err := e.Execute(owner, primaryKeyUnitCreate("other", "pk"), 0, nil)
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P07", Message: `relation "pk" already exists`})
		if result.Count != 0 || result.Rows != nil || e.Catalog.State != before || e.Storage.Relations[parent.Address()] != store || e.Storage.IndexRoots[index.Address()] != root {
			t.Fatal("backing-name conflict partly published CREATE group", result)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err = enginetest.Execute(e, ctx, primaryKeyUnitCreate("canceled", "canceled_pkey"), nil)
		if !errors.Is(err, context.Canceled) || result.Count != 0 || result.Rows != nil || e.Catalog.State != before || e.Storage.Relations[parent.Address()] != store || e.Storage.IndexRoots[index.Address()] != root {
			t.Fatal("canceled CREATE partly published group", result, err)
		}
		enginetest.LockEmpty(t, e)
	})
}

func indexClausesTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}
