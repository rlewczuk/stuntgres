package stored_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
)

func checkedDefaultOwnerEngine(t *testing.T, replacement bool) *core.Engine {
	t.Helper()
	e := engine.New()
	if replacement {
		if err := engine.Reset(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	definition := enginetest.HostOwnerDefinition(nil)
	definition.Programs[0].Inputs = []host.Type{host.Hstore, host.Hstore, host.Hstore}
	maps, err := host.New(definition)
	if err != nil {
		t.Fatal(err)
	}
	text, err := host.New(primitiveOwnerDefinition(host.Text, 1, "ok", new(primitiveOwnerObservation)))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.InstallHostDefinitions([]*host.Runtime{maps, text}); err != nil {
		t.Fatal(err)
	}
	// Keep the declaration lookup environment in the same real session as
	// SET search_path; enginetest.Run starts a fresh session for every call.
	declarations := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE SCHEMA host_types",
		"CREATE EXTENSION hstore SCHEMA host_types",
		"SET search_path=host_types,public,pg_catalog",
		"CREATE FUNCTION public.checked_map(required host_types.hstore, compared host_types.hstore DEFAULT NULLIF('a=>1'::host_types.hstore,'a=>1'::host_types.hstore), plain host_types.hstore DEFAULT 'a=>7'::host_types.hstore) RETURNS host_types.hstore LANGUAGE owner_host TRANSFORM FOR TYPE host_types.hstore AS 'increment'",
		"CREATE CAST (host_types.hstore AS text) WITH INOUT AS ASSIGNMENT",
		"CREATE FUNCTION public.checked_text(v text DEFAULT 'a=>7'::host_types.hstore) RETURNS text LANGUAGE primitive_owner AS 'compute'",
		"DROP CAST (host_types.hstore AS text)",
		"CREATE FUNCTION public.checked_sql(v integer DEFAULT 7) RETURNS integer LANGUAGE SQL RETURN v+1",
	} {
		enginetest.SessionSQL(t, declarations, q)
	}
	return e
}

func checkedDefaultOwnerProcedure(t *testing.T, read catalog.Read, name string) *catalog.Procedure {
	t.Helper()
	for _, d := range read.Descriptors() {
		if p, ok := d.(*catalog.Procedure); ok && p.Name() == name {
			return p
		}
	}
	t.Fatal("missing genuine checked declaration", name)
	return nil
}

func checkedDefaultOwnerRoots(t *testing.T, p *catalog.Procedure) []expr.Expr {
	t.Helper()
	var roots []expr.Expr
	if err := stored.VisitDefinitionExpressions(p, func(node expr.Expr) { roots = append(roots, node) }); err != nil {
		t.Fatal(err)
	}
	return roots
}

func checkedDefaultOwnerPayload(t *testing.T, read catalog.Read, p *catalog.Procedure) {
	t.Helper()
	layout := p.RoutineDefinition()
	if p.SQL != nil || p.SQLBody != nil || p.Host == nil || layout != p.Host.Layout || layout.Kind != proc.HostBody || p.SQLDefaults == nil {
		t.Fatal("host defaults acquired a fake SQL declaration/program")
	}
	if err := p.SQLDefaults.Validate(read); err != nil {
		t.Fatal(err)
	}
	if err := p.SQLDefaults.ValidateDefinition(p); err != nil {
		t.Fatal(err)
	}
	roots := checkedDefaultOwnerRoots(t, p)
	var expected []expr.Expr
	for slot := range layout.Inputs {
		if node, ok := p.SQLDefaults.Default(slot); ok {
			expected = append(expected, node)
		}
	}
	if !reflect.DeepEqual(roots, expected) {
		t.Fatal("visitor did not expose exactly the actual defaults in input-slot order")
	}
	for _, root := range roots {
		plan.WalkExpression(root, func(node expr.Expr) {
			typ := node.ResultUsage().Type
			if typ == nil {
				t.Fatal("checked default has no result type")
			}
			current, ok := read.LookupType(typ.ID())
			if !ok || current != typ {
				t.Fatal("default retained foreign type generation", typ)
			}
			if literal, ok := node.(plan.LiteralExpr); ok && literal.Value.Type() != typ {
				t.Fatal("literal retained foreign datum type backing")
			}
		})
	}
	for _, dep := range p.SQLDefaults.Dependencies() {
		current := read.Get(dep.Ref.Address)
		if dep.Ref.Domain() != read.Owner().Domain || dep.Ref.Epoch != read.Epoch() || current == nil || current.Revision() != dep.Revision {
			t.Fatal("default dependency retained source catalog authority", dep)
		}
	}
}

func TestHostCheckedDefaultExpressionOwnership(t *testing.T) {
	source := checkedDefaultOwnerEngine(t, false)
	read := source.CatalogRead()
	maps := checkedDefaultOwnerProcedure(t, read, "checked_map")
	text := checkedDefaultOwnerProcedure(t, read, "checked_text")
	for _, p := range []*catalog.Procedure{maps, text} {
		checkedDefaultOwnerPayload(t, read, p)
	}
	if _, ok := maps.SQLDefaults.Default(0); ok || len(checkedDefaultOwnerRoots(t, maps)) != 2 {
		t.Fatal("visitor manufactured the required argument's nonexistent default")
	}
	comparison, _ := maps.SQLDefaults.Default(1)
	if _, ok := comparison.(plan.ConditionalExpr); !ok {
		t.Fatal("missing actual NULLIF comparison root", comparison)
	}
	sourceComparison := comparison.(plan.ConditionalExpr)
	sourceMapDependencies := slices.Clone(maps.SQLDefaults.Dependencies())
	sourceTextDependencies := slices.Clone(text.SQLDefaults.Dependencies())
	sourceMapFormals := slices.Clone(maps.Host.Layout.Formals)
	sourceTextFormals := slices.Clone(text.Host.Layout.Formals)
	var equality *catalog.Procedure
	namespace, err := stored.ComparisonBindingNamespace(read, comparison, func(p *catalog.Procedure) error {
		if equality != nil && equality != p {
			return errors.New("unexpected comparison dependency")
		}
		equality = p
		return nil
	})
	if err != nil || equality == nil || read.Get(equality.Address()) != equality || equality.Name() != "hstore_eq" || namespace != equality.Namespace() {
		t.Fatal("default lost its genuine implicit hstore equality ownership", namespace, equality, err)
	}
	found := false
	for _, dep := range maps.SQLDefaults.Dependencies() {
		found = found || dep.Ref.Address == equality.Address()
	}
	if !found {
		t.Fatal("implicit equality missing from checked default dependencies")
	}
	castRoot, _ := text.SQLDefaults.Default(0)
	cast, ok := castRoot.(plan.DatumCastExpr)
	if !ok || cast.Method != "i" || cast.Context != "a" || !cast.Child.ResultUsage().Type.IsHstore() {
		t.Fatal("default lost historical INOUT assignment cast", castRoot)
	}
	// The validator must use the same visitor for the actual changed host DDL,
	// preserving both the namespace and source-position receipt checks.
	before := catalog.CloneState(read.State())
	before.RemoveDescriptor(maps.Address())
	stmt := enginetest.Statement(t, "CREATE FUNCTION public.checked_map(required host_types.hstore, compared host_types.hstore DEFAULT NULLIF('a=>1'::host_types.hstore,'a=>1'::host_types.hstore), plain host_types.hstore DEFAULT 'a=>7'::host_types.hstore) RETURNS host_types.hstore LANGUAGE owner_host TRANSFORM FOR TYPE host_types.hstore AS 'increment'")
	if err := control.ValidateComparisonBinding(read, source.Catalog.Read(before), stmt, namespace, 0); err != nil {
		t.Fatal("host comparison binding receipt rejected", err)
	}
	if err := control.ValidateComparisonBinding(read, source.Catalog.Read(before), stmt, maps.Namespace(), 0); err == nil {
		t.Fatal("wrong comparison namespace accepted")
	}
	if err := control.ValidateComparisonBinding(read, source.Catalog.Read(before), stmt, namespace, comparison.SourcePosition().Offset+1); err == nil {
		t.Fatal("comparison before declaration receipt position accepted")
	}
	standard := checkedDefaultOwnerProcedure(t, read, "checked_sql")
	if standard.SQL == nil || !standard.SQL.Standard || standard.SQLBody == nil || len(checkedDefaultOwnerRoots(t, standard)) < 2 {
		t.Fatal("SQL-standard control stopped visiting both its default and real body")
	}
	if _, err := stored.RangeIdentityConstraints(context.Background(), read.State(), nil); err != nil {
		t.Fatal("shared range-remap inspection rejected genuine host roots", err)
	}

	// Seed a destination with independently installed real types and capabilities,
	// then import the SOURCE payloads. The following remap, not destination SQL
	// parsing or Dump/Restore equality, must produce the checked owning artifacts.
	destination := checkedDefaultOwnerEngine(t, true)
	target := destination.CatalogRead()
	state := catalog.CloneState(target.State())
	dest := destination.Catalog.Read(state)
	if dest.Owner().Domain == read.Owner().Domain || dest.Epoch() == read.Epoch() {
		t.Fatal("fixture failed to distinguish destination catalog domain/epoch")
	}
	// Literal transport requires the replacement candidate's explicit scalar
	// import authorization as well as the declaration type mapper. Both codecs
	// come from genuine installed hstore catalogs, not a constructed lookalike.
	installedMaps := checkedDefaultOwnerProcedure(t, dest, maps.Name())
	sourceHstore := maps.Host.Layout.Result.Type
	destinationHstore := installedMaps.Host.Layout.Result.Type
	sourceType, sourceOK := read.LookupType(sourceHstore.ID())
	destinationType, destinationOK := dest.LookupType(destinationHstore.ID())
	if !sourceOK || !destinationOK || sourceType != sourceHstore || destinationType != destinationHstore || !sourceHstore.IsHstore() || !destinationHstore.IsHstore() || sourceHstore == destinationHstore {
		t.Fatal("scalar import lacks genuine distinct installed hstore codecs")
	}
	state.SetScalarImport(sourceHstore, destinationHstore)
	for _, p := range []*catalog.Procedure{maps, text} {
		installed := checkedDefaultOwnerProcedure(t, dest, p.Name())
		if installed.Address() != p.Address() || installed.Host.Layout.Result.Type == p.Host.Layout.Result.Type || installed.Host.Program == p.Host.Program {
			t.Fatal("fixture failed to distinguish installed destination backing")
		}
		state.SetDescriptor(p.Address(), p)
	}
	principal := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		if ref.ID() == 0 || ref != read.Owner().Principals.Principal(ref.ID()) && ref != dest.Owner().Principals.Principal(ref.ID()) {
			return authorization.PrincipalRef{}, ids.ErrCatalogReplaced
		}
		return dest.Owner().Principals.Principal(ref.ID()), nil
	}
	owners := catalog.NewProcedureRemapper(catalog.PrincipalRemap{Principal: principal, SeededOwner: func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
		return principal(ref)
	}})
	provider := func(p *catalog.Procedure) (*catalog.Procedure, error) {
		next, err := owners(p)
		if err != nil || next.Host == nil {
			return next, err
		}
		// Use the real destination registry, never a fabricated capability token.
		h := *next.Host
		for _, runtime := range destination.Catalog.HostRuntimes {
			if runtime.Provider() == h.Program.Runtime().Provider() {
				h.Program = runtime.Program(h.Program.ID())
				h.Tokens = slices.Clone(h.Tokens)
				for i, token := range h.Tokens {
					h.Tokens[i] = runtime.Conversion(token.Definition().ID)
				}
				next.Host = &h
				return next, nil
			}
		}
		return nil, errors.New("missing installed destination runtime")
	}
	remap := stored.NewRoutineRemap(dest, nil, nil, provider, false)
	if err := routine.RebindRoutineDefinitions(state, remap); err != nil {
		t.Fatal(err)
	}
	if err := remap.Finish(); err != nil {
		t.Fatal(err)
	}
	// Candidate-only authorization must not be needed by the finished payload.
	state.SetScalarImports(nil)
	for _, original := range []*catalog.Procedure{maps, text} {
		mapped, ok := dest.Procedure(original.Address())
		if !ok || mapped == original || mapped.Host == original.Host || mapped.Host.Layout == original.Host.Layout || &mapped.Host.Layout.Formals[0] == &original.Host.Layout.Formals[0] || mapped.SQLDefaults == original.SQLDefaults || mapped.Host.Program == original.Host.Program || mapped.Host.Layout.Result.Type == original.Host.Layout.Result.Type {
			t.Fatal("remap retained source declaration/payload/layout/capability/type backing")
		}
		checkedDefaultOwnerPayload(t, dest, mapped)
		if err := mapped.SQLDefaults.Validate(read); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("destination defaults accepted source catalog owner", err)
		}
		if err := (stored.ProcedureProvider{}).Validate(dest, mapped); err != nil {
			t.Fatal("destination declaration is not genuinely owned", err)
		}
		checkedDefaultOwnerPayload(t, read, original)
		if read.Get(original.Address()) != original {
			t.Fatal("remap mutated source catalog declaration")
		}
	}
	mappedMaps, _ := dest.Procedure(maps.Address())
	mappedComparison, _ := mappedMaps.SQLDefaults.Default(1)
	mappedNamespace, err := stored.ComparisonBindingNamespace(dest, mappedComparison, nil)
	if err != nil || mappedNamespace != namespace {
		t.Fatal("remapped comparison lost destination installed authority", mappedNamespace, err)
	}
	destinationComparison := mappedComparison.(plan.ConditionalExpr)
	if destinationComparison.RoutineRef.Domain() != dest.Owner().Domain || destinationComparison.RoutineRef.Epoch != dest.Epoch() || destinationComparison.RoutineRef.Domain() == sourceComparison.RoutineRef.Domain() || destinationComparison.Args[0].ResultUsage().Type == sourceComparison.Args[0].ResultUsage().Type || &destinationComparison.Args[0] == &sourceComparison.Args[0] {
		t.Fatal("comparison retained source reference/operand-vector/type backing")
	}
	mappedText, _ := dest.Procedure(text.Address())
	mappedCast, _ := mappedText.SQLDefaults.Default(0)
	nextCast := mappedCast.(plan.DatumCastExpr)
	if nextCast.Method != "i" || nextCast.Context != "a" || nextCast.Child.ResultUsage().Type == cast.Child.ResultUsage().Type {
		t.Fatal("historical cast lost selection or retained source hstore backing")
	}
	if !reflect.DeepEqual(comparison, checkedDefaultOwnerRoots(t, maps)[0]) || !reflect.DeepEqual(castRoot, checkedDefaultOwnerRoots(t, text)[0]) {
		t.Fatal("remap mutated source checked expressions")
	}
	if !slices.Equal(sourceMapDependencies, maps.SQLDefaults.Dependencies()) || !slices.Equal(sourceTextDependencies, text.SQLDefaults.Dependencies()) || !reflect.DeepEqual(sourceMapFormals, maps.Host.Layout.Formals) || !reflect.DeepEqual(sourceTextFormals, text.Host.Layout.Formals) {
		t.Fatal("remap mutated source dependency/formal artifacts")
	}
	if _, err := stored.RangeIdentityConstraints(context.Background(), state, nil); err != nil {
		t.Fatal("range-remap inspection rejected destination host roots", err)
	}
}

