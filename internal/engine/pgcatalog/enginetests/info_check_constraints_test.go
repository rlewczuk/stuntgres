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
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

type iccUnitFixture struct {
	f      defaultUnitFixture
	state  *catalog.State
	domain *catalog.Type
}

func newICCUnitFixture(t *testing.T) iccUnitFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	view := f.e.Catalog.Read(state)
	boolean, _ := view.LookupType(sqltype.Bool)
	integer, _ := view.LookupType(sqltype.Int4)
	literal := func(value bool) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, value)}
	}
	for i := range 3 {
		table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(24200 + i)}, ObjectNamespace: f.schema, ObjectName: []string{"a", "b", "c"}[i], ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.TableRelation, Attributes: []int32{3}, Columns: []catalog.Column{{Name: "Odd name", Usage: integer.Usage()}}, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}}, Constraints: catalog.RelationConstraints{Check: []catalog.CheckConstraint{{OID: ids.OID(24210 + i), Name: "same", Expression: literal(i != 2), NotValid: true}}}}
		if i == 0 {
			table.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 24213, Name: "table_nn", Attribute: 3, NotValid: true}}
		}
		state.SetDescriptor(table.Address(), table)
	}
	typ, err := sqltype.Domain(24220, "own_domain", "default_schema.own_domain", integer.Usage(), true, true)
	if err != nil {
		t.Fatal(err)
	}
	domain := &catalog.Type{SQLType: typ, ObjectNamespace: f.schema, ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.UsagePrivilege, authorization.UsagePrivilege)}}, DomainChecks: []catalog.DomainCheckConstraint{{OID: 24221, Name: "domain_ck", Expression: literal(true), NotValid: true}}, DomainNotNull: &catalog.DomainNotNullConstraint{OID: 24222, Name: "domain_nn"}}
	state.SetDescriptor(domain.Address(), domain)
	return iccUnitFixture{f, state, domain}
}

func (f iccUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoCheckConstraintsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}

func iccUnitProducer(t *testing.T) pgcatalog.Producer {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoCheckConstraintsOID)
	if !ok {
		t.Fatal("missing producer")
	}
	return produce
}

func iccUnitExpected() map[[4]string]int {
	return map[[4]string]int{
		{"captured_database", "default_schema", "same", "true"}:                     2,
		{"captured_database", "default_schema", "same", "false"}:                    1,
		{"captured_database", "default_schema", "table_nn", "Odd name IS NOT NULL"}: 1,
		{"captured_database", "default_schema", "domain_ck", "true"}:                1,
		{"captured_database", "default_schema", "domain_nn", "VALUE IS NOT NULL"}:   1,
	}
}

func iccUnitBag(t *testing.T, in pgcatalog.Input, rows [][]sqltype.Value) map[[4]string]int {
	t.Helper()
	if rows == nil {
		t.Fatal("nil successful rows")
	}
	bag := make(map[[4]string]int)
	for _, row := range rows {
		if len(row) != 4 {
			t.Fatal("projection width", row)
		}
		var key [4]string
		for i, value := range row {
			column := in.Source.Columns[i]
			domain, base := sqltype.ID(13248), sqltype.Name
			if i == 3 {
				domain, base = 13246, sqltype.VarChar
			}
			physical, _ := in.Catalog.LookupType(base)
			if value.Type() != column.Usage.Type || value.Type().ID() != domain || value.Type().BaseType() != physical || value.IsNull() || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoCheckConstraintsOID) || column.OriginAttribute != int16(i+1) {
				t.Fatal("domain/provenance", i, value, column)
			}
			payload, err := value.BaseValue()
			if err != nil || payload.Type() != physical {
				t.Fatal("base payload", i, payload, err)
			}
			key[i], err = payload.Text()
			if err != nil {
				t.Fatal("base text", i, err)
			}
		}
		// Superuser also sees actual builtin domain recipes; this fixture's
		// complete selected bag is independent of that unrelated inventory.
		if key[1] == "default_schema" {
			bag[key]++
		}
	}
	return bag
}

