package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

func installedCodecType(t *testing.T, e *core.Engine) *catalog.Type {
	t.Helper()
	for _, d := range e.CatalogRead().Descriptors() {
		if d, ok := d.(*catalog.Type); ok && d.SQLType.IsLtree() {
			return d
		}
	}
	t.Fatal("missing genuine installed scalar")
	return nil
}

func TestTypeProcedureDependenciesInstalledGraph(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "detached", true: "foreign"}[foreign], func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE EXTENSION ltree")
			d := installedCodecType(t, e)
			initial := e.CatalogRead()
			facts, err := pgcatalog.TypeFacts(t.Context(), initial, d, sqltype.OutputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			input := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(facts.Input)}
			output := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(facts.Output)}
			want := []ids.ObjectAddress{input, output}
			check := func() {
				t.Helper()
				read := e.CatalogRead()
				current := read.MustType(d.Address())
				var got []ids.ObjectAddress
				if err := pgcatalog.VisitTypeProcedureDependencies(t.Context(), read, current, func(a ids.ObjectAddress) bool { got = append(got, a); return true }); err != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("actual scalar codec graph", got, want, err)
				}
				edges, err := pgcatalog.CollectDepend(t.Context(), read, core.EngineCatalogRuntime{})
				if err != nil {
					t.Fatal(err)
				}
				counts := make(map[ids.ObjectAddress]int)
				array := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(current.Array)}
				parent := 0
				for _, edge := range edges {
					if edge.Dependent == current.Address() && edge.Referenced.ClassID == ids.ProcedureClassID {
						if edge.DepType != 'n' {
							t.Fatal("codec is not NORMAL", edge)
						}
						counts[edge.Referenced]++
					}
					if edge.Dependent == array {
						if edge.Referenced == current.Address() && edge.DepType == 'i' {
							parent++
						}
						if edge.Referenced == input || edge.Referenced == output {
							t.Fatal("invented element codec inheritance", edge)
						}
					}
				}
				if len(counts) != 2 || counts[input] != 1 || counts[output] != 1 || parent != 1 {
					t.Fatal("projected codec/internal edges", counts, parent)
				}
				if catalog.ExtensionMembership(read.MustProcedure(input)) != (ids.ObjectAddress{}) {
					return
				}
				stage := e.BeginCatalogStage(read)
				defer e.DiscardCatalogStage(stage)
				closure, err := view.DropClosure(core.StageDropProvider(t.Context(), stage, nil, nil, nil), []ids.ObjectAddress{input})
				if err != nil {
					t.Fatal(err)
				}
				found := false
				dependent := current.Address()
				if membership := catalog.ExtensionMembership(current); membership != (ids.ObjectAddress{}) {
					dependent = membership
				}
				for _, edge := range closure {
					if edge.Address == dependent {
						found = true
						if !edge.Normal || edge.Automatic || edge.Target != input {
							t.Fatal("retirement lost codec NORMAL edge", edge)
						}
					}
				}
				if !found || stage.RemovalCount() != 0 {
					t.Fatal("missing scalar/receiver retirement witness or speculative removal", found, stage.RemovalCount())
				}
			}
			check()
			for _, target := range []string{"TYPE public.ltree", "FUNCTION public." + initial.MustProcedure(input).Name() + "(cstring)", "FUNCTION public." + initial.MustProcedure(output).Name() + "(public.ltree)"} {
				enginetest.Run(t, e, "ALTER EXTENSION ltree DROP "+target)
			}
			check()
			if foreign {
				enginetest.Run(t, e, `CREATE EXTENSION "uuid-ossp"`)
				enginetest.Run(t, e, `ALTER EXTENSION "uuid-ossp" ADD TYPE public.ltree`)
			}
			enginetest.Run(t, e, "DROP EXTENSION ltree CASCADE")
			check()
			if _, ok := e.CatalogRead().Procedure(input); !ok {
				t.Fatal("origin retirement deleted required detached input")
			}
			if _, ok := e.CatalogRead().Procedure(output); !ok {
				t.Fatal("origin retirement deleted required detached output")
			}
		})
	}
}