func primitiveOwnerDefinition(typ host.Type, arity int, fault string, observed *primitiveOwnerObservation) host.LanguageDefinition {
	inputs := make([]host.Type, arity)
	for i := range inputs {
		inputs[i] = typ
	}
	conversion := host.ConversionDefinition{
		ID: string(typ), Type: typ, Bootstrap: true,
		FromName: host.Name{Schema: "public", Name: "primitive_owner_from"},
		ToName:   host.Name{Schema: "public", Name: "primitive_owner_to"},
		FromSQL: func(_ context.Context, v host.SQLValue) (host.Object, error) {
			if v.Type != typ {
				return host.Object{}, errors.New("wrong primitive carrier input")
			}
			if v.Null {
				return host.Object{Null: true}, nil
			}
			switch typ {
			case host.Bool:
				return host.Object{Value: v.Bool}, nil
			case host.Int4, host.Int8:
				return host.Object{Value: v.Int64}, nil
			case host.Text:
				return host.Object{Value: v.Text}, nil
			}
			return host.Object{}, errors.New("unsupported primitive")
		},
		ToSQL: func(_ context.Context, v host.Object) (host.SQLValue, error) {
			out := host.SQLValue{Type: typ, Null: v.Null}
			if fault == "to-error" {
				return out, errors.New("primitive owner TO failure")
			}
			if !v.Null {
				switch typ {
				case host.Bool:
					x, ok := v.Value.(bool)
					if !ok {
						return out, errors.New("wrong bool object")
					}
					out.Bool = x
				case host.Int4, host.Int8:
					x, ok := v.Value.(int64)
					if !ok {
						return out, errors.New("wrong integer object")
					}
					out.Int64 = x
				case host.Text:
					x, ok := v.Value.(string)
					if !ok {
						return out, errors.New("wrong text object")
					}
					out.Text = x
				}
			}
			switch fault {
			case "wrong-tag":
				out.Type = host.Hstore
			case "extra-payload":
				out.Hstore = []host.HstoreEntry{}
			case "null-payload":
				out.Null, out.Bool = true, true
			case "range":
				out.Int64 = 2147483648
			case "nul":
				out.Text = "a\x00b"
			case "utf8":
				out.Text = string([]byte{0xff})
			}
			return out, nil
		},
	}
	program := host.ProgramDefinition{
		ID: "compute", Version: "1", Inputs: inputs, Result: typ,
		Execute: func(_ context.Context, args []host.Object) (host.Object, error) {
			if fault == "program-error" {
				return host.Object{}, errors.New("primitive owner program failure")
			}
			if fault == "wrong-object" {
				return host.Object{Value: struct{}{}}, nil
			}
			if fault == "null-object" {
				return host.Object{Null: true, Value: int64(7)}, nil
			}
			for _, arg := range args {
				if arg.Null {
					return host.Object{Null: true}, nil
				}
			}
			switch typ {
			case host.Bool:
				return host.Object{Value: !args[0].Value.(bool)}, nil
			case host.Int4, host.Int8:
				sum := int64(5)
				for _, arg := range args {
					sum += arg.Value.(int64)
				}
				return host.Object{Value: sum}, nil
			case host.Text:
				return host.Object{Value: args[0].Value.(string) + "!"}, nil
			}
			return host.Object{}, errors.New("unsupported primitive")
		},
	}
	return host.LanguageDefinition{
		Provider: "primitive-owner", ABIVersion: "1", Name: "primitive_owner",
		HandlerName: host.Name{Schema: "public", Name: "primitive_owner_handler"},
		Handler: func(ctx context.Context, program host.ProgramFunc, args []host.Object) (host.Object, error) {
			observed.arguments, observed.continuation = args, program
			return program(ctx, args)
		},
		Programs: []host.ProgramDefinition{program}, Conversions: []host.ConversionDefinition{conversion},
		Defaults: []host.DefaultDefinition{{Type: typ, FromID: string(typ), ToID: string(typ)}},
	}
}

// These are borrowed observations of the actual invocation buffers, deliberately
// not copies. The public reference cohort owns scalar SQL outcomes and replay.
// This supplement checks the private descriptor/token/frame cleanup boundaries.
type primitiveOwnerObservation struct {
	arguments    []host.Object
	continuation host.ProgramFunc
}
