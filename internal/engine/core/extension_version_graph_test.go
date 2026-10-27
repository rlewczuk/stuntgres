package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
)

// Registration-only metadata fixtures never install a synthetic provider or
// stand in for the genuine fuzzy update used by publication/manifest tests.
type extensionVersionRegistration struct {
	*extension.FuzzystrmatchProvider
	inventory      catalog.ExtensionVersions
	defaultVersion string
	actions        []extension.UpdateAction
}

func (p *extensionVersionRegistration) Versions() catalog.ExtensionVersions {
	return p.inventory.Detached()
}
func (p *extensionVersionRegistration) Control() catalog.ExtensionControl {
	c := p.FuzzystrmatchProvider.Control()
	c.Version = p.defaultVersion
	return c
}
func (p *extensionVersionRegistration) Updates() []extension.UpdateAction { return p.actions }

func TestExtensionVersionGraph(t *testing.T) {
	v := extension.StockFuzzystrmatch.Versions()
	if err := v.Validate("1.2"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		from, to string
		path     []string
	}{
		{"1.0", "1.2", []string{"1.0", "1.1", "1.2"}}, {"1.1", "1.2", []string{"1.1", "1.2"}},
		{"1.2", "1.1", nil}, {"1.1", "1.0", nil}, {"absent", "1.2", nil}, {"1.1", "1.1", []string{"1.1"}},
	} {
		if got := v.Path(tc.from, tc.to); !reflect.DeepEqual(got, tc.path) {
			t.Fatal(tc.from, tc.to, got, tc.path)
		}
	}
	if v.InstallPath("1.0") != nil || !reflect.DeepEqual(v.InstallPath("1.2"), []string{"1.1", "1.2"}) || !reflect.DeepEqual(v.Installable(), []string{"1.1", "1.2"}) {
		t.Fatal("graph-only/base reachability", v)
	}
	cycle := catalog.ExtensionVersions{Bases: []string{"a"}, Edges: []catalog.ExtensionVersionEdge{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "a"}, {From: "a", To: "c"}, {From: "c", To: "d"}}}
	if err := cycle.Validate("d"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cycle.Path("a", "d"), []string{"a", "c", "d"}) || cycle.Path("a", "missing") != nil {
		t.Fatal("shortest cyclic directed path")
	}
	for _, provider := range []extension.Installer{extension.StockTablefunc, extension.StockAmcheck, extension.StockCitext, extension.StockCube, extension.StockEarthdistance, extension.StockLo, extension.StockUnaccent, extension.StockSeg, extension.StockIntarray, extension.StockLtree, extension.StockIntagg, extension.StockPgcrypto, extension.StockPgrowlocks, extension.StockUUIDOssp} {
		versions := provider.Versions()
		if len(versions.Bases) != 1 || versions.Bases[0] != provider.Control().Version || len(versions.Edges) != 0 || extension.ValidateActions(provider) != nil {
			t.Fatal("current-only inventory", versions)
		}
		versions.Bases[0] = "mutated"
		if provider.Versions().Bases[0] != provider.Control().Version {
			t.Fatal("shared inventory storage")
		}
	}
	v.Bases[0], v.Edges[0].From = "mutated", "mutated"
	if extension.StockFuzzystrmatch.Versions().Bases[0] != "1.1" || extension.StockFuzzystrmatch.Versions().Edges[0].From != "1.0" {
		t.Fatal("shared fuzzy graph storage")
	}
	for _, invalid := range []catalog.ExtensionVersions{
		{}, {Bases: []string{""}}, {Bases: []string{"a", "a"}}, {Bases: []string{"a--b"}},
		{Bases: []string{"a"}, Edges: []catalog.ExtensionVersionEdge{{From: "a", To: "a"}}},
		{Bases: []string{"a"}, Edges: []catalog.ExtensionVersionEdge{{From: "a", To: "b"}, {From: "a", To: "b"}}},
		{Bases: []string{"a"}, Edges: []catalog.ExtensionVersionEdge{{From: "a", To: "-b"}}},
		{Bases: []string{"a"}, Edges: []catalog.ExtensionVersionEdge{{From: "a", To: "b/c"}}},
	} {
		if invalid.Validate("a") == nil {
			t.Fatal("invalid graph admitted", invalid)
		}
	}
	if (catalog.ExtensionVersions{Bases: []string{"a"}}).Validate("b") == nil {
		t.Fatal("unreachable default admitted")
	}
	for _, tc := range []struct{ value, detail string }{
		{"", "Version names must not be empty."}, {"bad--/", `Version names must not contain "--".`},
		{"-bad/name", `Version names must not begin or end with "-".`}, {"bad/name", "Version names must not contain directory separator characters."},
	} {
		if got := catalog.ExtensionNameDetail(tc.value, true); got != tc.detail {
			t.Fatal(tc.value, got)
		}
	}
	good := extension.StockFuzzystrmatch.Updates()[0]
	for _, actions := range [][]extension.UpdateAction{nil, {good, good}, {{Edge: good.Edge}}, {{Edge: catalog.ExtensionVersionEdge{From: "1.2", To: "1.1"}, Stage: good.Stage}}} {
		p := &extensionVersionRegistration{extension.StockFuzzystrmatch, extension.StockFuzzystrmatch.Versions(), "1.2", actions}
		if core.EngineRegisterExtension((&core.Engine{}), "fuzzystrmatch", p) == nil {
			t.Fatal("invalid executable coverage admitted", actions)
		}
	}
	p := &extensionVersionRegistration{extension.StockFuzzystrmatch, extension.StockFuzzystrmatch.Versions(), "1.0", []extension.UpdateAction{good}}
	if core.EngineRegisterExtension((&core.Engine{}), "fuzzystrmatch", p) == nil {
		t.Fatal("graph-only default admitted")
	}
	if err := core.EngineRegisterExtension((&core.Engine{}), "fuzzystrmatch", extension.StockFuzzystrmatch); err != nil {
		t.Fatal("unreachable metadata edge incorrectly requires action", err)
	}
}