func TestTypeProcedureDependenciesAuthority(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION ltree")
	d := installedCodecType(t, e)
	read := e.CatalogRead()
	reject := func(label string, v catalog.Read, typ *catalog.Type) {
		t.Helper()
		called := false
		if err := pgcatalog.VisitTypeProcedureDependencies(t.Context(), v, typ, func(ids.ObjectAddress) bool { called = true; return true }); err == nil || called {
			t.Fatal(label, "forged authority visited", called, err)
		}
	}
	other := engine.New()
	enginetest.Run(t, other, "CREATE EXTENSION ltree")
	reject("foreign type generation", read, installedCodecType(t, other))
	reject("missing view", catalog.Read{}, d)
	reject("missing type", read, nil)
	for _, class := range []ids.OID{ids.TypeClassID, ids.ProcedureClassID} {
		owner := *read.Owner()
		owner.Providers = make(map[ids.OID]catalog.Provider)
		for key, provider := range read.Owner().Providers {
			if key != class {
				owner.Providers[key] = provider
			}
		}
		reject("missing registered provider", catalog.NewRead(&owner, read.State()), d)
	}
	forged := *d
	forgedOrigin := *d.Origin
	forged.Origin = &forgedOrigin
	forged.Origin.Declaration++
	state := catalog.CloneState(read.State())
	state.SetDescriptor(d.Address(), &forged)
	reject("forged current declaration", read.WithState(state), &forged)
	facts, err := pgcatalog.TypeFacts(t.Context(), read, d, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	codec := *read.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(facts.Input)})
	codec.Implementation = nil
	state = catalog.CloneState(read.State())
	state.SetDescriptor(codec.Address(), &codec)
	reject("missing current codec owner", read.WithState(state), d)
	calls := 0
	if err := pgcatalog.VisitTypeProcedureDependencies(t.Context(), read, d, func(ids.ObjectAddress) bool { calls++; return false }); err != nil || calls != 1 {
		t.Fatal("early stop", calls, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	calls = 0
	err = pgcatalog.VisitTypeProcedureDependencies(ctx, read, d, func(ids.ObjectAddress) bool { calls++; cancel(); return true })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("callback cancellation", calls, err)
	}
	calls = 0
	err = pgcatalog.VisitTypeProcedureDependencies(ctx, read, d, func(ids.ObjectAddress) bool { calls++; return true })
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("pre-canceled visitation", calls, err)
	}
}

func TestTypeProcedureDependenciesDomainFacts(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION ltree")
	enginetest.Run(t, e, "CREATE DOMAIN codec_domain AS public.ltree")
	read := e.CatalogRead()
	base := installedCodecType(t, e)
	baseFacts, err := pgcatalog.TypeFacts(t.Context(), read, base, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	var domain *catalog.Type
	for _, descriptor := range read.Descriptors() {
		if d, ok := descriptor.(*catalog.Type); ok && d.Name() == "codec_domain" {
			domain = d
		}
	}
	if domain == nil {
		t.Fatal("missing real domain")
	}
	// DefineDomain uses domain_in/domain_recv and the base output/send.
	// ltree has no send/analyze/typmod/subscript declarations.
	want := []ids.ObjectAddress{
		{ClassID: ids.ProcedureClassID, ObjectID: 2597},
		{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(baseFacts.Output)},
		{ClassID: ids.ProcedureClassID, ObjectID: 2598},
	}
	var got []ids.ObjectAddress
	if err := pgcatalog.VisitTypeProcedureDependencies(t.Context(), read, domain, func(a ids.ObjectAddress) bool { got = append(got, a); return true }); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("genuine domain facts, not base input inheritance", got, want, err)
	}
	edges, err := pgcatalog.CollectDepend(t.Context(), read, core.EngineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	outputEdges, inputEdges, baseEdges := 0, 0, 0
	for _, edge := range edges {
		if edge.Dependent != domain.Address() {
			continue
		}
		if edge.Referenced == want[1] && edge.DepType == 'n' {
			outputEdges++
		}
		if edge.Referenced == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(baseFacts.Input)}) {
			inputEdges++
		}
		if edge.Referenced == base.Address() && edge.DepType == 'n' {
			baseEdges++
		}
	}
	if outputEdges != 1 || inputEdges != 0 || baseEdges != 1 {
		t.Fatal("domain NORMAL projection", outputEdges, inputEdges, baseEdges)
	}
}
