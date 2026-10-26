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
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type ccruUnitFixture struct {
	f       defaultUnitFixture
	state   *catalog.State
	routine *catalog.Procedure
}

func newCCRUUnitFixture(t *testing.T) ccruUnitFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	view := f.e.Catalog.Read(state)
	boolean, _ := view.LookupType(sqltype.Bool)
	integer, _ := view.LookupType(sqltype.Int4)
	routine := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 24100}, ObjectNamespace: f.schema, ObjectName: "routine", ObjectRevision: 1, Owner: f.owner}
	state.SetDescriptor(routine.Address(), routine)
	call := plan.SQLRoutineExpr{Info: expr.Info{Usage: boolean.Usage()}, Procedure: routine, Ref: view.Owner().Domain.Ref(view.Epoch(), routine.Address())}
	// Real expression children retain repeated semantic routine occurrences;
	// the reference walker and final six-column DISTINCT both participate.
	repeated := call
	repeated.Args = []expr.Expr{call, call}
	for i := range 2 {
		table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(24101 + i)}, ObjectNamespace: f.schema, ObjectName: []string{"a", "b"}[i], ObjectRevision: 1, Owner: f.reader, RelationKind: catalog.TableRelation,
			Constraints: catalog.RelationConstraints{Check: []catalog.CheckConstraint{{OID: ids.OID(24120 + i), Name: "same", Expression: repeated}}}}
		state.SetDescriptor(table.Address(), table)
	}
	typ, err := sqltype.Domain(24110, "domain", "default_schema.domain", integer.Usage(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	domain := &catalog.Type{SQLType: typ, ObjectNamespace: f.schema, ObjectRevision: 1, Owner: f.reader, DomainChecks: []catalog.DomainCheckConstraint{{OID: 24122, Name: "domain_check", Expression: call, NotValid: true}}}
	state.SetDescriptor(domain.Address(), domain)
	return ccruUnitFixture{f: f, state: state, routine: routine}
}

func (f ccruUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoCheckConstraintRoutineUsageOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}

func ccruUnitProduce(t *testing.T) pgcatalog.Producer {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoCheckConstraintRoutineUsageOID)
	if !ok {
		t.Fatal("missing producer")
	}
	return produce
}

func ccruUnitRows(t *testing.T, in pgcatalog.Input, produce pgcatalog.Producer, want map[[6]string]int) [][]sqltype.Value {
	t.Helper()
	captured, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
		t.Fatal("anonymous capture identity", captured)
	}
	got := make(map[[6]string]int)
	for _, row := range captured.Anonymous {
		if len(row) != 6 {
			t.Fatal("projection width", row)
		}
		var key [6]string
		for i, value := range row {
			column := in.Source.Columns[i]
			if value.Type() != column.Usage.Type || value.Type().ID() != 13248 || value.IsNull() || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoCheckConstraintRoutineUsageOID) || column.OriginAttribute != int16(i+1) {
				t.Fatal("domain/provenance", i, value, column)
			}
			key[i] = infoUserMappingsUnitText(t, value)
		}
		got[key]++
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("usage bag", got, want)
	}
	return captured.Anonymous
}

func ccruUnitExpected(routine string) map[[6]string]int {
	return map[[6]string]int{
		{"captured_database", "default_schema", "same", "captured_database", "default_schema", routine}:         1,
		{"captured_database", "default_schema", "domain_check", "captured_database", "default_schema", routine}: 1,
	}
}

func TestInfoCheckConstraintRoutineUsageProducer(t *testing.T) {
	f := newCCRUUnitFixture(t)
	produce := ccruUnitProduce(t)
	want := ccruUnitExpected("routine_24100")
	empty := map[[6]string]int{}
	bootstrap := dtpUnitAuthority(t, f.f, f.f.e.Catalog.Read(f.state)).State.CurrentUser
	for _, actor := range []authorization.PrincipalRef{bootstrap, f.f.owner, f.f.member} {
		ccruUnitRows(t, f.input(t, f.state, actor), produce, want)
	}
	ccruUnitRows(t, f.input(t, f.state, f.f.reader), produce, empty)
	// EXECUTE, even with grant option, is not immediately usable owner authority.
	granted := catalog.CloneState(f.state)
	routine := *f.routine
	routine.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.ExecutePrivilege, authorization.ExecutePrivilege)}}
	granted.SetDescriptor(routine.Address(), &routine)
	ccruUnitRows(t, f.input(t, granted, f.f.reader), produce, empty)
	retained := ccruUnitRows(t, f.input(t, f.state, f.f.owner), produce, want)
	changed := catalog.CloneState(f.state)
	routine.ObjectName, routine.Owner = "renamed", f.f.reader
	changed.SetDescriptor(routine.Address(), &routine)
	ccruUnitRows(t, f.input(t, changed, f.f.owner), produce, empty)
	ccruUnitRows(t, f.input(t, changed, f.f.reader), produce, ccruUnitExpected("renamed_24100"))
	for _, row := range retained {
		if infoUserMappingsUnitText(t, row[5]) != "routine_24100" {
			t.Fatal("retained identity changed")
		}
	}
	// Different same-name sources must not be collapsed before their routine
	// identities are known. The domain continues to reference the original.
	separate := catalog.CloneState(f.state)
	other := *f.routine
	other.ObjectAddress.ObjectID, other.ObjectName = 24130, "other"
	separate.SetDescriptor(other.Address(), &other)
	table := *separate.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 24102})
	table.Constraints.Check = []catalog.CheckConstraint{{OID: 24121, Name: "same", Expression: plan.SQLRoutineExpr{Info: expr.Info{Usage: f.routineResult(t)}, Procedure: &other, Ref: f.f.e.Catalog.Domain.Ref(separate.Epoch(), other.Address())}}}
	separate.SetDescriptor(table.Address(), &table)
	both := ccruUnitExpected("routine_24100")
	both[[6]string{"captured_database", "default_schema", "same", "captured_database", "default_schema", "other_24130"}] = 1
	ccruUnitRows(t, f.input(t, separate, f.f.owner), produce, both)
	ccruUnitRows(t, f.input(t, catalog.CloneState(f.f.e.Catalog.State), f.f.owner), produce, empty)
}

