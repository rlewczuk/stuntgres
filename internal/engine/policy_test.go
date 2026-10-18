package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

// SQL only constructs the ordinary internal catalog fixture. Assertions exercise
// real binders, frozen presentation, provider validation and publication directly.
func policyUnitFixture(t *testing.T) (*Engine, *Operation, func(), *relationDescriptor) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	roleExecute(t, e, op, "CREATE TABLE t(id INTEGER, allowed BOOLEAN)")
	roleExecute(t, e, op, "CREATE TABLE helper(id INTEGER)")
	ref := roleTableRef(t, e, "t")
	d, _ := e.CatalogRead().Descriptor(ref.Address)
	return e, op, done, d.(*relationDescriptor)
}
func policyUnitBind(t *testing.T, e *Engine, op *Operation, d *relationDescriptor, condition string) (*policyExpression, error) {
	t.Helper()
	raw := statement(t, "CREATE POLICY p ON t USING("+condition+")").(*parser.CreatePolicy)
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	return e.bindPolicyExpression(&StatementScope{op: op}, auth, d, raw.Using)
}
func TestRLSPolicyFrozenExpression(t *testing.T) {
	for _, condition := range []string{
		"id>0", "allowed", "NULL", "'yes'", "CURRENT_USER IS NOT NULL",
		"DATE '2000-01-01'<CURRENT_DATE",
		"random()>=0", "pg_catalog.random()>=0 AND random()<1",
		"TIMESTAMP(3) '2000-01-01 00:00:00.1234' IS NOT NULL",
		"CAST(NULL AS integer) IS NULL", "id::policy_integer>0",
		"'a' COLLATE policy_collation='a'", "nextval('policy_sequence')>0",
		"'policy_integer'::regtype IS NOT NULL",
		"1/(id-id)>0",
		"(SELECT count(*)>0 FROM helper)",
		"EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id)",
		"EXISTS(SELECT 1 FROM helper WHERE EXISTS(SELECT 1 WHERE helper.id=t.id))",
		"id=ANY(SELECT id FROM helper)", "id<=ALL(SELECT id FROM helper)",
		"ROW(id,allowed)=(SELECT 1,TRUE)",
		"(SELECT row_number() OVER(ORDER BY helper.id) FROM helper LIMIT 1)>0",
	} {
		t.Run(condition, func(t *testing.T) {
			e, op, done, d := policyUnitFixture(t)
			defer done()
			roleExecute(t, e, op, "CREATE DOMAIN policy_integer AS INTEGER")
			roleExecute(t, e, op, `CREATE COLLATION policy_collation FROM "C"`)
			roleExecute(t, e, op, "CREATE SEQUENCE policy_sequence START 41")
			expression, err := policyUnitBind(t, e, op, d, condition)
			if err != nil {
				t.Fatal(err)
			}
			if condition == "random()>=0" && expression.expression.Properties().Volatility != Volatile {
				t.Fatal("policy binding lost volatile function behavior", expression.expression.Properties())
			}
			if err = validatePolicyExpression(e.CatalogRead(), d, expression); err != nil {
				t.Fatal(err)
			}
			sql, err := policyExpressionSQL(context.Background(), e.CatalogRead(), expression)
			if err != nil || sql == "" {
				t.Fatal(sql, err)
			}
			// Replay the actual renderer result through the same Boolean binder.
			// Frozen literal types, casts, provider identities and lexical frames
			// must survive capture again without adding casts or losing slots.
			for range 3 {
				replayed, err := policyUnitBind(t, e, op, d, sql)
				if err != nil {
					t.Fatal("rendered expression cannot bind", sql, err)
				}
				if err = validatePolicyExpression(e.CatalogRead(), d, replayed); err != nil {
					t.Fatal("invalid recaptured expression", sql, err)
				}
				redump, err := policyExpressionSQL(context.Background(), e.CatalogRead(), replayed)
				if err != nil || redump != sql {
					t.Fatal("unstable frozen expression replay", sql, redump, err)
				}
				sql = redump
			}
			original := expression.presentation
			next := cloneCatalog(e.CatalogRead().state)
			owner := *d
			owner.name = "renamed"
			owner.columns = append([]ColumnDescriptor(nil), d.columns...)
			owner.columns[0].Name = "key"
			next.descriptors[d.address] = &owner
			text, err := policyExpressionSQL(context.Background(), CatalogRead{owner: e, state: next}, expression)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(sql, `"t"."id"`) && !strings.Contains(text, `"renamed"."key"`) {
				t.Fatal("lost stable owner identity", text)
			}
			if expression.presentation != original || d.name != "t" || d.columns[0].Name != "id" {
				t.Fatal("mutated published definition")
			}
		})
	}
}
func TestRLSPolicyBooleanContext(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	for _, tc := range []struct{ condition, code, message string }{
		{"1", "42804", "argument of POLICY must be type boolean, not type integer"},
		{"count(*)>0", "42803", "aggregate functions are not allowed in policy expressions"},
		{"FALSE AND count(*)>0", "42803", "aggregate functions are not allowed in policy expressions"},
		{"row_number() OVER()>0", "42P20", "window functions are not allowed in policy expressions"},
		{"$1", "42P02", "there is no parameter $1"},
		{"EXISTS(SELECT $1)", "42P02", "there is no parameter $1"},
	} {
		_, err := policyUnitBind(t, e, op, d, tc.condition)
		diagnostic, ok := err.(*sqlstate.Error)
		if !ok || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Position != "" {
			t.Fatal(tc.condition, err)
		}
	}
}
func TestRLSPolicyPublicationCanceled(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	expression, err := policyUnitBind(t, e, op, d, "id>0")
	if err != nil {
		t.Fatal(err)
	}
	base := e.CatalogRead()
	next := *d
	next.revision++
	e.mu.Lock()
	oid, err := e.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next.policies = []policyDefinition{{OID: oid, Name: "p", permissive: true, command: "all", roles: []policyRole{{public: true}}, using: expression}}
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err = stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prepared.ctx = ctx
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err == nil {
		t.Fatal("canceled stage published")
	}
	if e.CatalogRead().state != base.state || len(d.policies) != 0 {
		t.Fatal("canceled publication changed immutable roots")
	}
}
func TestRLSPolicyRemap(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	roleExecute(t, e, op, "CREATE DOMAIN policy_integer AS INTEGER")
	roleExecute(t, e, op, `CREATE COLLATION policy_collation FROM "C"`)
	roleExecute(t, e, op, "CREATE SEQUENCE policy_sequence START 41")
	expression, err := policyUnitBind(t, e, op, d, "EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id::policy_integer) AND DATE '2000-01-01'<CURRENT_DATE AND 'a' COLLATE policy_collation='a' AND nextval('policy_sequence')>0")
	if err != nil {
		t.Fatal(err)
	}
	next := *d
	next.revision++
	e.mu.Lock()
	oid, err := e.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next.policies = []policyDefinition{{OID: oid, Name: "p", command: "all", permissive: true, roles: []policyRole{{ref: d.owner}, {ref: d.owner}}, using: expression}}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err = stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	imported := New()
	if _, err = imported.ReplaceCatalog(context.Background(), e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	view := imported.CatalogRead()
	var copied *relationDescriptor
	for _, descriptor := range view.state.descriptors {
		if table, ok := descriptor.(*relationDescriptor); ok && table.name == "t" {
			copied = table
			break
		}
	}
	if copied == nil || len(copied.policies) != 1 || len(copied.policies[0].roles) != 2 {
		t.Fatal("lost attached policy")
	}
	p := copied.policies[0]
	if imported.Roles().ValidatePrincipal(p.roles[0].ref) != nil || p.roles[1].ref != p.roles[0].ref {
		t.Fatal("lost principal provenance/multiplicity")
	}
	if err = validatePolicies(view, copied); err != nil {
		t.Fatal(err)
	}
	if p.using == expression || expression.presentation.sources[0].ref.Domain() != e.catalog.domain {
		t.Fatal("remap mutated original expression")
	}
	originalSQL, err := policyExpressionSQL(context.Background(), e.CatalogRead(), expression)
	if err != nil {
		t.Fatal(err)
	}
	importedSQL, err := policyExpressionSQL(context.Background(), view, p.using)
	if err != nil || importedSQL != originalSQL {
		t.Fatal("remapped presentation lost frozen context", originalSQL, importedSQL, err)
	}
}

func TestRLSPolicyDependencyClosure(t *testing.T) {
	for _, tc := range []struct {
		name, setup, condition string
		class                  ids.OID
	}{
		{"helper", "CREATE VIEW dependent_view AS SELECT id FROM helper", "EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id)", ids.RelationClassID},
		{"policy_integer", "CREATE DOMAIN policy_integer AS INTEGER", "id::policy_integer>0", ids.TypeClassID},
		{"policy_collation", `CREATE COLLATION policy_collation FROM "C"`, "'a' COLLATE policy_collation='a'", ids.CollationClassID},
		{"policy_sequence", "CREATE SEQUENCE policy_sequence", "nextval('policy_sequence')>0", ids.RelationClassID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, op, done, owner := policyUnitFixture(t)
			defer done()
			roleExecute(t, e, op, tc.setup)
			expression, err := policyUnitBind(t, e, op, owner, tc.condition)
			if err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			oid, err := e.allocateOIDLocked()
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			base := e.CatalogRead()
			state := cloneCatalog(base.state)
			attached := *owner
			attached.policies = []policyDefinition{{OID: oid, Name: "p", command: "all", permissive: true, roles: []policyRole{{public: true}}, using: expression}}
			state.descriptors[owner.address] = &attached
			base.state = state
			var target ids.ObjectAddress
			for address, descriptor := range state.descriptors {
				if address.ClassID == tc.class && descriptor.Name() == tc.name {
					target = address
					break
				}
			}
			if target == (ids.ObjectAddress{}) {
				t.Fatal("missing provider")
			}
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			edges, err := domainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{target})
			if err != nil {
				t.Fatal(err)
			}
			var labels []string
			for _, edge := range edges {
				if !edge.automatic {
					labels = append(labels, edge.label)
				}
			}
			want := "policy p on table t"
			if tc.name == "helper" {
				want = "view dependent_view\n" + want
			}
			if strings.Join(labels, "\n") != want {
				t.Fatal("incomplete or unordered closure", labels)
			}
			if err := stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{target}, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
				t.Fatal(err)
			}
			survivor, ok := stage.descriptors[owner.address].(*relationDescriptor)
			if !ok || len(survivor.policies) != 0 || len(attached.policies) != 1 {
				t.Fatal("cascade failed to prune only the staged policy")
			}
			if strings.Join(stage.viewCascadeNames, "\n") != "drop cascades to "+strings.ReplaceAll(want, "\n", "\ndrop cascades to ") {
				t.Fatal("cascade lost mixed closure notices", stage.viewCascadeNames)
			}
		})
	}
}
