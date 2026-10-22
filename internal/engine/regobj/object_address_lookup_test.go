package regobj

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// TestAddressLookupNamespaceAndNoticeCuts pins the two root capabilities an
// object address lookup reads through: qualified namespaces go to Namespace
// only after the schema exists, and parser notices go to Notice.
func TestAddressLookupNamespaceAndNoticeCuts(t *testing.T) {
	var text *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
		}
	}
	textArray, err := sqltype.Array(text.AssociatedArray(), "_text", text)
	if err != nil {
		t.Fatal(err)
	}
	values := func(kind string, names, args []string) [3]sqltype.Value {
		n, err := ObjectAddressTextArray(textArray, names)
		if err != nil {
			t.Fatal(err)
		}
		a, err := ObjectAddressTextArray(textArray, args)
		if err != nil {
			t.Fatal(err)
		}
		return [3]sqltype.Value{sqltype.String(text, kind), n, a}
	}
	schema := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20001}, ObjectName: "s"}
	state := catalog.NewState(0)
	state.SetDescriptor(schema.Address(), schema)
	state.BindName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s"}, schema.Address())
	view := catalog.NewRead(nil, state)

	sentinel := errors.New("namespace cut")
	var reached []parser.ObjectName
	type notice struct {
		level session.NoticeLevel
		err   sqlstate.Error
	}
	var notices []notice
	lookup := AddressLookup{
		Catalog: view,
		Capture: InputCapture{Authority: Authority{View: access.View{Catalog: view}}},
		Namespace: func(name parser.ObjectName) (analyze.NamespaceSelection, error) {
			reached = append(reached, name)
			return analyze.NamespaceSelection{}, sentinel
		},
		Notice: func(level session.NoticeLevel, err sqlstate.Error) {
			notices = append(notices, notice{level, err})
		},
	}
	ctx := context.Background()

	_, err = lookup.Lookup(ctx, values("collation", []string{"s", "c"}, nil))
	if !errors.Is(err, sentinel) {
		t.Fatalf("qualified lookup error = %v, want the Namespace result", err)
	}
	if len(reached) != 1 || len(reached[0].Parts) != 2 || reached[0].Parts[0].Text != "s" || reached[0].Parts[1].Text != "c" {
		t.Fatalf("Namespace calls = %+v, want one call with s.c", reached)
	}

	reached = nil
	_, err = lookup.Lookup(ctx, values("collation", []string{"nosuch", "c"}, nil))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "3F000" || diagnostic.Message != `schema "nosuch" does not exist` {
		t.Fatalf("missing schema error = %v, want 3F000", err)
	}
	if len(reached) != 0 {
		t.Fatalf("Namespace reached for a missing schema: %+v", reached)
	}

	long := strings.Repeat("x", 70)
	_, _ = lookup.Lookup(ctx, values("function", []string{"f"}, []string{long}))
	if len(notices) != 1 || notices[0].level != session.LevelNotice || notices[0].err.Code != "42622" {
		t.Fatalf("notices = %+v, want one 42622 NOTICE", notices)
	}
}
