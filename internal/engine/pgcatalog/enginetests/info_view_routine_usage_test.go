package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

type vruUnitFixture struct {
	f       defaultUnitFixture
	state   *catalog.State
	routine *catalog.Procedure
}

func newVRUUnitFixture(t *testing.T) vruUnitFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	view := f.e.Catalog.Read(state)
	integer, _ := view.LookupType(sqltype.Int4)
	routine := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 24100}, ObjectNamespace: f.schema, ObjectName: "routine", ObjectRevision: 1, Owner: f.owner, RoutineKind: catalog.FunctionRoutine, SQL: &proc.Definition{Result: integer.Usage()}}
	state.SetDescriptor(routine.Address(), routine)
	ref := view.Owner().Domain.Ref(view.Epoch(), routine.Address())
	call := plan.SQLRoutineExpr{Info: expr.Info{Usage: integer.Usage(), RoutineReference: routine}, Procedure: routine, Ref: ref, Signature: proc.InvocationSignature{Result: integer.Usage()}}
	if err := call.ValidateStoredProvider(); err != nil {
		t.Fatal("invalid stored call fixture", err)
	}
	for i, name := range []string{"a", "b"} {
		consumer := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(24101 + i)}, ObjectNamespace: f.schema, ObjectName: name, ObjectRevision: 1, Owner: f.reader, RelationKind: catalog.ViewRelation,
			View: stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.ProjectNode{Child: &plan.SingletonNode{}, Expressions: []expr.Expr{call, call}, AnalysisExpressions: []expr.Expr{call, call}}}, Dependencies: []catalog.ExpressionDependency{{Ref: ref, Revision: routine.Revision()}}})}
		state.SetDescriptor(consumer.Address(), consumer)
	}
	return vruUnitFixture{f: f, state: state, routine: routine}
}

func (f vruUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoViewRoutineUsageOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Database: "captured_database"})}
}

func vruUnitProduce(t *testing.T) pgcatalog.Producer {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoViewRoutineUsageOID)
	if !ok {
		t.Fatal("missing producer")
	}
	return produce
}

func vruUnitExpected(routine string) map[[6]string]int {
	return map[[6]string]int{
		{"captured_database", "default_schema", "a", "captured_database", "default_schema", routine}: 1,
		{"captured_database", "default_schema", "b", "captured_database", "default_schema", routine}: 1,
	}
}

func vruUnitBag(t *testing.T, source *catalog.Relation, rows [][]sqltype.Value) map[[6]string]int {
	t.Helper()
	if rows == nil {
		t.Fatal("nil successful capture")
	}
	got := make(map[[6]string]int)
	for _, row := range rows {
		if len(row) != 6 {
			t.Fatal("projection width", row)
		}
		var key [6]string
		for i, value := range row {
			column := source.Columns[i]
			if value.Type() != column.Usage.Type || value.Type().ID() != 13248 || value.IsNull() || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoViewRoutineUsageOID) || column.OriginAttribute != int16(i+1) {
				t.Fatal("domain/provenance", i, value, column)
			}
			key[i] = infoUserMappingsUnitText(t, value)
		}
		got[key]++
	}
	return got
}

func vruUnitRows(t *testing.T, in pgcatalog.Input, want map[[6]string]int) [][]sqltype.Value {
	t.Helper()
	captured, err := catalogrt.CaptureRows(in, vruUnitProduce(t))
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil {
		t.Fatal("anonymous capture identity", captured)
	}
	if got := vruUnitBag(t, in.Source, captured.Anonymous); !reflect.DeepEqual(got, want) {
		t.Fatal("usage bag", got, want)
	}
	return captured.Anonymous
}

