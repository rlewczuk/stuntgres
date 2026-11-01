package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func TestInfoElementTypesCapture(t *testing.T) {
	f := newElementUnitFixture(t)
	old := elementUnitInput(t, f.f, f.state, f.f.owner)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoElementTypesOID)
	foreign := engine.New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { in.Ref.Address.ObjectID++ },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			in.TypeNamePath = []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: 99999}}
		},
		func(in *pgcatalog.Input) { in.Source = nil },
		func(in *pgcatalog.Input) { in.Runtime = nil },
		func(in *pgcatalog.Input) { in.TypeNamePath = nil },
		func(in *pgcatalog.Input) {
			r := core.CatalogRuntimeFieldsOf(in.Runtime)
			r.Auth.RoleRecord = nil
			in.Runtime = core.NewCatalogRuntime(r)
		},
		func(in *pgcatalog.Input) {
			r := core.CatalogRuntimeFieldsOf(in.Runtime)
			r.Auth.State.CurrentUser = in.Owner.Principals.Principal(99999)
			in.Runtime = core.NewCatalogRuntime(r)
		},
	} {
		in := old
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("foreign capture accepted", rows, err)
		}
	}
	for _, kind := range []string{"width", "attributes", "name", "nullable", "unknown-nullability", "origin-table", "origin-attribute", "ordinal", "dimensions", "typmod", "collation", "attribute-nullability", "attribute-dimensions", "attribute-collation", "domain", "base", "namespace", "foreign-element", "array-link", "formal-usage", "formal-layout"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			source := *old.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			source.Attributes = append([]int32(nil), source.Attributes...)
			source.ColumnMeta.CatalogAttributes = append([]catalog.CatalogAttribute(nil), source.ColumnMeta.CatalogAttributes...)
			switch kind {
			case "width":
				source.Columns = source.Columns[:27]
			case "attributes":
				source.Attributes = source.Attributes[:27]
			case "name":
				source.Columns[0].Name = "invented"
			case "nullable":
				source.Columns[0].Nullable = false
			case "unknown-nullability":
				source.Columns[0].NullabilityKnown = false
			case "origin-table":
				source.Columns[0].OriginTable++
			case "origin-attribute":
				source.Columns[0].OriginAttribute++
			case "ordinal":
				source.Attributes[0] = 2
			case "dimensions":
				source.Columns[0].DeclaredDimensions = 1
			case "typmod":
				source.Columns[0].Usage.Typmod = 7
			case "collation":
				source.Columns[0].Usage.Collation.ID = 100
			case "attribute-nullability":
				source.ColumnMeta.CatalogAttributes[0].NotNull = true
			case "attribute-dimensions":
				source.ColumnMeta.CatalogAttributes[0].Dimensions = 1
			case "attribute-collation":
				source.ColumnMeta.CatalogAttributes[0].Collation = 100
			case "domain":
				source.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(13248)
			case "base":
				typ := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 19})
				typ.SQLType, _ = foreign.CatalogRead().LookupType(sqltype.Name)
				state.SetDescriptor(typ.Address(), &typ)
			case "namespace":
				relation := *f.table
				relation.ObjectNamespace.ObjectID = 99999
				state.SetDescriptor(relation.Address(), &relation)
			case "foreign-element":
				typ := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23})
				typ.SQLType, _ = foreign.CatalogRead().LookupType(sqltype.Int4)
				state.SetDescriptor(typ.Address(), &typ)
			case "array-link":
				typ := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23})
				typ.Array = 1009
				state.SetDescriptor(typ.Address(), &typ)
			case "formal-usage", "formal-layout":
				routine := *f.routine
				definition := *routine.SQL
				definition.Formals = append([]proc.Formal(nil), definition.Formals...)
				if kind == "formal-usage" {
					definition.Formals[0].Usage.Type, _ = foreign.CatalogRead().LookupType(1007)
				} else {
					definition.FormalInputs = append([]int(nil), definition.FormalInputs...)
					definition.FormalInputs[2] = 0
				}
				routine.SQL = &definition
				state.SetDescriptor(routine.Address(), &routine)
			}
			state.SetDescriptor(source.Address(), &source)
			if rows, err := produce(elementUnitInput(t, f.f, state, f.f.owner)); rows != nil || err == nil {
				t.Fatal("malformed capture accepted", kind, rows, err)
			}
		})
	}
}

func TestInfoElementTypesEmptyAndCancellation(t *testing.T) {
	f := newElementUnitFixture(t)
	state := catalog.CloneState(f.state)
	for address, descriptor := range f.f.e.Catalog.Read(f.state).Descriptors() {
		switch d := descriptor.(type) {
		case *catalog.Relation:
			copy := *d
			copy.Owner = f.f.owner
			copy.Security.ACL = authorization.ACLValue{Explicit: true}
			copy.Security.ColumnACL = nil
			state.SetDescriptor(address, &copy)
		case *catalog.Type:
			copy := *d
			copy.Owner, copy.ACL = f.f.owner, authorization.ACLValue{Explicit: true}
			state.SetDescriptor(address, &copy)
		case *catalog.Procedure:
			copy := *d
			copy.Owner, copy.ACL = f.f.owner, authorization.ACLValue{Explicit: true}
			state.SetDescriptor(address, &copy)
		}
	}
	in := elementUnitInput(t, f.f, state, f.f.reader)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoElementTypesOID)
	if rows, err := produce(in); rows == nil || len(rows) != 0 || err != nil {
		t.Fatal("owned empty result", rows, err)
	}
	bad := catalog.CloneState(state)
	source := *in.Source
	source.Columns = append([]catalog.Column(nil), source.Columns...)
	source.Columns[0].Name = "malformed_even_when_empty"
	bad.SetDescriptor(source.Address(), &source)
	if rows, err := produce(elementUnitInput(t, f.f, bad, f.f.reader)); rows != nil || err == nil {
		t.Fatal("empty capture skipped validation", rows, err)
	}
	for _, remaining := range []int{1, 6, 100} {
		in := elementUnitInput(t, f.f, f.state, f.f.owner)
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled capture published prefix", rows, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in.Context, in.Runtime = ctx, nil
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancellation displaced by validation", rows, err)
	}
}
