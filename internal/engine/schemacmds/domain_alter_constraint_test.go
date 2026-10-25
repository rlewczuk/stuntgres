package schemacmds_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Private seams only: parser/binder/catalog fixtures do not issue driver SQL.
func domainAlterUnitFence(t *testing.T, e *core.Engine, view catalog.Read, target ids.ObjectAddress) *typedef.DomainValidationFence {
	t.Helper()
	f, err := typedef.DomainUsesAt(view, target, []ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range f.Uses {
		use := &f.Uses[i]
		use.Storage = e.Storage.Relations[use.Relation.Address()]
		if use.Storage == nil {
			t.Fatal("missing physical use")
		}
		use.Versions = use.Storage.Versions[:len(use.Storage.Versions):len(use.Storage.Versions)]
		use.Ends, use.Generation = use.Storage.Ends, use.Storage.Generation
	}
	return f
}

func TestDomainAlterConstraintSelectedEvaluation(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE DOMAIN public.parent AS integer CHECK(FALSE)",
		"CREATE DOMAIN public.child AS public.parent CONSTRAINT a_error CHECK(1/0>0) CONSTRAINT b_selected CHECK(VALUE>0) CONSTRAINT c_unknown CHECK(NULL::boolean)",
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	child := domainUnitType(t, view, "child")
	integer, _ := view.LookupType(sqltype.Int4)
	boolean, _ := view.LookupType(sqltype.Bool)
	sentinel := sqltype.Boolean(boolean, true)
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{DomainValue: sentinel, DomainFrameUsage: boolean.Usage(), DomainValueActive: true}}
	prior := &exec.Context{}
	evaluation.Input.DomainChecks = prior
	selected := *child
	selected.DomainChecks = []catalog.DomainCheckConstraint{child.DomainChecks[1]}
	if err := core.PrepareOwnDomainChecks(&evaluation, &selected); err != nil {
		t.Fatal("selected preparation initialized unrelated recipe", err)
	}
	violation := errors.New("historical column violation")
	for _, n := range []int64{1, -1, 2} {
		value, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		err = evaluation.EvaluateSelectedDomainCheck(view, &selected, value, violation)
		if n < 0 && err != violation || n > 0 && err != nil {
			t.Fatal("selected truth was cached or ancestor evaluated", n, err)
		}
		if evaluation.DomainValue != sentinel || evaluation.DomainFrameUsage != boolean.Usage() || !evaluation.DomainValueActive || evaluation.Input.DomainChecks != prior || evaluation.RoutineRecipe || evaluation.RoutineGraft || evaluation.RoutineChecks != nil || evaluation.RoutineReferences != nil || evaluation.RoutineConsumer != nil {
			t.Fatal("VALUE/input/routine frame leaked")
		}
	}
	unknown := *child
	unknown.DomainChecks = []catalog.DomainCheckConstraint{child.DomainChecks[2]}
	if err := core.PrepareOwnDomainChecks(&evaluation, &unknown); err != nil {
		t.Fatal(err)
	}
	if err := evaluation.EvaluateSelectedDomainCheck(view, &unknown, sqltype.Null(integer), violation); err != nil {
		t.Fatal("UNKNOWN did not pass", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	evaluation.Context = ctx
	if err := evaluation.EvaluateSelectedDomainCheck(view, &selected, sqltype.Null(integer), violation); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if evaluation.DomainValue != sentinel || evaluation.Input.DomainChecks != prior {
		t.Fatal("cancellation leaked frame")
	}
	leaf := plan.DomainValueExpr{Info: expr.Info{Usage: child.SQLType.DomainBase()}}
	evaluation.Context = context.Background()
	evaluation.DomainValue = sqltype.Null(child.SQLType.DomainBase().Type)
	evaluation.DomainFrameUsage = child.SQLType.DomainBase()
	if _, err := leaf.Eval(&evaluation); err != nil {
		t.Fatal("immediate domain base frame", err)
	}
}

func TestDomainAlterConstraintDiscovery(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE DOMAIN public.d AS integer", "CREATE DOMAIN public.child AS public.d", "CREATE DOMAIN public.grandchild AS public.child", "CREATE TYPE public.unstored AS(v public.d)"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	table := domainUnitTable(t, e, op, "CREATE TABLE public.direct(a public.d,b public.grandchild)")
	view := e.CatalogRead()
	d := domainUnitType(t, view, "d")
	f := domainAlterUnitFence(t, e, view, d.Address())
	if len(f.Uses) != 1 || f.Uses[0].Relation != table || !slices.Equal(f.Uses[0].Columns, []int{0, 1}) || !f.Current(view, e.Storage.Relations) {
		t.Fatal("physical target/attribute deduplication", f.Uses)
	}
	domainUnitTable(t, e, op, "CREATE TABLE public.unrelated(v integer)")
	if !f.Current(e.CatalogRead(), e.Storage.Relations) {
		t.Fatal("unrelated declaration invalidated observation")
	}
	domainUnitTable(t, e, op, "CREATE TABLE public.late(v public.child)")
	if f.Current(e.CatalogRead(), e.Storage.Relations) {
		t.Fatal("late direct use escaped observation")
	}
	f = domainAlterUnitFence(t, e, e.CatalogRead(), d.Address())
	domainUnitTable(t, e, op, "CREATE TABLE public.blocker(v public.unstored[])")
	if f.Current(e.CatalogRead(), e.Storage.Relations) {
		t.Fatal("late stored container escaped observation")
	}
	_, err := typedef.DomainUsesAt(e.CatalogRead(), d.Address(), []ids.ObjectAddress{})
	domainUnitDiagnostic(t, err, "0A000", `cannot alter type "public.d" because column "blocker.v" uses it`)
}

func TestDomainAlterConstraintGenerations(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE DOMAIN public.d AS integer", "CREATE DOMAIN public.child AS public.d", "CREATE TYPE public.pair AS(v public.child)"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	table := domainUnitTable(t, e, op, "CREATE TABLE public.stored(v public.child,a public.child[],c public.pair)")
	before := e.CatalogRead()
	d := domainUnitType(t, before, "d")
	child := domainUnitType(t, before, "child")
	value, err := child.SQLType.Input(context.Background(), "-1", -1)
	if err != nil {
		t.Fatal(err)
	}
	array := table.Columns[1].Usage.Type
	arrayValue, err := array.Input(context.Background(), "[0:1]={-1,NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	composite, err := table.Columns[2].Usage.Type.Input(context.Background(), "(-1)", -1)
	if err != nil {
		t.Fatal(err)
	}
	store := &storage.Relation{Generation: 3, Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value, arrayValue, composite}}}}
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(table.Address(), store)
	e.Mu.Unlock()
	e.Visibility.SetCommitSeqLocked(3)
	// A permanently FALSE recipe proves pure retagging does not check old data.
	raw := enginetest.Statement(t, "CREATE DOMAIN public.unused AS integer CONSTRAINT positive CHECK(FALSE)").(*parser.CreateDomain)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	stage := e.BeginCatalogStage(auth.Catalog)
	next := *d
	if err == nil {
		err = e.BindDomainConstraintDefinitions(op.Context(), op, auth, stage, &next, raw.Constraints, sqltype.InputEnvironment{})
	}
	if err == nil {
		next.DomainChecks[0].NotValid = true
		next.SQLType, err = sqltype.Domain(d.SQLType.ID(), d.Name(), d.SQLType.SQLName(), d.SQLType.DomainBase(), true)
	}
	if err == nil {
		next.ObjectRevision, err = e.Catalog.RevisionLocked()
	}
	if err == nil {
		err = stage.Replace(&next)
	}
	stage.DomainAlterState().Ctx = context.Background()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	current := e.CatalogRead()
	newD := domainUnitType(t, current, "d")
	newChild := domainUnitType(t, current, "child")
	if !newD.DomainChecks[0].NotValid || !newChild.SQLType.HasDomainChecks() || newD.SQLType.ID() != d.SQLType.ID() || newChild.SQLType.ID() != child.SQLType.ID() || newChild.SQLType.DomainBase().Type != newD.SQLType {
		t.Fatal("same-ID checked ancestry lost")
	}
	mapped := e.Storage.Relations[table.Address()]
	if mapped == store || mapped.Versions[0].Values[0].Type() != newChild.SQLType || store.Versions[0].Values[0].Type() != child.SQLType || d.SQLType.HasDomainChecks() {
		t.Fatal("old row/type roots mutated")
	}
	mappedArray := mapped.Versions[0].Values[1]
	if mappedArray.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 0, Length: 2}) || !mappedArray.ArrayElement(1).IsNull() || mappedArray.ArrayElement(0).Type() != newChild.SQLType || mapped.Versions[0].Values[2].CompositeField(0).Type() != newChild.SQLType {
		t.Fatal("container generations/bounds/NULLs lost")
	}
	drop := e.BeginCatalogStage(current)
	if err := (tablecmds.TypeHost{tablecmds.TableHost{core.DDLHost{CatalogStage: drop}}}).RemoveDomainCheck(newD, newD.DomainChecks[0].OID); err != nil {
		t.Fatal(err)
	}
	drop.DomainAlterState().Ctx = context.Background()
	domainUnitPublish(t, e, drop)
	if domainUnitType(t, e.CatalogRead(), "child").SQLType.HasDomainChecks() || !newChild.SQLType.HasDomainChecks() || !newD.DomainChecks[0].NotValid {
		t.Fatal("last DROP changed retained generations")
	}
	t.Run("index_artifacts", domainAlterGenerationIndexes)
}