func TestInfoViewRoutineUsageProducer(t *testing.T) {
	f := newVRUUnitFixture(t)
	want := vruUnitExpected("routine_24100")
	empty := map[[6]string]int{}
	bootstrap := dtpUnitAuthority(t, f.f, f.f.e.Catalog.Read(f.state)).State.CurrentUser
	for _, actor := range []authorization.PrincipalRef{bootstrap, f.f.owner, f.f.member} {
		vruUnitRows(t, f.input(t, f.state, actor), want)
	}
	vruUnitRows(t, f.input(t, f.state, f.f.reader), empty)
	granted := catalog.CloneState(f.state)
	routine := *f.routine
	routine.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.ExecutePrivilege, authorization.ExecutePrivilege)}}
	granted.SetDescriptor(routine.Address(), &routine)
	vruUnitRows(t, f.input(t, granted, f.f.reader), empty)
	retained := vruUnitRows(t, f.input(t, f.state, f.f.owner), want)
	changed := catalog.CloneState(f.state)
	routine.ObjectName, routine.Owner = "renamed", f.f.reader
	changed.SetDescriptor(routine.Address(), &routine)
	vruUnitRows(t, f.input(t, changed, f.f.owner), empty)
	vruUnitRows(t, f.input(t, changed, f.f.reader), vruUnitExpected("renamed_24100"))
	for _, row := range retained {
		if infoUserMappingsUnitText(t, row[5]) != "routine_24100" {
			t.Fatal("retained identity changed")
		}
	}
	// The provider has no prokind predicate; real descriptor identity, rather
	// than an executable SELECT call kind, owns these direct dependency rows.
	for _, kind := range []catalog.RoutineKind{catalog.ProcedureRoutine, catalog.AggregateRoutine} {
		next := catalog.CloneState(f.state)
		routine := *f.routine
		routine.RoutineKind = kind
		next.SetDescriptor(routine.Address(), &routine)
		alias, _ := f.f.e.Catalog.Read(next).LookupType(sqltype.Regprocedure)
		value, err := sqltype.RegprocValue(alias, uint32(routine.Address().ObjectID))
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []ids.OID{24101, 24102} {
			consumer := *next.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id})
			consumer.View = stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.ProjectNode{Child: &plan.SingletonNode{}, Expressions: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: alias.Usage()}, Value: value}}}}})
			next.SetDescriptor(consumer.Address(), &consumer)
		}
		vruUnitRows(t, f.input(t, next, f.f.owner), want)
	}
	// A different routine OID survives beside repeated occurrences of the first.
	next := catalog.CloneState(f.state)
	other := *f.routine
	other.ObjectAddress.ObjectID = 24110
	other.Signature = "23"
	other.SQL = &proc.Definition{Inputs: []sqltype.Usage{f.routine.SQL.Result}, Result: f.routine.SQL.Result}
	next.SetDescriptor(other.Address(), &other)
	consumer := *next.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 24102})
	definition := stored.ViewOf(&consumer)
	query := *definition.Query()
	project := *query.Root.(*plan.ProjectNode)
	ref := f.f.e.Catalog.Domain.Ref(next.Epoch(), other.Address())
	call := plan.SQLRoutineExpr{Info: expr.Info{Usage: other.SQL.Result, RoutineReference: &other}, Procedure: &other, Ref: ref, Signature: proc.InvocationSignature{Inputs: other.SQL.Inputs, Result: other.SQL.Result},
		Args: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: other.SQL.Inputs[0]}, Value: sqltype.Null(other.SQL.Inputs[0].Type)}}, WrittenCount: 1, InputMap: []plan.RoutineInput{{Start: 0, Count: 1}}, ChildSlots: []int{0}}
	if err := call.ValidateStoredProvider(); err != nil {
		t.Fatal("invalid overload fixture", err)
	}
	project.AnalysisExpressions = append(append([]expr.Expr(nil), project.AnalysisExpressions...), call)
	query.Root = &project
	consumer.View = definition.With(func(parts *stored.ViewParts) {
		parts.Query = &query
		parts.Dependencies = append(append([]catalog.ExpressionDependency(nil), parts.Dependencies...), catalog.ExpressionDependency{Ref: ref, Revision: other.Revision()})
	})
	next.SetDescriptor(consumer.Address(), &consumer)
	both := vruUnitExpected("routine_24100")
	both[[6]string{"captured_database", "default_schema", "b", "captured_database", "default_schema", "routine_24110"}] = 1
	vruUnitRows(t, f.input(t, next, f.f.owner), both)
	vruUnitRows(t, f.input(t, catalog.CloneState(f.f.e.Catalog.State), f.f.owner), empty)
	view := f.f.e.Catalog.Read(f.state)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoViewRoutineUsageOID})
	row, _ := view.LookupType(13496)
	array, _ := view.LookupType(13495)
	names := []string{"table_catalog", "table_schema", "table_name", "specific_catalog", "specific_schema", "specific_name"}
	for i, column := range source.Columns {
		if column.Name != names[i] || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 {
			t.Fatal("source column shape", i, column)
		}
	}
	if pgcatalog.InfoViewRoutineUsageOID != 16255 || source.RowType != row.ID() || row.AssociatedArray() != array.ID() || array.Element() != row {
		t.Fatal("native source/row/array identities", source, row, array)
	}
}

