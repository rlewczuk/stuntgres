package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestEarthCoordinateProvider(t *testing.T) {
	e, ext, earth := earthUnitInstalled(t)
	view := e.CatalogRead()
	for i, a := range ext.Members[2:extension.EarthPointProcedureSlot] {
		proc := view.MustProcedure(a)
		symbol, err := extension.ProcedureSymbol(view, proc)
		if err != nil || symbol != sqltype.EarthOperation(i).Name() || proc.Strict != (i != 0) || proc.Volatility != expr.Immutable {
			t.Fatal("selected slot", i, symbol, err)
		}
		if err := extension.StockEarthdistance.ValidateProcedure(view, proc); err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Strict = !p.Strict }, func(p *catalog.Procedure) { p.Result = 23 }, func(p *catalog.Procedure) { p.Signature = "25" }, func(p *catalog.Procedure) { p.Implementation = extension.StockCube }, func(p *catalog.Procedure) { p.Extension = ids.ObjectAddress{} }, func(p *catalog.Procedure) { p.Volatility = expr.Volatile },
		} {
			next := catalog.CloneState(view.State())
			bad := *proc
			mutate(&bad)
			next.SetDescriptor(a, &bad)
			if err := extension.StockEarthdistance.ValidateExtension(view.WithState(next), ext); err == nil {
				t.Fatal("forged manifest admitted", i)
			}
		}
	}
	for _, members := range [][]ids.ObjectAddress{ext.Members[:2], ext.Members[:7], append(append([]ids.ObjectAddress(nil), ext.Members...), ext.Members[2])} {
		bad := *ext
		bad.Members = members
		if err := extension.StockEarthdistance.ValidateExtension(view, &bad); err == nil {
			t.Fatal("incomplete/duplicate manifest admitted")
		}
	}
	// A staged same-signature conflict cannot publish even a valid prefix.
	before := e.Catalog.State
	stage := e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	original := view.MustProcedure(ext.Members[5])
	duplicate := *original
	duplicate.ObjectAddress.ObjectID = 900000
	var conflict catalog.Conflict
	if err := stage.Create(&duplicate); !errors.As(err, &conflict) || e.Catalog.State != before {
		t.Fatal("staged conflict atomicity", err)
	}
	if earth.Array == 0 {
		t.Fatal("lost companion")
	}
}

func TestEarthCoordinateOwners(t *testing.T) {
	earthOwnerCodecUnits(t)
	e, ext, _ := earthUnitInstalled(t)
	view := e.CatalogRead()
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, view)
	text, err := extension.OwnerMetadata(view, roles, ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(text), "-- "))
	if err != nil || !found {
		t.Fatal("owner codec", err)
	}
	for slot, signature := range map[int]string{2: "", 3: "701", 4: "701", 5: "701,701", 6: "earth", 7: "earth"} {
		if record.Members[slot].Signature != signature {
			t.Fatal("nonportable signature", slot)
		}
	}
	encode := func(r extension.OwnerRecord) string {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data)
	}
	for _, mutate := range []func(*extension.OwnerRecord){
		func(r *extension.OwnerRecord) { r.Members = r.Members[:2] }, func(r *extension.OwnerRecord) { r.Members = r.Members[:7] }, func(r *extension.OwnerRecord) { r.Members[6].Signature = "701" }, func(r *extension.OwnerRecord) { r.Members[5].Slot = 4 }, func(r *extension.OwnerRecord) { r.Members[0].Owner = "different" }, func(r *extension.OwnerRecord) { r.Members[1].Schema = "different" }, func(r *extension.OwnerRecord) { r.Members[7].Kind = "type" }, func(r *extension.OwnerRecord) { r.Version = "1.1" },
	} {
		bad := record
		bad.Members = append([]extension.MemberOwner(nil), record.Members...)
		mutate(&bad)
		if _, _, err := extension.DecodeOwners(encode(bad)); err == nil {
			t.Fatal("invalid owner record admitted", bad)
		}
	}
	reversed := record
	reversed.Members = append([]extension.MemberOwner(nil), record.Members...)
	for i, j := 0, len(reversed.Members)-1; i < j; i, j = i+1, j-1 {
		reversed.Members[i], reversed.Members[j] = reversed.Members[j], reversed.Members[i]
	}
	if _, _, err := extension.DecodeOwners(encode(reversed)); err != nil {
		t.Fatal("record order became authority", err)
	}
	candidate := catalog.CloneState(view.State())
	if _, err := extension.ImportOwners(context.Background(), view.WithState(candidate), roles, []extension.OwnerRecord{reversed}); err != nil {
		t.Fatal("reordered genuine import", err)
	}
	bad := record
	bad.Members = append([]extension.MemberOwner(nil), record.Members...)
	bad.Members[1].Owner = "different"
	if _, err := extension.ImportOwners(context.Background(), view.WithState(candidate), roles, []extension.OwnerRecord{bad}); err == nil {
		t.Fatal("direct import bypassed role consistency")
	}
}

