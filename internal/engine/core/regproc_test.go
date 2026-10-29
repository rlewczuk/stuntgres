package core_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineReleaseInventory(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	released := catalog.BuiltinRoutineIdentities()
	if len(released) != 3397 {
		t.Fatal("incomplete released inventory")
	}
	identities := make(map[string]bool)
	for _, fact := range released {
		if byOID, ok := catalog.BuiltinRoutineIdentityByOID(fact.ID); !ok || byOID != fact {
			t.Fatal("released identity OID is not unique", fact)
		}
		key := fact.Name + "(" + string(fact.Signature) + ")"
		if fact.ID == 0 || fact.Namespace != ids.PGCatalogNamespaceOID || fact.Name == "" || identities[key] {
			t.Fatal("invalid/duplicate released identity", fact)
		}
		identities[key] = true
		if fact.Signature != "" {
			for symbol := range strings.SplitSeq(string(fact.Signature), ",") {
				id, err := strconv.ParseUint(symbol, 10, 32)
				if _, ok := view.LookupType(sqltype.ID(id)); err != nil || !ok {
					t.Fatal("missing released argument identity", fact, symbol, err)
				}
			}
		}
		if p, exists := view.Find(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: fact.ID}); exists {
			procedure, ok := p.(*catalog.Procedure)
			if !ok || procedure.Signature != fact.Signature || procedure.Name() != fact.Name || (stored.ProcedureProvider{}).Validate(view, procedure) != nil {
				t.Fatal("invalid genuine catalog declaration", p, fact)
			}
		}
	}
	for _, witness := range []struct {
		id        ids.OID
		name      string
		signature catalog.ProcedureSignature
	}{
		{1242, "boolin", "2275"}, {1299, "now", ""}, {1397, "abs", "23"}, {1396, "abs", "20"}, {2108, "sum", "23"}, {1644, "RI_FKey_check_ins", ""}, {6399, "pg_get_aios", ""},
	} {
		got, ok := regobj.RoutineIdentityByOID(view, witness.id)
		if !ok || got.Name != witness.name || got.Signature != witness.signature {
			t.Fatal("release witness", got, witness)
		}
	}
	path := analyze.NamespaceSelection{Path: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}}
	for _, name := range []string{"abs", "lower"} {
		if _, count := regobj.SelectRoutine(view, path, name, nil); count != 2 {
			t.Fatal("released overload ambiguity", name, count)
		}
	}
}

func TestRoutinePathCandidateMasking(t *testing.T) {
	e, op, _ := unaccentSQLInstalled(t)
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	catalogAddress := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	enginetest.RoleExecute(t, e, op, "ALTER EXTENSION unaccent DROP FUNCTION public.unaccent(regdictionary,text)")
	enginetest.RoleExecute(t, e, op, "ALTER FUNCTION public.unaccent(regdictionary,text) SET SCHEMA pg_catalog")
	view := e.CatalogRead()
	first := view.MustProcedure(view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: public, Name: "unaccent", Signature: string(catalog.UnaccentTextSignature)}))
	second := view.MustProcedure(view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: catalogAddress, Name: "unaccent", Signature: string(catalog.UnaccentDictionarySignature)}))
	path := []ids.ObjectAddress{public, catalogAddress}
	if _, count := regobj.SelectRoutine(view, analyze.NamespaceSelection{Path: path}, "unaccent", nil); count != 2 {
		t.Fatal("distinct later signature masked", count)
	}
	if got := regobj.ProcSpelling(view, path, first.Address().ObjectID); got != "public.unaccent" {
		t.Fatal("ambiguous earlier name output", got)
	}
	if got := regobj.RoutineSpelling(view, path, second.Address().ObjectID, true, false); got != "unaccent(regdictionary,text)" {
		t.Fatal("visible later exact signature", got)
	}
	selected, count := regobj.SelectRoutine(view, analyze.NamespaceSelection{Path: path}, "unaccent", &first.Signature)
	if count != 1 || selected.ID != first.Address().ObjectID {
		t.Fatal("exact signature selection", selected, count)
	}
	// Real conversion descriptors duplicate source-fixed identities by OID, not
	// by adding overloads or changing lookup/execution authority.
	if _, count := regobj.SelectRoutine(view, analyze.NamespaceSelection{Path: path}, "utf8_to_iso8859_1", nil); count != 1 {
		t.Fatal("conversion identity dedup", count)
	}
}

