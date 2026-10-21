package engine

import (
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPgClassOptionHistory(t *testing.T) {
	option := func(name, value string) parser.ViewOption {
		return parser.ViewOption{Name: parser.Identifier{Text: name}, Value: value, HasValue: true}
	}
	created := []parser.ViewOption{option("security_invoker", "false"), option("security_barrier", "true")}
	barrier, invoker, mode, err := stored.ViewOptions(created)
	if err != nil || !barrier || invoker || mode != stored.CheckNone {
		t.Fatal("execution flags", barrier, invoker, mode, err)
	}
	old := transformViewOptions(nil, created, false)
	if !reflect.DeepEqual(old, []string{"security_invoker=false", "security_barrier=true"}) {
		t.Fatal(old)
	}
	set := transformViewOptions(old, []parser.ViewOption{option("security_invoker", "off")}, false)
	if !reflect.DeepEqual(set, []string{"security_barrier=true", "security_invoker=off"}) || !reflect.DeepEqual(old, []string{"security_invoker=false", "security_barrier=true"}) {
		t.Fatal("SET ordering/immutability", set, old)
	}
	reset := transformViewOptions(set, []parser.ViewOption{{Name: parser.Identifier{Text: "security_barrier"}}}, true)
	if !reflect.DeepEqual(reset, []string{"security_invoker=off"}) {
		t.Fatal(reset)
	}
	if got := transformViewOptions(reset, []parser.ViewOption{{Name: parser.Identifier{Text: "security_invoker"}}}, true); len(got) != 0 {
		t.Fatal("RESET final option", got)
	}
	if got := transformViewOptions(nil, nil, false); len(got) != 0 {
		t.Fatal("replacement absent options", got)
	}
	lexical := []parser.ViewOption{option("security_invoker", "0"), option("security_barrier", "on"), option("check_option", "local")}
	if got := transformViewOptions(nil, lexical, false); !reflect.DeepEqual(got, []string{"security_invoker=0", "security_barrier=on", "check_option=local"}) {
		t.Fatal("lexical facts", got)
	}
	e := New()
	original := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.SingletonNode{}}, Options: old, Barrier: true, Invoker: false, CheckOption: stored.CheckNone, Rule: 200001})
	mapped, err := stored.RemapView(e.CatalogRead(), original, nil, func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch++; return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil)
	if err != nil || mapped == original || mapped.RuleOID() != original.RuleOID() || mapped.Barrier() != original.Barrier() || mapped.Invoker() != original.Invoker() || !reflect.DeepEqual(mapped.Options(), old) {
		t.Fatal("remap lost installed options", mapped, err)
	}
	mapped = mapped.With(func(p *stored.ViewParts) {
		p.Options = transformViewOptions(p.Options, []parser.ViewOption{option("security_invoker", "true")}, false)
	})
	if !reflect.DeepEqual(original.Options(), old) || !reflect.DeepEqual(mapped.Options(), []string{"security_barrier=true", "security_invoker=true"}) {
		t.Fatal("remapped mutation touched retained facts")
	}
}
