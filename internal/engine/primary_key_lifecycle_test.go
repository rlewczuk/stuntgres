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
	candidate.columns = slices.Clone(parent.columns)
	candidate.columns[0].Usage = indexClausesTypes()[sqltype.Int8].Usage()
	e.mu.Lock()
	next, err := e.rebuildAlterUniquesLocked(stage, &candidate, []columnTransformation{{attribute: parent.attributes[0]}})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	old, rebuilt := parent.uniqueConstraints[0], next.uniqueConstraints[0]
	if next.address != parent.address || !rebuilt.Primary || rebuilt.Name != old.Name || rebuilt.OID == old.OID || rebuilt.Index == old.Index || next.notNullConstraints[0].OID == parent.notNullConstraints[0].OID || rebuilt.OID >= next.notNullConstraints[0].OID || next.notNullConstraints[1] != parent.notNullConstraints[1] {
		t.Fatal("affected p/index/n remint and order", parent, next)
	}
	private := e.stageRead(stage)
	backing := private.state.descriptors[rebuilt.Index].(*relationDescriptor)
	if backing.sqlIndex.constraint == nil || !backing.sqlIndex.constraint.Primary || backing.sqlIndex.constraint.OID != rebuilt.OID || backing.sqlIndex.constraint.Index != backing.address || backing.columns[0].Usage.Type.ID() != sqltype.Int8 {
		t.Fatal(backing)
	}
	if _, exists := private.state.descriptors[index.address]; exists {
		t.Fatal("old backing retained in candidate")
	}
	if e.CatalogRead().state != base.state || !reflect.DeepEqual(parent.uniqueConstraints[0], old) || index.sqlIndex.constraint.OID != old.OID || parent.notNullConstraints[0].OID == next.notNullConstraints[0].OID || parent.columns[0].Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("candidate changed retained source/savepoint state")
	}
	for _, transformations := range [][]columnTransformation{nil, {{attribute: parent.attributes[2]}}} {
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
	definition := *index.sqlIndex
	definition.keys = slices.Clone(index.sqlIndex.keys)
	class, ok := sqltype.LookupIndexClass("text_pattern_ops")
	if !ok {
		t.Fatal("missing real text provider")
	}
	definition.keys[1].class, definition.keys[1].explicitClass = class, true
	annotated.sqlIndex, annotated.comment = &definition, "native descriptor comment"
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
	sourceRoot := e.indexRoots[source.address]
	ddl := &parser.CreateTable{Name: primaryKeyUnitName("clone"), Elements: []parser.TableElement{
		{Column: &parser.Column{Name: "lead", TypeName: parser.TypeName{Name: "boolean"}}},
		{Like: &parser.TableLike{Name: primaryKeyUnitName("t"), Options: []parser.LikeOption{{Name: "indexes", Including: true}, {Name: "comments", Including: true}}}},
		{Unique: &parser.UniqueConstraint{Name: parser.Identifier{Text: "chosen"}, Columns: []parser.Identifier{{Text: "a"}, {Text: "b"}}}},
	}}
	if _, err := e.Execute(owner, ddl, 0, nil); err != nil {
		t.Fatal(err)
	}
	clone := primaryKeyUnitRelation(t, e, "clone")
	if len(clone.uniqueConstraints) != 2 || !clone.uniqueConstraints[0].Primary || clone.uniqueConstraints[0].Name != "clone_pkey" || clone.uniqueConstraints[1].Primary || clone.uniqueConstraints[1].Name != "chosen" || !reflect.DeepEqual(clone.uniqueConstraints[0].Attributes, []int32{2, 3}) {
		t.Fatal("copied PK is not an explicit UNIQUE dedup candidate", clone.uniqueConstraints)
	}
	c := clone.uniqueConstraints[0]
	copied := e.state.descriptors[c.Index].(*relationDescriptor)
	if c.OID == parent.uniqueConstraints[0].OID || c.Index == source.address || copied.comment != "native descriptor comment" || copied.sqlIndex.keys[1].class != class || !copied.sqlIndex.keys[1].explicitClass || copied.sqlIndex.keys[0].attribute != 2 || copied.sqlIndex.keys[1].attribute != 3 || copied.sqlIndex.parent != clone.address || !copied.sqlIndex.constraint.Primary {
		t.Fatal("LIKE lost real provider/comment/attachment", copied)
	}
	if e.indexRoots[c.Index] == sourceRoot || len(e.indexRoots[c.Index].entries) != 0 || len(sourceRoot.entries) != 2 || index.comment != "" || index.sqlIndex.keys[1].explicitClass {
		t.Fatal("LIKE transplanted root or mutated retained source")
	}
	bound, err := bindCreateUniques(ddl, clone.index, []createLikeInput{{source: parent, offset: 1, options: likeIndexes | likeComments}}, e.CatalogRead())
	if err != nil || len(bound) != 2 || bound[0].source != source || bound[0].like == nil || !bound[0].copyComments || bound[1].source != nil {
		t.Fatal("bound source provenance", bound, err)
	}
}

func TestPrimaryKeyCreateGroupFailureFence(t *testing.T) {
	e, owner, parent, index := primaryKeyUnitFixture(t)
	before, store, root := e.state, e.storage[parent.address], e.indexRoots[index.address]
	result, err := e.Execute(owner, primaryKeyUnitCreate("other", "pk"), 0, nil)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P07", Message: `relation "pk" already exists`})
	if result.Count != 0 || result.Rows != nil || e.state != before || e.storage[parent.address] != store || e.indexRoots[index.address] != root {
		t.Fatal("backing-name conflict partly published CREATE group", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = testExecute(e, ctx, primaryKeyUnitCreate("canceled", "canceled_pkey"), nil)
	if !errors.Is(err, context.Canceled) || result.Count != 0 || result.Rows != nil || e.state != before || e.storage[parent.address] != store || e.indexRoots[index.address] != root {
		t.Fatal("canceled CREATE partly published group", result, err)
	}
	lockEmpty(t, e)
}
