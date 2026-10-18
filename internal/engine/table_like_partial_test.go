package engine

import (
	"context"
	"reflect"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
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
	source := e.CatalogRead().MustRelation(roleTableRef(t, e, "src").Address)
	external := roleTableRef(t, e, "anchor")
	externalType := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), domainUnitType(t, e.CatalogRead(), "predicate_type").Address())
	ordinary := e.CatalogRead().MustRelation(roleTableRef(t, e, "source_partial").Address)
	expression := e.CatalogRead().MustRelation(roleTableRef(t, e, "source_expression").Address)
	sourcePredicate := ordinary.SQLIndex.Predicate
	sourceAttributes := slices.Clone(sourcePredicate.Attributes)
	sourceDependencies := slices.Clone(sourcePredicate.Dependencies)
	sourceRoots := e.indexRoots
	op, done := roleOperation(t, e, "postgres")
	defer done()
	stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(lead TEXT,LIKE src INCLUDING ALL)")
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	local := e.catalog.Domain.Ref(stage.Base.Epoch(), destination.Address())
	local.Address.SubID = 4
	var copied []*catalog.Relation
	for _, descriptor := range stage.Descriptors {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		copied = append(copied, index)
		old := ordinary
		if index.Name() == "clone_expr_idx" {
			old = expression
		}
		predicate := index.SQLIndex.Predicate
		if predicate == nil || predicate == old.SQLIndex.Predicate || !reflect.DeepEqual(predicate.Attributes, []int32{1, 2, 3, 4, 5}) {
			t.Fatal("source predicate/container/layout retained", index.Name(), predicate)
		}
		if index.Address() == old.Address() || index.Revision() == old.Revision() || index.SQLIndex.Parent != destination.Address() || !index.SQLIndex.Ready || !index.SQLIndex.Valid || !index.SQLIndex.Live {
			t.Fatal("index identity/parent/state copied", index)
		}
		if predicate.Definition.ResultUsage() != old.SQLIndex.Predicate.Definition.ResultUsage() || predicate.Execution.ResultUsage() != old.SQLIndex.Predicate.Execution.ResultUsage() || predicate.Definition.Properties() != old.SQLIndex.Predicate.Definition.Properties() || predicate.Execution.Properties() != old.SQLIndex.Predicate.Execution.Properties() || plannedVolatility(predicate.Execution) != expr.Immutable {
			t.Fatal("typed predicate properties changed", predicate)
		}
		if !slices.Contains(predicate.Dependencies, local) {
			t.Fatal("destination-local predicate dependency missing", predicate.Dependencies)
		}
		for _, dependency := range predicate.Dependencies {
			if dependency.Address.ObjectID == source.Address().ObjectID {
				t.Fatal("source row dependency retained", dependency)
			}
		}
		for _, node := range []expr.Expr{predicate.Definition, predicate.Execution} {
			if _, err := (scalarRenderer{owner: destination}).sql(context.Background(), e.stageRead(stage), node); err != nil {
				t.Fatal("wrong owner in predicate tree", err)
			}
		}
		integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
		var preparation indexExecutionPreparation
		for _, truth := range []sqltype.Value{sqltype.Boolean(boolean, true), sqltype.Boolean(boolean, false), sqltype.Null(boolean)} {
			id, _ := sqltype.Integer(integer, 11)
			key, _ := sqltype.Integer(integer, 7)
			n, _ := sqltype.Integer(integer, 2)
			row := []sqltype.Value{sqltype.String(text, "prefix"), id, key, truth, n}
			evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
			got, err := preparation.predicateMatches(&evaluation, destination, &catalog.IndexDefinition{Predicate: predicate}, row, nil, nil)
			want, _ := TruthOf(truth)
			if err != nil || got != (want == True) {
				t.Fatal("execution predicate retains source slot", got, want, err)
			}
		}
		var executionDependencies []ids.ObjectRef
		if _, err := (scalarRenderer{owner: destination, dependencies: &executionDependencies}).sql(context.Background(), e.stageRead(stage), predicate.Execution); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(executionDependencies, external) || slices.Contains(executionDependencies, externalType) {
			t.Fatal("fixture lacks analyzed-only dependency", executionDependencies)
		}
		if index.Name() == "clone_k_id_idx" {
			if index.SQLIndex.Keys[0].Attribute != 3 || !reflect.DeepEqual(index.SQLIndex.Include, []int32{2}) || !slices.Contains(predicate.Dependencies, external) {
				t.Fatal("key/include/external identity lost", index.SQLIndex)
			}
			booleanType := e.catalog.Domain.Ref(stage.Base.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Bool)})
			regclassType := e.catalog.Domain.Ref(stage.Base.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)})
			regtypeType := e.catalog.Domain.Ref(stage.Base.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regtype)})
			if !slices.Equal(predicate.Dependencies, []ids.ObjectRef{booleanType, local, regclassType, external, regtypeType, externalType}) {
				t.Fatal("analyzed predicate dependency manifest", predicate.Dependencies)
			}
		} else if index.Name() == "clone_expr_idx" {
			key := index.SQLIndex.Keys[0].Derived
			if key == nil || !reflect.DeepEqual(key.Attributes, destination.Attributes) {
				t.Fatal("derived key layout lost", key)
			}
			target := local
			target.Address.SubID = 5
			if !slices.Contains(key.Dependencies, target) {
				t.Fatal("dropped attribute mapping lost", key.Dependencies)
			}
			for _, node := range []expr.Expr{key.Definition, key.Execution} {
				if _, err := (scalarRenderer{owner: destination}).sql(context.Background(), e.stageRead(stage), node); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			t.Fatal("source index name reused", index.Name())
		}
	}
	if len(copied) != 2 {
		t.Fatal("partial indexes omitted", len(copied))
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	for _, index := range copied {
		root := e.indexRoots[index.Address()]
		if root == nil || root.membership == nil || len(root.entries) != 0 || len(root.membership) != 0 {
			t.Fatal("copy root not independently empty", root)
		}
		for address, old := range sourceRoots {
			if root == old || e.indexRoots[address] != old {
				t.Fatal("source root reused or changed", index.Name())
			}
		}
	}
	run(t, e, "INSERT INTO clone VALUES('x',1,10,TRUE,2),('x',2,20,FALSE,0),('x',3,30,NULL,0),('x',4,NULL,TRUE,2),('x',5,10,TRUE,2)")
	assertMembership := func(count int) {
		want := make(map[ids.RowID]ids.VersionID)
		for _, row := range e.storage[destination.Address()].versions {
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
			partialWantMembership(t, e.indexRoots[index.Address()], want)
		}
	}
	assertMembership(3)
	run(t, e, "UPDATE clone SET active=FALSE WHERE id=1")
	run(t, e, "UPDATE clone SET active=NULL WHERE id=4")
	run(t, e, "UPDATE clone SET active=TRUE,n=2 WHERE id=2 OR id=3")
	assertMembership(3)
	if ordinary.SQLIndex.Predicate != sourcePredicate || !reflect.DeepEqual(sourcePredicate.Attributes, sourceAttributes) || !reflect.DeepEqual(sourcePredicate.Dependencies, sourceDependencies) {
		t.Fatal("source definition mutated")
	}
}

