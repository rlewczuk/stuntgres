package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPrimaryKeyTypeCandidateIdentity(t *testing.T) {
	e, _, parent, index := primaryKeyUnitFixture(t)
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	candidate := *parent
	candidate.Columns = slices.Clone(parent.Columns)
	candidate.Columns[0].Usage = indexClausesTypes()[sqltype.Int8].Usage()
	e.mu.Lock()
	next, err := e.rebuildAlterUniquesLocked(stage, &candidate, []columnTransformation{{attribute: parent.Attributes[0]}})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	old, rebuilt := parent.UniqueConstraints[0], next.UniqueConstraints[0]
	if next.Address() != parent.Address() || !rebuilt.Primary || rebuilt.Name != old.Name || rebuilt.OID == old.OID || rebuilt.Index == old.Index || next.NotNullConstraints[0].OID == parent.NotNullConstraints[0].OID || rebuilt.OID >= next.NotNullConstraints[0].OID || next.NotNullConstraints[1] != parent.NotNullConstraints[1] {
		t.Fatal("affected p/index/n remint and order", parent, next)
	}
	private := e.stageRead(stage)
	backing := private.MustRelation(rebuilt.Index)
	if backing.SQLIndex.Constraint == nil || !backing.SQLIndex.Constraint.Primary || backing.SQLIndex.Constraint.OID != rebuilt.OID || backing.SQLIndex.Constraint.Index != backing.Address() || backing.Columns[0].Usage.Type.ID() != sqltype.Int8 {
		t.Fatal(backing)
	}
	if _, exists := private.Find(index.Address()); exists {
		t.Fatal("old backing retained in candidate")
	}
	if e.CatalogRead().State() != base.State() || !reflect.DeepEqual(parent.UniqueConstraints[0], old) || index.SQLIndex.Constraint.OID != old.OID || parent.NotNullConstraints[0].OID == next.NotNullConstraints[0].OID || parent.Columns[0].Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("candidate changed retained source/savepoint state")
	}
	for _, transformations := range [][]columnTransformation{nil, {{attribute: parent.Attributes[2]}}} {
		e.mu.Lock()
		unchanged, err := e.rebuildAlterUniquesLocked(e.BeginCatalogStage(base), parent, transformations)
		e.mu.Unlock()
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
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	source := primaryKeyUnitRelation(t, e, "pk")
	sourceRoot := e.indexRoots[source.Address()]
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
	if len(clone.UniqueConstraints) != 2 || !clone.UniqueConstraints[0].Primary || clone.UniqueConstraints[0].Name != "clone_pkey" || clone.UniqueConstraints[1].Primary || clone.UniqueConstraints[1].Name != "chosen" || !reflect.DeepEqual(clone.UniqueConstraints[0].Attributes, []int32{2, 3}) {
		t.Fatal("copied PK is not an explicit UNIQUE dedup candidate", clone.UniqueConstraints)
	}
	c := clone.UniqueConstraints[0]
	copied := e.catalog.state.MustRelation(c.Index)
	if c.OID == parent.UniqueConstraints[0].OID || c.Index == source.Address() || copied.Comment != "native descriptor comment" || copied.SQLIndex.Keys[1].Class != class || !copied.SQLIndex.Keys[1].ExplicitClass || copied.SQLIndex.Keys[0].Attribute != 2 || copied.SQLIndex.Keys[1].Attribute != 3 || copied.SQLIndex.Parent != clone.Address() || !copied.SQLIndex.Constraint.Primary {
		t.Fatal("LIKE lost real provider/comment/attachment", copied)
	}
	if e.indexRoots[c.Index] == sourceRoot || len(e.indexRoots[c.Index].entries) != 0 || len(sourceRoot.entries) != 2 || index.Comment != "" || index.SQLIndex.Keys[1].ExplicitClass {
		t.Fatal("LIKE transplanted root or mutated retained source")
	}
	bound, err := bindCreateUniques(ddl, clone.Index, []createLikeInput{{source: parent, offset: 1, options: likeIndexes | likeComments}}, e.CatalogRead())
	if err != nil || len(bound) != 2 || bound[0].source != source || bound[0].like == nil || !bound[0].copyComments || bound[1].source != nil {
		t.Fatal("bound source provenance", bound, err)
	}
}

func TestPrimaryKeyCreateGroupFailureFence(t *testing.T) {
	e, owner, parent, index := primaryKeyUnitFixture(t)
	withPublicSchemaUnitOwner(t, e, "pk_owner", func() {
		before, store, root := e.catalog.state, e.storage[parent.Address()], e.indexRoots[index.Address()]
		result, err := e.Execute(owner, primaryKeyUnitCreate("other", "pk"), 0, nil)
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P07", Message: `relation "pk" already exists`})
		if result.Count != 0 || result.Rows != nil || e.catalog.state != before || e.storage[parent.Address()] != store || e.indexRoots[index.Address()] != root {
			t.Fatal("backing-name conflict partly published CREATE group", result)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err = testExecute(e, ctx, primaryKeyUnitCreate("canceled", "canceled_pkey"), nil)
		if !errors.Is(err, context.Canceled) || result.Count != 0 || result.Rows != nil || e.catalog.state != before || e.storage[parent.Address()] != store || e.indexRoots[index.Address()] != root {
			t.Fatal("canceled CREATE partly published group", result, err)
		}
		lockEmpty(t, e)
	})
}
