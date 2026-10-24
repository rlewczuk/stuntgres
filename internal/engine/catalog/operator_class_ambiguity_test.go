package catalog

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestOperatorClassDefaultAmbiguityDiagnostics(t *testing.T) {
	state := NewState(1)
	public := &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "public"}
	pg := &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectName: "pg_catalog"}
	enum, err := sqltype.Enum(96000, "ambiguity_enum", "public.ambiguity_enum", []string{"low", "high"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sqltype.Domain(96001, "ambiguity_domain", "public.ambiguity_domain", enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []Descriptor{public, pg, &Type{SQLType: enum, ObjectNamespace: public.Address()}, &Type{SQLType: domain, ObjectNamespace: public.Address()}} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(DescriptorNameKey(d), d.Address())
	}
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
			d := &Type{SQLType: typ, ObjectNamespace: pg.Address()}
			state.SetDescriptor(d.Address(), d)
			state.BindName(DescriptorNameKey(d), d.Address())
		}
	}
	if integer == nil {
		t.Fatal("missing builtin integer")
	}
	for i, input := range []ids.OID{ids.OID(enum.ID()), ids.OID(enum.ID()), ids.OID(sqltype.Int4), ids.OID(sqltype.Int4)} {
		d := &OperatorClass{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(97000 + i)}, Method: 98000, InputType: input, Default: true}
		state.SetDescriptor(d.Address(), d)
	}
	view := NewRead(nil, state)
	for _, typ := range []*sqltype.Descriptor{enum, domain, integer} {
		class, ok, original := view.DefaultIndexClass(98000, typ)
		var identity *DefaultClassAmbiguity
		var fallback *sqlstate.Error
		if ok || class.OID() != 0 || !errors.As(original, &identity) || identity.Input != typ.BaseType().ID() || !errors.As(original, &fallback) {
			t.Fatal("lost exact base identity", class, ok, original)
		}
		retained := *fallback
		secondary := errors.New("secondary classification")
		decorated := retained
		decorated.Position = "17"
		decorated.Context = "outer diagnostic context"
		wrapped := sqlstate.WithDiagnostic(errors.Join(original, secondary), &decorated)
		for _, visibility := range []struct {
			path []ids.ObjectAddress
			name string
		}{
			{[]ids.ObjectAddress{public.Address(), pg.Address()}, "ambiguity_enum"},
			{[]ids.ObjectAddress{pg.Address()}, "public.ambiguity_enum"},
			{[]ids.ObjectAddress{public.Address(), pg.Address()}, "ambiguity_enum"},
		} {
			name := visibility.name
			if typ == integer {
				name = "integer"
			}
			for _, source := range []error{original, wrapped} {
				rendered := RenderDefaultClassAmbiguity(source, TypeDiagnosticNamer(view, visibility.path))
				var got *sqlstate.Error
				var cause *DefaultClassAmbiguity
				want := retained
				if source == wrapped {
					want = decorated
					if !errors.Is(rendered, secondary) {
						t.Fatal("lost secondary error", rendered)
					}
				}
				want.Message = "there are multiple default operator classes for data type " + name
				if !errors.As(rendered, &got) || !reflect.DeepEqual(*got, want) || !errors.As(rendered, &cause) || cause != identity {
					t.Fatal("visibility/full diagnostic/cause", rendered, got, want)
				}
				// Presenting a retained error twice must preserve its decorated
				// fields, original identity and every absent optional field.
				again := RenderDefaultClassAmbiguity(rendered, TypeDiagnosticNamer(view, visibility.path))
				if !errors.As(again, &got) || !reflect.DeepEqual(*got, want) {
					t.Fatal("repeated presentation changed diagnostic", again)
				}
			}
			if !reflect.DeepEqual(*fallback, retained) || !reflect.DeepEqual(decorated, sqlstate.Error{Severity: retained.Severity, SeverityUnlocalized: retained.SeverityUnlocalized, Code: retained.Code, Message: retained.Message, Position: "17", Context: "outer diagnostic context"}) {
				t.Fatal("presentation mutated retained diagnostic", fallback, decorated)
			}
		}
	}
	ordinary := errors.New("ordinary")
	if RenderDefaultClassAmbiguity(ordinary, nil) != ordinary || RenderDefaultClassAmbiguity(nil, nil) != nil {
		t.Fatal("changed unrelated error")
	}
}
