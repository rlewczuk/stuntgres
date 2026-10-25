package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type dcUnitFixture struct {
	f                      defaultUnitFixture
	state                  *catalog.State
	a, b, nn, child, plain *catalog.Type
}

func newDCUnitFixture(t *testing.T) dcUnitFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	view := f.e.Catalog.Read(state)
	integer, _ := view.LookupType(sqltype.Int4)
	boolean, _ := view.LookupType(sqltype.Bool)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
	makeDomain := func(id sqltype.ID, name string, base sqltype.Usage, check, nn bool) *catalog.Type {
		typ, err := sqltype.Domain(id, name, "default_schema."+name, base, check, nn)
		if err != nil {
			t.Fatal(err)
		}
		d := &catalog.Type{SQLType: typ, ObjectNamespace: f.schema, ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}}
		if check {
			d.DomainChecks = []catalog.DomainCheckConstraint{{OID: ids.OID(id) + 100, Name: "same", Expression: literal}}
		}
		if nn {
			d.DomainNotNull = &catalog.DomainNotNullConstraint{OID: ids.OID(id) + 100, Name: "named_nn"}
		}
		state.SetDescriptor(d.Address(), d)
		return d
	}
	a := makeDomain(22000, "a", integer.Usage(), true, false)
	b := makeDomain(22001, "b", integer.Usage(), true, false)
	nn := makeDomain(22002, "nn", integer.Usage(), false, true)
	child := makeDomain(22003, "child", a.SQLType.Usage(), false, false)
	plain := makeDomain(22004, "plain", integer.Usage(), false, false)
	makeDomain(22005, "nn_child", nn.SQLType.Usage(), false, false)
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22200}, ObjectNamespace: f.schema, ObjectName: "table_check", ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.TableRelation, Constraints: catalog.RelationConstraints{Check: []catalog.CheckConstraint{{OID: 22300, Name: "same", Expression: literal}}}}
	state.SetDescriptor(table.Address(), table)
	return dcUnitFixture{f: f, state: state, a: a, b: b, nn: nn, child: child, plain: plain}
}

func (f dcUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoDomainConstraintsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}

func dcUnitExpected(user ...[3]string) map[[8]string]int {
	rows := map[[8]string]int{}
	for _, row := range append([][3]string{{"information_schema", "cardinal_number_domain_check", "cardinal_number"}, {"information_schema", "yes_or_no_check", "yes_or_no"}}, user...) {
		rows[[8]string{"captured_database", row[0], row[1], "captured_database", row[0], row[2], "NO", "NO"}]++
	}
	return rows
}

func dcUnitBag(t *testing.T, rows [][]sqltype.Value) map[[8]string]int {
	t.Helper()
	bag := map[[8]string]int{}
	for _, row := range rows {
		if len(row) != 8 {
			t.Fatal("projection width", len(row))
		}
		var key [8]string
		for i, value := range row {
			base, err := value.BaseValue()
			if err != nil || value.IsNull() {
				t.Fatal("invalid domain cell", i, value, err)
			}
			key[i], err = base.Text()
			if err != nil {
				t.Fatal(err)
			}
		}
		bag[key]++
	}
	return bag
}

func dcUnitCheck(t *testing.T, in pgcatalog.Input, want map[[8]string]int) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoDomainConstraintsOID)
	if !ok {
		t.Fatal("missing domain_constraints producer")
	}
	captured, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
		t.Fatal("capture identity or empty-source contract", captured)
	}
	for _, row := range captured.Anonymous {
		for i, value := range row {
			column := in.Source.Columns[i]
			id := sqltype.ID(13248)
			if i >= 6 {
				id = 13256
			}
			owned, _ := in.Catalog.LookupType(id)
			physical, _ := in.Catalog.LookupType(owned.BaseType().ID())
			if value.Type() != owned || owned.BaseType() != physical || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoDomainConstraintsOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
				t.Fatal("domain generation, schema or origin", i, value, column)
			}
		}
	}
	if got := dcUnitBag(t, captured.Anonymous); !reflect.DeepEqual(got, want) {
		t.Fatal("complete constraint bag", got, want)
	}
	return captured.Anonymous
}

