package rolecmds_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func templateTemporaryName(name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_temp"}, {Text: name}}}
}

func templateTemporaryCopy(t *testing.T, e, source *core.Engine, executor *session.Session, name string) *core.Engine {
	t.Helper()
	source.ReplacementMu.RLock()
	defer source.ReplacementMu.RUnlock()
	owner := e.Roles().LookupRef("postgres")
	e.Mu.Lock()
	lease, err := source.AcquireDatabaseTemplateLocked(executor)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	target := rolecmds.NewDatabaseRuntime(e, source, name, owner, ddl.CreateDatabaseOptions{AllowConnections: true, ConnectionLimit: -1})
	e.Mu.Unlock()
	defer lease.Release()
	if err := e.Locks.AdmitDomain(target.Catalog.Domain); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ReplaceDatabaseTemplate(t.Context(), source, source.CatalogRead()); err != nil {
		t.Fatal("template metadata remap", err)
	}
	return target
}

func TestDatabaseTemplateTemporaryMetadata(t *testing.T) {
	e, source := templateUnitSource(t)
	op, done := enginetest.RoleOperation(t, source, "postgres")
	defer done()
	table := primaryKeyUnitCreate("temp_graph", "temp_graph_pk")
	table.Persistence = "temporary"
	table.Parents = []parser.ObjectName{{Parts: []parser.Identifier{{Text: "public"}, {Text: "t"}}}}
	table.Elements[0].Column.Constraints = []parser.ColumnConstraint{{Default: parser.FunctionExpr{Name: templateTemporaryName("graph_function")}}}
	table.Elements = append(table.Elements, parser.TableElement{Column: &parser.Column{Name: "n", TypeName: parser.TypeName{Name: "integer"}, Constraints: []parser.ColumnConstraint{{Default: sequence.SerialDefault("pg_temp", "graph_sequence")}}}})
	statements := []parser.Statement{
		&parser.CreateSequence{Name: templateTemporaryName("graph_sequence"), Persistence: "temporary", Options: []parser.SequenceOption{{Name: "start", Value: 41}}},
		&parser.CreateFunction{Name: templateTemporaryName("graph_function"), Result: parser.TypeName{Name: "integer"}, ResultSpecified: true, Language: parser.Identifier{Text: "sql"}, Body: "SELECT 7"},
		table,
	}
	for _, method := range []string{"hash", "brin"} {
		statements = append(statements, &parser.CreateIndex{Name: parser.Identifier{Text: "temp_graph_" + method}, Table: templateTemporaryName("temp_graph"), Method: method, Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "a"}}}})
	}
	insert := primaryKeyUnitInsert(1)
	insert.Name = templateTemporaryName("temp_graph")
	insert.Rows[0] = append(insert.Rows[0], parser.DefaultExpr{})
	statements = append(statements, insert)
	for _, statement := range statements {
		if _, err := source.Execute(op, statement, 0, nil); err != nil {
			t.Fatal("source TEMP graph fixture", err)
		}
	}
	original := source.CatalogRead()
	namespace := op.Session.TemporaryNamespace
	sourceNamespace := original.MustNamespace(namespace)
	address := original.State().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "temp_graph"})
	sequenceAddress := original.State().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "graph_sequence"})
	functionAddress := ids.ObjectAddress{}
	for a, descriptor := range original.Descriptors() {
		if procedure, ok := descriptor.(*catalog.Procedure); ok && procedure.Namespace() == namespace && procedure.Name() == "graph_function" {
			functionAddress = a
		}
	}
	if functionAddress == (ids.ObjectAddress{}) {
		t.Fatal("missing source routine fixture")
	}
	sourceHeap := source.Storage.Relations[address]
	sourceRows := append(sourceHeap.Versions[:0:0], sourceHeap.Versions...)
	sourceGenerator := source.Storage.Sequences[sequenceAddress]
	source.Mu.Lock()
	sourceGenerator.Last, sourceGenerator.Called = 99, true
	source.Mu.Unlock()
	target := templateTemporaryCopy(t, e, source, op.Session, "temp_metadata_clone")
	if source.Catalog.State != original.State() || source.Catalog.State.MustNamespace(namespace) != sourceNamespace || source.Storage.Relations[address] != sourceHeap || !reflect.DeepEqual(sourceHeap.Versions, sourceRows) || source.Storage.Sequences[sequenceAddress] != sourceGenerator || sourceGenerator.Last != 99 {
		t.Fatal("template copy mutated source TEMP metadata or backing")
	}
	read := target.CatalogRead()
	detached := read.MustNamespace(namespace).Temporary
	if !detached.IsDetached() || detached.Domain() != target.Catalog.Domain || detached.ID() != op.Session.SessionToken.ID() || detached == op.Session.SessionToken || detached == target.Catalog.Domain.Session(detached.ID()) || session.TokenAttached(detached) {
		t.Fatal("copied TEMP metadata retained live/source session authority")
	}
	for a, descriptor := range read.Descriptors() {
		ns, isNamespace := descriptor.(*catalog.Namespace)
		parent, member := read.Namespace(descriptor.Namespace())
		if !isNamespace && (!member || !parent.Temporary.IsDetached()) || isNamespace && !ns.Temporary.IsDetached() {
			continue
		}
		if err := target.Catalog.Provider(a.ClassID).Validate(read, descriptor); err != nil {
			t.Fatal("invalid remapped TEMP descriptor", descriptor.Name(), err)
		}
		if isNamespace && ns.Temporary != detached {
			t.Fatal("namespace/toast grouping lost")
		}
	}
	cloneTable := read.MustRelation(address)
	for _, recipe := range cloneTable.ColumnMeta.Defaults {
		for _, dependency := range recipe.Dependencies {
			if dependency.Domain() != target.Catalog.Domain || dependency.Epoch != read.Epoch() {
				t.Fatal("copied default retained source reference", dependency)
			}
		}
		if _, _, err := stored.RenderColumnDefault(t.Context(), read, recipe.Expression); err != nil {
			t.Fatal("copied default cannot resolve destination routine/sequence", err)
		}
	}
	sourceProcedure := original.MustProcedure(functionAddress)
	cloneProcedure := read.MustProcedure(functionAddress)
	// Definition is immutable syntax/type layout. Unchanged builtin usages may
	// share it; authority belongs to the declaration and captured catalog ref.
	if cloneProcedure == sourceProcedure || cloneProcedure.Owner != sourceProcedure.Owner || cloneProcedure.RoutineDefinition() == nil {
		t.Fatal("copied routine lost its destination declaration or preserved principal")
	}
	call, ok := cloneTable.ColumnMeta.Defaults[1].Expression.(plan.SQLRoutineExpr)
	if !ok || call.Procedure == nil || call.Procedure == sourceProcedure || call.RoutineReference != call.Procedure || call.Procedure.Owner != cloneProcedure.Owner || !exec.SameRoutineDeclaration(cloneProcedure, call.Procedure) || call.Ref != target.Catalog.Domain.Ref(read.Epoch(), functionAddress) || call.ResultUsage() != cloneProcedure.RoutineDefinition().Result || !call.Signature.ValidFor(cloneProcedure.RoutineDefinition()) {
		t.Fatal("copied function default lost destination declaration/reference/result generation")
	}
	if err := call.ValidateStoredProvider(); err != nil {
		t.Fatal("copied routine capture is not a valid stored provider", err)
	}
	if _, _, err := stored.RenderColumnDefault(t.Context(), original, call); err == nil {
		t.Fatal("copied function default acquired source catalog authority")
	}
	if _, _, err := stored.RenderColumnDefault(t.Context(), read, original.MustRelation(address).ColumnMeta.Defaults[1].Expression); err == nil {
		t.Fatal("source function default acquired destination catalog authority")
	}
	if target.Storage.Relations[address] == sourceHeap || len(target.Storage.Relations[address].Versions) != 0 || target.Storage.Sequences[sequenceAddress] == sourceGenerator || target.Storage.Sequences[sequenceAddress].Last != 41 || target.Storage.Sequences[sequenceAddress].Called {
		t.Fatal("source TEMP physical contents/generator state copied or shared")
	}
	indexes := 0
	for a, descriptor := range read.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != address {
			continue
		}
		indexes++
		root := target.Storage.IndexRoots[a]
		if root == nil || root == source.Storage.IndexRoots[a] || len(root.Entries) != 0 || root.Hash != nil && len(root.Hash.Buckets) != 0 || root.Brin != nil && len(root.Brin.Ranges) != 0 {
			t.Fatal("copied TEMP index did not receive private empty backing", index.Name())
		}
	}
	if indexes != 3 {
		t.Fatal("TEMP primary/B-tree, hash and BRIN metadata missing", indexes)
	}
	if _, err := databaseCreateUnitPublish(t, e, source, t.Context(), target.DatabaseName, target); err != nil {
		t.Fatal(err)
	}
	// Reuse the source's numeric ID in the destination: ordinary startup still
	// cannot attach a detached namespace or mint its opaque metadata identity.
	physical, err := enginetest.OpenSession(t.Context(), target, session.SessionStart{ID: detached.ID()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		physical.RequestEnd(session.CleanClose)
		if err := physical.WaitEnd(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	if physical.SessionToken == detached || physical.TemporaryNamespace != (ids.ObjectAddress{}) {
		t.Fatal("equal numeric physical owner adopted detached metadata")
	}
	forged := &session.Operation{Session: &session.Session{SessionToken: detached, State: session.Live}}
	forged.Self = forged
	if err := session.ValidateOperation(target.Catalog.Domain, forged); err == nil {
		t.Fatal("detached metadata validated as a live operation")
	}
	if err := session.TransactionAuthority(target.Catalog.Domain, forged); err == nil {
		t.Fatal("detached metadata validated as transaction authority")
	}
	if ticket, err := target.Locks.RegisterSession(detached); err == nil || ticket != nil {
		t.Fatal("detached metadata registered advisory authority")
	}
	ref := target.Catalog.Domain.Tx(detached, 1, read.Epoch(), physical.Transactions, 0)
	if ticket, err := target.Locks.Register(ref); err == nil || ticket != nil {
		t.Fatal("detached metadata registered transaction lock authority")
	}
	// Model a second, genuine live owner group at the same displayed ID. The
	// source now has detached and live groups that a re-clone must not merge.
	target.Mu.Lock()
	liveAddress, err := target.Catalog.AllocateOIDLocked()
	if err != nil {
		target.Mu.Unlock()
		t.Fatal(err)
	}
	live := *read.MustNamespace(namespace)
	live.ObjectAddress = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: liveAddress}
	live.ObjectName = "pg_temp_live_same_id"
	live.Temporary = physical.SessionToken
	next := catalog.CloneState(target.Catalog.State)
	next.SetDescriptor(live.Address(), &live)
	next.BindName(catalog.DescriptorNameKey(&live), live.Address())
	if err := (catalog.NamespaceProvider{}).Validate(target.Catalog.Read(next), &live); err != nil {
		target.Mu.Unlock()
		t.Fatal(err)
	}
	target.Catalog.PublishLocked(next)
	target.Mu.Unlock()
	physical.Mu.Lock()
	physical.TemporaryNamespace = live.Address()
	physical.Mu.Unlock()
	again := templateTemporaryCopy(t, e, target, physical, "temp_metadata_reclone")
	copied := again.CatalogRead()
	oldGroup, liveGroup := copied.MustNamespace(namespace).Temporary, copied.MustNamespace(live.Address()).Temporary
	if !oldGroup.IsDetached() || !liveGroup.IsDetached() || oldGroup == liveGroup || oldGroup.ID() != liveGroup.ID() || oldGroup.Domain() != again.Catalog.Domain || liveGroup.Domain() != again.Catalog.Domain || oldGroup == detached || liveGroup == physical.SessionToken {
		t.Fatal("re-clone collapsed equal-ID detached/live TEMP owner groups")
	}
	// The source's genuinely live TEMP child still protects its persistent
	// parent from native replacement; the clone's detached child does not.
	sourceRoot := source.Catalog.State
	if err := engine.Reset(t.Context(), source); err == nil || err.Error() != control.TemporaryInheritanceReplacementError {
		t.Fatal("live TEMP dependency replacement guard changed", err)
	}
	if source.Catalog.State != sourceRoot || source.Storage.Relations[address] != sourceHeap {
		t.Fatal("rejected replacement changed the live source graph")
	}
	liveTable := primaryKeyUnitCreate("live_guard", "live_guard_pk")
	liveTable.Persistence = "temporary"
	liveInsert := primaryKeyUnitInsert(2)
	liveInsert.Name = templateTemporaryName("live_guard")
	liveOp, err := physical.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []parser.Statement{liveTable, liveInsert} {
		if _, err := target.Execute(liveOp, statement, 0, nil); err != nil {
			liveOp.Done()
			t.Fatal(err)
		}
	}
	liveOp.Done()
	liveTableAddress := target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: live.Address(), Name: "live_guard"})
	liveHeap := target.Storage.Relations[liveTableAddress]
	for _, phase := range []string{"restore", "reset"} {
		var err error
		if phase == "restore" {
			stage, stageErr := target.RestoreStage()
			if stageErr != nil {
				t.Fatal(stageErr)
			}
			_, err = target.ReplaceCatalog(t.Context(), stage, stage.CatalogRead())
		} else {
			err = engine.Reset(t.Context(), target)
		}
		if err != nil {
			t.Fatal("detached metadata vetoed "+phase, err)
		}
		current := target.CatalogRead()
		if current.Get(namespace) != nil || current.Get(address) != nil || current.Get(sequenceAddress) != nil {
			t.Fatal("non-live copied metadata retained by " + phase)
		}
		if current.Get(live.Address()) != &live || target.Storage.Relations[liveTableAddress] != liveHeap || len(liveHeap.Versions) != 1 || physical.TemporaryNamespace != live.Address() {
			t.Fatal("real live TEMP ownership/backing lost during " + phase)
		}
	}
}
