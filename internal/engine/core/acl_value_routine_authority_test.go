package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Released identities are independent of the production inventory. These probes
// exercise actual bound carriers and original manifests, not public SQL results.
func TestACLValueRoutineOriginalAuthority(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	other, otherOp, otherScope := strictNullUnitFixture(t)
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	for _, tc := range []struct {
		name, expression, signature string
		oid, result                 ids.OID
		kind                        functions.ACLKind
		volatility                  expr.Volatility
		set                         bool
		provider                    functions.RoutineProvider
		formals                     []sqltype.ID
	}{
		{"acldefault", "acldefault('r',$1::oid)", "18,26", 3943, 1034, functions.ACLDefault, expr.Immutable, false, functions.CatalogRoutineProvider, []sqltype.ID{18, 26}},
		{"makeaclitem", "makeaclitem($1::oid,$2::oid,$3::text,$4::boolean)", "26,26,25,16", 1365, 1033, functions.ACLMake, expr.Immutable, false, functions.CatalogRoutineProvider, []sqltype.ID{26, 26, 25, 16}},
		{"aclexplode", "aclexplode(acl => $1::aclitem[])", "1034", 1689, 2249, functions.ACLExplode, expr.Stable, true, functions.CatalogRoutineProvider, []sqltype.ID{1034}},
		{"aclcontains", "$1::aclitem[] @> $2::aclitem", "1034,1033", 1037, 16, functions.ACLContains, expr.Immutable, false, functions.OperatorRoutineProvider, []sqltype.ID{1034, 1033}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
			call, ok := node.(functions.ACLValue)
			if !ok || call.Kind != tc.kind || len(call.Children()) != len(tc.formals) {
				t.Fatal("actual ACL carrier/full IN arity", node)
			}
			for i, formal := range tc.formals {
				if call.Args[i].ResultUsage().Type.ID() != formal {
					t.Fatal("physical coerced IN type", i, call.Args[i].ResultUsage(), formal)
				}
			}
			if got := ids.OID(call.ResultUsage().Type.ID()); got != tc.result {
				t.Fatalf("physical result ID: got %d, want %d", got, tc.result)
			}
			if got := call.Properties().Volatility; got != tc.volatility {
				t.Fatalf("physical volatility: got %v, want %v", got, tc.volatility)
			}
			if got := exec.SourceReturnsSet(call); got != tc.set {
				t.Fatalf("sourceReturnsSet: got %t, want %t", got, tc.set)
			}
			// Containment is deliberately not a function-source capability.
			if got, want := exec.SourceAdmitted(call), tc.kind != functions.ACLContains; got != want {
				t.Fatalf("sourceAdmitted: got %t, want %t", got, want)
			}
			// bindACLValue returns the bare carrier directly, without setting
			// the binder's target-set observation. Actual set classification
			// above and source/project admission below own this SRF contract.
			if binder.Clause.QueryHasTargetSet {
				t.Fatalf("bare ACL binder queryHasTargetSet: got %t, want false", binder.Clause.QueryHasTargetSet)
			}
			if tc.set {
				shape := call.ResultUsage().Type
				if !call.Named || shape.CompositeLen() != 4 {
					t.Fatal("named sole IN/four OUT shape", call)
				}
				for i, out := range []struct {
					name string
					typ  sqltype.ID
				}{{"grantor", 26}, {"grantee", 26}, {"privilege_type", 25}, {"is_grantable", 16}} {
					member := shape.CompositeMember(i)
					if member.Attribute != int32(i+1) || member.Name != out.name || member.Usage.Type.ID() != out.typ {
						t.Fatal("independent OUT record layout", i, member, out)
					}
				}
			}
			captured, err := exec.ExpressionRoutineDescriptor(call)
			if err != nil || captured == nil || captured.Builtin == nil {
				t.Fatal("missing selected original declaration", captured, err)
			}
			address := ids.ObjectAddress{ids.ProcedureClassID, tc.oid, 0}
			declaration := captured.Builtin
			identity := catalog.RoutineIdentity{tc.oid, ids.PGCatalogNamespaceOID, tc.name, catalog.ProcedureSignature(tc.signature)}
			if declaration.Identity != identity || declaration.Kind != catalog.FunctionRoutine || declaration.Result != tc.result || !declaration.Strict || declaration.Volatility != tc.volatility || declaration.Parallel != 's' || declaration.ReturnsSet != tc.set || declaration.Defaults != 0 || declaration.Variadic != 0 || declaration.Providers != tc.provider {
				t.Fatal("independent released full-IN declaration", declaration)
			}
			if captured.Address() != address || captured.Namespace() != (ids.ObjectAddress{ids.NamespaceClassID, 11, 0}) || captured.Name() != tc.name || string(captured.Signature) != tc.signature || captured.Result != tc.result || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != tc.volatility || captured.Revision() == 0 || len(captured.Body) != 0 {
				t.Fatal("selected owned descriptor/no SQL helper body", captured)
			}
			original := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
			selected := 0
			var missing []catalog.ExpressionDependency
			for _, dependency := range original {
				if dependency.Ref.Address == address {
					selected++
					if dependency.Ref.Domain() != e.Catalog.Domain || dependency.Ref.Epoch != evaluation.Catalog.Epoch() || dependency.Revision != captured.Revision() {
						t.Fatal("original selected lifetime", dependency)
					}
				} else {
					missing = append(missing, dependency)
					if dependency.Ref.Address.ClassID == ids.ProcedureClassID {
						t.Fatal("implicit constructor/codec/helper EXECUTE edge", dependency)
					}
				}
			}
			if selected != 1 {
				t.Fatal("selected original dependency multiplicity", selected)
			}
			// In particular, aclexplode must pass actual source/project admission;
			// scalar planning intentionally skips SRFs and cannot prove authority.
			if err := exec.AdmitRoutineCall(&evaluation, call, false, false); err != nil {
				t.Fatal("original source/project authority", err)
			}
			for _, manifest := range [][]catalog.ExpressionDependency{nil, {}, missing} {
				probe := evaluation
				probe.RoutineChecks = manifest
				if err := exec.AdmitRoutineCall(&probe, call, false, false); err == nil {
					t.Fatal("missing original lifetime repaired from current OID")
				}
			}
			for _, mode := range []string{"foreign", "epoch", "revision"} {
				checks := append([]catalog.ExpressionDependency(nil), original...)
				for i := range checks {
					if checks[i].Ref.Address == address {
						switch mode {
						case "foreign":
							checks[i].Ref = other.Catalog.Domain.Ref(checks[i].Ref.Epoch, checks[i].Ref.Address)
						case "epoch":
							checks[i].Ref.Epoch++
						case "revision":
							checks[i].Revision++
						}
					}
				}
				probe := evaluation
				probe.RoutineChecks = checks
				if err := exec.AdmitRoutineCall(&probe, call, false, false); err == nil {
					t.Fatal("invalid selected original lifetime admitted", mode)
				}
			}
			probe := evaluation
			probe.RoutineAuthority = foreign.RoutineAuthority
			if err := exec.AdmitRoutineCall(&probe, call, false, false); err == nil {
				t.Fatal("equal-OID foreign authority admitted")
			}
			remapped, _, err := stored.RetagTypes(other.CatalogRead(), call, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == nil || old.ID() == 2249 {
					return old
				}
				current, _ := other.CatalogRead().LookupType(old.ID())
				return current
			})
			if err != nil {
				t.Fatal("actual ACL carrier retag", err)
			}
			mapped := remapped.(functions.ACLValue)
			if mapped.Kind != call.Kind || mapped.Named != call.Named || mapped.Namespace != call.Namespace || mapped.SourcePosition() != call.SourcePosition() || mapped.Properties() != call.Properties() {
				t.Fatal("retag lost selected carrier contract", mapped, err)
			}
			for _, manifest := range [][]catalog.ExpressionDependency{original, nil, {}} {
				probe.RoutineChecks = manifest
				if err := exec.AdmitRoutineCall(&probe, mapped, false, false); err == nil {
					t.Fatal("retag manufactured original destination authority")
				}
			}
			changed := *engine.RoutineAuthorityView(&evaluation)
			changed.Catalog = changed.Catalog.WithState(catalog.CloneState(changed.Catalog.State()))
			replacement := *captured
			replacement.Strict = false
			changed.Catalog.State().SetDescriptor(address, &replacement)
			probe = evaluation
			probe.RoutineAuthority = &changed
			if err := exec.AdmitRoutineCall(&probe, call, false, false); err == nil {
				t.Fatal("incompatible current declaration admitted")
			}
			invalid := call
			invalid.RoutineReference = (*catalog.Procedure)(nil)
			if err := exec.AdmitRoutineCall(&evaluation, invalid, false, false); err == nil {
				t.Fatal("typed-nil selected descriptor admitted")
			}
			if !reflect.DeepEqual(binder.Dependencies, original) {
				t.Fatal("negative probes mutated original manifest")
			}
			if err := exec.AdmitRoutineCall(&evaluation, call, false, false); err != nil {
				t.Fatal("intact original authority changed after probes", err)
			}
		})
	}
}

