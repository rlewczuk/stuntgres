package engine

import (
	"context"
	"reflect"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTableLikePartialIndexClone(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE anchor(a INT)")
	run(t, e, "CREATE DOMAIN predicate_type AS INT")
	run(t, e, "CREATE TABLE src(id INT,k INT,active BOOL,gap INT,n INT)")
	run(t, e, "ALTER TABLE src DROP COLUMN gap")
	run(t, e, "INSERT INTO src VALUES(1,10,TRUE,2),(2,20,FALSE,0),(3,30,NULL,0),(4,NULL,TRUE,2),(5,10,TRUE,2)")
	run(t, e, "CREATE INDEX source_partial ON src(k) INCLUDE(id) WHERE active AND ('anchor'::regclass IS NOT NULL) AND ('predicate_type'::regtype IS NOT NULL)")
	run(t, e, "CREATE INDEX source_expression ON src((10/n)) WHERE active")
	source := e.CatalogRead().state.descriptors[roleTableRef(t, e, "src").Address].(*relationDescriptor)
	external := roleTableRef(t, e, "anchor")
	externalType := ObjectRef{owner: e.owner, Epoch: e.CatalogRead().Epoch(), Address: domainUnitType(t, e.CatalogRead(), "predicate_type").Address()}
	ordinary := e.CatalogRead().state.descriptors[roleTableRef(t, e, "source_partial").Address].(*relationDescriptor)
	expression := e.CatalogRead().state.descriptors[roleTableRef(t, e, "source_expression").Address].(*relationDescriptor)
	sourcePredicate := ordinary.sqlIndex.predicate
	sourceAttributes := slices.Clone(sourcePredicate.attributes)
	sourceDependencies := slices.Clone(sourcePredicate.dependencies)
	sourceRoots := e.indexRoots
	op, done := roleOperation(t, e, "postgres")
	defer done()
	stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(lead TEXT,LIKE src INCLUDING ALL)")
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	local := ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: destination.address}
	local.Address.SubID = 4
	var copied []*relationDescriptor
	for _, descriptor := range stage.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		copied = append(copied, index)
		old := ordinary
		if index.name == "clone_expr_idx" {
			old = expression
		}
		predicate := index.sqlIndex.predicate
		if predicate == nil || predicate == old.sqlIndex.predicate || !reflect.DeepEqual(predicate.attributes, []int32{1, 2, 3, 4, 5}) {
			t.Fatal("source predicate/container/layout retained", index.name, predicate)
		}
		if index.address == old.address || index.Revision() == old.Revision() || index.sqlIndex.parent != destination.address || !index.sqlIndex.ready || !index.sqlIndex.valid || !index.sqlIndex.live {
			t.Fatal("index identity/parent/state copied", index)
		}
		if predicate.definition.ResultUsage() != old.sqlIndex.predicate.definition.ResultUsage() || predicate.execution.ResultUsage() != old.sqlIndex.predicate.execution.ResultUsage() || predicate.definition.Properties() != old.sqlIndex.predicate.definition.Properties() || predicate.execution.Properties() != old.sqlIndex.predicate.execution.Properties() || plannedVolatility(predicate.execution) != Immutable {
			t.Fatal("typed predicate properties changed", predicate)
		}
		if !slices.Contains(predicate.dependencies, local) {
			t.Fatal("destination-local predicate dependency missing", predicate.dependencies)
		}
		for _, dependency := range predicate.dependencies {
			if dependency.Address.ObjectID == source.address.ObjectID {
				t.Fatal("source row dependency retained", dependency)
			}
		}
		for _, node := range []BoundExpr{predicate.definition, predicate.execution} {
			if _, err := (scalarRenderer{owner: destination}).sql(context.Background(), e.stageRead(stage), node); err != nil {
				t.Fatal("wrong owner in predicate tree", err)
			}
		}
		integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
		for _, truth := range []sqltype.Value{sqltype.Boolean(boolean, true), sqltype.Boolean(boolean, false), sqltype.Null(boolean)} {
			id, _ := sqltype.Integer(integer, 11)
			key, _ := sqltype.Integer(integer, 7)
			n, _ := sqltype.Integer(integer, 2)
			row := []sqltype.Value{sqltype.String(text, "prefix"), id, key, truth, n}
			evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
			got, err := indexPredicateMatches(&evaluation, predicate, row)
			want, _ := TruthOf(truth)
			if err != nil || got != (want == True) {
				t.Fatal("execution predicate retains source slot", got, want, err)
			}
		}
		var executionDependencies []ObjectRef
		if _, err := (scalarRenderer{owner: destination, dependencies: &executionDependencies}).sql(context.Background(), e.stageRead(stage), predicate.execution); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(executionDependencies, external) || slices.Contains(executionDependencies, externalType) {
			t.Fatal("fixture lacks analyzed-only dependency", executionDependencies)
		}
		if index.name == "clone_k_id_idx" {
			if index.sqlIndex.keys[0].attribute != 3 || !reflect.DeepEqual(index.sqlIndex.include, []int32{2}) || !slices.Contains(predicate.dependencies, external) {
				t.Fatal("key/include/external identity lost", index.sqlIndex)
			}
			booleanType := ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(sqltype.Bool)}}
			regclassType := ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(sqltype.Regclass)}}
			regtypeType := ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(sqltype.Regtype)}}
			if !slices.Equal(predicate.dependencies, []ObjectRef{booleanType, local, regclassType, external, regtypeType, externalType}) {
				t.Fatal("analyzed predicate dependency manifest", predicate.dependencies)
			}
		} else if index.name == "clone_expr_idx" {
			key := index.sqlIndex.keys[0].derived
			if key == nil || !reflect.DeepEqual(key.attributes, destination.attributes) {
				t.Fatal("derived key layout lost", key)
			}
			target := local
			target.Address.SubID = 5
			if !slices.Contains(key.dependencies, target) {
				t.Fatal("dropped attribute mapping lost", key.dependencies)
			}
			for _, node := range []BoundExpr{key.definition, key.execution} {
				if _, err := (scalarRenderer{owner: destination}).sql(context.Background(), e.stageRead(stage), node); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			t.Fatal("source index name reused", index.name)
		}
	}
	if len(copied) != 2 {
		t.Fatal("partial indexes omitted", len(copied))
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	for _, index := range copied {
		root := e.indexRoots[index.address]
		if root == nil || root.membership == nil || len(root.entries) != 0 || len(root.membership) != 0 {
			t.Fatal("copy root not independently empty", root)
		}
		for address, old := range sourceRoots {
			if root == old || e.indexRoots[address] != old {
				t.Fatal("source root reused or changed", index.name)
			}
		}
	}
	run(t, e, "INSERT INTO clone VALUES('x',1,10,TRUE,2),('x',2,20,FALSE,0),('x',3,30,NULL,0),('x',4,NULL,TRUE,2),('x',5,10,TRUE,2)")
	assertMembership := func(count int) {
		want := make(map[RowID]VersionID)
		for _, row := range e.storage[destination.address].versions {
			truth, err := TruthOf(row.values[3])
			if err != nil {
				t.Fatal(err)
			}
			if truth == True {
				want[row.row] = row.version
			}
		}
		if len(want) != count {
			t.Fatal("unexpected current physical members", want, count)
		}
		for _, index := range copied {
			partialWantMembership(t, e.indexRoots[index.address], want)
		}
	}
	assertMembership(3)
	run(t, e, "UPDATE clone SET active=FALSE WHERE id=1")
	run(t, e, "UPDATE clone SET active=NULL WHERE id=4")
	run(t, e, "UPDATE clone SET active=TRUE,n=2 WHERE id=2 OR id=3")
	assertMembership(3)
	if ordinary.sqlIndex.predicate != sourcePredicate || !reflect.DeepEqual(sourcePredicate.attributes, sourceAttributes) || !reflect.DeepEqual(sourcePredicate.dependencies, sourceDependencies) {
		t.Fatal("source definition mutated")
	}
}