func TestDomainAlterConstraintReplayEligibility(t *testing.T) {
	t.Run("fresh_owner_pair", domainAlterReplayOwnership)
	for _, tc := range []struct {
		sql   string
		valid bool
	}{
		{"ALTER DOMAIN public.d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", true},
		{"ALTER DOMAIN public.d ADD CONSTRAINT positive CHECK(VALUE>0)", true},
		{"ALTER DOMAIN public.d ADD CONSTRAINT positive CHECK(VALUE>0) NO INHERIT NOT VALID", false},
		{"ALTER DOMAIN d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", false},
		{"ALTER DOMAIN pg_temp.d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", false},
		{"ALTER DOMAIN public.d ADD CHECK(VALUE>0) NOT VALID", false},
		{"ALTER DOMAIN public.d DROP CONSTRAINT positive", false},
		{"ALTER DOMAIN public.d VALIDATE CONSTRAINT positive", false},
		{"ALTER DOMAIN public.d ADD CONSTRAINT positive CHECK($1>0) NOT VALID", false},
		{"ALTER DOMAIN public.d ADD CONSTRAINT positive CHECK($1>0)", false},
	} {
		raw := enginetest.Statement(t, tc.sql).(*parser.AlterDomain)
		if err := control.CheckAlterDomain(raw); (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE DOMAIN public.d AS integer", "CREATE DOMAIN public.provider AS public.d"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	d := domainUnitType(t, view, "d")
	provider := domainUnitType(t, view, "provider")
	recipeStage := domainUnitStage(t, e, op, "CREATE DOMAIN public.recipe AS integer CONSTRAINT late CHECK(VALUE::public.provider>0)")
	defer e.DiscardCatalogStage(recipeStage)
	var check catalog.DomainCheckConstraint
	for _, descriptor := range recipeStage.Descriptors() {
		if domain, ok := descriptor.(*catalog.Type); ok && domain.SQLType.IsDomain() {
			check = domain.DomainChecks[0]
		}
	}
	check.NotValid = true
	copy := *d
	copy.DomainChecks = []catalog.DomainCheckConstraint{check}
	graph := dump.Graph{{OID: d.Address().ObjectID}: nil, {OID: provider.Address().ObjectID}: {{OID: d.Address().ObjectID}}, {OID: 50002, Phase: dump.Rows}: {{OID: 50002}}, {OID: 50002}: nil, {OID: 50002, Phase: dump.TableCompletion}: {{OID: 50002, Phase: dump.Rows}}}
	if err := regobj.DomainInvalidDumpEvents(context.Background(), control.Resolver{}, view, graph, map[ids.OID]*catalog.Type{d.Address().ObjectID: &copy}); err != nil {
		t.Fatal(err)
	}
	late := dump.Event{OID: d.Address().ObjectID, Phase: dump.DomainChecks}
	if !slices.Contains(graph[late], dump.Event{OID: provider.Address().ObjectID}) || !slices.Contains(graph[late], dump.Event{OID: 50002, Phase: dump.TableCompletion}) || len(graph[dump.Event{OID: d.Address().ObjectID}]) != 0 {
		t.Fatal("post-data providers leaked into CREATE", graph)
	}
	if _, err := dump.Order(context.Background(), graph, nil); err != nil {
		t.Fatal("invented CREATE cycle", err)
	}
	graph[dump.Event{OID: provider.Address().ObjectID}] = append(graph[dump.Event{OID: provider.Address().ObjectID}], late)
	if _, err := dump.Order(context.Background(), graph, nil); err == nil {
		t.Fatal("genuine unresolved cycle accepted")
	}
}

// Setup uses the existing private engine fixture; assertions concern actual
// immutable produced roots/outputs and the package's generation transport seam.
func domainAlterGenerationIndexes(t *testing.T) {
	for _, definition := range []struct{ name, table, index string }{
		{"btree_expression", "CREATE TABLE public.subject(id integer)", "CREATE INDEX checked_idx ON public.subject(((id::public.d)::integer))"},
		{"hash_expression", "CREATE TABLE public.subject(id integer)", "CREATE INDEX checked_idx ON public.subject USING hash(((id::public.d)::integer))"},
		{"predicate", "CREATE TABLE public.subject(id integer)", "CREATE INDEX checked_idx ON public.subject(id) WHERE (id::public.d)::integer>0"},
		{"domain_key", "CREATE TABLE public.subject(id public.d)", "CREATE INDEX checked_idx ON public.subject(id)"},
	} {
		t.Run(definition.name, func(t *testing.T) {
			e := engine.New()
			for _, q := range []string{"CREATE SEQUENCE public.steps", "CREATE DOMAIN public.d AS integer CONSTRAINT counted CHECK(nextval('public.steps')>0)", definition.table, definition.index, "INSERT INTO public.subject VALUES(1),(2)", "DELETE FROM public.subject WHERE id=1"} {
				enginetest.Run(t, e, q)
			}
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			view := e.CatalogRead()
			d := domainUnitType(t, view, "d")
			sequence := enginetest.RoleTableRef(t, e, "steps").Address
			parent := enginetest.RoleTableRef(t, e, "subject").Address
			idx := enginetest.RoleTableRef(t, e, "checked_idx").Address
			store, root := e.Storage.Relations[parent], e.Storage.IndexRoots[idx]
			beforeRows := slices.Clone(store.Versions)
			beforeCount := e.Storage.Sequences[sequence].Last
			if beforeCount != 2 {
				t.Fatal("setup did not produce exactly two CHECK events", beforeCount)
			}
			raw := enginetest.Statement(t, "CREATE DOMAIN public.unused AS integer CONSTRAINT never_replay CHECK(FALSE)").(*parser.CreateDomain)
			raw.Constraints[0].Check.NotValid = true
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			stage := e.BeginCatalogStage(auth.Catalog)
			next := *d
			next.DomainChecks = slices.Clone(d.DomainChecks)
			if err == nil {
				err = e.BindDomainConstraintDefinitions(op.Context(), op, auth, stage, &next, raw.Constraints, sqltype.InputEnvironment{})
			}
			if err == nil {
				next.SQLType, err = sqltype.Domain(d.SQLType.ID(), d.Name(), d.SQLType.SQLName(), d.SQLType.DomainBase(), true)
			}
			if err == nil {
				next.ObjectRevision, err = e.Catalog.RevisionLocked()
			}
			if err == nil {
				err = stage.Replace(&next)
			}
			stage.DomainAlterState().Ctx = context.Background()
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			domainUnitPublish(t, e, stage)
			if e.Storage.Sequences[sequence].Last != beforeCount {
				t.Fatal("generation remap replayed SQL truth")
			}
			current := e.Storage.Relations[parent]
			if len(current.Versions) != len(beforeRows) || current.Ends != store.Ends || current.Generation != store.Generation {
				t.Fatal("transport lost all-version storage lineage")
			}
			if definition.name == "domain_key" && current == store {
				t.Fatal("changed domain datums retained their old storage generation")
			}
			// Committed rows intentionally drop pending indexOutputs at commit.
			// An expression-only integer parent can share its unchanged immutable
			// row storage; forcing a wrapper allocation would prove nothing.
			for i, row := range beforeRows {
				if row.Row != store.Versions[i].Row || row.Version != store.Versions[i].Version || row.Values[0] != store.Versions[i].Values[0] {
					t.Fatal("source row mutated")
				}
				if current.Versions[i].Row != row.Row || current.Versions[i].Version != row.Version || current.Versions[i].Born != row.Born {
					t.Fatal("physical version identity/order changed")
				}
			}
			currentIndex := e.Catalog.State.MustRelation(idx)
			currentParent := e.Catalog.State.MustRelation(parent)
			sameKey := func(old, current sqltype.Value, position int) {
				t.Helper()
				before, err := old.BaseValue()
				if err != nil {
					t.Fatal(err)
				}
				after, err := current.BaseValue()
				if err != nil || before != after {
					t.Fatal("index scalar payload changed", before, after, err)
				}
				usage, err := index.KeyUsage(currentParent, currentIndex.SQLIndex.Keys[position])
				if err != nil || current.Type() != usage.Type {
					t.Fatal("index key retained stale logical conversion authority", err)
				}
			}
			newRoot := e.Storage.IndexRoots[idx]
			if root.Hash == nil {
				if len(newRoot.Entries) != len(root.Entries) {
					t.Fatal("B-tree membership changed")
				}
				for i, entry := range root.Entries {
					next := newRoot.Entries[i]
					if entry.Row != next.Row || entry.Version != next.Version || entry.Position != next.Position || len(entry.Keys) != len(next.Keys) {
						t.Fatal("B-tree row identities changed")
					}
					for key, value := range entry.Keys {
						sameKey(value, next.Keys[key], key)
					}
				}
			} else {
				if newRoot.Hash.Storage != current {
					t.Fatal("hash storage identity changed")
				}
				entries := make(map[index.RowIdentity]storage.HashEntry)
				for _, bucket := range newRoot.Hash.Buckets {
					for _, entry := range bucket {
						entries[index.RowIdentity{Row: entry.Row, Version: entry.Version}] = entry
					}
				}
				count := 0
				for _, bucket := range root.Hash.Buckets {
					for _, entry := range bucket {
						next, ok := entries[index.RowIdentity{Row: entry.Row, Version: entry.Version}]
						if !ok || next.Position != entry.Position {
							t.Fatal("hash row membership changed")
						}
						sameKey(entry.Key, next.Key, 0)
						count++
					}
				}
				if len(entries) != count {
					t.Fatal("hash gained unexpected membership")
				}
			}
		})
	}
}

func domainAlterReplayOwnership(t *testing.T) {
	prefix := "CREATE SCHEMA owned AUTHORIZATION postgres; "
	directives := "SET SESSION AUTHORIZATION 'captured_owner'; SET SESSION AUTHORIZATION 'postgres'; "
	create := "CREATE DOMAIN owned.d AS integer CONSTRAINT upper_bound CHECK(VALUE<10); "
	owner := "ALTER DOMAIN owned.d OWNER TO captured_owner; "
	for _, tc := range []struct {
		name, sql string
		valid     bool
	}{
		{"exact", prefix + directives + create + owner + "CREATE TABLE owned.t(v owned.d); INSERT INTO owned.t VALUES(-1); ALTER DOMAIN owned.d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID;", true},
		{"missing_directives", prefix + create + owner, false},
		{"missing_bootstrap_transition", prefix + "SET SESSION AUTHORIZATION 'captured_owner'; " + create + owner, false},
		{"wrong_target_role", prefix + directives + create + "ALTER DOMAIN owned.d OWNER TO other_owner;", false},
		{"identity_target", prefix + directives + create + "ALTER DOMAIN owned.d OWNER TO CURRENT_USER;", false},
		{"unqualified", prefix + directives + create + "ALTER DOMAIN d OWNER TO captured_owner;", false},
		{"wrong_namespace", prefix + directives + create + "ALTER DOMAIN public.d OWNER TO captured_owner;", false},
		{"wrong_kind", prefix + directives + create + "ALTER TYPE owned.d OWNER TO captured_owner;", false},
		{"existing_other_domain", prefix + "CREATE DOMAIN owned.other AS integer; " + directives + create + "ALTER DOMAIN owned.other OWNER TO captured_owner;", false},
		{"intervening_statement", prefix + directives + create + "CREATE TABLE owned.gap(v integer); " + owner, false},
		{"intervening_directive", prefix + directives + create + "SET SESSION AUTHORIZATION 'postgres'; " + owner, false},
		{"reversed_pair", prefix + create + directives + owner, false},
		{"consumed_pair", prefix + directives + create + owner + owner, false},
		{"no_constructor", prefix + directives + owner, false},
		{"enum_not_domain", prefix + directives + "CREATE TYPE owned.d AS ENUM('a'); " + owner, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			for _, q := range []string{"CREATE ROLE captured_owner", "CREATE ROLE other_owner", "CREATE TABLE public.sentinel(v integer)", "INSERT INTO public.sentinel VALUES(42)"} {
				enginetest.Run(t, e, q)
			}
			before, stores, roots := e.Catalog.State, maps.Clone(e.Storage.Relations), maps.Clone(e.Storage.IndexRoots)
			err := engine.Restore(context.Background(), e, strings.NewReader(tc.sql))
			if !tc.valid {
				if err == nil || e.Catalog.State != before || !maps.Equal(e.Storage.Relations, stores) || !maps.Equal(e.Storage.IndexRoots, roots) {
					t.Fatal("ineligible owner replay changed destination", err)
				}
				message := typedef.RestoreTypeOwnerBoundary("domain")
				if tc.name == "wrong_kind" {
					message = table.RestoreInheritedOwnerBoundary
				}
				domainUnitDiagnostic(t, err, "0A000", message)
				return
			}
			if err != nil {
				t.Fatal("exact fresh-domain replay", err)
			}
			view := e.CatalogRead()
			namespace := view.Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "owned"})[0]
			ns := view.MustNamespace(namespace)
			domain := view.MustType(view.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: "d"})[0])
			role, ok := e.Roles().Lookup("captured_owner")
			bootstrap, rootOK := e.Roles().Lookup("postgres")
			if !ok || !rootOK || domain.Owner != role.Ref || ns.Owner != bootstrap.Ref || ns.ACL.Explicit || !typedef.DefaultDomainACL(domain) {
				t.Fatal("replay invented namespace ACL or lost real domain ownership/default ACL")
			}
			if len(domain.DomainChecks) != 2 || !domain.DomainChecks[0].NotValid || domain.DomainChecks[1].NotValid {
				t.Fatal("replay lost selected validity state")
			}
			s := enginetest.StartSession(t, e, nil)
			enginetest.TransactionRows(t, s, "SELECT v::integer AS v FROM owned.t", -1)
		})
	}
}
