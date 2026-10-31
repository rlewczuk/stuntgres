package stored_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPGRulesProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	first := enginetest.InheritanceUnitRelation(f.e, "rules_a", 23001, "id")
	second := enginetest.InheritanceUnitRelation(f.e, "rules_b", 23002, "id")
	first.Owner, second.Owner = f.owner, f.owner
	first.Security.ACL = authorization.ACLValue{Explicit: true}
	first.Rules = stored.RulesPayload([]stored.Rule{
		stored.NewRule(stored.RuleParts{OID: 23003, Name: "r", Event: "INSERT", Mode: catalog.RuleOrigin, Instead: true}),
		// Invalid actions deliberately establish exclusion before rendering.
		stored.NewRule(stored.RuleParts{OID: 23004, Name: "_RETURN", Event: "SELECT", Mode: catalog.RuleOrigin, Actions: []stored.RuleAction{stored.NewRuleAction(stored.RuleActionParts{})}}),
		stored.NewRule(stored.RuleParts{OID: 23005, Name: "_return", Event: "DELETE", Mode: catalog.RuleOrigin}),
	})
	second.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{OID: 23006, Name: "r", Event: "UPDATE", Mode: catalog.RuleOrigin})})
	view := inheritanceUnitView(f.e, first, second)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.RulesOID})
	produce, ok := pgcatalog.ProducerFor(pgcatalog.RulesOID)
	if !ok {
		t.Fatal("missing rule producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Presentation: stored.Presentation{Deparse: true}, Auth: core.AuthorizationView{}})}
	rows, err := produce(input)
	if err != nil {
		t.Fatal(err)
	}
	var got [][]string
	for _, row := range rows {
		if len(row) != 4 {
			t.Fatal("rule width", row)
		}
		cells := make([]string, 4)
		for i, value := range row {
			if value.Type() != source.Columns[i].Usage.Type || !value.ValidResult(source.Columns[i].Usage) || value.IsNull() {
				t.Fatal("rule cell descriptor", i, value)
			}
			cells[i], err = value.Text()
			if err != nil {
				t.Fatal(err)
			}
		}
		got = append(got, cells)
	}
	sort.Slice(got, func(i, j int) bool { return got[i][1]+got[i][2] < got[j][1]+got[j][2] })
	want := [][]string{
		{"public", "rules_a", "_return", "CREATE RULE _return AS\n    ON DELETE TO public.rules_a DO NOTHING;"},
		{"public", "rules_a", "r", "CREATE RULE r AS\n    ON INSERT TO public.rules_a DO INSTEAD NOTHING;"},
		{"public", "rules_b", "r", "CREATE RULE r AS\n    ON UPDATE TO public.rules_b DO NOTHING;"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rules: %#v, want %#v", got, want)
	}
	// No authorization capability was installed: explicit-empty table ACLs do
	// not hide public metadata, and stored same-named rules remain separate.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = canceled
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled prefix", rows, err)
	}
	lateContext, lateCancel := context.WithCancel(context.Background())
	defer lateCancel()
	compact := catalog.NewState(view.Epoch())
	compact.SetDescriptor(first.Namespace(), view.MustNamespace(first.Namespace()))
	compact.SetDescriptor(first.Address(), first)
	input.Catalog = view.WithState(compact)
	input.Context = &cancelAtErrCheck{Context: lateContext, cancel: lateCancel, at: 7}
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("late cancellation exposed prefix", rows, err)
	}
	input.Context = context.Background()
	broken := *first
	broken.Rules = stored.RulesPayload([]stored.Rule{
		stored.NewRule(stored.RuleParts{OID: 23003, Name: "r", Event: "INSERT", Mode: catalog.RuleOrigin}),
		stored.NewRule(stored.RuleParts{OID: 23007, Name: "broken", Event: "INSERT", Mode: catalog.RuleOrigin, Actions: []stored.RuleAction{stored.NewRuleAction(stored.RuleActionParts{Syntax: &parser.Insert{}, Binding: analyze.Binding{Ref: ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 23999}}}})}}),
	})
	input.Catalog = inheritanceUnitView(f.e, &broken)
	if rows, err := produce(input); rows != nil || !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("failed render exposed prefix", rows, err)
	}
	input.Catalog = f.e.CatalogRead()
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("empty source", rows, err)
	}
	if source.Columns[0].Usage.Type.ID() != sqltype.Name || source.Columns[3].Usage.Type.ID() != sqltype.Text {
		t.Fatal("rule logical types")
	}
}