func TestInfoViewRoutineUsageCapture(t *testing.T) {
	t.Run("paired_statement_frontier", vruUnitPairedCapture)
	f := newVRUUnitFixture(t)
	produce := vruUnitProduce(t)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Catalog.Epoch(), in.Source.Address()) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { in.Ref.Address.ObjectID = pgcatalog.InfoViewColumnUsageOID },
		func(in *pgcatalog.Input) { in.Roles = authorization.Read{} },
		func(in *pgcatalog.Input) {
			in.Roles = engine.New().RoleStore.Published.Root.Read(authorization.PrincipalRef{})
		},
		func(in *pgcatalog.Input) { in.Catalog = engine.New().CatalogRead() },
		func(in *pgcatalog.Input) {
			runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
			runtime.Auth.RoleRecord = nil
			in.Runtime = core.NewCatalogRuntime(runtime)
		},
	} {
		in := f.input(t, f.state, f.f.owner)
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, malformed := range []string{"width", "domain", "domain-usage", "domain-generation", "base-generation", "missing-provider", "routine-namespace", "view-namespace", "wrong-provider-class"} {
		t.Run(malformed, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			view := f.f.e.Catalog.Read(state)
			switch malformed {
			case "width", "domain", "domain-usage", "domain-generation":
				source := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoViewRoutineUsageOID})
				source.Columns = append([]catalog.Column(nil), source.Columns...)
				switch malformed {
				case "width":
					source.Columns = source.Columns[:5]
				case "domain":
					base, _ := view.LookupType(sqltype.Name)
					source.Columns[0].Usage = base.Usage()
				case "domain-usage":
					source.Columns[0].Usage.Typmod = 3
				case "domain-generation":
					source.Columns[0].Usage.Type, _ = engine.New().CatalogRead().LookupType(13248)
				}
				state.SetDescriptor(source.Address(), &source)
			case "base-generation":
				base := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				base.SQLType, _ = engine.New().CatalogRead().LookupType(sqltype.Name)
				state.SetDescriptor(base.Address(), &base)
			case "missing-provider":
				state.RemoveDescriptor(f.routine.Address())
			case "routine-namespace":
				routine := *f.routine
				routine.ObjectNamespace.ObjectID = 999999
				state.SetDescriptor(routine.Address(), &routine)
			case "view-namespace":
				consumer := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 24102})
				consumer.ObjectNamespace.ObjectID = 999999
				state.SetDescriptor(consumer.Address(), &consumer)
			case "wrong-provider-class":
				state.SetDescriptor(f.routine.Address(), &catalog.Namespace{ObjectAddress: f.routine.Address(), ObjectName: "not_a_routine"})
			}
			if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
				t.Fatal("malformed metadata accepted", rows, err)
			}
		})
	}
	t.Run("missing_provider_after_row", func(t *testing.T) {
		state := catalog.CloneState(f.state)
		var last *catalog.Relation
		count := 0
		for _, d := range state.Descriptors() {
			if relation, ok := d.(*catalog.Relation); ok && relation.RelationKind == catalog.ViewRelation && relation.View != nil {
				last = relation
				count++
			}
		}
		if count != 2 {
			t.Fatal("fixture must visit an earlier valid view", count)
		}
		consumer := *last
		missing := f.f.e.Catalog.Domain.Ref(state.Epoch(), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 999999})
		consumer.View = stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.ProjectNode{Child: &plan.SingletonNode{}, Expressions: []expr.Expr{plan.SQLRoutineExpr{Info: expr.Info{Usage: f.routine.SQL.Result}, Procedure: f.routine, Ref: missing}}}}})
		state.SetDescriptor(consumer.Address(), &consumer)
		// Replacing a value at the same address leaves descriptor traversal
		// order unchanged; the missing provider follows a qualifying view.
		if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
			t.Fatal("late missing provider exposed prefix", rows, err)
		}
	})
}

func TestInfoViewRoutineUsageCancellation(t *testing.T) {
	f := newVRUUnitFixture(t)
	produce := vruUnitProduce(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	const checkpoints = 1 << 30
	count := &dtpCancelContext{Context: context.Background(), remaining: checkpoints}
	in := f.input(t, f.state, f.f.owner)
	in.Context = count
	if rows, err := produce(in); err != nil || !reflect.DeepEqual(vruUnitBag(t, in.Source, rows), vruUnitExpected("routine_24100")) {
		t.Fatal("baseline capture", rows, err)
	}
	late := &dtpCancelContext{Context: context.Background(), remaining: checkpoints - count.remaining}
	for _, ctx := range []context.Context{ctx, &dtpCancelContext{Context: context.Background(), remaining: 2}, late} {
		in := f.input(t, f.state, f.f.owner)
		in.Context = ctx
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed prefix", rows, err)
		}
	}
	// Cancellation must precede even invalid authority validation.
	in = f.input(t, f.state, f.f.owner)
	in.Context, in.Roles = ctx, authorization.Read{}
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("validation displaced cancellation", rows, err)
	}
}

