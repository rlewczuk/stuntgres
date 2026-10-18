package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Fixtures construct ordinary physical catalogs through existing package
// providers. The witnesses below drive real scopes/controller/cache functions;
// no driver SQL, mocked binder, sleep or production observer is involved.
type inheritanceAlterAdmissionCase struct {
	e      *Engine
	op     *Operation
	scope  *StatementScope
	raw    *parser.AlterTable
	auth   AuthorizationView
	cancel context.CancelFunc
}

func inheritanceAlterAdmissionScope(t *testing.T, e *Engine, role, sql string) *inheritanceAlterAdmissionCase {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	principal, err := e.ResolveLogin(role)
	if err != nil {
		t.Fatal(err)
	}
	session, err := e.StartSession(ctx, SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: principal})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		session.RequestEnd(CleanClose)
		if err := session.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	raw := statement(t, sql).(*parser.AlterTable)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(context.Canceled) })
	if err := scope.gate(raw); err != nil {
		t.Fatal(err)
	}
	if err := scope.acquireRelation(raw, true); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return &inheritanceAlterAdmissionCase{e, op, scope, raw, auth, cancel}
}
func (c *inheritanceAlterAdmissionCase) run() error {
	c.e.mu.Lock()
	prepared, err := c.e.prepareAlterTableActions(c.scope, c.op, c.auth, c.scope.lockedDescriptor.(*relationDescriptor), c.scope.lockedRef, c.raw.Actions)
	if err != nil {
		c.e.mu.Unlock()
		return err
	}
	command := *c.raw
	command.Actions = prepared.actions
	_, err = c.e.alterInheritanceLocked(c.scope, c.op, c.auth, &command, 0, prepared.destination, prepared.types)
	return err
}
func inheritanceAlterAXHolder(t *testing.T, e *Engine, table *relationDescriptor) *StatementScope {
	t.Helper()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	if err := scope.gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	ref := e.catalog.domain.Ref(e.CatalogRead().Epoch(), table.address)
	if err := e.locks.Acquire(op.Context(), scope.ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	return scope
}
func inheritanceAlterWorker(t *testing.T, c *inheritanceAlterAdmissionCase, holder *StatementScope) (<-chan error, func()) {
	t.Helper()
	result := make(chan error, 1)
	joined := false
	go func() { result <- c.run() }()
	t.Cleanup(func() {
		c.cancel()
		holder.Finish(nil)
		if !joined {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("ALTER admission worker did not join")
			}
		}
	})
	return result, func() { joined = true }
}
func inheritanceAlterJoin(t *testing.T, result <-chan error, joined func()) error {
	t.Helper()
	select {
	case err := <-result:
		joined()
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("ALTER controller did not finish")
		return nil
	}
}
func inheritanceAlterPending(t *testing.T, c *inheritanceAlterAdmissionCase, table *relationDescriptor) *inheritanceAlterState {
	t.Helper()
	lockPending(t, c.e, 1)
	ref := c.e.catalog.domain.Ref(c.e.CatalogRead().Epoch(), table.address)
	lockState := c.e.locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
	correct := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Ticket == c.scope.ticket && entry.Pending[0].Mode == lock.AccessExclusive
	if !correct {
		t.Fatal("ALTER did not queue the actual descendant AX request")
	}
	// The pending request's manager synchronization establishes that this worker
	// is suspended after preparation and before any stage or row capture.
	state := c.scope.inheritanceAlter
	if state == nil || state.stage != nil || c.scope.view != nil || c.scope.partitionValidationView != nil || len(state.changes.storage) != 0 {
		t.Fatal("queued preparation allocated stage/view or evaluated rows")
	}
	for address, prepared := range state.prepared {
		if prepared == nil || prepared.old == nil {
			t.Fatalf("missing actual prepared node for %v", address)
		}
		for index, step := range prepared.steps {
			if step.err != nil {
				t.Fatalf("%s cached action %d failed: %v", prepared.old.name, index, step.err)
			}
		}
		for index, step := range prepared.inline {
			if step.err != nil {
				t.Fatalf("%s cached inline action %d failed: %v", prepared.old.name, index, step.err)
			}
		}
		for index, step := range prepared.usingNotNull {
			if step.err != nil {
				t.Fatalf("%s cached USING INDEX not-null action %d failed: %v", prepared.old.name, index, step.err)
			}
		}
		if prepared.afterDropError != nil {
			t.Fatalf("%s cached post-DROP effects failed: %v", prepared.old.name, prepared.afterDropError)
		}
		if prepared.usingCaptureError != nil {
			t.Fatalf("%s cached index capture failed: %v", prepared.old.name, prepared.usingCaptureError)
		}
		if prepared.checksError != nil {
			t.Fatalf("%s cached CHECK preparation failed: %v", prepared.old.name, prepared.checksError)
		}
	}
	return state
}
func inheritanceAlterAssertRows(t *testing.T, e *Engine, table *relationDescriptor, want []sqltype.Value) {
	t.Helper()
	store := e.storage[table.address]
	if store == nil || len(store.versions) != 1 || !reflect.DeepEqual(store.versions[0].values, want) {
		t.Fatal("wrong physical replacement row", table.name, store)
	}
}
func inheritanceAlterRowRoots(e *Engine) map[ids.ObjectAddress][]rowVersion {
	roots := make(map[ids.ObjectAddress][]rowVersion, len(e.storage))
	for address, store := range e.storage {
		rows := slices.Clone(store.versions)
		for i := range rows {
			rows[i].values = slices.Clone(rows[i].values)
		}
		roots[address] = rows
	}
	return roots
}
func inheritanceAlterGrandchild(t *testing.T, e *Engine, c *relationDescriptor) *relationDescriptor {
	t.Helper()
	stage, g := queryCreateStage(t, e, "g", 1)
	g.inheritance = &tableInheritance{parents: []ids.ObjectAddress{c.address}, columns: map[int32]inheritedColumn{g.attributes[0]: {origins: []inheritanceOrigin{{parent: c.address, attribute: c.attributes[0]}}}}, notNull: map[int32]inheritedConstraint{}, checks: map[string]inheritedConstraint{}}
	if err := e.stageQueryTableRows(context.Background(), stage, g, [][]sqltype.Value{{inheritanceQueryInt(e, 30)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	return g
}

func TestInheritanceAlterAdmissionGrowth(t *testing.T) {
	e, p, c := inheritanceDMLPublishedGraph(t)
	if c.hasSubclasses {
		t.Fatal("fixture already has a grandchild")
	}
	holder := inheritanceAlterAXHolder(t, e, c)
	command := inheritanceAlterAdmissionScope(t, e, "postgres", "ALTER TABLE p ADD COLUMN extra INT DEFAULT 7")
	result, joined := inheritanceAlterWorker(t, command, holder)
	state := inheritanceAlterPending(t, command, c)
	if state.prepared[p.address] == nil || state.prepared[c.address] != nil {
		t.Fatal("prefix was not prepared up to the actual child wait")
	}
	beforeGrowth := e.CatalogRead()
	g := inheritanceAlterGrandchild(t, e, c)
	grown := e.CatalogRead()
	currentChild := grown.MustRelation(c.address)
	if currentChild == c || !currentChild.hasSubclasses || currentChild.subclassPredecessor != c || currentChild.revision != currentChild.subclassPublication || !inheritanceMarkerPublication(c, currentChild) {
		t.Fatal("first-grandchild publication was not the actual narrow marker copy")
	}
	if grown.Epoch() != beforeGrowth.Epoch() {
		t.Fatal("ordinary growth replaced catalog lifetime")
	}
	holder.Finish(nil)
	if err := inheritanceAlterJoin(t, result, joined); err != nil {
		t.Fatal("additive first-grandchild growth was treated as replacement", err)
	}
	if state.stage == nil || state.stage.base.state != grown.state || state.stage.base.Get(g.address) != g || len(state.targets) != 3 {
		t.Fatal("sole stage did not capture reconciled actual growth")
	}
	if e.CatalogRead().state != grown.state {
		t.Fatal("controller published before statement Finish")
	}
	for _, old := range []*relationDescriptor{p, currentChild, g} {
		ref := e.catalog.domain.Ref(grown.Epoch(), old.address)
		if !command.scope.holdsColumnDropGrant(ref) {
			t.Fatal("expanded graph lacks real AX admission", old.name)
		}
		replacement := command.scope.catalog.descriptors[old.address].(*relationDescriptor)
		if len(replacement.columns) != 2 || replacement.columns[1].Name != "extra" {
			t.Fatal("new grandchild missed actual provider application", old.name)
		}
	}
	for _, pair := range []struct {
		old   *relationDescriptor
		value int64
	}{{p, 10}, {c, 20}, {g, 30}} {
		inheritanceAlterAssertRows(t, e, pair.old, []sqltype.Value{inheritanceQueryInt(e, pair.value)})
	}
	if err := command.scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		old   *relationDescriptor
		value int64
	}{{p, 10}, {c, 20}, {g, 30}} {
		current := e.CatalogRead().MustRelation(pair.old.address)
		inheritanceAlterAssertRows(t, e, current, []sqltype.Value{inheritanceQueryInt(e, pair.value), inheritanceQueryInt(e, 7)})
	}
	lockEmpty(t, e)
}

func TestInheritanceAlterAdmissionReplacement(t *testing.T) {
	e, p, c := inheritanceDMLPublishedGraph(t)
	holder := inheritanceAlterAXHolder(t, e, c)
	command := inheritanceAlterAdmissionScope(t, e, "postgres", "ALTER TABLE p ADD COLUMN extra INT DEFAULT 7")
	result, joined := inheritanceAlterWorker(t, command, holder)
	state := inheritanceAlterPending(t, command, c)
	g := inheritanceAlterGrandchild(t, e, c)
	marker := e.CatalogRead().MustRelation(c.address)
	if !inheritanceMarkerPublication(c, marker) {
		t.Fatal("fixture failed to publish genuine first-child marker")
	}
	// A subsequent actual metadata publication keeps the marker's fields but is
	// not the exact sticky-marker publication. It must never inherit its waiver.
	later := *marker
	later.revision++
	later.comment = "subsequent physical descriptor mutation"
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Replace(&later); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	stable := e.CatalogRead()
	stores := maps.Clone(e.storage)
	if inheritanceMarkerPublication(c, &later) {
		t.Fatal("subsequent mutation laundered the first-child marker")
	}
	holder.Finish(nil)
	err := inheritanceAlterJoin(t, result, joined)
	if !errors.Is(err, ids.ErrCatalogReplaced) || state.stage != nil {
		t.Fatal("subsequent replacement reached stage or bypassed exact fence", err)
	}
	command.scope.Finish(err)
	if e.CatalogRead().state != stable.state || !reflect.DeepEqual(stores, e.storage) || e.CatalogRead().Get(g.address) != g || len(e.CatalogRead().MustRelation(p.address).columns) != 1 {
		t.Fatal("replacement veto leaked earlier prepared root or retargeted the graph")
	}
	lockEmpty(t, e)
}

func TestInheritanceAlterAdmissionStops(t *testing.T) {
	t.Run("valid_root_notnull", func(t *testing.T) {
		e := New()
		for _, q := range []string{"CREATE ROLE alter_owner LOGIN", "CREATE ROLE other_owner LOGIN", "CREATE TABLE p(a INT CONSTRAINT nn NOT NULL)", "CREATE TABLE c() INHERITS(p)", "ALTER TABLE p OWNER TO alter_owner", "ALTER TABLE c OWNER TO other_owner"} {
			ownershipUnitExec(t, e, q)
		}
		p := retiredUnitTable(t, e, "p")
		c := retiredUnitTable(t, e, "c")
		holder := inheritanceAlterAXHolder(t, e, c)
		command := inheritanceAlterAdmissionScope(t, e, "alter_owner", "ALTER TABLE p ALTER a SET NOT NULL")
		result, joined := inheritanceAlterWorker(t, command, holder)
		if err := inheritanceAlterJoin(t, result, joined); err != nil {
			t.Fatal("valid root fact reached held/foreign-owner child", err)
		}
		if command.scope.holdsColumnDropGrant(e.catalog.domain.Ref(e.CatalogRead().Epoch(), c.address)) || command.scope.catalog.descriptors[c.address] != nil || command.scope.catalog.storage[c.address].replacement != nil {
			t.Fatal("valid NN no-op acquired or changed child")
		}
		if err := command.scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		if retiredUnitTable(t, e, "c") != c || retiredUnitTable(t, e, "p").notNullConstraints[0].OID != p.notNullConstraints[0].OID {
			t.Fatal("valid root NN changed physical identities")
		}
		holder.Finish(nil)
		lockEmpty(t, e)
	})
	t.Run("plain_add_merge_prefix", func(t *testing.T) {
		e := New()
		for _, q := range []string{"CREATE ROLE alter_owner LOGIN", "CREATE ROLE other_owner LOGIN", "CREATE TABLE p(a INT)", "CREATE TABLE c(extra INT DEFAULT 9) INHERITS(p)", "CREATE TABLE leaf() INHERITS(c)", "INSERT INTO c VALUES(1,19)", "INSERT INTO leaf VALUES(2,29)", "ALTER TABLE p OWNER TO alter_owner", "ALTER TABLE c OWNER TO alter_owner", "ALTER TABLE leaf OWNER TO other_owner"} {
			ownershipUnitExec(t, e, q)
		}
		c := retiredUnitTable(t, e, "c")
		leaf := retiredUnitTable(t, e, "leaf")
		holder := inheritanceAlterAXHolder(t, e, leaf)
		command := inheritanceAlterAdmissionScope(t, e, "alter_owner", "ALTER TABLE p ADD COLUMN extra INT DEFAULT 7")
		result, joined := inheritanceAlterWorker(t, command, holder)
		if err := inheritanceAlterJoin(t, result, joined); err != nil {
			t.Fatal("plain ADD crossed existing-column merge stop", err)
		}
		ref := e.catalog.domain.Ref(e.CatalogRead().Epoch(), leaf.address)
		if command.scope.holdsColumnDropGrant(ref) || command.scope.catalog.descriptors[leaf.address] != nil {
			t.Fatal("plain ADD locked/owned below actual merge stop")
		}
		if err := command.scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		current := retiredUnitTable(t, e, "c")
		attribute := current.attributes[current.index["extra"]]
		if !current.inheritance.columns[attribute].local || len(current.inheritance.columns[attribute].origins) != 1 || !sameGroupExpression(current.defaults[attribute].expression, c.defaults[attribute].expression) || retiredUnitTable(t, e, "leaf") != leaf {
			t.Fatal("merge replaced local recipe or changed stopped leaf")
		}
		inheritanceAlterAssertRows(t, e, current, []sqltype.Value{inheritanceQueryInt(e, 1), inheritanceQueryInt(e, 19)})
		inheritanceAlterAssertRows(t, e, leaf, []sqltype.Value{inheritanceQueryInt(e, 2), inheritanceQueryInt(e, 29)})
		holder.Finish(nil)
		lockEmpty(t, e)
	})
	for _, kind := range []string{"notnull", "check"} {
		t.Run("inline_"+kind, func(t *testing.T) {
			e := New()
			for _, q := range []string{"CREATE TABLE p(a INT)", "CREATE TABLE c(extra INT) INHERITS(p)", "CREATE TABLE leaf() INHERITS(c)", "INSERT INTO c VALUES(1,19)"} {
				ownershipUnitExec(t, e, q)
			}
			suffix := " NOT NULL"
			value := "NULL"
			want := "23502"
			if kind == "check" {
				suffix = " CHECK(extra>0)"
				value = "-1"
				want = "23514"
			}
			ownershipUnitExec(t, e, "INSERT INTO leaf VALUES(2,"+value+")")
			leaf := retiredUnitTable(t, e, "leaf")
			holder := inheritanceAlterAXHolder(t, e, leaf)
			before := e.CatalogRead()
			stores := maps.Clone(e.storage)
			rows := inheritanceAlterRowRoots(e)
			command := inheritanceAlterAdmissionScope(t, e, "postgres", "ALTER TABLE p ADD COLUMN extra INT DEFAULT 7"+suffix)
			result, joined := inheritanceAlterWorker(t, command, holder)
			state := inheritanceAlterPending(t, command, leaf)
			c := retiredUnitTable(t, e, "c")
			if state.prepared[c.address] == nil {
				t.Fatal("inline fact did not reach existing-column merge node")
			}
			holder.Finish(nil)
			err := inheritanceAlterJoin(t, result, joined)
			var coded interface{ SQLState() string }
			if !errors.As(err, &coded) || coded.SQLState() != want {
				t.Fatal("inline fact failed to reach physical leaf validator", err)
			}
			command.scope.Finish(err)
			if e.CatalogRead().state != before.state || !reflect.DeepEqual(stores, e.storage) {
				t.Fatal("late inline leaf failure partially changed parent/child")
			}
			if !reflect.DeepEqual(rows, inheritanceAlterRowRoots(e)) {
				t.Fatal("late inline veto rewrote physical row values")
			}
			lockEmpty(t, e)
		})
	}
}

func TestInheritanceAlterAdmissionPreparedEffects(t *testing.T) {
	for _, provider := range []struct{ name, declaration, removed, action string }{
		{"identity", "a INT GENERATED BY DEFAULT AS IDENTITY,b INT", "p_a_seq", "ALTER a DROP IDENTITY"},
		{"primary_index", "a INT PRIMARY KEY,b INT", "p_pkey", "DROP CONSTRAINT p_pkey"},
	} {
		t.Run("removed_"+provider.name+"_default", func(t *testing.T) {
			e := New()
			for _, q := range []string{"CREATE TABLE p(" + provider.declaration + ")", "CREATE TABLE c() INHERITS(p)", "INSERT INTO p(a,b) VALUES(1,10)", "INSERT INTO c(a,b) VALUES(2,20)"} {
				ownershipUnitExec(t, e, q)
			}
			removed := retiredUnitTable(t, e, provider.removed)
			before := e.CatalogRead()
			stores := maps.Clone(e.storage)
			rows := inheritanceAlterRowRoots(e)
			generators := maps.Clone(e.sequences)
			command := inheritanceAlterAdmissionScope(t, e, "postgres", "ALTER TABLE p "+provider.action+",ADD COLUMN z REGCLASS DEFAULT 'public."+provider.removed+"'::regclass")
			err := command.run()
			var coded interface{ SQLState() string }
			if !errors.As(err, &coded) || coded.SQLState() != "42P01" {
				t.Fatal("later DEFAULT bound a provider actually removed in phase zero", provider.name, err)
			}
			command.scope.Finish(err)
			if e.CatalogRead().state != before.state || e.CatalogRead().Get(removed.address) != removed || !reflect.DeepEqual(stores, e.storage) || !reflect.DeepEqual(rows, inheritanceAlterRowRoots(e)) || !reflect.DeepEqual(generators, e.sequences) {
				t.Fatal("removed-provider binding failure partially applied actual DROP effects")
			}
			lockEmpty(t, e)
		})
	}
	for _, interim := range []bool{true, false} {
		t.Run(map[bool]string{true: "interim_rowtype_array_cook_cancel", false: "final_rowtype_array_controller"}[interim], func(t *testing.T) {
			e := New()
			for _, q := range []string{"CREATE TABLE p(a INT,b INT)", "CREATE TABLE c(z INT DEFAULT 8,u INT DEFAULT 9) INHERITS(p)", "INSERT INTO p VALUES(1,10)", "INSERT INTO c VALUES(2,20,8,9)"} {
				ownershipUnitExec(t, e, q)
			}
			p := retiredUnitTable(t, e, "p")
			c := retiredUnitTable(t, e, "c")
			before := e.CatalogRead()
			stores := maps.Clone(e.storage)
			rows := inheritanceAlterRowRoots(e)
			holder := inheritanceAlterAXHolder(t, e, c)
			// These are existing scalar projections, not self-containing columns or a
			// new cast family. Interim cooking is tested separately from fullUsage
			// publication: no claim that an old-shape named payload is publishable.
			defaults := []string{"7", "9"}
			if interim {
				defaults = []string{"(ROW(7)::p).a", "((ARRAY[ROW(8,9)::p])[1]).z"}
			}
			command := inheritanceAlterAdmissionScope(t, e, "postgres", "ALTER TABLE p DROP COLUMN b,ADD COLUMN z INT DEFAULT "+defaults[0]+",ADD COLUMN u INT DEFAULT "+defaults[1]+" CONSTRAINT ck CHECK(((ARRAY[ROW(a,z,u)::p])[1]).u>0)")
			result, joined := inheritanceAlterWorker(t, command, holder)
			state := inheritanceAlterPending(t, command, c)
			prepared := state.prepared[p.address]
			if prepared == nil || prepared.steps[0].drop == nil {
				t.Fatal("rowtype witness did not use actual DROP preparation")
			}
			if prepared.checks.next == nil || len(prepared.checks.next.checkConstraints) != 1 {
				t.Fatal("rowtype fixture did not produce its actual cooked CHECK")
			}
			assertShape := func(typ *sqltype.Descriptor, names []string) {
				t.Helper()
				if !typ.IsComposite() || typ.CompositeLen() != len(names) {
					t.Fatal("wrong actual cooked composite shape", typ, names)
				}
				for i, name := range names {
					if typ.CompositeMember(i).Name != name {
						t.Fatal("reader exposed the base rowtype instead of the actual phase result", typ.CompositeMember(i), name)
					}
				}
			}
			assertPhase := func(effects []Descriptor, names []string) {
				t.Helper()
				row, array := false, false
				for _, effect := range effects {
					typ, ok := effect.(*typeDescriptor)
					if !ok {
						continue
					}
					if typ.typ.ID() == p.rowType {
						assertShape(typ.typ, names)
						row = true
					}
					if typ.typ.IsArray() && typ.typ.Element().ID() == p.rowType {
						assertShape(typ.typ.Element(), names)
						array = true
					}
				}
				if !row || !array {
					t.Fatal("actual rowtype/associated-array provider results were not both captured", names)
				}
			}
			// DROP itself owns its actual rowtype synchronization effects; the
			// subsequent metadata helper correctly has nothing to cook again.
			var dropTypes []Descriptor
			for _, effect := range prepared.steps[0].drop.effects {
				if effect.descriptor != nil {
					dropTypes = append(dropTypes, effect.descriptor)
				}
			}
			assertPhase(dropTypes, []string{"a"})
			assertPhase(prepared.steps[1].types, []string{"a", "z"})
			assertPhase(prepared.steps[2].types, []string{"a", "z", "u"})
			assertShape(before.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(p.rowType)}).typ, []string{"a", "b"})
			if interim {
				for index, names := range [][]string{{"a"}, {"a", "z"}} {
					expression := prepared.steps[index+1].added.expression
					composite, array := false, false
					walkBoundExpression(expression, func(node expr.Expr) {
						typ := node.ResultUsage().Type
						if typ == nil {
							return
						}
						if typ.ID() == p.rowType {
							assertShape(typ, names)
							composite = true
						}
						if typ.IsArray() && typ.Element().ID() == p.rowType {
							assertShape(typ.Element(), names)
							array = true
						}
					})
					if !composite || index == 1 && !array {
						t.Fatal("actual default binder did not consume the phase-specific rowtype/array", index)
					}
				}
				command.cancel()
				err := inheritanceAlterJoin(t, result, joined)
				if !errors.Is(err, context.Canceled) || state.stage != nil {
					t.Fatal("interim metadata cooking escaped cancellation boundary", err)
				}
				command.scope.Finish(err)
				holder.Finish(nil)
				if e.CatalogRead().state != before.state || !reflect.DeepEqual(stores, e.storage) || !reflect.DeepEqual(rows, inheritanceAlterRowRoots(e)) {
					t.Fatal("canceled rowtype cooking changed original physical state")
				}
				lockEmpty(t, e)
				return
			}
			finalComposite, finalArray := false, false
			walkBoundExpression(prepared.checks.next.checkConstraints[0].expression, func(node expr.Expr) {
				typ := node.ResultUsage().Type
				if typ == nil {
					return
				}
				if typ.ID() == p.rowType {
					assertShape(typ, []string{"a", "z", "u"})
					finalComposite = true
				}
				if typ.IsArray() && typ.Element().ID() == p.rowType {
					assertShape(typ.Element(), []string{"a", "z", "u"})
					finalArray = true
				}
			})
			if !finalComposite || !finalArray {
				t.Fatal("actual inline CHECK bound base rather than final rowtype/array")
			}
			holder.Finish(nil)
			if err := inheritanceAlterJoin(t, result, joined); err != nil {
				t.Fatal(err)
			}
			if e.CatalogRead().state != before.state {
				t.Fatal("rowtype controller published before Finish")
			}
			if err := command.scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			inheritanceAlterAssertRows(t, e, retiredUnitTable(t, e, "p"), []sqltype.Value{inheritanceQueryInt(e, 1), inheritanceQueryInt(e, 7), inheritanceQueryInt(e, 9)})
			inheritanceAlterAssertRows(t, e, retiredUnitTable(t, e, "c"), []sqltype.Value{inheritanceQueryInt(e, 2), inheritanceQueryInt(e, 8), inheritanceQueryInt(e, 9)})
			lockEmpty(t, e)
		})
	}
	for _, cancelAtWait := range []bool{false, true} {
		t.Run(map[bool]string{false: "consume", true: "cancel"}[cancelAtWait], func(t *testing.T) {
			e := New()
			for _, q := range []string{"CREATE TABLE p(a INT,b INT CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(b>0))", "CREATE TABLE c() INHERITS(p)", "INSERT INTO p VALUES(1,10)", "INSERT INTO c VALUES(2,20)", "CREATE VIEW old_b AS SELECT b FROM p"} {
				ownershipUnitExec(t, e, q)
			}
			p := retiredUnitTable(t, e, "p")
			c := retiredUnitTable(t, e, "c")
			view := retiredUnitTable(t, e, "old_b")
			before := e.CatalogRead()
			stores := maps.Clone(e.storage)
			rows := inheritanceAlterRowRoots(e)
			holder := inheritanceAlterAXHolder(t, e, c)
			// SQL text ADD precedes DROP; the real provider phases must nevertheless
			// retire old b and its namespace/dependency facts before binding new b.
			command := inheritanceAlterAdmissionScope(t, e, "postgres", "ALTER TABLE p ADD COLUMN b INT DEFAULT 7 CONSTRAINT nn NOT NULL CONSTRAINT ck CHECK(b>0),DROP COLUMN b CASCADE")
			result, joined := inheritanceAlterWorker(t, command, holder)
			state := inheritanceAlterPending(t, command, c)
			prepared := state.prepared[p.address]
			if prepared == nil || prepared.steps[1].drop == nil || prepared.steps[0].added.column == nil || len(prepared.inline) != 1 {
				t.Fatal("actual mixed providers were not prepared before descendant wait")
			}
			drop := prepared.steps[1].drop
			add := prepared.steps[0]
			if drop.old != p || drop.ref != command.scope.lockedRef || drop.next.hasAttribute(p.attributes[p.index["b"]]) || add.old != drop.next || add.added.attribute == p.attributes[p.index["b"]] {
				t.Fatal("mixed ADD cooked against original or guessed DROP shape")
			}
			cooked := prepared.checks.next
			if cooked == nil || len(cooked.checkConstraints) != 1 || len(cooked.notNullConstraints) != 1 {
				t.Fatal("mixed fixture did not produce its actual cooked NN/CHECK")
			}
			newAttribute := cooked.attributes[cooked.index["b"]]
			if !slices.Equal(constraintAttributeKeys(cooked, cooked.checkConstraints[0].expression), []int32{newAttribute}) || cooked.checkConstraints[0].Name != "ck" || cooked.notNullConstraints[0].Name != "nn" || cooked.checkConstraints[0].OID != 0 || cooked.notNullConstraints[0].OID != 0 {
				t.Fatal("cached binder retained removed keys/identities or lost chosen names")
			}
			key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: p.namespace, Name: "old_b"}
			if prepared.reader.lookup(key) != (ids.ObjectAddress{}) || prepared.reader.base.state != before.state || e.CatalogRead().state != before.state || e.CatalogRead().Get(view.address) != view {
				t.Fatal("actual ordered DROP effects were absent or prematurely published")
			}
			if cancelAtWait {
				command.cancel()
				err := inheritanceAlterJoin(t, result, joined)
				if !errors.Is(err, context.Canceled) || state.stage != nil {
					t.Fatal("canceled preparation reached stage or ignored cancellation", err)
				}
				command.scope.Finish(err)
				holder.Finish(nil)
				if e.CatalogRead().state != before.state || !reflect.DeepEqual(stores, e.storage) {
					t.Fatal("canceled mixed preparation changed physical catalog/store")
				}
				for address, store := range stores {
					if !reflect.DeepEqual(rows[address], store.versions) {
						t.Fatal("canceled preparation rewrote rows")
					}
				}
				lockEmpty(t, e)
				return
			}
			holder.Finish(nil)
			if err := inheritanceAlterJoin(t, result, joined); err != nil {
				t.Fatal(err)
			}
			candidate := command.scope.catalog.descriptors[p.address].(*relationDescriptor)
			if candidate.notNullConstraints[0].OID == 0 || candidate.notNullConstraints[0].OID == p.notNullConstraints[0].OID || candidate.checkConstraints[0].OID == 0 || candidate.checkConstraints[0].OID == p.checkConstraints[0].OID || candidate.notNullConstraints[0].Name != cooked.notNullConstraints[0].Name || candidate.notNullConstraints[0].Attribute != cooked.notNullConstraints[0].Attribute || candidate.checkConstraints[0].Name != cooked.checkConstraints[0].Name || !sameGroupExpression(candidate.checkConstraints[0].expression, cooked.checkConstraints[0].expression) {
				t.Fatal("sole pipeline lost exact cooked names/keys/recipe or reused removed fact OIDs")
			}
			if cooked.notNullConstraints[0].OID != 0 || cooked.checkConstraints[0].OID != 0 {
				t.Fatal("normal physical OID assignment mutated immutable preparation")
			}
			if _, consumed := command.scope.consumedColumnDrops[drop]; !consumed || state.stage == nil {
				t.Fatal("sole pipeline did not consume actual DROP effects")
			}
			if e.CatalogRead().state != before.state {
				t.Fatal("cached provider effects published before Finish")
			}
			if err := command.scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			if _, present := e.CatalogRead().Find(view.address); present {
				t.Fatal("genuine CASCADE dependency survived cached DROP")
			}
			for _, pair := range []struct {
				old   *relationDescriptor
				value int64
			}{{p, 1}, {c, 2}} {
				current := e.CatalogRead().MustRelation(pair.old.address)
				if current.attributes[current.index["b"]] == pair.old.attributes[pair.old.index["b"]] {
					t.Fatal("mixed DROP/ADD reused old stable attribute")
				}
				inheritanceAlterAssertRows(t, e, current, []sqltype.Value{inheritanceQueryInt(e, pair.value), inheritanceQueryInt(e, 7)})
			}
			lockEmpty(t, e)
		})
	}
}