func TestTableLikePartialUniqueClone(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE src(id INT,k INT,active BOOL,gap INT,n INT)")
	run(t, e, "ALTER TABLE src DROP COLUMN gap")
	run(t, e, "CREATE UNIQUE INDEX source_unique ON src(k) NULLS NOT DISTINCT WHERE active")
	source := e.CatalogRead().MustRelation(roleTableRef(t, e, "source_unique").Address)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(lead TEXT,LIKE src INCLUDING INDEXES)")
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	var copied *catalog.Relation
	for _, descriptor := range stage.Descriptors {
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil {
			copied = index
		}
	}
	if copied == nil || copied.Address() == source.Address() || !copied.SQLIndex.Unique || !copied.SQLIndex.NullsNotDistinct || copied.SQLIndex.Constraint != nil || len(destination.UniqueConstraints) != 0 || copied.SQLIndex.Keys[0].Attribute != 3 || copied.SQLIndex.Predicate == nil || copied.SQLIndex.Predicate == source.SQLIndex.Predicate {
		t.Fatal("standalone partial unique/backing authority lost", copied)
	}
	predicate := copied.SQLIndex.Predicate
	for _, node := range []expr.Expr{predicate.Definition, predicate.Execution} {
		column, ok := node.(columnExpression)
		if !ok || column.origin.Address.ObjectID != destination.Address().ObjectID || column.origin.Address.SubID != 4 || column.slot != 3 {
			t.Fatal("partial unique predicate row mapping", node)
		}
	}
	queryCreatePublish(t, e, stage)
	if e.indexRoots[copied.Address()] == nil || e.indexRoots[copied.Address()] == e.indexRoots[source.Address()] || e.indexRoots[copied.Address()].membership == nil {
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
			source := e.CatalogRead().MustRelation(roleTableRef(t, e, "source_constant").Address)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(lead TEXT,LIKE src INCLUDING INDEXES)")
			if err != nil {
				t.Fatal(err)
			}
			defer e.DiscardCatalogStage(stage)
			var copied *catalog.Relation
			for _, descriptor := range stage.Descriptors {
				if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil {
					copied = index
				}
			}
			if copied == nil {
				t.Fatal("constant predicate index omitted")
			}
			if truth == "TRUE" {
				if copied.SQLIndex.Predicate != nil {
					t.Fatal("TRUE canonicalization changed")
				}
			} else {
				p := copied.SQLIndex.Predicate
				if p == nil || p == source.SQLIndex.Predicate || !reflect.DeepEqual(p.Attributes, destination.Attributes) {
					t.Fatal("constant predicate container/layout shared")
				}
			}
			queryCreatePublish(t, e, stage)
			run(t, e, "INSERT INTO clone VALUES('x',1,0),('x',2,0)")
			root := e.indexRoots[copied.Address()]
			if truth == "TRUE" {
				if root.membership != nil || len(root.entries) != 2 {
					t.Fatal("TRUE did not include all rows", root)
				}
			} else {
				partialWantMembership(t, root, map[ids.RowID]ids.VersionID{})
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
		err = e.schemaTableLocked(op, auth, stage, statement(t, "CREATE TABLE anchor(a INT)").(*parser.CreateTable), source.Namespace(), source.Owner, sqltype.InputEnvironment{}, nil)
	}
	if err == nil {
		auth.catalog = e.stageRead(stage)
		auth.recaptureCatalog(auth.catalog)
	}
	raw := statement(t, "CREATE INDEX private_partial ON private_source(k) WHERE active AND ('anchor'::regclass IS NOT NULL)").(*parser.CreateIndex)
	var index *catalog.Relation
	if err == nil {
		var predicate *catalog.IndexPredicate
		predicate, err = e.bindIndexPredicate(op, sqltype.InputEnvironment{}, auth, source, raw.Predicate)
		if err == nil {
			definition, columns, attributes, lookup, bindErr := e.bindIndexAttributes(op, sqltype.InputEnvironment{}, auth, source, raw)
			err = bindErr
			if err == nil {
				definition.Predicate = predicate
				oid, allocationErr := e.catalog.allocateOIDLocked()
				err = allocationErr
				if err == nil {
					revision, revisionErr := e.catalog.revisionLocked()
					err = revisionErr
					if err == nil {
						for i := range columns {
							columns[i].OriginTable = uint32(oid)
							columns[i].OriginAttribute = int16(i + 1)
						}
						index = &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: source.Namespace(), ObjectName: "private_partial", ObjectRevision: revision, Owner: source.Owner, Columns: columns, Attributes: attributes, AttributeHighWater: int32(len(attributes)), Index: lookup, RelationKind: catalog.IndexRelation, SQLIndex: definition}
						err = stage.Create(index)
					}
				}
			}
		}
	}
	if err == nil {
		auth.catalog = e.stageRead(stage)
		auth.recaptureCatalog(auth.catalog)
		err = e.schemaTableLocked(op, auth, stage, statement(t, "CREATE TABLE clone(lead TEXT,LIKE private_source INCLUDING ALL)").(*parser.CreateTable), source.Namespace(), source.Owner, sqltype.InputEnvironment{}, nil)
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
	lockState := e.locks.Inspect()
	held := len(lockState.Resources)
	if held != 0 {
		t.Fatal("private providers received incumbent holds", held)
	}
	queryCreatePublish(t, e, stage)
	current := e.CatalogRead().MustRelation(source.Address())
	if !current.HasIndexes || current.Revision() != source.Revision() || current == source {
		t.Fatal("private normalization invariant", current)
	}
	if e.indexRoots[index.Address()] == nil {
		t.Fatal("private predicate index root absent")
	}
}