func TestTableLikePartialUniqueClone(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE src(id INT,k INT,active BOOL,gap INT,n INT)")
	run(t, e, "ALTER TABLE src DROP COLUMN gap")
	run(t, e, "CREATE UNIQUE INDEX source_unique ON src(k) NULLS NOT DISTINCT WHERE active")
	source := e.CatalogRead().state.descriptors[roleTableRef(t, e, "source_unique").Address].(*relationDescriptor)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(lead TEXT,LIKE src INCLUDING INDEXES)")
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	var copied *relationDescriptor
	for _, descriptor := range stage.descriptors {
		if index, ok := descriptor.(*relationDescriptor); ok && index.sqlIndex != nil {
			copied = index
		}
	}
	if copied == nil || copied.address == source.address || !copied.sqlIndex.unique || !copied.sqlIndex.nullsNotDistinct || copied.sqlIndex.constraint != nil || len(destination.uniqueConstraints) != 0 || copied.sqlIndex.keys[0].attribute != 3 || copied.sqlIndex.predicate == nil || copied.sqlIndex.predicate == source.sqlIndex.predicate {
		t.Fatal("standalone partial unique/backing authority lost", copied)
	}
	predicate := copied.sqlIndex.predicate
	for _, node := range []BoundExpr{predicate.definition, predicate.execution} {
		column, ok := node.(columnExpression)
		if !ok || column.origin.Address.ObjectID != destination.address.ObjectID || column.origin.Address.SubID != 4 || column.slot != 3 {
			t.Fatal("partial unique predicate row mapping", node)
		}
	}
	queryCreatePublish(t, e, stage)
	if e.indexRoots[copied.address] == nil || e.indexRoots[copied.address] == e.indexRoots[source.address] || e.indexRoots[copied.address].membership == nil {
		t.Fatal("partial unique root shared")
	}
}

