package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestUUIDOsspExpressionIdentity(t *testing.T) {
	e, ext := uuidOsspUnitInstalled(t)
	view := e.CatalogRead()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "uuid_generate_v4"}}}}
	node, err := binder.bindInstalledUUIDOssp(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := node.(installedUUIDOsspFunctionExpression)
	if n.ref.Address != ext.members[0] || n.ResultUsage().Type.ID() != sqltype.UUID || plannedVolatility(n) != Volatile || !n.validShape() || !closedUUIDDefault(n) || generationImmutable(n) {
		t.Fatal("installed UUID shape/properties", n)
	}
	if generationHasTableOID(n) {
		t.Fatal("zero-child UUID leaf acquired a tableoid dependency")
	}
	if err := validateGenerationPlan(view, nil, n); err != nil {
		t.Fatal("installed UUID plan validation", err)
	}
	found := false
	for _, d := range binder.dependencies {
		if d.ref == n.ref && d.revision == n.procedure.revision {
			found = true
		}
	}
	if !found {
		t.Fatal("missing genuine procedure dependency")
	}
	visits := 0
	boundExpressionChildren(n, func(BoundExpr) { visits++ })
	if visits != 0 || !sameGroupExpression(n, n) {
		t.Fatal("not a zero-child identity leaf")
	}
	refs, err := pgDependExpressionRefs(context.Background(), view, n)
	if err != nil || !slices.Contains(refs, n.ref.Address) {
		t.Fatal(refs, err)
	}
	retagged, _, err := retagBoundTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	if err := retagged.(installedUUIDOsspFunctionExpression).validate(view); err != nil {
		t.Fatal(err)
	}
	rendered, err := (scalarRenderer{}).sql(context.Background(), view, retagged)
	if err != nil || !strings.Contains(rendered, "uuid_generate_v4") || !strings.Contains(rendered, "public") || strings.Contains(rendered, "pg_catalog") {
		t.Fatal("installed renderer", rendered, err)
	}
	mapped, err := remapBoundExpression(n, func(ref ObjectRef) ObjectRef { ref.Epoch++; return ref }, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := mapped.(installedUUIDOsspFunctionExpression)
	if m.ref.Epoch != n.ref.Epoch+1 || m.procedure != n.procedure || m.Name != "uuidv4" || plannedVolatility(m) != Volatile {
		t.Fatal("remap lost captured provider", m)
	}
	proc := remapExtensionDescriptor(n.procedure, map[ObjectAddress]ObjectAddress{n.procedure.address: {ClassID: ProcedureClassID, ObjectID: 999999}}).(*procedureDescriptor)
	if proc.uuidOssp != stockUUIDOssp || proc.signature != "" || proc.result != 2950 || proc.address == n.procedure.address {
		t.Fatal(proc)
	}
	for _, mutate := range []func(*installedUUIDOsspFunctionExpression){
		func(x *installedUUIDOsspFunctionExpression) { x.Name = "gen_random_uuid" },
		func(x *installedUUIDOsspFunctionExpression) { x.ref.Epoch = 0 },
		func(x *installedUUIDOsspFunctionExpression) { copy := *x.procedure; x.procedure = &copy },
		func(x *installedUUIDOsspFunctionExpression) { x.Property.Volatility = Immutable },
		func(x *installedUUIDOsspFunctionExpression) { x.Property.RowDependent = true },
		func(x *installedUUIDOsspFunctionExpression) { x.Property.ParameterDependent = true },
		func(x *installedUUIDOsspFunctionExpression) { x.Property.DomainDependent = true },
		func(x *installedUUIDOsspFunctionExpression) { x.Property.RuntimeDomainConversion = true },
		func(x *installedUUIDOsspFunctionExpression) { x.Timestamp = x.Usage.Type },
	} {
		bad := n
		mutate(&bad)
		if err := bad.validate(view); err == nil {
			t.Fatal("invalid stored identity accepted", bad)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := n.Eval(&EvalContext{Context: ctx}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled leaf demanded session/entropy", err)
	}
}

func TestUUIDOsspControls(t *testing.T) {
	record := extensionOwnerRecord{Name: "uuid-ossp", Schema: "public", Version: "1.1", Owner: "installer", Members: []extensionMemberOwner{{Kind: "procedure", Name: "uuid_generate_v4", Signature: "", Owner: "postgres"}}}
	encode := func(r extensionOwnerRecord) string {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return extensionOwnersMarker + " " + base64.StdEncoding.EncodeToString(data)
	}
	got, recognized, err := decodeExtensionOwners(encode(record))
	if err != nil || !recognized || !reflect.DeepEqual(got, record) {
		t.Fatal("zero-signature owner roundtrip", got, recognized, err)
	}
	for _, mutate := range []func(*extensionOwnerRecord){
		func(r *extensionOwnerRecord) { r.Version = "missing" },
		func(r *extensionOwnerRecord) { r.Members = nil },
		func(r *extensionOwnerRecord) { r.Members = append(r.Members, r.Members[0]) },
		func(r *extensionOwnerRecord) { r.Members[0].Name = "uuidv4" },
		func(r *extensionOwnerRecord) { r.Members[0].Signature = "25" },
		func(r *extensionOwnerRecord) { r.Members[0].Kind = "dictionary" },
		func(r *extensionOwnerRecord) { r.Members[0].Owner = "" },
	} {
		bad := record
		bad.Members = slices.Clone(record.Members)
		mutate(&bad)
		if _, recognized, err := decodeExtensionOwners(encode(bad)); !recognized || err == nil {
			t.Fatal("invalid owner metadata accepted", bad, err)
		}
	}
}

func TestUUIDOsspGroupedExpressionIdentity(t *testing.T) {
	e, _ := uuidOsspUnitInstalled(t)
	view := e.CatalogRead()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "uuid_generate_v4"}}}}
	node, err := binder.bindInstalledUUIDOssp(raw)
	if err != nil {
		t.Fatal(err)
	}
	grouped, err := binder.rewriteGroup(node, nil)
	if err != nil {
		t.Fatal("actual grouping rewrite", err)
	}
	n := grouped.(installedUUIDOsspFunctionExpression)
	if !n.Properties().StageDependent || n.Properties().Volatility != Volatile || n.procedure != node.(installedUUIDOsspFunctionExpression).procedure {
		t.Fatal("grouping lost staged volatility/installed identity", n)
	}
	if closedUUIDDefault(grouped) {
		t.Fatal("grouped execution leaf admitted as a closed DEFAULT")
	}
	if err := n.validate(view); err != nil {
		t.Fatal("legitimate grouped installed identity rejected", err)
	}
	for _, mutate := range []func(*installedUUIDOsspFunctionExpression){
		func(x *installedUUIDOsspFunctionExpression) { x.Property.RowDependent = true },
		func(x *installedUUIDOsspFunctionExpression) { x.Property.ParameterDependent = true },
		func(x *installedUUIDOsspFunctionExpression) { x.Property.DomainDependent = true },
		func(x *installedUUIDOsspFunctionExpression) { x.Property.RuntimeDomainConversion = true },
		func(x *installedUUIDOsspFunctionExpression) { x.Property.Volatility = Stable },
	} {
		bad := n
		mutate(&bad)
		if err := bad.validate(view); err == nil || closedUUIDDefault(bad) {
			t.Fatal("group stage masked invalid leaf properties or relaxed DEFAULT", bad)
		}
	}
	retagged, _, err := retagBoundTypes(view, grouped, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal("retagging legitimate grouped leaf", err)
	}
	mapped, err := remapBoundExpression(retagged, func(ref ObjectRef) ObjectRef { return ref }, nil)
	if err != nil {
		t.Fatal("remapping legitimate grouped leaf", err)
	}
	rebound, _, err := retagBoundTypes(view, mapped, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal("retagging remapped grouped leaf", err)
	}
	got := rebound.(installedUUIDOsspFunctionExpression)
	if got.ref != n.ref || got.procedure != n.procedure || !got.Properties().StageDependent || plannedVolatility(got) != Volatile || closedUUIDDefault(got) {
		t.Fatal("grouped remap lost identity/stage or relaxed DEFAULT", got)
	}
}