func iccUnitRows(t *testing.T, in pgcatalog.Input, want map[[4]string]int) [][]sqltype.Value {
	t.Helper()
	captured, err := core.CaptureCatalogSourceRows(in, iccUnitProducer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.View != nil || captured.Ref != in.Ref {
		t.Fatal("capture ownership", captured)
	}
	if got := iccUnitBag(t, in, captured.Anonymous); !reflect.DeepEqual(got, want) {
		t.Fatal("definition bag", got, want)
	}
	return captured.Anonymous
}

func TestInfoCheckConstraintsProducer(t *testing.T) {
	f := newICCUnitFixture(t)
	want, empty := iccUnitExpected(), map[[4]string]int{}
	bootstrap := dtpUnitAuthority(t, f.f, f.f.e.Catalog.Read(f.state)).State.CurrentUser
	for _, actor := range []authorization.PrincipalRef{bootstrap, f.f.owner, f.f.member} {
		iccUnitRows(t, f.input(t, f.state, actor), want)
	}
	// Neither table SELECT nor domain USAGE (even grantable) grants ownership.
	in := f.input(t, f.state, f.f.reader)
	if rows := iccUnitRows(t, in, empty); len(rows) != 0 {
		t.Fatal("reader saw unrelated definitions", rows)
	}
	in = f.input(t, f.state, f.f.owner)
	retained := iccUnitRows(t, in, want)
	next := catalog.CloneState(f.state)
	for _, descriptor := range f.f.e.Catalog.Read(next).Descriptors() {
		switch d := descriptor.(type) {
		case *catalog.Relation:
			if d.Namespace() == f.f.schema {
				copy := *d
				copy.Owner = f.f.reader
				next.SetDescriptor(copy.Address(), &copy)
			}
		case *catalog.Type:
			if d.Namespace() == f.f.schema {
				copy := *d
				copy.Owner = f.f.reader
				next.SetDescriptor(copy.Address(), &copy)
			}
		}
	}
	f.f.e.Catalog.PublishLocked(next)
	iccUnitRows(t, f.input(t, next, f.f.owner), empty)
	iccUnitRows(t, f.input(t, next, f.f.reader), want)
	if got := iccUnitBag(t, in, retained); !reflect.DeepEqual(got, want) {
		t.Fatal("retained rows changed", got)
	}
	cleared := catalog.CloneState(f.state)
	for _, descriptor := range f.f.e.Catalog.Read(f.state).Descriptors() {
		if descriptor.Namespace() == f.f.schema {
			cleared.RemoveDescriptor(descriptor.Address())
		}
	}
	iccUnitRows(t, f.input(t, cleared, f.f.owner), empty)
}

func TestInfoCheckConstraintsCapture(t *testing.T) {
	t.Run("paired_statement_frontier", iccUnitPairedCapture)
	f := newICCUnitFixture(t)
	produce := iccUnitProducer(t)
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
			t.Fatal("invalid authority accepted", rows, err)
		}
	}
	for _, malformed := range []string{"width", "domain", "domain-generation", "base-generation", "character-domain", "character-base", "namespace", "attribute", "owner"} {
		t.Run(malformed, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			view := f.f.e.Catalog.Read(state)
			switch malformed {
			case "width", "domain", "domain-generation", "character-domain":
				source := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoCheckConstraintsOID})
				source.Columns = append([]catalog.Column(nil), source.Columns...)
				switch malformed {
				case "width":
					source.Columns = source.Columns[:3]
				case "domain":
					base, _ := view.LookupType(sqltype.Name)
					source.Columns[0].Usage = base.Usage()
				case "domain-generation":
					source.Columns[0].Usage.Type, _ = engine.New().CatalogRead().LookupType(13248)
				case "character-domain":
					source.Columns[3].Usage.Type, _ = engine.New().CatalogRead().LookupType(13246)
				}
				state.SetDescriptor(source.Address(), &source)
			case "base-generation", "character-base":
				base := sqltype.Name
				if malformed == "character-base" {
					base = sqltype.VarChar
				}
				d := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base)})
				d.SQLType, _ = engine.New().CatalogRead().LookupType(base)
				state.SetDescriptor(d.Address(), &d)
			case "namespace", "attribute", "owner":
				d := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 24200})
				if malformed == "namespace" {
					d.ObjectNamespace.ObjectID = 999999
				}
				if malformed == "attribute" {
					d.Attributes = []int32{4}
				}
				if malformed == "owner" {
					d.Owner = authorization.PrincipalRef{}
				}
				state.SetDescriptor(d.Address(), &d)
			}
			if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
				t.Fatal("malformed metadata accepted", rows, err)
			}
		})
	}
	in := f.input(t, f.state, f.f.owner)
	runtime := in.Runtime.(core.EngineCatalogRuntime)
	expression := f.domain.DomainChecks[0].Expression
	if text, err := runtime.DomainExpression(t.Context(), in.Catalog, f.domain, expression); err != nil || text != "true" {
		t.Fatal(text, err)
	}
	copy := *f.domain
	foreign := *f.domain
	foreign.ObjectRevision++
	for _, domain := range []*catalog.Type{nil, &copy, &foreign, in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23})} {
		if _, err := runtime.DomainExpression(t.Context(), in.Catalog, domain, expression); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("invalid domain owner", err)
		}
	}
	if _, err := runtime.DomainExpression(t.Context(), engine.New().CatalogRead(), f.domain, expression); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("foreign capture", err)
	}
	if _, err := runtime.ColumnExpression(t.Context(), in.Catalog, nil, expression); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("nil relation", err)
	}
	table := *in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 24200})
	if _, err := runtime.ColumnExpression(t.Context(), in.Catalog, &table, expression); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("copied relation", err)
	}
	replaced := catalog.CloneState(f.state)
	replaced.SetDescriptor(copy.Address(), &copy)
	fresh := f.input(t, replaced, f.f.owner).Runtime.(core.EngineCatalogRuntime)
	if _, err := fresh.DomainExpression(t.Context(), fresh.Auth.Catalog, f.domain, expression); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("stale domain owner", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runtime.DomainExpression(canceled, in.Catalog, f.domain, expression); !errors.Is(err, context.Canceled) {
		t.Fatal("domain cancellation", err)
	}
}