func TestRoutineRecursiveRemapAndDump(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	proc, _ := view.LookupType(sqltype.Regproc)
	array, _ := view.LookupType(1008)
	builtin, _ := sqltype.RegprocValue(proc, 1397)
	unknown, _ := sqltype.RegprocValue(proc, 4294967295)
	dynamic, _ := sqltype.RegprocValue(proc, 90000)
	original, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: -2, Length: 4}}, []sqltype.Value{dynamic, builtin, unknown, sqltype.Null(proc)})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := regobj.RemapRoutineDatum(original, func(id ids.OID) ids.OID { return id })
	if err != nil || unchanged != original {
		t.Fatal("unchanged immutable array copied", err)
	}
	remapped, err := regobj.RemapRoutineDatum(original, func(id ids.OID) ids.OID {
		if id == 90000 {
			return 90001
		}
		return id
	})
	if err != nil || !reflect.DeepEqual(original.ArrayDimensions(), remapped.ArrayDimensions()) {
		t.Fatal("remap shape", err)
	}
	id, _ := remapped.ArrayElement(0).ProcedureOID()
	originalID, _ := original.ArrayElement(0).ProcedureOID()
	fixedID, _ := remapped.ArrayElement(1).ProcedureOID()
	unknownID, _ := remapped.ArrayElement(2).ProcedureOID()
	if id != 90001 || originalID != 90000 || fixedID != 1397 || unknownID != 4294967295 || !remapped.ArrayElement(3).IsNull() {
		t.Fatal("remap identities/null/ownership")
	}
	text, err := regobj.Literal(context.Background(), view, plan.LiteralExpr{Info: expr.Info{Usage: array.Usage()}, Value: original})
	if err != nil || !strings.Contains(text, "[-2:1]") || !strings.Contains(text, "pg_catalog.abs(integer)") || !strings.Contains(text, "::regprocedure::regproc") || !strings.Contains(text, "4294967295") {
		t.Fatal("overload-safe recursive dump", text, err)
	}
	var refs []ids.ObjectAddress
	if err := regobj.WalkRoutineReferences(view, original, func(address ids.ObjectAddress) error { refs = append(refs, address); return nil }); err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{{ClassID: ids.ProcedureClassID, ObjectID: 1397}}) {
		t.Fatal("genuine builtin dependency or lookup-only isolation lost", refs, err)
	}
}

func TestRoutineDomainCompositeRemap(t *testing.T) {
	view := engine.New().CatalogRead()
	procedure, _ := view.LookupType(sqltype.Regprocedure)
	array, _ := view.LookupType(1008)
	domain, err := sqltype.Domain(61000, "routine_domain", "routine_domain", procedure.Usage())
	if err != nil {
		t.Fatal(err)
	}
	composite, err := sqltype.Composite(61001, "routine_row", "routine_row", 61002, []sqltype.CompositeMember{{Attribute: 1, Name: "d", Usage: domain.Usage()}, {Attribute: 2, Name: "a", Usage: array.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := sqltype.RegprocValue(procedure, 90000)
	wrapped, err := domain.Wrap(value)
	if err != nil {
		t.Fatal(err)
	}
	row, err := sqltype.CompositeValue(composite, []sqltype.Value{wrapped, sqltype.Null(array)})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := regobj.RemapRoutineDatum(row, func(id ids.OID) ids.OID { return id })
	if err != nil || unchanged != row {
		t.Fatal("unchanged composite copied", err)
	}
	remapped, err := regobj.RemapRoutineDatum(row, func(id ids.OID) ids.OID {
		if id == 90000 {
			return 90001
		}
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := remapped.CompositeField(0).ProcedureOID()
	old, _ := row.CompositeField(0).ProcedureOID()
	if id != 90001 || old != 90000 || remapped.Type() != composite || remapped.CompositeField(0).Type() != domain || !remapped.CompositeField(1).IsNull() {
		t.Fatal("composite/domain identity ownership")
	}
}

func TestRoutineDiagnosticOwnership(t *testing.T) {
	view := engine.New()
	op, auth := conversionUnitAuth(t, view)
	types, err := core.CaptureRegtypeInput(auth, op)
	if err != nil {
		t.Fatal(err)
	}
	input, err := core.CaptureRoutineInput(auth, types)
	if err != nil {
		t.Fatal(err)
	}
	_, err = input.ResolveRegprocedure(context.Background(), "abs(NONE)")
	var original *sqlstate.Error
	if !errors.As(err, &original) || original.Code != "42601" || original.Position != "1" || original.Context != `invalid type name "NONE"` {
		t.Fatalf("decoded argument source: %#v", err)
	}
	saved := *original
	literal := analyze.RegtypeInputError(err, parser.Position{Character: 17})
	var translated *sqlstate.Error
	if !errors.As(literal, &translated) || translated.Position != "17" || translated.Context != saved.Context || *original != saved {
		t.Fatal("literal translation mutated argument diagnostic", literal, err)
	}
}

func collationUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func conversionUnitAuth(t *testing.T, e *core.Engine) (*session.Operation, core.AuthorizationView) {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return op, auth
}