func TestEarthCoordinateExpression(t *testing.T) {
	ctx := context.Background()
	source, ext, earth := earthUnitInstalled(t)
	before := source.CatalogRead()
	floating, _ := before.LookupType(sqltype.Float8)
	cube := earth.SQLType.DomainBase().Type
	target := New()
	owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
	defer closeOwner()
	namespace := intarrayUnitTemporaryNamespace(t, target, owner)
	installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
	extensionUnitPublish(t, target, installer, cubeUnitStage(t, target, installer, "unaccent", namespace.Name()))
	closeInstaller()
	if _, err := target.ReplaceCatalog(ctx, source, before); err != nil {
		t.Fatal(err)
	}
	after := target.CatalogRead()
	importedExt := after.MustExtension(after.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "earthdistance"}))
	imported := after.MustType(importedExt.Members[0])
	importedCube := imported.SQLType.DomainBase().Type
	if imported.SQLType == earth.SQLType || importedCube == cube {
		t.Fatal("destination descriptor setup did not remint")
	}
	importing := catalog.CloneState(after.State())
	importing.SetScalarImports(map[*sqltype.Descriptor]*sqltype.Descriptor{earth.SQLType: imported.SQLType, cube: importedCube})
	importView := after.WithState(importing)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): importedExt.Address(), {ClassID: ids.TypeClassID, ObjectID: ids.OID(cube.ID())}: {ClassID: ids.TypeClassID, ObjectID: ids.OID(importedCube.ID())}}
	for i, a := range ext.Members {
		mapping[a] = importedExt.Members[i]
	}
	mapRef := func(r ids.ObjectRef) ids.ObjectRef {
		a := r.Address
		if mapped, ok := mapping[a]; ok {
			a = mapped
		}
		return target.Catalog.Domain.Ref(after.Epoch(), a)
	}
	mapType := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == earth.SQLType {
			return imported.SQLType
		}
		if d == cube {
			return importedCube
		}
		if d == nil {
			return nil
		}
		n, _ := after.LookupType(d.ID())
		return n
	}
	mapperProc := extension.ProcedureRemapper(mapping, replacementProcedurePolicyFixture(source, target), nil)
	for slot := 2; slot < extension.EarthPointProcedureSlot; slot++ {
		op := sqltype.EarthOperation(slot - 2)
		proc := before.MustProcedure(ext.Members[slot])
		args := make([]expr.Expr, op.Arity())
		formal := floating
		if op >= sqltype.EarthLatitude {
			formal = earth.SQLType
		}
		for i := range args {
			v := sqltype.Null(formal)
			if formal == floating {
				v, _ = sqltype.Float(floating, 0)
			}
			args[i] = plan.LiteralExpr{Info: expr.Info{Usage: formal.Usage()}, Value: v}
		}
		result := floating
		if op == sqltype.EarthLL {
			result = earth.SQLType
		}
		info := expr.Info{Usage: result.Usage(), RoutineReference: proc, Property: expr.Properties{Volatility: expr.Immutable}}
		kernel, err := functions.NewEarthCoordinate(info, op, earth.SQLType, args)
		if err != nil {
			t.Fatal(err)
		}
		node := plan.InstalledEarthExpr{EarthCoordinate: kernel, Procedure: proc, Ref: source.Catalog.Domain.Ref(before.Epoch(), proc.Address())}
		if err := stored.ValidateInstalledEarth(node, before); err != nil {
			t.Fatal(err)
		}
		for _, rewrite := range []func() (expr.Expr, error){func() (expr.Expr, error) { return node.Rebuild(info, args) }, func() (expr.Expr, error) { return node.Substitute(info, args) }} {
			next, err := rewrite()
			n, ok := next.(plan.InstalledEarthExpr)
			if err != nil || !ok || n.Ref != node.Ref || n.Procedure != proc || !analyze.SameGroupExpression(node, n) {
				t.Fatal("rewrite shed identity", err)
			}
		}
		sql, err := (stored.ScalarRenderer{}).SQL(ctx, before, node)
		if err != nil || !strings.Contains(sql, op.Name()) {
			t.Fatal("stored rendering", sql, err)
		}
		counterfeit := node
		copy := *proc
		counterfeit.Procedure = &copy
		if err := stored.ValidateInstalledEarth(counterfeit, before); err == nil {
			t.Fatal("counterfeit procedure accepted")
		}
		broken := node
		broken.Ref.Address = ext.Address()
		if err := stored.ValidateInstalledEarth(broken, before); err == nil {
			t.Fatal("foreign reference accepted")
		}
		badResult := node
		badResult.Usage.Type, _ = before.LookupType(sqltype.Int4)
		if err := stored.ValidateInstalledEarth(badResult, before); err == nil {
			t.Fatal("wrong result accepted")
		}
		unsupported := node
		unsupported.EarthCoordinate = functions.EarthCoordinate{}
		if err := stored.ValidateInstalledEarth(unsupported, before); err == nil {
			t.Fatal("unsupported kernel accepted")
		}
		for _, fresh := range sqltype.Builtins() {
			if fresh.ID() != sqltype.Float8 {
				continue
			}
			bad := node
			if op == sqltype.EarthRadiusOperation {
				bad.Usage.Type = fresh
			} else if formal == floating {
				bad.Args = append([]expr.Expr(nil), args...)
				bad.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: fresh.Usage()}, Value: sqltype.Null(fresh)}
			} else {
				continue
			}
			if err := stored.ValidateInstalledEarth(bad, before); err == nil {
				t.Fatal("same-OID foreign float generation accepted")
			}
		}
		if formal == earth.SQLType {
			foreign, err := sqltype.Domain(earth.SQLType.ID(), "earth", "public.earth", cube.Usage(), true)
			if err != nil {
				t.Fatal(err)
			}
			bad := node
			bad.Args = []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: foreign.Usage()}, Value: sqltype.Null(foreign)}}
			if err := stored.ValidateInstalledEarth(bad, before); err == nil {
				t.Fatal("same-OID foreign domain generation accepted")
			}
		}
		query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: node.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{node}}
		mapper := stored.ViewQueryRemapper{Catalog: importView, Ref: mapRef, Type: mapType, Procedure: mapperProc}
		remapped, err := mapper.Query(query)
		if err != nil {
			t.Fatal("coordinate remap", slot, err)
		}
		n := remapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledEarthExpr)
		if n.Plan.Earth() != imported.SQLType || n.Ref != mapRef(node.Ref) || !exec.SameRoutineDeclaration(after.MustProcedure(n.Ref.Address), n.Procedure) {
			t.Fatal("remap shed destination authority")
		}
		if err := stored.ValidateInstalledEarth(n, after); err != nil {
			t.Fatal(err)
		}
		stale := n
		stale.Procedure = proc
		if err := stored.ValidateInstalledEarth(stale, after); err == nil {
			t.Fatal("source procedure accepted after remap")
		}
		if node.Plan.Earth() != earth.SQLType || node.Procedure != proc {
			t.Fatal("remap mutated source")
		}
	}
}
