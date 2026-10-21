package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Non-public binder/authority test: released declarations and exact dependency
// manifests, not a second public SQL result scenario or native-derived golden.
func TestNetworkFormatOriginalAuthority(t *testing.T) {
	for _, tc := range []struct {
		expr, name, sig string
		id, result      ids.OID
	}{
		{"abbrev(ip)", "abbrev", "869", 598, 25}, {"abbrev(net)", "abbrev", "650", 599, 25}, {"host(ip)", "host", "869", 699, 25}, {"text(ip)", "text", "869", 730, 25}, {"family(ip)", "family", "869", 711, 23}, {"masklen(ip)", "masklen", "869", 697, 23}, {"broadcast(ip)", "broadcast", "869", 698, 869}, {"inet_same_family(ip,ip)", "inet_same_family", "869,869", 4071, 16},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			e := New()
			admin, finish := roleOperation(t, e, "postgres")
			defer finish()
			roleExecute(t, e, admin, "CREATE TABLE nf_unit(ip inet,net cidr)")
			ref := roleTableRef(t, e, "public.nf_unit")
			input, _ := sessionPresentation(admin.Session)
			raw := statement(t, "SELECT "+tc.expr).(*parser.Select).Targets[0]
			e.mu.Lock()
			auth, err := e.authorizationLocked(admin)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			binder := newExpressionBinder(context.Background(), e, input, admin, nil)
			binder.Query.Auth, binder.Target.Ref = &auth, ref
			binder.Target.Relation = auth.Catalog.MustRelation(ref.Address)
			node, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			outer, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil || outer == nil || outer.Builtin == nil {
				t.Fatal("missing original declaration", outer, err)
			}
			d := outer.Builtin
			if d.Identity != (catalog.RoutineIdentity{tc.id, ids.PGCatalogNamespaceOID, tc.name, catalog.ProcedureSignature(tc.sig)}) || d.Result != tc.result || !d.Strict || d.Volatility != expr.Immutable || d.Parallel != 's' || d.ReturnsSet || d.Variadic != 0 || d.Defaults != 0 || ids.OID(node.ResultUsage().Type.ID()) != tc.result {
				t.Fatal("released declaration", d)
			}
			found := map[ids.OID]bool{}
			for _, dep := range binder.Dependencies {
				if dep.Ref.Address.ClassID == ids.ProcedureClassID {
					found[dep.Ref.Address.ObjectID] = true
				}
			}
			if !reflect.DeepEqual(found, map[ids.OID]bool{tc.id: true}) {
				t.Fatal("exact selected dependencies", found)
			}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: binder.Dependencies}}
			if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal(err)
			}
			foreign := New()
			for _, mode := range []string{"missing", "foreign", "stale"} {
				var checks []catalog.ExpressionDependency
				for _, dep := range binder.Dependencies {
					if dep.Ref.Address == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.id}) {
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
					t.Fatal("accepted invalid authority", mode)
				}
			}
			remapped, _, err := retagBoundTypes(foreign.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				current, _ := foreign.CatalogRead().LookupType(old.ID())
				return current
			})
			if err != nil {
				t.Fatal(err)
			}
			evaluation.RoutineChecks = []catalog.ExpressionDependency{}
			if err := exec.AdmitRoutineCall(&evaluation, remapped, true, false); err == nil {
				t.Fatal("retag manufactured authority")
			}
			evaluation.RoutineChecks = binder.Dependencies
			if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("source authority mutated", err)
			}
		})
	}
}

func TestNetworkFormatStrictParameterNull(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, expression := range []string{"text($1::inet)", "($1::inet)::text", "CAST($1::inet AS text)", "host($1::inet)", "abbrev($1::cidr)", "family($1::inet)", "masklen($1::inet)", "broadcast($1::inet)", "inet_same_family($1::inet,INET '::1')", "inet_same_family(INET '::1',$1::inet)"} {
		t.Run(expression, func(t *testing.T) {
			node, b, evaluation := strictNullUnitBind(t, e, op, scope, expression)
			evaluation.Parameters = []sqltype.Value{sqltype.Null(b.Params.Parameters[1].Type)}
			// No original routine manifest is available for computation. A known-NULL
			// formal must prune without invoking/admitting the selected routine.
			evaluation.RoutineChecks = []catalog.ExpressionDependency{}
			planned, changed, err := preflightCoercionsWithPolicy(&evaluation, node, false)
			if err != nil || !changed {
				t.Fatal("known NULL was not pruned before admission", changed, err)
			}
			value, err := planned.Eval(&evaluation)
			if err != nil || !value.IsNull() || value.Type() != node.ResultUsage().Type || planned.Properties() != node.Properties() {
				t.Fatal(value, err)
			}
			input, err := b.Params.Parameters[1].Type.Input(context.Background(), "::1", -1)
			if err != nil {
				t.Fatal(err)
			}
			evaluation.Parameters[0] = input
			// The original analyzed expression and its manifest are not replaced by
			// the execution-local NULL result. Restoring authority permits real work.
			evaluation.RoutineChecks = b.Dependencies
			again, _, err := preflightCoercionsWithPolicy(&evaluation, node, false)
			if err != nil {
				t.Fatal(err)
			}
			got, err := again.Eval(&evaluation)
			if err != nil || got.IsNull() || got.Type() != node.ResultUsage().Type {
				t.Fatal("NULL pruning changed original analysis", got, err)
			}
		})
	}
}

func TestNetworkFormatStoredLiteralIdentity(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct {
		expression string
		literals   int
	}{
		{"abbrev(INET '192.168.1.5/24')", 1}, {"abbrev(CIDR '10.1')", 1},
		{"host(INET '192.168.1.5/24')", 1}, {"text(INET '192.168.1.5/24')", 1},
		{"family(INET '::1')", 1}, {"masklen(INET '::1')", 1},
		{"broadcast(INET '2001:db8::1/113')", 1},
		{"inet_same_family(INET '::1',INET '::ffff:1.2.3.4')", 2},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			node, b, _ := strictNullUnitBind(t, e, op, scope, tc.expression)
			raw := statement(t, "SELECT "+tc.expression).(*parser.Select).Targets[0]
			if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
				t.Fatal("stored literal occurrence pairing", err)
			}
			if len(b.Capture.Literals) != tc.literals {
				t.Fatal("lost original literal occurrences", len(b.Capture.Literals), tc.literals)
			}
			// Captured leaves retain their declared network type, not the parent's
			// TEXT/INT4/BOOL result or an erased function-call placeholder.
			for _, literal := range b.Capture.Literals {
				if !sqltype.IsNetwork(literal.ResultUsage().Type.BaseType().ID()) {
					t.Fatal("lost typed network literal", literal.ResultUsage())
				}
			}
		})
	}
}