func TestInfoCheckConstraintsCancellation(t *testing.T) {
	f := newICCUnitFixture(t)
	produce := iccUnitProducer(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	const checkpoints = 1 << 30
	count := &dtpCancelContext{Context: context.Background(), remaining: checkpoints}
	in := f.input(t, f.state, f.f.owner)
	in.Context = count
	if rows, err := produce(in); err != nil || len(rows) != 6 {
		t.Fatal("baseline", rows, err)
	}
	late := &dtpCancelContext{Context: context.Background(), remaining: checkpoints - count.remaining}
	for _, ctx := range []context.Context{ctx, &dtpCancelContext{Context: context.Background(), remaining: 1}, late} {
		in.Context = ctx
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancel exposed prefix", rows, err)
		}
	}
	failure := errors.New("renderer failure")
	for _, domain := range []bool{false, true} {
		in = f.input(t, f.state, f.f.owner)
		in.Runtime = iccFailRuntime{EngineCatalogRuntime: in.Runtime.(core.EngineCatalogRuntime), failure: failure, domain: domain}
		if rows, err := produce(in); rows != nil || err != failure {
			t.Fatal("renderer failure exposed prefix", rows, err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		in = f.input(t, f.state, f.f.owner)
		in.Context = ctx
		in.Runtime = iccCancelRuntime{EngineCatalogRuntime: in.Runtime.(core.EngineCatalogRuntime), cancel: cancel, calls: &calls, domain: domain}
		rows, err := produce(in)
		cancel()
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("renderer cancellation exposed prefix", rows, err)
		}
	}
}

type iccFailRuntime struct {
	core.EngineCatalogRuntime
	failure error
	domain  bool
}

func (r iccFailRuntime) ColumnExpression(ctx context.Context, capture catalog.Read, relation *catalog.Relation, expression expr.Expr) (string, error) {
	if !r.domain {
		return "", r.failure
	}
	return r.EngineCatalogRuntime.ColumnExpression(ctx, capture, relation, expression)
}

func (r iccFailRuntime) DomainExpression(ctx context.Context, capture catalog.Read, domain *catalog.Type, expression expr.Expr) (string, error) {
	if r.domain {
		return "", r.failure
	}
	return r.EngineCatalogRuntime.DomainExpression(ctx, capture, domain, expression)
}

type iccCancelRuntime struct {
	core.EngineCatalogRuntime
	cancel context.CancelFunc
	calls  *int
	domain bool
}

func (r iccCancelRuntime) ColumnExpression(ctx context.Context, capture catalog.Read, relation *catalog.Relation, expression expr.Expr) (string, error) {
	if !r.domain {
		*r.calls++
		if *r.calls == 2 {
			r.cancel()
		}
	}
	return r.EngineCatalogRuntime.ColumnExpression(ctx, capture, relation, expression)
}

func (r iccCancelRuntime) DomainExpression(ctx context.Context, capture catalog.Read, domain *catalog.Type, expression expr.Expr) (string, error) {
	if r.domain {
		r.cancel()
	}
	return r.EngineCatalogRuntime.DomainExpression(ctx, capture, domain, expression)
}

func iccUnitPairedCapture(t *testing.T) {
	f := newICCUnitFixture(t)
	initialView := f.f.e.Catalog.Read(f.state)
	boolean, _ := initialView.LookupType(sqltype.Bool)
	language, err := catalog.LookupLanguage(initialView, "sql")
	if err != nil {
		t.Fatal(err)
	}
	routine := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 24230}, ObjectNamespace: f.f.schema, ObjectName: "before", ObjectRevision: 1, Owner: f.f.owner, RoutineKind: catalog.FunctionRoutine, Result: ids.OID(sqltype.Bool), Language: language.Address(), Volatility: expr.Volatile, SQL: &proc.Definition{Result: boolean.Usage()}}
	f.state.SetDescriptor(routine.Address(), routine)
	initialDomain := *f.domain
	initialDomain.DomainChecks = []catalog.DomainCheckConstraint{{OID: 24221, Name: "domain_ck", Expression: plan.SQLRoutineExpr{Info: expr.Info{Usage: boolean.Usage(), RoutineReference: routine, Property: expr.Properties{Volatility: expr.Volatile}}, Procedure: routine, Ref: initialView.Owner().Domain.Ref(initialView.Epoch(), routine.Address()), Signature: proc.InvocationSignature{Result: boolean.Usage()}}}}
	f.state.SetDescriptor(initialDomain.Address(), &initialDomain)
	f.domain = &initialDomain
	f.f.e.Catalog.PublishLocked(f.state)
	f.f.op.Session.DatabaseName = "captured_database"
	f.f.actor(f.f.owner)
	scope, err := f.f.e.StartStatement(f.f.op)
	if err != nil {
		t.Fatal(err)
	}
	// The source carries statement presentation, unlike fresh default unit inputs.
	scope.Output.QuoteAllIdentifiers = true
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
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoCheckConstraintsOID})
	next := catalog.CloneState(f.state)
	domain := *f.domain
	domain.Owner = f.f.reader
	next.SetDescriptor(domain.Address(), &domain)
	renamed := *routine
	renamed.ObjectName = "after"
	next.SetDescriptor(renamed.Address(), &renamed)
	f.f.e.Catalog.PublishLocked(next)
	f.f.actor(f.f.reader)
	f.f.e.Mu.Lock()
	captured, err := core.EngineOpenRelationSourceLocked(f.f.e, scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := f.input(t, f.state, f.f.owner)
	if captured.View != nil || captured.Anonymous == nil {
		t.Fatal("not detached")
	}
	retained := captured.Anonymous
	captured.Close()
	oldWant := iccUnitExpected()
	delete(oldWant, [4]string{"captured_database", "default_schema", "domain_ck", "true"})
	oldWant[[4]string{"captured_database", "default_schema", "domain_ck", `"default_schema"."before"()`}] = 1
	if got := iccUnitBag(t, in, retained); !reflect.DeepEqual(got, oldWant) {
		t.Fatal("paired frontier changed", got)
	}
	want := iccUnitExpected()
	delete(want, [4]string{"captured_database", "default_schema", "domain_ck", "true"})
	delete(want, [4]string{"captured_database", "default_schema", "domain_nn", "VALUE IS NOT NULL"})
	iccUnitRows(t, f.input(t, next, f.f.owner), want)
	freshWant := map[[4]string]int{
		{"captured_database", "default_schema", "domain_ck", "default_schema.after()"}: 1,
		{"captured_database", "default_schema", "domain_nn", "VALUE IS NOT NULL"}:      1,
	}
	iccUnitRows(t, f.input(t, next, f.f.reader), freshWant)
}
