package core_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestAccessExecutionInquiryOriginalAuthority(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	other, otherOp, otherScope := strictNullUnitFixture(t)
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	for _, family := range []struct {
		name  string
		first ids.OID
		kind  functions.AccessKind
	}{
		{"has_function_privilege", 2256, functions.AccessFunction},
		{"has_language_privilege", 2262, functions.AccessLanguage},
		{"has_foreign_data_wrapper_privilege", 3000, functions.AccessForeignWrapper},
		{"has_server_privilege", 3006, functions.AccessServer},
		{"has_database_privilege", 2250, functions.AccessDatabase},
		{"has_schema_privilege", 2268, functions.AccessSchema},
		{"has_type_privilege", 3138, functions.AccessType},
		{"has_tablespace_privilege", 2390, functions.AccessTablespace},
	} {
		for i, signature := range []string{"19,25,25", "19,26,25", "26,25,25", "26,26,25", "25,25", "26,25"} {
			var args []string
			for j, id := range strings.Split(signature, ",") {
				typ := map[string]string{"19": "name", "25": "text", "26": "oid"}[id]
				args = append(args, fmt.Sprintf("$%d::%s", j+1, typ))
			}
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, family.name+"("+strings.Join(args, ",")+")")
			call, ok := node.(functions.AccessInquiry)
			if !ok || call.Kind != family.kind || call.Count != len(args) || call.ResultUsage().Type.ID() != sqltype.Bool || call.Properties().Volatility != expr.Stable || call.Validate() != nil {
				t.Fatal("typed carrier", node)
			}
			captured, err := exec.ExpressionRoutineDescriptor(call)
			if err != nil || captured == nil || captured.Builtin == nil {
				t.Fatal("selected declaration", captured, err)
			}
			identity := catalog.RoutineIdentity{family.first + ids.OID(i), ids.PGCatalogNamespaceOID, family.name, catalog.ProcedureSignature(signature)}
			d := captured.Builtin
			if d.Identity != identity || d.Kind != catalog.FunctionRoutine || d.Result != 16 || !d.Strict || d.Volatility != expr.Stable || d.Parallel != 's' || d.ReturnsSet || d.Defaults != 0 || d.Variadic != 0 || d.Providers != functions.CatalogRoutineProvider {
				t.Fatal("independent released identity", d)
			}
			if err := exec.AdmitRoutineCall(&evaluation, call, false, false); err != nil {
				t.Fatal(err)
			}
			original := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
			var missing []catalog.ExpressionDependency
			selected := 0
			for _, dep := range original {
				if dep.Ref.Address == captured.Address() {
					selected++
				} else {
					missing = append(missing, dep)
				}
			}
			if selected != 1 {
				t.Fatal("selected manifest multiplicity", selected)
			}
			for _, manifest := range [][]catalog.ExpressionDependency{nil, {}, missing} {
				probe := evaluation
				probe.RoutineChecks = manifest
				if err := exec.AdmitRoutineCall(&probe, call, false, false); err == nil {
					t.Fatal("missing original manifest repaired")
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
				if err := exec.AdmitRoutineCall(&probe, call, false, false); err == nil {
					t.Fatal("invalid original manifest", mutation)
				}
			}
			probe := evaluation
			probe.RoutineAuthority = foreign.RoutineAuthority
			if err := exec.AdmitRoutineCall(&probe, call, false, false); err == nil {
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
			if err := exec.AdmitRoutineCall(&probe, mapped, false, false); err == nil {
				t.Fatal("retag manufactured authority")
			}
			invalid := call
			invalid.RoutineReference = (*catalog.Procedure)(nil)
			if err := exec.AdmitRoutineCall(&evaluation, invalid, false, false); err == nil {
				t.Fatal("typed nil admitted")
			}
			changed := *core.RoutineAuthorityView(&evaluation)
			changed.Catalog = changed.Catalog.WithState(catalog.CloneState(changed.Catalog.State()))
			replacement := *captured
			replacement.Strict = false
			changed.Catalog.State().SetDescriptor(captured.Address(), &replacement)
			probe = evaluation
			probe.RoutineAuthority = &changed
			if err := exec.AdmitRoutineCall(&probe, call, false, false); err == nil {
				t.Fatal("incompatible current declaration admitted")
			}
			replacement = *captured
			declaration := *captured.Builtin
			declaration.Providers = functions.OperatorRoutineProvider
			replacement.Builtin = &declaration
			changed.Catalog.State().SetDescriptor(captured.Address(), &replacement)
			if err := exec.AdmitRoutineCall(&probe, call, false, false); err == nil {
				t.Fatal("provider-disjoint current declaration admitted")
			}
		}
	}
}