func TestInfoDomainConstraintsProducer(t *testing.T) {
	f := newDCUnitFixture(t)
	all := dcUnitExpected([3]string{"default_schema", "same", "a"}, [3]string{"default_schema", "same", "b"}, [3]string{"default_schema", "named_nn", "nn"})
	in := f.input(t, f.state, f.f.owner)
	for _, actor := range []authorization.PrincipalRef{f.f.owner, f.f.member, f.f.op.Session.Authorization.State.CurrentUser} {
		dcUnitCheck(t, f.input(t, f.state, actor), all)
	}
	source := in.Source
	if source.RowType != 13334 {
		t.Fatal("canonical row identity", source.RowType)
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13334})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13333})
	if row.OwningRelation != source.Address() || row.Array != 13333 || array.SQLType.Element() != row.SQLType || array.OwningRelation != source.Address() {
		t.Fatal("row/array backlinks", row, array)
	}
	pending := catalog.CloneState(f.state)
	a := *f.a
	a.DomainChecks = append([]catalog.DomainCheckConstraint(nil), a.DomainChecks...)
	a.DomainChecks[0].NotValid = true
	pending.SetDescriptor(a.Address(), &a)
	dcUnitCheck(t, f.input(t, pending, f.f.owner), all)
	// No seeded-name exemption: the genuine bootstrap owner/ACL authority
	// participates in the same visibility test as user definitions.
	hidden := catalog.CloneState(f.state)
	for _, descriptor := range in.Catalog.Descriptors() {
		if d, ok := descriptor.(*catalog.Type); ok && d.SQLType.IsDomain() {
			copy := *d
			copy.Owner, copy.ACL = f.f.owner, authorization.ACLValue{Explicit: true}
			hidden.SetDescriptor(copy.Address(), &copy)
		}
	}
	dcUnitCheck(t, f.input(t, hidden, f.f.reader), map[[8]string]int{})
	empty := catalog.CloneState(f.state)
	for _, descriptor := range in.Catalog.Descriptors() {
		if d, ok := descriptor.(*catalog.Type); ok && d.SQLType.IsDomain() {
			copy := *d
			copy.DomainChecks, copy.DomainNotNull = nil, nil
			empty.SetDescriptor(copy.Address(), &copy)
		}
	}
	dcUnitCheck(t, f.input(t, empty, f.f.owner), map[[8]string]int{})
}

func TestInfoDomainConstraintsCapture(t *testing.T) {
	f := newDCUnitFixture(t)
	old := f.input(t, f.state, f.f.owner)
	all := dcUnitExpected([3]string{"default_schema", "same", "a"}, [3]string{"default_schema", "same", "b"}, [3]string{"default_schema", "named_nn", "nn"})
	retained := dcUnitCheck(t, old, all)
	changed := catalog.CloneState(f.state)
	a := *f.a
	var err error
	a.SQLType, err = a.SQLType.RebindDomain(a.SQLType.DomainBase(), "renamed", "default_schema.renamed")
	if err != nil {
		t.Fatal(err)
	}
	a.Owner = f.f.reader
	changed.SetDescriptor(a.Address(), &a)
	changed.RemoveDescriptor(f.b.Address())
	dcUnitCheck(t, f.input(t, changed, f.f.owner), dcUnitExpected([3]string{"default_schema", "named_nn", "nn"}))
	dcUnitCheck(t, f.input(t, changed, f.f.reader), dcUnitExpected([3]string{"default_schema", "same", "renamed"}))
	allowed := a
	allowed.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.f.reader, Grantee: authorization.ACLGrantee{Role: f.f.owner}, Privileges: authorization.UsagePrivilege}}}
	granted := catalog.CloneState(changed)
	granted.SetDescriptor(allowed.Address(), &allowed)
	dcUnitCheck(t, f.input(t, granted, f.f.owner), dcUnitExpected([3]string{"default_schema", "same", "renamed"}, [3]string{"default_schema", "named_nn", "nn"}))
	dcUnitCheck(t, f.input(t, changed, f.f.owner), dcUnitExpected([3]string{"default_schema", "named_nn", "nn"}))
	dcUnitCheck(t, old, all)
	if !reflect.DeepEqual(dcUnitBag(t, retained), all) {
		t.Fatal("retained rows changed")
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoDomainConstraintsOID)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = New().CatalogRead() },
		func(in *pgcatalog.Input) {
			in.Roles = New().RoleStore.Published.Root.Read(authorization.PrincipalRef{})
		},
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Epoch = 0 },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			r := in.Runtime.(core.EngineCatalogRuntime)
			r.Auth.RoleRecord = nil
			in.Runtime = r
		},
		func(in *pgcatalog.Input) {
			r := in.Runtime.(core.EngineCatalogRuntime)
			r.Auth.State.CurrentUser = authorization.PrincipalRef{}
			in.Runtime = r
		},
	} {
		in := old
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, kind := range []string{"width", "domain", "generation", "base-generation", "typmod", "collation", "namespace", "owner", "definition"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			if kind == "namespace" || kind == "owner" || kind == "definition" {
				d := *f.a
				switch kind {
				case "namespace":
					d.ObjectNamespace.ObjectID = 999999
				case "owner":
					d.Owner = authorization.PrincipalRef{}
				case "definition":
					d.DomainChecks = []catalog.DomainCheckConstraint{{Name: "invalid"}}
				}
				state.SetDescriptor(d.Address(), &d)
			} else if kind == "base-generation" {
				d := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				d.SQLType, _ = New().CatalogRead().LookupType(sqltype.Name)
				state.SetDescriptor(d.Address(), &d)
			} else {
				d := *old.Source
				d.Columns = append([]catalog.Column(nil), d.Columns...)
				switch kind {
				case "width":
					d.Columns = d.Columns[:7]
				case "domain":
					d.Columns[0].Usage.Type, _ = old.Catalog.LookupType(sqltype.Name)
				case "generation":
					typ := d.Columns[0].Usage.Type
					d.Columns[0].Usage.Type, err = typ.RebindDomain(typ.DomainBase(), typ.Name(), typ.SQLName())
					if err != nil {
						t.Fatal(err)
					}
				case "typmod":
					d.Columns[0].Usage.Typmod = 7
				case "collation":
					d.Columns[0].Usage.Collation.ID = 0
				}
				state.SetDescriptor(d.Address(), &d)
			}
			if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
				t.Fatal("invalid source accepted", rows, err)
			}
		})
	}
	infoDomainConstraintsPairedCapture(t)
}