func TestTableLikePartialConstants(t *testing.T) {
	for _, truth := range []string{"TRUE", "FALSE", "NULL"} {
		t.Run(truth, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE src(id INT,n INT)")
			key := "(10/n)"
			if truth == "TRUE" {
				key = "id"
			}
			run(t, e, "CREATE INDEX source_constant ON src("+key+") WHERE "+truth)
			source := e.CatalogRead().state.descriptors[roleTableRef(t, e, "source_constant").Address].(*relationDescriptor)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(lead TEXT,LIKE src INCLUDING INDEXES)")
			if err != nil {
				t.Fatal(err)
			}
			defer e.DiscardCatalogStage(stage)
			var copied *relationDescriptor
			for _, descriptor := range stage.descriptors {
				if index, ok := descriptor.(*relationDescriptor); ok && index.sqlIndex != nil {
					copied = index
				}
			}
			if copied == nil {
				t.Fatal("constant predicate index omitted")
			}
			if truth == "TRUE" {
				if copied.sqlIndex.predicate != nil {
					t.Fatal("TRUE canonicalization changed")
				}
			} else {
				p := copied.sqlIndex.predicate
				if p == nil || p == source.sqlIndex.predicate || !reflect.DeepEqual(p.attributes, destination.attributes) {
					t.Fatal("constant predicate container/layout shared")
				}
			}
			queryCreatePublish(t, e, stage)
			run(t, e, "INSERT INTO clone VALUES('x',1,0),('x',2,0)")
			root := e.indexRoots[copied.address]
			if truth == "TRUE" {
				if root.membership != nil || len(root.entries) != 2 {
					t.Fatal("TRUE did not include all rows", root)
				}
			} else {
				partialWantMembership(t, root, map[RowID]VersionID{})
			}
		})
	}
}

func TestTableLikePartialPrivateProvenance(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.gate(statement(t, "CREATE TABLE clone(id INT)")); err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	stage, source, err := tableLikeBuild(t, e, op, "CREATE TABLE private_source(id INT,k INT,active BOOL)")
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err == nil {
		err = e.schemaTableLocked(op, auth, stage, statement(t, "CREATE TABLE anchor(a INT)").(*parser.CreateTable), source.namespace, source.owner, sqltype.InputEnvironment{}, nil)
	}
	if err == nil {
		auth.catalog = e.stageRead(stage)
	}
	raw := statement(t, "CREATE INDEX private_partial ON private_source(k) WHERE active AND ('anchor'::regclass IS NOT NULL)").(*parser.CreateIndex)
	var index *relationDescriptor
	if err == nil {
		var predicate *indexPredicate
		predicate, err = e.bindIndexPredicate(op, sqltype.InputEnvironment{}, auth, source, raw.Predicate)
		if err == nil {
			definition, columns, attributes, lookup, bindErr := e.bindIndexAttributes(op, sqltype.InputEnvironment{}, auth, source, raw)
			err = bindErr
			if err == nil {
				definition.predicate = predicate
				oid, allocationErr := e.allocateOIDLocked()
				err = allocationErr
				if err == nil {
					revision, revisionErr := e.revisionLocked()
					err = revisionErr
					if err == nil {
						for i := range columns {
							columns[i].OriginTable = uint32(oid)
							columns[i].OriginAttribute = int16(i + 1)
						}
						index = &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: oid}, namespace: source.namespace, name: "private_partial", revision: revision, owner: source.owner, columns: columns, attributes: attributes, attributeHighWater: int32(len(attributes)), index: lookup, relationKind: indexRelation, sqlIndex: definition}
						err = stage.Create(index)
					}
				}
			}
		}
	}
	if err == nil {
		auth.catalog = e.stageRead(stage)
		err = e.schemaTableLocked(op, auth, stage, statement(t, "CREATE TABLE clone(lead TEXT,LIKE private_source INCLUDING ALL)").(*parser.CreateTable), source.namespace, source.owner, sqltype.InputEnvironment{}, nil)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(stage.likeSources) != 2 {
		t.Fatal("private source/index not captured", stage.likeSources)
	}
	if err := scope.acquireDefinitionProviders(stage); err != nil {
		t.Fatal(err)
	}
	e.locks.mu.Lock()
	held := len(e.locks.entries)
	e.locks.mu.Unlock()
	if held != 0 {
		t.Fatal("private providers received incumbent holds", held)
	}
	queryCreatePublish(t, e, stage)
	current := e.CatalogRead().state.descriptors[source.address].(*relationDescriptor)
	if !current.hasIndexes || current.Revision() != source.Revision() || current == source {
		t.Fatal("private normalization invariant", current)
	}
	if e.indexRoots[index.address] == nil {
		t.Fatal("private predicate index root absent")
	}
}
