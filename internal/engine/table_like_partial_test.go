package engine

import (
	"context"
	"reflect"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
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
	externalType := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), domainUnitType(t, e.CatalogRead(), "predicate_type").Address())
	ordinary := e.CatalogRead().MustRelation(roleTableRef(t, e, "source_partial").Address)
	expression := e.CatalogRead().MustRelation(roleTableRef(t, e, "source_expression").Address)
	sourcePredicate := ordinary.SQLIndex.Predicate
	sourceAttributes := slices.Clone(sourcePredicate.Attributes)
	sourceDependencies := slices.Clone(sourcePredicate.Dependencies)
	sourceRoots := e.Storage.IndexRoots
	op, done := roleOperation(t, e, "postgres")
	defer done()
	stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(lead TEXT,LIKE src INCLUDING ALL)")
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	local := e.Catalog.Domain.Ref(stage.Base.Epoch(), destination.Address())
	local.Address.SubID = 4
	var copied []*catalog.Relation
	for _, descriptor := range stage.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil {
			continue
		}
		copied = append(copied, idx)
		old := ordinary
		if idx.Name() == "clone_expr_idx" {
			old = expression
		}
		predicate := idx.SQLIndex.Predicate
		if predicate == nil || predicate == old.SQLIndex.Predicate || !reflect.DeepEqual(predicate.Attributes, []int32{1, 2, 3, 4, 5}) {
			t.Fatal("source predicate/container/layout retained", idx.Name(), predicate)
		}
		if idx.Address() == old.Address() || idx.Revision() == old.Revision() || idx.SQLIndex.Parent != destination.Address() || !idx.SQLIndex.Ready || !idx.SQLIndex.Valid || !idx.SQLIndex.Live {
			t.Fatal("index identity/parent/state copied", idx)
		}
		if predicate.Definition.ResultUsage() != old.SQLIndex.Predicate.Definition.ResultUsage() || predicate.Execution.ResultUsage() != old.SQLIndex.Predicate.Execution.ResultUsage() || predicate.Definition.Properties() != old.SQLIndex.Predicate.Definition.Properties() || predicate.Execution.Properties() != old.SQLIndex.Predicate.Execution.Properties() || exec.PlannedVolatility(predicate.Execution) != expr.Immutable {
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
			if _, err := (stored.ScalarRenderer{Owner: destination}).SQL(context.Background(), e.StageRead(stage), node); err != nil {
				t.Fatal("wrong owner in predicate tree", err)
			}
		}
		integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
		var preparation index.ExecutionPreparation
		for _, truth := range []sqltype.Value{sqltype.Boolean(boolean, true), sqltype.Boolean(boolean, false), sqltype.Null(boolean)} {
			id, _ := sqltype.Integer(integer, 11)
			key, _ := sqltype.Integer(integer, 7)
			n, _ := sqltype.Integer(integer, 2)
			row := []sqltype.Value{sqltype.String(text, "prefix"), id, key, truth, n}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			got, err := preparation.PredicateMatches(&evaluation, destination, &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Predicate: predicate}, row, nil, nil)
			want, _ := plan.TruthOf(truth)
			if err != nil || got != (want == plan.True) {
				t.Fatal("execution predicate retains source slot", got, want, err)
			}
		}
		var executionDependencies []ids.ObjectRef
		if _, err := (stored.ScalarRenderer{Owner: destination, Dependencies: &executionDependencies}).SQL(context.Background(), e.StageRead(stage), predicate.Execution); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(executionDependencies, external) || slices.Contains(executionDependencies, externalType) {
			t.Fatal("fixture lacks analyzed-only dependency", executionDependencies)
		}
		if idx.Name() == "clone_k_id_idx" {
			if idx.SQLIndex.Keys[0].Attribute != 3 || !reflect.DeepEqual(idx.SQLIndex.Include, []int32{2}) || !slices.Contains(predicate.Dependencies, external) {
				t.Fatal("key/include/external identity lost", idx.SQLIndex)
			}
			booleanType := e.Catalog.Domain.Ref(stage.Base.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Bool)})
			regclassType := e.Catalog.Domain.Ref(stage.Base.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)})
			regtypeType := e.Catalog.Domain.Ref(stage.Base.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regtype)})
			if !slices.Equal(predicate.Dependencies, []ids.ObjectRef{booleanType, local, regclassType, external, regtypeType, externalType}) {
				t.Fatal("analyzed predicate dependency manifest", predicate.Dependencies)
			}
		} else if idx.Name() == "clone_expr_idx" {
			key := idx.SQLIndex.Keys[0].Derived
			if key == nil || !reflect.DeepEqual(key.Attributes, destination.Attributes) {
				t.Fatal("derived key layout lost", key)
			}
			target := local
			target.Address.SubID = 5
			if !slices.Contains(key.Dependencies, target) {
				t.Fatal("dropped attribute mapping lost", key.Dependencies)
			}
			for _, node := range []expr.Expr{key.Definition, key.Execution} {
				if _, err := (stored.ScalarRenderer{Owner: destination}).SQL(context.Background(), e.StageRead(stage), node); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			t.Fatal("source index name reused", idx.Name())
		}
	}
	if len(copied) != 2 {
		t.Fatal("partial indexes omitted", len(copied))
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	for _, index := range copied {
		root := e.Storage.IndexRoots[index.Address()]
		if root == nil || root.Membership == nil || len(root.Entries) != 0 || len(root.Membership) != 0 {
			t.Fatal("copy root not independently empty", root)
		}
		for address, old := range sourceRoots {
			if root == old || e.Storage.IndexRoots[address] != old {
				t.Fatal("source root reused or changed", index.Name())
			}
		}
	}
	run(t, e, "INSERT INTO clone VALUES('x',1,10,TRUE,2),('x',2,20,FALSE,0),('x',3,30,NULL,0),('x',4,NULL,TRUE,2),('x',5,10,TRUE,2)")
	assertMembership := func(count int) {
		want := make(map[ids.RowID]ids.VersionID)
		for _, row := range e.Storage.Relations[destination.Address()].Versions {
			truth, err := plan.TruthOf(row.Values[3])
			if err != nil {
				t.Fatal(err)
			}
			if truth == plan.True {
				want[row.Row] = row.Version
			}
		}
		if len(want) != count {
			t.Fatal("unexpected current physical members", want, count)
		}
		for _, index := range copied {
			partialWantMembership(t, e.Storage.IndexRoots[index.Address()], want)
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
	for _, descriptor := range stage.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil {
			copied = index
		}
	}
	if copied == nil || copied.Address() == source.Address() || !copied.SQLIndex.Unique || !copied.SQLIndex.NullsNotDistinct || copied.SQLIndex.Constraint != nil || len(destination.Constraints.Unique) != 0 || copied.SQLIndex.Keys[0].Attribute != 3 || copied.SQLIndex.Predicate == nil || copied.SQLIndex.Predicate == source.SQLIndex.Predicate {
		t.Fatal("standalone partial unique/backing authority lost", copied)
	}
	predicate := copied.SQLIndex.Predicate
	for _, node := range []expr.Expr{predicate.Definition, predicate.Execution} {
		column, ok := node.(plan.ColumnExpr)
		if !ok || column.Origin.Address.ObjectID != destination.Address().ObjectID || column.Origin.Address.SubID != 4 || column.Slot != 3 {
			t.Fatal("partial unique predicate row mapping", node)
		}
	}
	queryCreatePublish(t, e, stage)
	if e.Storage.IndexRoots[copied.Address()] == nil || e.Storage.IndexRoots[copied.Address()] == e.Storage.IndexRoots[source.Address()] || e.Storage.IndexRoots[copied.Address()].Membership == nil {
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
			for _, descriptor := range stage.Descriptors() {
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
			root := e.Storage.IndexRoots[copied.Address()]
			if truth == "TRUE" {
				if root.Membership != nil || len(root.Entries) != 2 {
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
	if err := scope.Gate(statement(t, "CREATE TABLE clone(id INT)")); err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	stage, source, err := tableLikeBuild(t, e, op, "CREATE TABLE private_source(id INT,k INT,active BOOL)")
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err == nil {
		err = SchemaTableLocked(e, op, auth, stage, statement(t, "CREATE TABLE anchor(a INT)").(*parser.CreateTable), source.Namespace(), source.Owner, sqltype.InputEnvironment{}, nil)
	}
	if err == nil {
		auth.Catalog = e.StageRead(stage)
		auth.Recapture(auth.Catalog)
	}
	raw := statement(t, "CREATE INDEX private_partial ON private_source(k) WHERE active AND ('anchor'::regclass IS NOT NULL)").(*parser.CreateIndex)
	var created *catalog.Relation
	if err == nil {
		var predicate *catalog.IndexPredicate
		predicate, err = bindIndexPredicate(e, op, sqltype.InputEnvironment{}, auth, source, raw.Predicate)
		if err == nil {
			definition, columns, attributes, lookup, bindErr := index.BindAttributes(TableHost{core.DDLHost{E: e, Op: op}}, auth, sqltype.InputEnvironment{}, source, raw)
			err = bindErr
			if err == nil {
				definition.Predicate = predicate
				oid, allocationErr := e.Catalog.AllocateOIDLocked()
				err = allocationErr
				if err == nil {
					revision, revisionErr := e.Catalog.RevisionLocked()
					err = revisionErr
					if err == nil {
						for i := range columns {
							columns[i].OriginTable = uint32(oid)
							columns[i].OriginAttribute = int16(i + 1)
						}
						created = &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: source.Namespace(), ObjectName: "private_partial", ObjectRevision: revision, Owner: source.Owner, Columns: columns, Attributes: attributes, ColumnMeta: catalog.ColumnMetadata{HighWater: int32(len(attributes))}, Index: lookup, RelationKind: catalog.IndexRelation, SQLIndex: definition}
						err = stage.Create(created)
					}
				}
			}
		}
	}
	if err == nil {
		auth.Catalog = e.StageRead(stage)
		auth.Recapture(auth.Catalog)
		err = SchemaTableLocked(e, op, auth, stage, statement(t, "CREATE TABLE clone(lead TEXT,LIKE private_source INCLUDING ALL)").(*parser.CreateTable), source.Namespace(), source.Owner, sqltype.InputEnvironment{}, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(core.StageTableOrZero(stage).LikeSources) != 2 {
		t.Fatal("private source/index not captured", core.StageTableOrZero(stage).LikeSources)
	}
	if err := scope.AcquireDefinitionProviders(stage); err != nil {
		t.Fatal(err)
	}
	lockState := e.Locks.Inspect()
	held := len(lockState.Resources)
	if held != 0 {
		t.Fatal("private providers received incumbent holds", held)
	}
	queryCreatePublish(t, e, stage)
	current := e.CatalogRead().MustRelation(source.Address())
	if !current.History.HasIndexes || current.Revision() != source.Revision() || current == source {
		t.Fatal("private normalization invariant", current)
	}
	if e.Storage.IndexRoots[created.Address()] == nil {
		t.Fatal("private predicate index root absent")
	}
}
