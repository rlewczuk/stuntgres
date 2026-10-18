package engine

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRoutineReleaseInventory(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	if len(builtinRoutineIdentities) != 3397 || len(routineByOID) != 3397 {
		t.Fatal("incomplete released inventory")
	}
	identities := make(map[string]bool)
	for _, fact := range builtinRoutineIdentities {
		key := fact.name + "(" + string(fact.signature) + ")"
		if fact.id == 0 || fact.namespace != ids.PGCatalogNamespaceOID || fact.name == "" || identities[key] {
			t.Fatal("invalid/duplicate released identity", fact)
		}
		identities[key] = true
		if fact.signature != "" {
			for symbol := range strings.SplitSeq(string(fact.signature), ",") {
				id, err := strconv.ParseUint(symbol, 10, 32)
				if _, ok := view.LookupType(sqltype.ID(id)); err != nil || !ok {
					t.Fatal("missing released argument identity", fact, symbol, err)
				}
			}
		}
		if p, exists := view.Find(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: fact.id}); exists {
			procedure, ok := p.(*procedureDescriptor)
			if !ok || procedure.signature != fact.signature || procedure.name != fact.name || (extensionCatalogProvider{ids.ProcedureClassID}).Validate(view, procedure) != nil {
				t.Fatal("invalid genuine catalog declaration", p, fact)
			}
		}
	}
	for _, witness := range []struct {
		id        ids.OID
		name      string
		signature procedureSignature
	}{
		{1242, "boolin", "2275"}, {1299, "now", ""}, {1397, "abs", "23"}, {1396, "abs", "20"}, {2108, "sum", "23"}, {1644, "RI_FKey_check_ins", ""}, {6399, "pg_get_aios", ""},
	} {
		got, ok := routineIdentityByOID(view, witness.id)
		if !ok || got.name != witness.name || got.signature != witness.signature {
			t.Fatal("release witness", got, witness)
		}
	}
	path := namespaceSelection{path: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}}
	for _, name := range []string{"abs", "lower"} {
		if _, count := selectRoutine(view, path, name, nil); count != 2 {
			t.Fatal("released overload ambiguity", name, count)
		}
	}
}

func TestRoutinePathCandidateMasking(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	catalog := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	first := &procedureDescriptor{address: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90000}, namespace: public, revision: 1, name: "unaccent", signature: unaccentTextSignature, routineKind: functionRoutine, owner: e.authorizationDomain.Principal(10), result: 25, volatility: expr.Stable, strict: true, provider: stockUnaccent}
	second := *first
	second.address.ObjectID, second.namespace, second.signature = 90001, catalog, unaccentDictionarySignature
	stage := e.BeginCatalogStage(view)
	if err := stage.Create(first); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(&second); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	view = e.CatalogRead()
	path := []ids.ObjectAddress{public, catalog}
	if _, count := selectRoutine(view, namespaceSelection{path: path}, "unaccent", nil); count != 2 {
		t.Fatal("distinct later signature masked", count)
	}
	if got := regprocSpelling(view, path, first.address.ObjectID); got != "public.unaccent" {
		t.Fatal("ambiguous earlier name output", got)
	}
	if got := routineSpelling(view, path, second.address.ObjectID, true, false); got != "unaccent(regdictionary,text)" {
		t.Fatal("visible later exact signature", got)
	}
	selected, count := selectRoutine(view, namespaceSelection{path: path}, "unaccent", &first.signature)
	if count != 1 || selected.id != first.address.ObjectID {
		t.Fatal("exact signature selection", selected, count)
	}
	// Real conversion descriptors duplicate source-fixed identities by OID, not
	// by adding overloads or changing lookup/execution authority.
	if _, count := selectRoutine(view, namespaceSelection{path: path}, "utf8_to_iso8859_1", nil); count != 1 {
		t.Fatal("conversion identity dedup", count)
	}
}

func TestRoutineRecursiveRemapAndDump(t *testing.T) {
	e := New()
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
	unchanged, err := remapRoutineDatum(original, func(id ids.OID) ids.OID { return id })
	if err != nil || unchanged != original {
		t.Fatal("unchanged immutable array copied", err)
	}
	remapped, err := remapRoutineDatum(original, func(id ids.OID) ids.OID {
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
	text, err := catalogLiteralSQL(context.Background(), view, literalExpression{Info: expr.Info{Usage: array.Usage()}, value: original})
	if err != nil || !strings.Contains(text, "[-2:1]") || !strings.Contains(text, "pg_catalog.abs(integer)") || !strings.Contains(text, "::regprocedure::regproc") || !strings.Contains(text, "4294967295") {
		t.Fatal("overload-safe recursive dump", text, err)
	}
	var refs []ids.ObjectAddress
	if err := walkRoutineReferences(view, original, func(address ids.ObjectAddress) error { refs = append(refs, address); return nil }); err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{{ClassID: ids.ProcedureClassID, ObjectID: 1397}}) {
		t.Fatal("genuine builtin dependency or lookup-only isolation lost", refs, err)
	}
}

func TestRoutineDomainCompositeRemap(t *testing.T) {
	view := New().CatalogRead()
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
	unchanged, err := remapRoutineDatum(row, func(id ids.OID) ids.OID { return id })
	if err != nil || unchanged != row {
		t.Fatal("unchanged composite copied", err)
	}
	remapped, err := remapRoutineDatum(row, func(id ids.OID) ids.OID {
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
	view := New()
	op, auth := conversionUnitAuth(t, view)
	types, err := view.captureRegtypeInput(auth, op)
	if err != nil {
		t.Fatal(err)
	}
	input, err := view.captureRoutineInput(auth, types)
	if err != nil {
		t.Fatal(err)
	}
	_, err = input.ResolveRegprocedure(context.Background(), "abs(NONE)")
	var original *sqlstate.Error
	if !errors.As(err, &original) || original.Code != "42601" || original.Position != "1" || original.Context != `invalid type name "NONE"` {
		t.Fatalf("decoded argument source: %#v", err)
	}
	saved := *original
	literal := regtypeInputError(err, parser.Position{Character: 17})
	var translated *sqlstate.Error
	if !errors.As(literal, &translated) || translated.Position != "17" || translated.Context != saved.Context || *original != saved {
		t.Fatal("literal translation mutated argument diagnostic", literal, err)
	}
	parameter := input.RoutineParameter(2, false).(capturedRoutineInput)
	if parameter.types.portal != "unnamed portal parameter $2 = '...'" || input.types.portal != "" {
		t.Fatal("parameter authority/redaction mutated original input")
	}
	runtime := input.RoutineLiteral(17).(capturedRoutineInput)
	if runtime.types.position.Character != 17 || input.types.position.Character != 0 || runtime.types.portal != "" {
		t.Fatal("literal authority retained portal or mutated original")
	}
}
