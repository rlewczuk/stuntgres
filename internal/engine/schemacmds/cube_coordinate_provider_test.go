package schemacmds_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func cubeCoordinateUnitMembers(t *testing.T, view catalog.Read, ext *catalog.Extension, base *catalog.Type) ([]ids.ObjectAddress, []ids.ObjectAddress) {
	t.Helper()
	var procedures, operators []ids.ObjectAddress
	for _, name := range []string{"cube_ll_coord", "cube_ur_coord", "cube_coord", "cube_coord_llur"} {
		a := view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: name, Signature: string(extension.CubeSignature(base.SQLType.ID())) + ",23"})
		if a == (ids.ObjectAddress{}) {
			t.Fatal("missing routine", name)
		}
		procedures = append(procedures, a)
	}
	for _, name := range []string{"->", "~>"} {
		a := view.NameAddress(ids.NameKey{ClassID: ids.OperatorClassID, Namespace: ext.TargetNamespace, Name: name, Signature: string(extension.CubeSignature(base.SQLType.ID())) + ",23"})
		if a == (ids.ObjectAddress{}) {
			t.Fatal("missing operator", name)
		}
		operators = append(operators, a)
	}
	return procedures, operators
}

func TestCubeCoordinateProviderIdentity(t *testing.T) {
	e, ext, base := cubeUnitInstalled(t)
	view := e.CatalogRead()
	procedures, operators := cubeCoordinateUnitMembers(t, view, ext, base)
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	v, err := base.SQLType.Input(context.Background(), "(3,1),(1,4)", -1)
	if err != nil {
		t.Fatal(err)
	}
	integer, _ := view.LookupType(sqltype.Int4)
	index, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	args := []expr.Expr{binder.Literal(v, parser.Position{}), binder.Literal(index, parser.Position{})}
	for i, address := range procedures {
		proc := view.MustProcedure(address)
		if i >= 2 {
			op := view.MustOperator(operators[i-2])
			if op.Function != address.ObjectID || op.Left != ids.OID(base.SQLType.ID()) || op.Right != 23 || op.Result != 701 || op.Commutator != 0 || op.Negator != 0 || op.Restriction != 0 || op.Join != 0 || op.Merge || op.Hash {
				t.Fatal("operator identity", op)
			}
			if err := (routine.OperatorProvider{}).Validate(view, op); err != nil {
				t.Fatal(err)
			}
		}
		bound, err := binder.BindSelectedOperatorRoutine(proc, args, parser.Position{})
		if err != nil {
			t.Fatal(err)
		}
		call, ok := bound.(plan.InstalledCubeExpr)
		if !ok || call.Procedure != proc || call.Ref.Address != address {
			t.Fatal("selected provider lost", bound)
		}
		if err := stored.ValidateInstalledCube(call, view); err != nil {
			t.Fatal(err)
		}
		result, err := call.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := result.Float()
		if err != nil || got != []float64{1, 3, 3, 1}[i] {
			t.Fatal("installed evaluation", got, err)
		}
		counterfeit := *proc
		call.Procedure = &counterfeit
		if err := stored.ValidateInstalledCube(call, view); err == nil {
			t.Fatal("same-name counterfeit accepted")
		}
		call.Procedure = proc
		foreignCube, err := sqltype.Cube(base.SQLType.ID(), "cube", "public.cube")
		if err != nil {
			t.Fatal(err)
		}
		foreignValue, err := foreignCube.Input(context.Background(), "(3,1),(1,4)", -1)
		if err != nil {
			t.Fatal(err)
		}
		foreignCall := call
		foreignCall.Args = []expr.Expr{binder.Literal(foreignValue, parser.Position{}), args[1]}
		if err := stored.ValidateInstalledCube(foreignCall, view); err == nil {
			t.Fatal("same-OID foreign cube generation accepted")
		}
		for _, rewrite := range []func() (expr.Expr, error){
			func() (expr.Expr, error) { return call.Rebuild(expr.InfoOf(call), call.Args) },
			func() (expr.Expr, error) { return call.Substitute(expr.InfoOf(call), call.Args) },
		} {
			next, err := rewrite()
			retained, ok := next.(plan.InstalledCubeExpr)
			if err != nil || !ok || retained.Procedure != proc || retained.Ref != call.Ref {
				t.Fatal("reconstruction lost installed authority", err)
			}
		}
		for _, fresh := range sqltype.Builtins() {
			if fresh.ID() == sqltype.Int4 {
				foreignIndex, err := index.Retag(fresh)
				if err != nil {
					t.Fatal(err)
				}
				foreign := call
				foreign.Args = []expr.Expr{args[0], binder.Literal(foreignIndex, parser.Position{})}
				if err := stored.ValidateInstalledCube(foreign, view); err == nil {
					t.Fatal("foreign int4 generation accepted")
				}
			}
			if fresh.ID() == sqltype.Float8 {
				foreign := call
				foreign.Usage.Type = fresh
				if err := stored.ValidateInstalledCube(foreign, view); err == nil {
					t.Fatal("foreign float8 generation accepted")
				}
			}
		}
	}
	metadata, err := extension.OwnerMetadata(view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
	if err != nil || !found {
		t.Fatal("owner record", err)
	}
	seen := 0
	for _, member := range record.Members {
		if member.Kind == "operator" && (member.Name == "->" || member.Name == "~>") {
			if member.Signature != "public.cube, integer" {
				t.Fatal("nonportable coordinate ownership", member)
			}
			seen++
		}
	}
	if seen != 2 {
		t.Fatal("missing coordinate owners", seen)
	}
}

func TestCubeCoordinateProviderConflicts(t *testing.T) {
	e, ext, base := cubeUnitInstalled(t)
	procedures, operators := cubeCoordinateUnitMembers(t, e.CatalogRead(), ext, base)
	for _, corrupt := range []func(*catalog.Procedure, *catalog.Operator){
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Signature = "23,23" },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Result = 23 },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Implementation = nil },
		func(p *catalog.Procedure, _ *catalog.Operator) { p.Extension.ObjectID = 990001 },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Right = o.Left },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Function = procedures[0].ObjectID },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Commutator = o.Address().ObjectID },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Negator = o.Address().ObjectID },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Hash = true },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Merge = true },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Restriction = 101 },
		func(_ *catalog.Procedure, o *catalog.Operator) { o.Join = 105 },
	} {
		next := catalog.CloneState(e.Catalog.State)
		p, o := *next.MustProcedure(procedures[2]), *next.MustOperator(operators[0])
		corrupt(&p, &o)
		next.SetDescriptor(p.Address(), &p)
		next.SetDescriptor(o.Address(), &o)
		if err := extension.StockCube.ValidateExtension(e.Catalog.Read(next), ext); err == nil {
			t.Fatal("corrupt manifest admitted")
		}
	}
	missing := *ext
	missing.Members = append([]ids.ObjectAddress(nil), ext.Members...)
	for i, address := range missing.Members {
		if address == operators[0] {
			missing.Members[i] = operators[1]
		}
	}
	if err := extension.StockCube.ValidateExtension(e.CatalogRead(), &missing); err == nil {
		t.Fatal("missing member admitted")
	}
	staging := engine.New()
	op, done := enginetest.RoleOperation(t, staging, "postgres")
	defer done()
	before := staging.Catalog.State
	stage := cubeUnitStage(t, staging, op, "cube", "")
	candidate := stage.State()
	stagedExt := candidate.MustExtension(candidate.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
	stagedBase := candidate.MustType(stagedExt.Members[0])
	ps, os := cubeCoordinateUnitMembers(t, staging.Catalog.Read(candidate), stagedExt, stagedBase)
	for _, address := range append(ps, os...) {
		var duplicate catalog.Descriptor
		switch original := candidate.Get(address).(type) {
		case *catalog.Procedure:
			n := *original
			n.ObjectAddress.ObjectID = 900000
			duplicate = &n
		case *catalog.Operator:
			n := *original
			n.ObjectAddress.ObjectID = 900001
			duplicate = &n
		}
		var conflict catalog.Conflict
		if err := stage.Create(duplicate); !errors.As(err, &conflict) {
			t.Fatal("staged conflict", err)
		}
		if stage.Get(address) != candidate.Get(address) || staging.Catalog.State != before {
			t.Fatal("conflict published mutation")
		}
	}
	if err := extension.StockCube.ValidateExtension(e.CatalogRead(), ext); err != nil {
		t.Fatal("negative validation changed source", err)
	}
}

func TestCubeCoordinateProviderControls(t *testing.T) {
	// The shared genuine import campaign perturbs allocator identities, remaps
	// procedures, retags cube/int4/float8 generations, executes the stored call,
	// and rejects stale providers after publication retires import authority.
	cubeCallableIdentity(t, sqltype.CubeLowerCoordinate, sqltype.CubeSignedCoordinate)
}
