package regobj

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func builtinAddressReader(q AddressLookup, state *catalog.State) AddressLookup {
	q.Catalog = catalog.NewRead(q.Capture.Authority.View.Owner, state)
	q.Capture.Authority.View.Recapture(q.Catalog)
	return q
}

func TestAddressLookupDynamicCast(t *testing.T) {
	state, base, values := addressRoutineFixture(t)
	source, err := sqltype.Enum(90001, "address_cast_source", "pg_catalog.address_cast_source", []string{"red"})
	if err != nil {
		t.Fatal(err)
	}
	typ := &catalog.Type{SQLType: source, ObjectNamespace: base.Capture.Path[0]}
	state.SetDescriptor(typ.Address(), typ)
	state.BindName(catalog.DescriptorNameKey(typ), typ.Address())
	cast := &catalog.Cast{OID: 90002, Source: source.ID(), Target: sqltype.Int4, ObjectRevision: 1}
	state.SetDescriptor(cast.Address(), cast)
	state.BindName(catalog.DescriptorNameKey(cast), cast.Address())
	retained := builtinAddressReader(base, state)
	input := values("cast", []string{"pg_catalog.address_cast_source"}, []string{"integer"})
	check := func(q AddressLookup, want ids.ObjectAddress) {
		t.Helper()
		got, err := q.Lookup(context.Background(), input)
		if err != nil || got != want {
			t.Fatalf("cast inverse = %v, %v; want %v", got, err, want)
		}
	}
	check(retained, ids.ObjectAddress{ClassID: 2605, ObjectID: 90002})
	absent := catalog.CloneState(state)
	absent.RemoveDescriptor(cast.Address())
	_, err = builtinAddressReader(base, absent).Lookup(context.Background(), input)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" || diagnostic.Message != "cast from type address_cast_source to type integer does not exist" {
		t.Fatalf("absent cast = %v", err)
	}
	replacement := *cast
	replacement.OID = 90003
	current := catalog.CloneState(absent)
	current.SetDescriptor(replacement.Address(), &replacement)
	current.BindName(catalog.DescriptorNameKey(&replacement), replacement.Address())
	check(builtinAddressReader(base, current), ids.ObjectAddress{ClassID: 2605, ObjectID: 90003})
	check(retained, ids.ObjectAddress{ClassID: 2605, ObjectID: 90002})

	// The fixed released cast remains available without a dynamic descriptor.
	got, err := retained.Lookup(context.Background(), values("cast", []string{"integer"}, []string{"bigint"}))
	if err != nil || got != (ids.ObjectAddress{ClassID: 2605, ObjectID: 20006}) {
		t.Fatalf("released integer/bigint cast = %v, %v", got, err)
	}
	for _, tc := range []struct {
		source, target, code string
	}{
		{"NONE", "integer", "42601"},
		{"address_missing_type", "integer", "42704"},
		{"pg_catalog.address_cast_source", "address_missing_type", "42704"},
	} {
		_, err := retained.Lookup(context.Background(), values("cast", []string{tc.source}, []string{tc.target}))
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatalf("endpoint precedence %s/%s = %v; want %s", tc.source, tc.target, err, tc.code)
		}
	}
}

func TestAddressLookupTextSearchConfiguration(t *testing.T) {
	state, base, values := addressRoutineFixture(t)
	cfg := &catalog.TSConfiguration{
		ObjectAddress:   ids.ObjectAddress{ClassID: 3602, ObjectID: 90011},
		ObjectNamespace: base.Capture.Path[0], ObjectName: "address_configuration", ObjectRevision: 1,
	}
	state.SetDescriptor(cfg.Address(), cfg)
	state.BindName(catalog.DescriptorNameKey(cfg), cfg.Address())
	retained := builtinAddressReader(base, state)
	check := func(q AddressLookup, want ids.ObjectAddress) {
		t.Helper()
		for _, names := range [][]string{{"address_configuration"}, {"pg_catalog", "address_configuration"}} {
			got, err := q.Lookup(context.Background(), values("text search configuration", names, nil))
			if err != nil || got != want {
				t.Fatalf("configuration inverse %q = %v, %v; want %v", names, got, err, want)
			}
		}
	}
	check(retained, ids.ObjectAddress{ClassID: 3602, ObjectID: 90011})
	absent := catalog.CloneState(state)
	absent.RemoveDescriptor(cfg.Address())
	_, err := builtinAddressReader(base, absent).Lookup(context.Background(), values("text search configuration", []string{"pg_catalog", "address_configuration"}, nil))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" || diagnostic.Message != `text search configuration "pg_catalog.address_configuration" does not exist` {
		t.Fatalf("absent configuration = %v", err)
	}
	replacement := *cfg
	replacement.ObjectAddress.ObjectID = 90012
	current := catalog.CloneState(absent)
	current.SetDescriptor(replacement.Address(), &replacement)
	current.BindName(catalog.DescriptorNameKey(&replacement), replacement.Address())
	check(builtinAddressReader(base, current), ids.ObjectAddress{ClassID: 3602, ObjectID: 90012})
	check(retained, ids.ObjectAddress{ClassID: 3602, ObjectID: 90011})
}
