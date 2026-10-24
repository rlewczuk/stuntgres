package engine

import (
	"context"
	"strings"
	"testing"

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
)

func TestCubeConstructorIdentity(t *testing.T) {
	cubeCallableIdentity(t, sqltype.CubeScalar, sqltype.CubeIsPoint)
}

func cubeCallableIdentity(t *testing.T, first, last sqltype.CubeOperation) {
	t.Helper()
	source, ext, base := cubeUnitInstalled(t)
	before := source.CatalogRead()
	floating, _ := before.LookupType(sqltype.Float8)
	array, _ := before.LookupType(1022)
	integer, _ := before.LookupType(sqltype.Int4)
	boolean, _ := before.LookupType(sqltype.Bool)
	ctx := context.Background()
	value := func(d *sqltype.Descriptor, text string) expr.Expr {
		v, err := d.Input(ctx, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: v}
	}
	// A genuine retained TEMP allocator collision forces imported cube OIDs
	// and descriptor generations to differ, using the existing component seam.
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
	importedExt := target.catalog.State.MustExtension(target.catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
	imported := target.catalog.State.MustType(importedExt.Members[0])
	if imported.SQLType == base.SQLType || imported.SQLType.ID() == base.SQLType.ID() {
		t.Fatal("remint setup did not perturb cube identity")
	}
	if after.State().ScalarImports() != nil {
		t.Fatal("published catalog retained temporary scalar import authority")
	}
	// Migration happens against a private staged candidate. ReplaceCatalog
	// correctly retired this source-to-destination capability before publish.
	importing := catalog.CloneState(after.State())
	importing.SetScalarImports(map[*sqltype.Descriptor]*sqltype.Descriptor{base.SQLType: imported.SQLType})
	importView := after.WithState(importing)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): importedExt.Address(), base.Address(): imported.Address()}
	for i, address := range ext.Members {
		mapping[address] = importedExt.Members[i]
	}
	mapRef := func(ref ids.ObjectRef) ids.ObjectRef {
		address := ref.Address
		if next, ok := mapping[address]; ok {
			address = next
		}
		return target.catalog.Domain.Ref(after.Epoch(), address)
	}
	mapType := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == base.SQLType {
			return imported.SQLType
		}
		if d == nil {
			return nil
		}
		next, ok := after.LookupType(d.ID())
		if !ok {
			t.Fatal("missing imported builtin", d.ID())
		}
		return next
	}
	procedureMapper := extension.ProcedureRemapper(mapping, replacementProcedurePolicyFixture(source, target), nil)
	for operation := first; operation <= last; operation++ {
		formals, count := functions.CubeFormals(operation, base.SQLType, floating, array, integer)
		args := make([]expr.Expr, count)
		for i := range args {
			switch formals[i] {
			case base.SQLType:
				args[i] = value(base.SQLType, "(1),(2)")
			case array:
				args[i] = value(array, "{1,2}")
			case integer:
				args[i] = value(integer, "1")
			default:
				args[i] = value(floating, "3")
			}
		}
		result := base.SQLType
		if operation == sqltype.CubeDimension {
			result = integer
		}
		if operation == sqltype.CubeIsPoint {
			result = boolean
		}
		if operation >= sqltype.CubeLowerCoordinate {
			result = floating
		}
		identity := functions.Cube{Operation: operation, Args: args}
		procedure := before.State().MustProcedure(before.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: identity.Name(), Signature: identity.Signature()}))
		info := expr.Info{Usage: result.Usage(), Property: expr.Properties{Volatility: expr.Immutable}, RoutineReference: procedure}
		kernel, err := functions.NewCube(info, operation, base.SQLType, args)
		if err != nil {
			t.Fatal(err)
		}
		node := plan.InstalledCubeExpr{Cube: kernel, Procedure: procedure, Ref: source.catalog.Domain.Ref(before.Epoch(), procedure.Address())}
		if err := stored.ValidateInstalledCube(node, before); err != nil {
			t.Fatal(err)
		}
		if !plan.GenerationImmutable(node) || exec.PlannedVolatility(node) != expr.Immutable {
			t.Fatal("immutable provider", operation)
		}
		for _, rewrite := range []func() (expr.Expr, error){
			func() (expr.Expr, error) { return node.Rebuild(info, args) },
			func() (expr.Expr, error) { return node.Substitute(info, args) },
			func() (expr.Expr, error) {
				n, _, e := node.Retag(info, args, false, expr.Retag{Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }})
				return n, e
			},
		} {
			next, err := rewrite()
			if err != nil {
				t.Fatal(err)
			}
			actual, ok := next.(plan.InstalledCubeExpr)
			if !ok || actual.Operation != operation || actual.CubeType != base.SQLType || actual.Procedure != procedure || actual.Ref != node.Ref || !analyze.SameGroupExpression(node, actual) {
				t.Fatal("rebuild erased authority", next)
			}
		}
		for _, rewrite := range []func() (expr.Expr, error){
			func() (expr.Expr, error) { return kernel.Rebuild(info, args) },
			func() (expr.Expr, error) { return kernel.Substitute(info, args) },
			func() (expr.Expr, error) {
				n, _, e := kernel.Retag(info, args, false, expr.Retag{Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }})
				return n, e
			},
		} {
			next, err := rewrite()
			if err != nil {
				t.Fatal(err)
			}
			actual, ok := next.(functions.Cube)
			if !ok || actual.Operation != operation || actual.CubeType != base.SQLType || actual.ResultUsage() != kernel.ResultUsage() || len(actual.Args) != len(args) {
				t.Fatal("kernel reconstruction lost selection", next)
			}
		}
		other := node
		other.Ref.Address = ext.Address()
		if analyze.SameGroupExpression(node, other) {
			t.Fatal("group equality lost procedure identity")
		}
		if _, err := node.Rebuild(info, args[:len(args)-1]); err == nil {
			t.Fatal("rebuild accepted wrong arity")
		}
		if sql, err := (stored.ScalarRenderer{}).SQL(ctx, before, node); err != nil || !strings.Contains(sql, "public") || !strings.Contains(sql, node.Name()) {
			t.Fatal("qualified stored call", sql, err)
		}
		query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "cube", Usage: node.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{node}}
		if operation >= sqltype.CubeExtend {
			mapper := stored.ViewQueryRemapper{Catalog: after, Ref: mapRef, Type: mapType, Procedure: procedureMapper}
			if _, err := mapper.Query(query); err == nil {
				t.Fatal("published catalog authorized a new cross-OID scalar import")
			}
		}
		mapper := stored.ViewQueryRemapper{Catalog: importView, Ref: mapRef, Type: mapType, Procedure: procedureMapper}
		remapped, err := mapper.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		actual := remapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledCubeExpr)
		if !exec.SameRoutineDeclaration(after.MustProcedure(mapRef(node.Ref).Address), actual.Procedure) || actual.Procedure.Revision() != procedure.Revision() || actual.Ref != mapRef(node.Ref) || actual.CubeType != imported.SQLType || actual.Operation != operation {
			t.Fatal("remap lost authority", actual)
		}
		if err := stored.ValidateInstalledCube(actual, after); err != nil {
			t.Fatal(err)
		}
		for i, arg := range actual.Args {
			if arg.ResultUsage().Type != mapType(args[i].ResultUsage().Type) {
				t.Fatal("formal generation not reminted", i)
			}
		}
		if node.Procedure != procedure || node.CubeType != base.SQLType || node.ResultUsage().Type != result {
			t.Fatal("mapping mutated source")
		}
		if _, err := actual.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}); err != nil {
			t.Fatal("remapped kernel execution", err)
		}
		stale := actual
		stale.Procedure = procedure
		if err := stored.ValidateInstalledCube(stale, after); err == nil {
			t.Fatal("stale provider accepted")
		}
		foreign := actual
		foreign.Args = append([]expr.Expr(nil), actual.Args...)
		arg := actual.Args[0].(plan.LiteralExpr)
		if !arg.ResultUsage().Type.IsCube() {
			var fresh *sqltype.Descriptor
			for _, candidate := range sqltype.Builtins() {
				if candidate.ID() == arg.ResultUsage().Type.ID() {
					fresh = candidate
					break
				}
			}
			if fresh == nil {
				t.Fatal("missing foreign builtin descriptor", arg.ResultUsage().Type.ID())
			}
			arg.Value, err = arg.Value.Retag(fresh)
			if err != nil {
				t.Fatal(err)
			}
			arg.Usage.Type = fresh
			foreign.Args[0] = arg
			if err := stored.ValidateInstalledCube(foreign, after); err == nil {
				t.Fatal("foreign builtin formal generation accepted")
			}
		}
		changed := info
		changed.Property = expr.Properties{Volatility: expr.Volatile, ParameterDependent: true}
		rewritten, err := node.Rebuild(changed, args)
		if err != nil || rewritten.Properties() != changed.Property {
			t.Fatal("rebuild erased child properties", err)
		}
	}
	if after.State().ScalarImports() != nil || target.CatalogRead().State() != after.State() {
		t.Fatal("component migration changed published catalog authority")
	}
}
