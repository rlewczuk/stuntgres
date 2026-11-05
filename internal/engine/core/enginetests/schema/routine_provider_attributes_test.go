package schema_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Exercise actual seeded/installed declarations, not synthetic callable flags.
func TestRoutineProviderAttributeEligibility(t *testing.T) {
	e := engine.New()
	definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "attribute_handler"}, Wrapper: &fdwUnitWrapper{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.InstallForeignDefinitions(nil, []fdw.WrapperDefinition{definition}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, source := range []string{
		"CREATE EXTENSION unaccent",
		"CREATE FUNCTION attribute_sql() RETURNS integer LANGUAGE sql AS 'SELECT 7'",
		"CREATE FUNCTION attribute_trigger() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'",
		"CREATE FUNCTION attribute_event() RETURNS event_trigger LANGUAGE plpgsql AS 'BEGIN RETURN; END'",
		"CREATE TYPE attribute_range AS RANGE (subtype=integer)",
		"CREATE AGGREGATE attribute_sum(integer) (SFUNC=int4pl, STYPE=integer, INITCOND='0')",
	} {
		enginetest.RoleExecute(t, e, op, source)
	}
	view := e.CatalogRead()
	families := map[string]*catalog.Procedure{}
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok {
			continue
		}
		family := ""
		switch {
		case d.SQL != nil:
			continue
		case d.Implementation != nil:
			family = "installed"
		case d.RangeConstructor != nil:
			family = "range"
		case d.PLHandler != catalog.NoPLHandler:
			family = "language-handler"
		case d.BrinSupport != 0:
			family = "brin-support"
		case d.GinSupport != 0:
			family = "gin-support"
		case d.AccessMethod != catalog.NoAccessMethod:
			family = "access-method"
		case d.Foreign != nil:
			family = "foreign"
		case d.Conversion.Name() != "":
			family = "conversion"
		case d.Aggregate != nil:
			family = "aggregate"
		case d.Builtin != nil:
			family = "builtin-scalar"
			if d.Builtin.ReturnsSet {
				family = "builtin-set"
			}
		}
		if family != "" && families[family] == nil {
			families[family] = d
		}
	}
	for _, family := range []string{"installed", "range", "language-handler", "brin-support", "gin-support", "access-method", "foreign", "conversion", "aggregate", "builtin-scalar", "builtin-set"} {
		d := families[family]
		if d == nil {
			t.Fatalf("missing genuine family %s", family)
		}
		// Utility lookup uses the procedure-name index, not the descriptor
		// enumeration above. A real installed provider must be discoverable by
		// the same exact name/signature before testing its attribute gate.
		key := catalog.DescriptorNameKey(d)
		if view.NameAddress(key) != d.Address() {
			t.Fatalf("%s provider lost exact name/signature binding", family)
		}
		indexed := false
		for candidate := range catalog.DefinitionProceduresNamedAt(view, nil, d.Namespace(), d.Name()) {
			if candidate == d && candidate.Signature == d.Signature {
				indexed = true
			}
		}
		if !indexed {
			t.Fatalf("%s provider missing from utility lookup index", family)
		}
		if family == "foreign" {
			// FDW handlers genuinely take no arguments; do not replace this
			// capability with a validator or invent a callable signature.
			if d.Signature != catalog.ForeignHandlerSignature || d.Foreign.Name != definition.HandlerName || d.Foreign.Wrapper != definition.Wrapper || d.Result != 3115 {
				t.Fatal("foreign handler lost installed producer identity")
			}
		}
		t.Run(family, func(t *testing.T) {
			if err := (stored.ProcedureProvider{}).Validate(view, d); err != nil {
				t.Fatal("canonical provider", err)
			}
			original := *d
			var arguments []string
			if d.Signature != "" {
				for id := range strings.SplitSeq(string(d.Signature), ",") {
					n, err := strconv.ParseUint(id, 10, 32)
					if err != nil {
						t.Fatal(err)
					}
					typ := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(n)})
					arguments = append(arguments, catalog.DependencyIdentifier(view.MustNamespace(typ.Namespace()).Name())+"."+catalog.DependencyIdentifier(typ.Name()))
				}
			}
			target := catalog.DependencyIdentifier(view.MustNamespace(d.Namespace()).Name()) + "." + catalog.DependencyIdentifier(d.Name()) + "(" + strings.Join(arguments, ",") + ")"
			stageAction := func(action string) (*core.CatalogStage, error) {
				t.Helper()
				raw := enginetest.Statement(t, "ALTER FUNCTION "+target+" "+action).(*parser.AlterRoutine)
				e.Mu.Lock()
				defer e.Mu.Unlock()
				auth, err := e.AuthorizationLocked(op)
				if err != nil {
					return nil, err
				}
				stage := e.BeginCatalogStage(auth.Catalog)
				err = routine.StageAlterRoutine(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, raw)
				return stage, err
			}
			for _, action := range []string{"SECURITY DEFINER", "SECURITY INVOKER", "SET application_name='ignored'", "RESET application_name", "RESET ALL", "STRICT", "CALLED ON NULL INPUT", "IMMUTABLE", "STABLE", "VOLATILE", "COST 9 SECURITY INVOKER"} {
				stage, err := stageAction(action)
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "execution attributes are unsupported for native routine providers"}) {
					t.Fatalf("%s diagnostic: %#v", action, err)
				}
				if stage == nil || stage.Candidate.DescriptorCount() != 0 || e.CatalogRead().State() != view.State() || !reflect.DeepEqual(*d, original) {
					t.Fatal("rejected action staged or published a descriptor, or modified retained backing")
				}
			}
			for _, change := range []func(*catalog.Procedure){
				func(p *catalog.Procedure) { p.SecurityDefiner = true },
				func(p *catalog.Procedure) {
					p.Configuration = catalog.NewRoutineConfiguration([]string{"application_name=ignored"})
				},
			} {
				candidate := original
				change(&candidate)
				state := catalog.CloneState(view.State())
				state.SetDescriptor(candidate.Address(), &candidate)
				if err := (stored.ProcedureProvider{}).Validate(view.WithState(state), &candidate); err == nil {
					t.Fatal("impossible provider execution state accepted")
				}
				if d.Implementation != nil {
					if _, err := extension.ProcedureSymbol(view.WithState(state), &candidate); err == nil {
						t.Fatal("direct installed identity accepted ignored execution state")
					}
				}
			}
			stage, err := stageAction("PARALLEL RESTRICTED COST 9")
			if err != nil {
				t.Fatal("metadata-only ALTER", err)
			}
			candidate := stage.Candidate.Get(d.Address()).(*catalog.Procedure)
			if candidate.Parallel != 'r' || candidate.Cost != 9 || candidate.Strict != d.Strict || candidate.Volatility != d.Volatility || candidate.Implementation != d.Implementation || candidate.Builtin != d.Builtin || candidate.SQL != d.SQL {
				t.Fatal("metadata changed intrinsic identity")
			}
			state := catalog.CloneState(view.State())
			state.SetDescriptor(candidate.Address(), candidate)
			if err := (stored.ProcedureProvider{}).Validate(view.WithState(state), candidate); err != nil {
				t.Fatal("metadata candidate provider identity", err)
			}
			returnsSet := d.ReturnsSet()
			rowsStage, err := stageAction("ROWS 23")
			if returnsSet {
				if err != nil || rowsStage.Candidate.Get(d.Address()).(*catalog.Procedure).Rows != 23 {
					t.Fatal("genuine set cardinality rejected", err)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "ROWS is not applicable when function does not return a set" || rowsStage.Candidate.DescriptorCount() != 0 {
					t.Fatal("scalar cardinality admitted or wrong diagnostic", err)
				}
			}
			if d.Builtin != nil || d.Implementation != nil {
				for _, change := range []func(*catalog.Procedure){
					func(p *catalog.Procedure) { p.Strict = !p.Strict },
					func(p *catalog.Procedure) {
						p.Volatility = expr.Immutable
						if d.Volatility == expr.Immutable {
							p.Volatility = expr.Volatile
						}
					},
				} {
					bad := original
					change(&bad)
					state := catalog.CloneState(view.State())
					state.SetDescriptor(bad.Address(), &bad)
					if err := (stored.ProcedureProvider{}).Validate(view.WithState(state), &bad); err == nil {
						t.Fatal("intrinsic strictness/volatility identity weakened")
					}
				}
			}
		})
	}
	for _, name := range []string{"attribute_sql", "attribute_trigger", "attribute_event"} {
		enginetest.RoleExecute(t, e, op, "ALTER FUNCTION "+name+"() SECURITY DEFINER SET application_name='supported' STABLE")
		current := e.CatalogRead()
		address := current.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
		d := current.MustProcedure(address)
		if d.SQL == nil || !d.SecurityDefiner || d.Volatility != expr.Stable || d.Configuration.Len() != 1 || d.Configuration.At(0) != "application_name=supported" {
			t.Fatal("supported SQL/PL entry lost execution attributes", name)
		}
	}
}