func inheritanceUnitView(e *core.Engine, relations ...*catalog.Relation) catalog.Read {
	state := catalog.CloneState(e.CatalogRead().State())
	for _, d := range relations {
		state.SetDescriptor(d.Address(), d)
		state.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: d.Name()}, d.Address())
	}
	return e.Catalog.Read(state)
}

func newDefaultUnitFixture(t *testing.T) defaultUnitFixture {
	t.Helper()
	e := engine.New()
	roles := enginetest.CloneRoleFixture(e.RoleStore.Published)
	refs := make([]authorization.PrincipalRef, 3)
	for i, name := range []string{"default_owner", "default_reader", "default_member"} {
		ref := e.RoleStore.Domain.Principal(uint64(20001 + i))
		refs[i] = ref
		enginetest.PutFixtureRole(roles, authorization.RoleDescriptor{Ref: ref, Name: name, Login: true, Inherit: true, ConnectionLimit: -1})

	}
	enginetest.SetFixtureEdges(roles, append(append([]authorization.Membership(nil), enginetest.FixtureEdges(roles)...), authorization.Membership{Role: refs[0], Member: refs[2], Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Inherit: true, Set: true}))
	// Install a fully identified graph before transactions branch from this
	// fixture. Candidate-only helpers intentionally leave new edge IDs at zero.
	root, frontier, err := roles.Root.AssignMembershipOIDs(authorization.FirstMembershipOID)
	if err != nil {
		t.Fatal(err)
	}
	roles.Root = root
	e.RoleStore.InstallMembershipFrontierLocked(frontier)
	e.RoleStore.PublishLocked(roles)
	schema := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20010}
	next := catalog.CloneState(e.Catalog.State)
	namespace := &catalog.Namespace{ObjectAddress: schema, ObjectName: "default_schema", ObjectRevision: 2, Owner: refs[0]}
	next.SetDescriptor(schema, namespace)
	next.BindName(catalog.DescriptorNameKey(namespace), schema)
	e.Catalog.PublishLocked(next)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	return defaultUnitFixture{e: e, op: op, owner: refs[0], reader: refs[1], member: refs[2], schema: schema}
}

// cancelAtErrCheck drives existing cooperative cancellation boundaries without
// sleeps or production hooks. Its embedded real context owns Done and Err state.
type cancelAtErrCheck struct {
	context.Context
	cancel context.CancelFunc
	at     int32
	checks atomic.Int32
}

func (c *cancelAtErrCheck) Err() error {
	if c.checks.Add(1) == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

type defaultUnitFixture struct {
	e                     *core.Engine
	op                    *session.Operation
	owner, reader, member authorization.PrincipalRef
	schema                ids.ObjectAddress
}

func (f defaultUnitFixture) apply(s *parser.AlterDefaultPrivileges) error {
	_, err := f.e.Execute(f.op, s, 0, nil)
	return err
}

func (f defaultUnitFixture) grant(t *testing.T, class, privilege string, schemas []string) *parser.AlterDefaultPrivileges {
	t.Helper()
	s := enginetest.DefaultUnitStatement(class, privilege, []string{"default_owner"}, schemas, []string{"default_reader"})
	if err := f.apply(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f defaultUnitFixture) actor(ref authorization.PrincipalRef) {
	f.e.Mu.Lock()
	f.op.Session.Mu.Lock()
	f.op.Session.Authorization.State.CurrentUser = ref
	f.op.Session.Authorization.Revision++
	f.op.Session.Mu.Unlock()
	f.e.Mu.Unlock()
}
