package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestNetworkMaskOriginalAuthority(t *testing.T) {
	for _, tc := range []struct {
		expression, name, sig string
		oid, result           ids.OID
	}{
		{"hostmask(ip)", "hostmask", "869", 1362, 869}, {"netmask(ip)", "netmask", "869", 696, 869}, {"network(ip)", "network", "869", 683, 650},
		{"inet_merge(ip,ip)", "inet_merge", "869,869", 4063, 650}, {"set_masklen(ip,16)", "set_masklen", "869,23", 605, 869}, {"set_masklen(net,16)", "set_masklen", "650,23", 635, 650},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			e := New()
			admin, finish := roleOperation(t, e, "postgres")
			defer finish()
			roleExecute(t, e, admin, "CREATE TABLE nm_unit(ip inet,net cidr)")
			ref := roleTableRef(t, e, "public.nm_unit")
			input, _ := admin.Session.Settings.Presentation()
			raw := statement(t, "SELECT "+tc.expression).(*parser.Select).Targets[0]
			e.mu.Lock()
			auth, err := e.authorizationLocked(admin)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			binder := newExpressionBinder(context.Background(), e, input, admin, nil)
			binder.Query.Auth, binder.Target.Ref = &auth, ref
			binder.Target.Relation = auth.Catalog.MustRelation(ref.Address)
			bindTestTargetNamespace(binder)
			node, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			outer, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil || outer == nil || outer.Builtin == nil {
				t.Fatal("missing selected routine", err)
			}
			d := outer.Builtin
			if d.Identity != (catalog.RoutineIdentity{tc.oid, ids.PGCatalogNamespaceOID, tc.name, catalog.ProcedureSignature(tc.sig)}) || d.Result != tc.result || !d.Strict || d.Volatility != expr.Immutable || d.Parallel != 's' || d.ReturnsSet || d.Variadic != 0 || d.Defaults != 0 || ids.OID(node.ResultUsage().Type.ID()) != tc.result {
				t.Fatal("released declaration", d)
			}
			found := map[ids.OID]bool{}
			for _, dep := range binder.Dependencies {
				if dep.Ref.Address.ClassID == ids.ProcedureClassID {
					found[dep.Ref.Address.ObjectID] = true
				}
			}
			if !reflect.DeepEqual(found, map[ids.OID]bool{tc.oid: true}) {
				t.Fatal("wrong dependency identities", found)
			}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: binder.Dependencies}}
			if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal(err)
			}
			foreign := New()
			for _, mode := range []string{"missing", "foreign", "stale"} {
				var checks []catalog.ExpressionDependency
				for _, dep := range binder.Dependencies {
					if dep.Ref.Address == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.oid}) {
						switch mode {
						case "missing":
							continue
						case "foreign":
							dep.Ref = foreign.catalog.Domain.Ref(dep.Ref.Epoch, dep.Ref.Address)
						case "stale":
							dep.Revision++
						}
					}
					checks = append(checks, dep)
				}
				evaluation.RoutineChecks = checks
				if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err == nil {
					t.Fatal("invalid selected authority", mode)
				}
			}
			remapped, _, err := stored.RetagTypes(foreign.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				current, _ := foreign.CatalogRead().LookupType(old.ID())
				return current
			})
			if err != nil {
				t.Fatal(err)
			}
			call := remapped.(functions.NetworkFormat)
			if call.Validate() != nil || call.Plan == node.(functions.NetworkFormat).Plan {
				t.Fatal("foreign plan survived reownership")
			}
			for _, child := range call.Children() {
				current, _ := foreign.CatalogRead().LookupType(child.ResultUsage().Type.ID())
				if child.ResultUsage().Type != current {
					t.Fatal("stale child descriptor")
				}
			}
			evaluation.RoutineChecks = []catalog.ExpressionDependency{}
			if err := exec.AdmitRoutineCall(&evaluation, remapped, true, false); err == nil {
				t.Fatal("retag fabricated authority")
			}
			evaluation.RoutineChecks = binder.Dependencies
			if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("original authority changed", err)
			}
		})
	}
}

func TestNetworkMaskStoredLiteralIdentity(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	type literalIdentity struct {
		id    sqltype.ID
		value string
	}
	for _, tc := range []struct {
		expression string
		literals   []literalIdentity
	}{
		{"hostmask(INET '192.168.1.5/24')", []literalIdentity{{sqltype.Inet, "192.168.1.5/24"}}},
		{"netmask(INET '2001:db8::ffff/113')", []literalIdentity{{sqltype.Inet, "2001:db8::ffff/113"}}},
		{"network(INET '192.168.1.5/24')", []literalIdentity{{sqltype.Inet, "192.168.1.5/24"}}},
		{"inet_merge(INET '::1',INET '::2')", []literalIdentity{{sqltype.Inet, "::1"}, {sqltype.Inet, "::2"}}},
		{"set_masklen(INET '192.168.1.5/24',16)", []literalIdentity{{sqltype.Inet, "192.168.1.5/24"}, {sqltype.Int4, "16"}}},
		{"set_masklen(CIDR '192.168.1.0/24',16)", []literalIdentity{{sqltype.Cidr, "192.168.1.0/24"}, {sqltype.Int4, "16"}}},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			node, b, _ := strictNullUnitBind(t, e, op, scope, tc.expression)
			raw := statement(t, "SELECT "+tc.expression).(*parser.Select).Targets[0]
			if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
				t.Fatal("literal pairing", err)
			}
			want := make(map[literalIdentity]int)
			for _, identity := range tc.literals {
				want[identity]++
			}
			got := make(map[literalIdentity]int)
			for _, literal := range b.Capture.Literals {
				value, err := literal.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
				if err != nil || value.IsNull() || value.Type() != literal.ResultUsage().Type || value.Type() != b.Type(value.Type().ID()) {
					t.Fatal("lost exact literal authority", value, err)
				}
				text, err := value.Type().Output(context.Background(), value)
				if err != nil {
					t.Fatal(err)
				}
				got[literalIdentity{value.Type().ID(), text}]++
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("stored literal identities", got, want)
			}
			call := node.(functions.NetworkFormat)
			if call.Kind == sqltype.NetworkSetMasklen && call.Args[1].ResultUsage().Type.ID() != sqltype.Int4 {
				t.Fatal("setter integer child lost")
			}
		})
	}
}