type fdwUnitWrapper struct {
	paths    []catalog.ForeignPath
	chosen   catalog.ForeignPath
	scan     *fdwUnitScan
	beginErr error
}

func (*fdwUnitWrapper) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	return catalog.ForeignRelSize{Rows: 3}, nil
}

func (w *fdwUnitWrapper) GetForeignPaths(context.Context, catalog.ForeignRequest, catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return w.paths, nil
}

func (w *fdwUnitWrapper) GetForeignPlan(_ context.Context, _ catalog.ForeignRequest, path catalog.ForeignPath) (catalog.ForeignPlan, error) {
	w.chosen = path
	return catalog.ForeignPlan{Attributes: path.Attributes, Private: path.Private}, nil
}

func (w *fdwUnitWrapper) BeginForeignScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (catalog.ForeignScan, error) {
	return w.scan, w.beginErr
}

type fdwUnitScan struct {
	position, rescans, ends int
	rescanErr, endErr       error
	cleanupCanceled         bool
	onEnd                   func(context.Context) error
}

func (s *fdwUnitScan) IterateForeignScan(context.Context) (catalog.ForeignRow, bool, error) {
	if s.position == 3 {
		return nil, false, nil
	}
	s.position++
	return catalog.ForeignRow{}, true, nil
}

func (s *fdwUnitScan) ReScanForeignScan(context.Context, catalog.ForeignScanParameters) error {
	s.rescans++
	s.position = 0
	return s.rescanErr
}

func (s *fdwUnitScan) EndForeignScan(ctx context.Context) error {
	s.ends++
	s.cleanupCanceled = ctx.Err() != nil
	if s.onEnd != nil {
		return s.onEnd(ctx)
	}
	return s.endErr
}
