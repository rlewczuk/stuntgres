package enginetests_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestAccessRelationsOriginalAuthority(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	other, otherOp, otherScope := strictNullUnitFixture(t)
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	for _, family := range []struct {
		name       string
		first      ids.OID
		kind       functions.AccessKind
		signatures []string
	}{
		{"has_table_privilege", 1922, functions.AccessTable, []string{"19,25,25", "19,26,25", "26,25,25", "26,26,25", "25,25", "26,25"}},
		{"has_sequence_privilege", 2181, functions.AccessSequence, []string{"19,25,25", "19,26,25", "26,25,25", "26,26,25", "25,25", "26,25"}},
		{"has_column_privilege", 3012, functions.AccessColumnPrivilege, []string{"19,25,25,25", "19,25,21,25", "19,26,25,25", "19,26,21,25", "26,25,25,25", "26,25,21,25", "26,26,25,25", "26,26,21,25", "25,25,25", "25,21,25", "26,25,25", "26,21,25"}},
		{"has_any_column_privilege", 3024, functions.AccessAnyColumn, []string{"19,25,25", "19,26,25", "26,25,25", "26,26,25", "25,25", "26,25"}},
	} {
		for i, signature := range family.signatures {
			t.Run(fmt.Sprint(family.first+ids.OID(i)), func(t *testing.T) {
				var args []string
				for j, id := range strings.Split(signature, ",") {
					args = append(args, fmt.Sprintf("$%d::%s", j+1, map[string]string{"19": "name", "25": "text", "26": "oid", "21": "smallint"}[id]))
				}
				node, binder, evaluation := strictNullUnitBind(t, e, op, scope, family.name+"("+strings.Join(args, ",")+")")
				call, ok := node.(functions.AccessInquiry)
				if !ok || call.Kind != family.kind || call.Count != len(args) || call.Validate() != nil {
					t.Fatal("typed carrier", node)
				}
				captured, err := exec.ExpressionRoutineDescriptor(call)
				if err != nil || captured == nil || captured.Builtin == nil {
					t.Fatal("selected declaration", err)
				}
				identity := catalog.RoutineIdentity{family.first + ids.OID(i), ids.PGCatalogNamespaceOID, family.name, catalog.ProcedureSignature(signature)}
				d := captured.Builtin
				if d.Identity != identity || d.Kind != catalog.FunctionRoutine || d.Result != 16 || !d.Strict || d.Volatility != expr.Stable || d.Parallel != 's' || d.ReturnsSet || d.Defaults != 0 || d.Variadic != 0 || d.Providers != functions.CatalogRoutineProvider {
					t.Fatal("released declaration", d)
				}
				if err := exec.AdmitRoutineCall(&evaluation, call, false, false); err != nil {
					t.Fatal(err)
				}
				rebuilt, err := call.Rebuild(call.Info, append([]expr.Expr(nil), call.Children()...))
				if err != nil {
					t.Fatal(err)
				}
				retained, err := exec.ExpressionRoutineDescriptor(rebuilt)
				if err != nil || retained != captured {
					t.Fatal("rebuild lost selected authority", err)
				}
				if err := exec.AdmitRoutineCall(&evaluation, rebuilt, false, false); err != nil {
					t.Fatal("rebuilt authority", err)
				}
				rendered, err := call.Render()
				if err != nil || rendered.Name != family.name || len(rendered.Args) != call.Count {
					t.Fatal("rendered typed authority", err)
				}
				if _, ok, err := call.Capture(expr.CaptureFunction); err != nil || !ok {
					t.Fatal("capture", ok, err)
				}
				for written := range call.Count {
					child, err := call.CaptureOperand(written)
					if err != nil || child != call.Args[written] {
						t.Fatal("capture operand authority", written, err)
					}
				}
				original := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
				var missing []catalog.ExpressionDependency
				selected := 0
				for _, dep := range original {
					if dep.Ref.Address == captured.Address() {
						selected++
						if dep.Revision != captured.Revision() || dep.Ref.Domain() != e.Catalog.Domain {
							t.Fatal("original lifetime", dep)
						}
					} else {
						missing = append(missing, dep)
					}
				}
				if selected != 1 {
					t.Fatal("manifest multiplicity", selected)
				}
				for _, manifest := range [][]catalog.ExpressionDependency{nil, {}, missing} {
					probe := evaluation
					probe.RoutineChecks = manifest
					if exec.AdmitRoutineCall(&probe, call, false, false) == nil {
						t.Fatal("missing original manifest admitted")
					}
				}
				for _, mutation := range []string{"foreign", "epoch", "revision"} {
					checks := append([]catalog.ExpressionDependency(nil), original...)
					for j := range checks {
						if checks[j].Ref.Address == captured.Address() {
							switch mutation {
							case "foreign":
								checks[j].Ref = other.Catalog.Domain.Ref(checks[j].Ref.Epoch, checks[j].Ref.Address)
							case "epoch":
								checks[j].Ref.Epoch++
							case "revision":
								checks[j].Revision++
							}
						}
					}
					probe := evaluation
					probe.RoutineChecks = checks
					if exec.AdmitRoutineCall(&probe, call, false, false) == nil {
						t.Fatal("invalid original lifetime", mutation)
					}
				}
				probe := evaluation
				probe.RoutineAuthority = foreign.RoutineAuthority
				if exec.AdmitRoutineCall(&probe, call, false, false) == nil {
					t.Fatal("foreign authority admitted")
				}
				mapped, _, err := stored.RetagTypes(other.CatalogRead(), call, func(old *sqltype.Descriptor) *sqltype.Descriptor {
					if old == nil {
						return nil
					}
					typ, _ := other.CatalogRead().LookupType(old.ID())
					return typ
				})
				if err != nil {
					t.Fatal(err)
				}
				if exec.AdmitRoutineCall(&probe, mapped, false, false) == nil {
					t.Fatal("foreign retag graft admitted")
				}
				invalid := call
				invalid.RoutineReference = (*catalog.Procedure)(nil)
				if exec.AdmitRoutineCall(&evaluation, invalid, false, false) == nil {
					t.Fatal("typed nil admitted")
				}
				for _, mutation := range []string{"strict", "provider", "formal"} {
					changed := *core.RoutineAuthorityView(&evaluation)
					changed.Recapture(changed.Catalog.WithState(catalog.CloneState(changed.Catalog.State())))
					replacement := *captured
					declaration := *captured.Builtin
					replacement.Builtin = &declaration
					switch mutation {
					case "strict":
						replacement.Strict = false
					case "provider":
						declaration.Providers = functions.OperatorRoutineProvider
					case "formal":
						replacement.Signature = "25"
					}
					changed.Catalog.State().SetDescriptor(captured.Address(), &replacement)
					probe = evaluation
					probe.RoutineAuthority = &changed
					if exec.AdmitRoutineCall(&probe, call, false, false) == nil {
						t.Fatal("incompatible current declaration", mutation)
					}
				}
				// ACL-only COW preserves the original declaration/lifetime, but
				// current execution authority still decides this new admission.
				changed := *core.RoutineAuthorityView(&evaluation)
				changed.Recapture(changed.Catalog.WithState(catalog.CloneState(changed.Catalog.State())))
				changed.State.CurrentUser = changed.Roles.LookupRef("pg_monitor")
				probe = evaluation
				probe.RoutineAuthority = &changed
				if err := exec.AdmitRoutineCall(&probe, call, false, false); err != nil {
					t.Fatal("PUBLIC baseline", err)
				}
				replacement := *captured
				replacement.ObjectRevision++
				replacement.ACL = authorization.ACLValue{Explicit: true}
				if !exec.SameRoutineDeclaration(captured, &replacement) {
					t.Fatal("ACL COW changed declaration")
				}
				changed.Catalog.State().SetDescriptor(captured.Address(), &replacement)
				err = exec.AdmitRoutineCall(&probe, call, false, false)
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for function "+family.name {
					t.Fatal("current EXECUTE was not checked across ACL COW", err)
				}
				granted := replacement
				granted.ObjectRevision++
				granted.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: captured.Owner, Grantee: authorization.ACLGrantee{Role: changed.State.CurrentUser}, Privileges: authorization.ExecutePrivilege}}}
				changed.Catalog.State().SetDescriptor(captured.Address(), &granted)
				if err := exec.AdmitRoutineCall(&probe, call, false, false); err != nil {
					t.Fatal("fresh current grant did not restore admission", err)
				}
			})
		}
	}
}