func TestInfoDomainConstraintsCancellation(t *testing.T) {
	f := newDCUnitFixture(t)
	in := f.input(t, f.state, f.f.owner)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoDomainConstraintsOID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in.Context = ctx
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("precanceled capture exposed rows", rows, err)
	}
	// Keep exactly one domain's three genuine definitions. Locate its actual
	// iteration position, then cancel on the second CHECK, after one emission.
	state := catalog.CloneState(f.state)
	for _, descriptor := range in.Catalog.Descriptors() {
		if d, ok := descriptor.(*catalog.Type); ok && d.SQLType.IsDomain() {
			copy := *d
			copy.DomainChecks, copy.DomainNotNull = nil, nil
			if d.Address() == f.a.Address() {
				check := f.a.DomainChecks[0]
				copy.DomainChecks = []catalog.DomainCheckConstraint{check, {OID: check.OID + 10, Name: "second", Expression: check.Expression}, {OID: check.OID + 11, Name: "third", Expression: check.Expression}}
			}
			state.SetDescriptor(copy.Address(), &copy)
		}
	}
	in = f.input(t, state, f.f.owner)
	position := 0
	for _, d := range in.Catalog.Descriptors() {
		position++
		if d.Address() == f.a.Address() {
			break
		}
	}
	for _, remaining := range []int{2, position + 3} {
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled capture exposed prefix", remaining, rows, err)
		}
	}
}

func infoDomainConstraintsPairedCapture(t *testing.T) {
	t.Helper()
	f := newDCUnitFixture(t)
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
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoDomainConstraintsOID})
	next := catalog.CloneState(f.state)
	a := *f.a
	a.DomainChecks = nil
	next.SetDescriptor(a.Address(), &a)
	f.f.e.Catalog.PublishLocked(next)
	f.f.e.Mu.Lock()
	captured, err := core.EngineOpenRelationSourceLocked(f.f.e, scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	all := dcUnitExpected([3]string{"default_schema", "same", "a"}, [3]string{"default_schema", "same", "b"}, [3]string{"default_schema", "named_nn", "nn"})
	if !reflect.DeepEqual(dcUnitBag(t, captured.Anonymous), all) {
		t.Fatal("source resampled published state instead of paired capture")
	}
	dcUnitCheck(t, f.input(t, next, f.f.owner), dcUnitExpected([3]string{"default_schema", "same", "b"}, [3]string{"default_schema", "named_nn", "nn"}))
}