// Bind actual supported providers under genuine catalog/statement authority.
// SQL creates incidental state; assertions exercise private planning and original
// typed frames, not public SQL response witnesses or synthetic probe providers.
func strictNullUnitFixture(t *testing.T) (*engine.Engine, *session.Operation, *engine.StatementScope) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	for _, query := range []string{
		"CREATE TABLE strict_null_rows(v integer)",
		"CREATE SEQUENCE strict_null_steps START 1",
		"CREATE DOMAIN strict_null_checked AS integer CONSTRAINT strict_null_present CHECK(VALUE IS NOT NULL)",
	} {
		enginetest.RoleExecute(t, e, op, query)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	return e, op, scope
}

func strictNullUnitBind(t *testing.T, e *engine.Engine, op *session.Operation, scope *engine.StatementScope, text string) (expr.Expr, *analyze.Binder, exec.Context) {
	t.Helper()
	raw := enginetest.Statement(t, "SELECT "+text).(*parser.Select).Targets[0]
	ref := enginetest.RoleTableRef(t, e, "strict_null_rows")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b := engine.NewExpressionBinder(context.Background(), e, scope.Input, op, nil)
	b.Query.Auth, b.Target.Ref = &auth, ref
	b.Target.Relation = auth.Catalog.MustRelation(ref.Address)
	bindTestTargetNamespace(b)
	b.Clause.AggregateClause = "SELECT"
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal("actual provider binding", text, err)
	}
	return node, b, exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: engine.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
}

// Scalar unit fixtures explicitly admit their input row, just like production owners.
func bindTestTargetNamespace(b *analyze.Binder) {
	relation := b.Target.Relation
	columns := make([]analyze.QueryColumn, len(relation.Columns))
	for i, column := range relation.Columns {
		columns[i] = analyze.QueryColumn{Descriptor: column, Slot: i, Physical: i, Leaf: analyze.QueryTargetLeaf}
	}
	qualifier := analyze.QueryQualifier{Name: relation.Name(), Original: relation.Name(), Ref: b.Target.Ref, Columns: columns}
	if relation.Namespace() != (ids.ObjectAddress{}) {
		qualifier.Schema = b.BindingCatalog().Get(relation.Namespace()).Name()
	}
	b.Query.Namespace = &analyze.QueryNamespace{Columns: columns, Qualifiers: []analyze.QueryQualifier{qualifier}, Frame: relation.Columns}
}