func TestNetworkMaskStrictDemand(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct {
		expression    string
		bad, good     []string
		message, want string
	}{
		{"set_masklen($1::inet,$2::integer)", []string{"192.168.1.5/24", "33"}, []string{"192.168.1.5/24", "16"}, "invalid mask length: 33", "192.168.1.5/16"},
		{"set_masklen($1::cidr,$2::integer)", []string{"192.168.1.0/24", "33"}, []string{"192.168.1.0/24", "16"}, "invalid mask length: 33", "192.168.0.0/16"},
		{"inet_merge($1::inet,$2::inet)", []string{"1.2.3.4", "::1"}, []string{"::1", "::2"}, "cannot merge addresses from different families", "::/126"},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			node, b, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
			setParameters := func(inputs []string) {
				evaluation.Parameters = make([]sqltype.Value, len(inputs))
				for i, input := range inputs {
					value, err := b.Params.Parameters[i+1].Type.Input(context.Background(), input, -1)
					if err != nil {
						t.Fatal(err)
					}
					evaluation.Parameters[i] = value
				}
			}
			setParameters(tc.bad)
			_, _, err := exec.PreflightCoercionsWithPolicy(&evaluation, node, false)
			diagnostic, ok := err.(interface{ SQLState() string })
			if !ok || diagnostic.SQLState() != "22023" || err.Error() != "stuntgres: "+tc.message {
				t.Fatal("provider error lost before row demand", err)
			}
			// A failed execution-local fold must not alter the analyzed node.
			setParameters(tc.good)
			planned, changed, err := exec.PreflightCoercionsWithPolicy(&evaluation, node, false)
			if err != nil || !changed {
				t.Fatal("same analyzed expression did not recover", changed, err)
			}
			value, err := planned.Eval(&evaluation)
			if err != nil || value.Type() != node.ResultUsage().Type {
				t.Fatal(value, err)
			}
			text, err := value.Type().Output(context.Background(), value)
			if err != nil || text != tc.want {
				t.Fatal("recovered result", text, err)
			}
		})
	}
	for _, expression := range []string{"set_masklen($1::inet,v)", "set_masklen($1::cidr,v)"} {
		node, b, evaluation := strictNullUnitBind(t, e, op, scope, expression)
		input, err := b.Params.Parameters[1].Type.Input(context.Background(), "192.168.1.0/24", -1)
		if err != nil {
			t.Fatal(err)
		}
		evaluation.Parameters = []sqltype.Value{input}
		planned, _, err := exec.PreflightCoercionsWithPolicy(&evaluation, node, false)
		if err != nil || !planned.Properties().RowDependent {
			t.Fatal("preflight forced a row-dependent mask", expression, err)
		}
	}
	for _, expression := range []string{
		"hostmask($1::inet)", "netmask($1::inet)", "network($1::inet)",
		"inet_merge($1::inet,INET '::1')", "inet_merge(INET '::1',$1::inet)",
		"set_masklen($1::inet,16)", "set_masklen($1::cidr,16)",
		"set_masklen(INET '::1',$1::integer)", "set_masklen(CIDR '::1/128',$1::integer)",
	} {
		t.Run("known_null/"+expression, func(t *testing.T) {
			node, b, evaluation := strictNullUnitBind(t, e, op, scope, expression)
			evaluation.Parameters = []sqltype.Value{sqltype.Null(b.Params.Parameters[1].Type)}
			// A known-NULL strict call must disappear before planning admission.
			// Deliberately omit its routine manifest: no provider can be invoked.
			evaluation.RoutineChecks = []catalog.ExpressionDependency{}
			planned, changed, err := exec.PreflightCoercionsWithPolicy(&evaluation, node, false)
			if err != nil || !changed {
				t.Fatal("known NULL reached routine admission", changed, err)
			}
			value, err := planned.Eval(&evaluation)
			if err != nil || !value.IsNull() || value.Type() != node.ResultUsage().Type {
				t.Fatal("typed strict NULL result", value, err)
			}
			input := "::1"
			if b.Params.Parameters[1].Type.ID() == sqltype.Int4 {
				input = "16"
			}
			nonnull, err := b.Params.Parameters[1].Type.Input(context.Background(), input, -1)
			if err != nil {
				t.Fatal(err)
			}
			evaluation.Parameters[0] = nonnull
			evaluation.RoutineChecks = b.Dependencies
			again, _, err := exec.PreflightCoercionsWithPolicy(&evaluation, node, false)
			if err != nil {
				t.Fatal("known NULL changed original expression authority", err)
			}
			value, err = again.Eval(&evaluation)
			if err != nil || value.IsNull() || value.Type() != node.ResultUsage().Type {
				t.Fatal("nonnull recovery", value, err)
			}
			if kind := node.(functions.NetworkFormat).Kind; kind == sqltype.NetworkMerge || kind == sqltype.NetworkSetMasklen {
				evaluation.RoutineChecks = []catalog.ExpressionDependency{}
				if _, _, err := exec.PreflightCoercionsWithPolicy(&evaluation, node, false); err == nil {
					t.Fatal("nonnull provider bypassed original routine authority")
				}
			}
		})
	}
}