func (f ccruUnitFixture) routineResult(t *testing.T) sqltype.Usage {
	t.Helper()
	boolean, ok := f.f.e.CatalogRead().LookupType(sqltype.Bool)
	if !ok {
		t.Fatal("missing bool")
	}
	return boolean.Usage()
}

func TestInfoCheckConstraintRoutineUsageCapture(t *testing.T) {
	t.Run("paired_statement_frontier", ccruUnitPairedCapture)
	f := newCCRUUnitFixture(t)
	produce := ccruUnitProduce(t)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Catalog.Epoch(), in.Source.Address()) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { in.Ref.Address.ObjectID = pgcatalog.InfoDomainConstraintsOID },
		func(in *pgcatalog.Input) { in.Roles = authorization.Read{} },
		func(in *pgcatalog.Input) { in.Catalog = engine.New().CatalogRead() },
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth.RoleRecord = nil
			in.Runtime = runtime
		},
	} {
		in := f.input(t, f.state, f.f.owner)
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, malformed := range []string{"width", "domain", "domain-generation", "base-generation", "missing-provider", "namespace"} {
		t.Run(malformed, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			view := f.f.e.Catalog.Read(state)
			switch malformed {
			case "width", "domain", "domain-generation":
				source := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoCheckConstraintRoutineUsageOID})
				source.Columns = append([]catalog.Column(nil), source.Columns...)
				if malformed == "width" {
					source.Columns = source.Columns[:5]
				} else if malformed == "domain" {
					base, _ := view.LookupType(sqltype.Name)
					source.Columns[0].Usage = base.Usage()
				} else {
					source.Columns[0].Usage.Type, _ = engine.New().CatalogRead().LookupType(13248)
				}
				state.SetDescriptor(source.Address(), &source)
			case "base-generation":
				base := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				base.SQLType, _ = engine.New().CatalogRead().LookupType(sqltype.Name)
				state.SetDescriptor(base.Address(), &base)
			case "missing-provider":
				state.RemoveDescriptor(f.routine.Address())
			case "namespace":
				routine := *f.routine
				routine.ObjectNamespace.ObjectID = 999999
				state.SetDescriptor(routine.Address(), &routine)
			}
			if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
				t.Fatal("malformed metadata accepted", rows, err)
			}
		})
	}
}

func TestInfoCheckConstraintRoutineUsageCancellation(t *testing.T) {
	f := newCCRUUnitFixture(t)
	produce := ccruUnitProduce(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// Measure the real traversal's cancellation checkpoints, then interrupt at
	// its final checkpoint. Multiple qualifying sources precede that point.
	const checkpoints = 1 << 30
	count := &dtpCancelContext{Context: context.Background(), remaining: checkpoints}
	in := f.input(t, f.state, f.f.owner)
	in.Context = count
	if rows, err := produce(in); err != nil || len(rows) != 2 {
		t.Fatal("baseline capture", rows, err)
	}
	late := &dtpCancelContext{Context: context.Background(), remaining: checkpoints - count.remaining}
	for _, ctx := range []context.Context{ctx, &dtpCancelContext{Context: context.Background(), remaining: 1}, late} {
		in := f.input(t, f.state, f.f.owner)
		in.Context = ctx
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed prefix", rows, err)
		}
	}
}

func ccruUnitPairedCapture(t *testing.T) {
	f := newCCRUUnitFixture(t)
	f.f.e.Catalog.PublishLocked(f.state)
	f.f.op.Session.DatabaseName = "captured_database"
	f.f.actor(f.f.owner)
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
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoCheckConstraintRoutineUsageOID})
	// Advance publication after the inquiry captured its paired frontier. The
	// real source-opening path must neither sample this newer catalog nor lose
	// the captured authorization, database name or domain generation.
	next := catalog.CloneState(f.state)
	routine := *f.routine
	routine.ObjectName, routine.Owner = "renamed", f.f.reader
	next.SetDescriptor(routine.Address(), &routine)
	f.f.e.Catalog.PublishLocked(next)
	f.f.e.Mu.Lock()
	captured, err := core.EngineOpenRelationSourceLocked(f.f.e, scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Anonymous == nil || captured.View != nil {
		t.Fatal("expected detached anonymous source")
	}
	got := make(map[[6]string]int)
	for _, row := range captured.Anonymous {
		if len(row) != 6 {
			t.Fatal("projection width", row)
		}
		var key [6]string
		for i, value := range row {
			if value.Type() != source.Columns[i].Usage.Type || !value.ValidResult(source.Columns[i].Usage) {
				t.Fatal("captured domain generation", i, value)
			}
			key[i] = infoUserMappingsUnitText(t, value)
		}
		got[key]++
	}
	if !reflect.DeepEqual(got, ccruUnitExpected("routine_24100")) {
		t.Fatal("source did not preserve paired frontier", got)
	}
	produce := ccruUnitProduce(t)
	ccruUnitRows(t, f.input(t, next, f.f.owner), produce, map[[6]string]int{})
	ccruUnitRows(t, f.input(t, next, f.f.reader), produce, ccruUnitExpected("renamed_24100"))
}