func vruUnitPairedCapture(t *testing.T) {
	f := newVRUUnitFixture(t)
	f.f.e.Catalog.PublishLocked(f.state)
	f.f.op.Session.DatabaseName = "captured_database"
	f.f.actor(f.f.member)
	scope, err := f.f.e.StartStatement(f.f.op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	f.f.e.Mu.Lock()
	err = f.f.e.CaptureCommentSnapshotLocked(scope)
	f.f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	view := scope.Inquiry.CommentCatalog
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoViewRoutineUsageOID})
	next := catalog.CloneState(f.state)
	routine := *f.routine
	routine.ObjectName, routine.Owner = "renamed", f.f.reader
	next.SetDescriptor(routine.Address(), &routine)
	// Publish a distinct, canonical domain/source generation and revoke the
	// member's inherited ownership after the statement captured both roots.
	domain := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13248})
	base, _ := view.LookupType(sqltype.Name)
	domain.SQLType, err = sqltype.Domain(13248, "sql_identifier", "information_schema.sql_identifier", base.Usage())
	if err != nil {
		t.Fatal(err)
	}
	next.SetDescriptor(domain.Address(), &domain)
	nextSource := *source
	nextSource.Columns = append([]catalog.Column(nil), source.Columns...)
	for i := range nextSource.Columns {
		nextSource.Columns[i].Usage = domain.SQLType.Usage()
	}
	next.SetDescriptor(nextSource.Address(), &nextSource)
	roles := cloneRoleFixture(f.f.e.RoleStore.Published)
	edges := make([]authorization.Membership, 0)
	for _, edge := range fixtureEdges(roles) {
		if edge.Role != f.f.owner || edge.Member != f.f.member {
			edges = append(edges, edge)
		}
	}
	setFixtureEdges(roles, edges)
	f.f.e.RoleStore.PublishLocked(roles)
	f.f.e.Catalog.PublishLocked(next)
	f.f.actor(f.f.reader)
	f.f.op.Session.DatabaseName = "new_database"
	f.f.e.Mu.Lock()
	captured, err := core.EngineOpenRelationSourceLocked(f.f.e, scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.View != nil || !reflect.DeepEqual(vruUnitBag(t, source, captured.Anonymous), vruUnitExpected("routine_24100")) {
		t.Fatal("source did not preserve paired frontier", captured)
	}
	vruUnitRows(t, f.input(t, next, f.f.owner), map[[6]string]int{})
	vruUnitRows(t, f.input(t, next, f.f.member), map[[6]string]int{})
	vruUnitRows(t, f.input(t, next, f.f.reader), vruUnitExpected("renamed_24100"))
}

func TestInfoViewRoutineUsageStoredCallIdentity(t *testing.T) {
	f := newVRUUnitFixture(t)
	consumer := f.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 24101})
	call := stored.ViewOf(consumer).Query().Root.(*plan.ProjectNode).Expressions[0].(plan.SQLRoutineExpr)
	if err := call.ValidateStoredProvider(); err != nil {
		t.Fatal("valid stored call rejected", err)
	}
	// The stored-call seam checks descriptor association and complete address,
	// not catalog-domain/epoch validity; retained TEMP lifetimes exercise that
	// separately through the real runtime consumer boundary.
	for _, mutate := range []func(*plan.SQLRoutineExpr){
		func(n *plan.SQLRoutineExpr) { copy := *n.Procedure; n.Procedure = &copy },
		func(n *plan.SQLRoutineExpr) { n.Ref.Address.ObjectID++ },
		func(n *plan.SQLRoutineExpr) { n.Ref.Address.ClassID = ids.RelationClassID },
		func(n *plan.SQLRoutineExpr) { n.Ref.Address.SubID = 1 },
		func(n *plan.SQLRoutineExpr) { n.Ref = ids.ObjectRef{} },
		func(n *plan.SQLRoutineExpr) { n.Signature.Result = sqltype.Usage{} },
	} {
		bad := call
		mutate(&bad)
		if err := bad.ValidateStoredProvider(); err == nil {
			t.Fatal("malformed stored call accepted", bad)
		}
	}
}
