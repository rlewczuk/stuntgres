package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// These fixtures exercise package functions with real initialized catalogs and
// independently constructed ASTs. No parser or public SQL adapter is involved.
func databaseCreateUnitTarget(t *testing.T, e, seed *Engine, name string) *Engine {
	t.Helper()
	target := e.newDatabaseRuntime(seed, name, e.Roles().LookupRef("postgres"))
	if err := e.Locks.AdmitDomain(target.Catalog.Domain); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ReplaceCatalogInOperation(context.Background(), seed, seed.CatalogRead(), nil); err != nil {
		t.Fatal("clone retained template:", err)
	}
	return target
}

func databaseCreateUnitPublish(e *Engine, ctx context.Context, name string, target *Engine) (Result, error) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.publishDatabaseTargetLocked(ctx, name, target, e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID))
}

func databaseCreateUnitInstall(t *testing.T, e, seed *Engine, name string) *Engine {
	t.Helper()
	target := databaseCreateUnitTarget(t, e, seed, name)
	result, err := databaseCreateUnitPublish(e, context.Background(), name, target)
	if err != nil || result.CommandTag != "CREATE DATABASE" || result.Count != 0 || result.Rows != nil {
		t.Fatalf("publication: %#v %v", result, err)
	}
	return target
}

// Failure assertions include the allocator and every previously visible root,
// not just a failed name lookup; a partial fanout must not escape unnoticed.
func databaseCreateUnitUnchanged(t *testing.T, e *Engine) func() {
	t.Helper()
	frontier := e.Cluster.NextOID
	targets := maps.Clone(e.Cluster.Targets)
	roots := make(map[*Engine]*catalog.State, len(targets))
	for _, target := range targets {
		roots[target] = target.Catalog.State
	}
	return func() {
		t.Helper()
		if e.Cluster.NextOID != frontier || !maps.Equal(targets, e.Cluster.Targets) {
			t.Fatal("failed publication changed registered targets or identity frontier")
		}
		for target, root := range roots {
			if target.Catalog.State != root {
				t.Fatal("failed publication changed a visible catalog root")
			}
		}
	}
}

func databaseCreateUnitRows(t *testing.T, e *Engine) map[string]ids.OID {
	t.Helper()
	view := e.CatalogRead()
	descriptor, ref := pgDatabaseUnitSource(t, e, view)
	e.Mu.Lock()
	rows, err := e.captureCatalogSourceLocked(context.Background(), view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ref, descriptor)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make(map[string]ids.OID)
	for _, row := range rows.Anonymous {
		name, err := row[1].Text()
		if err != nil {
			t.Fatal(err)
		}
		oid, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := result[name]; exists {
			t.Fatal("duplicate pg_database name", name)
		}
		result[name] = ids.OID(oid)
	}
	return result
}

func TestDatabaseCreateRegistry(t *testing.T) {
	e, seed := New(), New()
	old := e.CatalogRead()
	initial := e.Cluster.DefaultTarget
	first := databaseCreateUnitInstall(t, e, seed, "registry_first")
	second := databaseCreateUnitInstall(t, first, seed, "registry_second")
	a, b := first.Catalog.DatabaseAddress(), second.Catalog.DatabaseAddress()
	if a.ObjectID == 0 || a.ObjectID <= initial.Catalog.DatabaseAddress().ObjectID || b.ObjectID <= a.ObjectID {
		t.Fatal("database identities are not distinct and monotonic", a, b)
	}
	want := map[string]ids.OID{initial.DatabaseName: initial.Catalog.DatabaseAddress().ObjectID, "registry_first": a.ObjectID, "registry_second": b.ObjectID}
	for _, target := range []*Engine{e, first, second} {
		if got := databaseCreateUnitRows(t, target); !reflect.DeepEqual(got, want) {
			t.Fatalf("registered catalog rows: %v want %v", got, want)
		}
		for name, oid := range want {
			database := target.CatalogRead().MustDatabase(ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: oid})
			if database.Name() != name || database.Owner != e.Roles().LookupRef("postgres") || database.ACL.Explicit {
				t.Fatal("registry descriptor disagrees", database)
			}
		}
	}
	if old.Get(a) != nil || old.Get(b) != nil || e.Cluster.DefaultTarget != initial {
		t.Fatal("publication mutated a retained view or retargeted the default")
	}
	candidate := databaseCreateUnitTarget(t, e, seed, "registry_first")
	unchanged := databaseCreateUnitUnchanged(t, e)
	_, err := databaseCreateUnitPublish(e, context.Background(), "registry_first", candidate)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P04" {
		t.Fatal("sequential duplicate:", err)
	}
	unchanged()
	t.Run("identity_reservations", func(t *testing.T) {
		host, template := New(), New()
		// A private DDL stage may have reserved OIDs without publishing its
		// descriptors. Database publication must not consume those identities.
		host.Mu.Lock()
		pending, err := host.Catalog.AllocateOIDLocked()
		host.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		selected := databaseCreateUnitInstall(t, host, template, "identity_selected")
		if selected.Catalog.DatabaseAddress().ObjectID <= pending {
			t.Fatal("database reused a transaction-private allocation")
		}
		for _, runtime := range []*Engine{host, selected} {
			if err := catalog.ValidateDefinitionOIDs(runtime.CatalogRead()); err != nil {
				t.Fatal("registration collided with a seeded definition:", err)
			}
			// Future local allocations must skip projected registrations even
			// when the local frontier reaches another target's database OID.
			runtime.Mu.Lock()
			runtime.Catalog.InstallOIDFrontierLocked(selected.Catalog.DatabaseAddress().ObjectID)
			allocated, err := runtime.Catalog.AllocateOIDLocked()
			runtime.Mu.Unlock()
			if err != nil || allocated == selected.Catalog.DatabaseAddress().ObjectID {
				t.Fatal("local allocator reused a database identity", allocated, err)
			}
		}
		op, done := roleOperation(t, selected, "postgres")
		defer done()
		if _, err := selected.Execute(op, primaryKeyUnitCreate("identity_table", "identity_pk"), 0, nil); err != nil {
			t.Fatal("selected database DDL:", err)
		}
		second := databaseCreateUnitInstall(t, host, template, "identity_second")
		for _, runtime := range []*Engine{host, selected, second} {
			if err := catalog.ValidateDefinitionOIDs(runtime.CatalogRead()); err != nil {
				t.Fatal("later registration collided with stored DDL:", err)
			}
		}
	})
	for _, frontier := range []ids.OID{0, ^ids.OID(0)} {
		e.Cluster.NextOID = frontier
		candidate := databaseCreateUnitTarget(t, e, seed, "exhausted")
		unchanged := databaseCreateUnitUnchanged(t, e)
		if _, err := databaseCreateUnitPublish(e, context.Background(), "exhausted", candidate); err == nil {
			t.Fatal("exhausted identity frontier accepted", frontier)
		}
		unchanged()
	}
}

func TestDatabaseCreateTemplateCopy(t *testing.T) {
	e, seed := New(), New()
	op, done := roleOperation(t, seed, "postgres")
	defer done()
	for _, statement := range []parser.Statement{
		primaryKeyUnitCreate("t", "template_pk"), primaryKeyUnitInsert(1),
		&parser.CreateSequence{Name: primaryKeyUnitName("template_seq"), Persistence: "logged", Options: []parser.SequenceOption{{Name: "start", Value: 41}}},
	} {
		if _, err := seed.Execute(op, statement, 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	left := databaseCreateUnitInstall(t, e, seed, "copy_left")
	right := databaseCreateUnitInstall(t, e, seed, "copy_right")
	table := primaryKeyUnitRelation(t, seed, "t")
	indexAddress := table.Constraints.Unique[0].Index
	sequenceAddress := primaryKeyUnitRelation(t, seed, "template_seq").Address()
	for _, target := range []*Engine{left, right} {
		view := target.CatalogRead()
		for _, name := range []string{"pg_catalog", "pg_toast", "information_schema", "public"} {
			address, ok := view.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
			if !ok {
				t.Fatal("missing real template namespace", name)
			}
			namespace := view.MustNamespace(address)
			owner := "postgres"
			if name == "public" {
				owner = "pg_database_owner"
			}
			if namespace.Owner != target.Roles().LookupRef(owner) {
				t.Fatal("template owner was not reprovenanced", name, namespace.Owner)
			}
		}
	}
	leftOp, leftDone := roleOperation(t, left, "postgres")
	defer leftDone()
	if result, err := left.Execute(leftOp, primaryKeyUnitInsert(2), 0, nil); err != nil || result.Count != 1 {
		t.Fatal("clone insert", result, err)
	}
	// A copied index must enforce existing keys, then maintain only its own rows.
	if _, err := left.Execute(leftOp, primaryKeyUnitInsert(1), 0, nil); err == nil {
		t.Fatal("copied unique index lost template membership")
	}
	for target, count := range map[*Engine]int{seed: 1, right: 1, left: 2} {
		if len(target.Storage.Relations[table.Address()].Versions) != count || len(target.Storage.IndexRoots[indexAddress].Entries) != count {
			t.Fatal("clone row/index mutation leaked or lost membership", count)
		}
	}
	var leftMemory, rightMemory sequence.SessionMemory
	left.Mu.Lock()
	value, err := sequence.CallLocked(functions.SequenceRequest{Name: "nextval"}, left.Catalog.State.MustRelation(sequenceAddress), left.Storage.Sequences[sequenceAddress], &leftMemory)
	left.Mu.Unlock()
	if err != nil || value != 41 {
		t.Fatal("left nextval", value, err)
	}
	left.Mu.Lock()
	value, err = sequence.CallLocked(functions.SequenceRequest{Name: "nextval"}, left.Catalog.State.MustRelation(sequenceAddress), left.Storage.Sequences[sequenceAddress], &leftMemory)
	left.Mu.Unlock()
	if err != nil || value != 42 {
		t.Fatal("left second nextval", value, err)
	}
	right.Mu.Lock()
	value, err = sequence.CallLocked(functions.SequenceRequest{Name: "nextval"}, right.Catalog.State.MustRelation(sequenceAddress), right.Storage.Sequences[sequenceAddress], &rightMemory)
	right.Mu.Unlock()
	if err != nil || value != 41 || seed.Storage.Sequences[sequenceAddress].Called {
		t.Fatal("sequence mutation leaked across copies or into seed", value, err)
	}
}

func TestDatabaseCreatePublication(t *testing.T) {
	e, seed := New(), New()
	candidates := []*Engine{databaseCreateUnitTarget(t, e, seed, "contended"), databaseCreateUnitTarget(t, e, seed, "contended")}
	frontier := e.Cluster.NextOID
	start := make(chan struct{})
	type outcome struct {
		target *Engine
		err    error
	}
	results := make(chan outcome, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, candidate := range candidates {
		go func(target *Engine) {
			<-start
			_, err := databaseCreateUnitPublish(e, ctx, "contended", target)
			results <- outcome{target, err}
		}(candidate)
	}
	close(start)
	joined := make([]outcome, 0, len(candidates))
	for range candidates {
		select {
		case result := <-results:
			joined = append(joined, result)
		case <-ctx.Done():
			t.Fatal("publication workers did not finish:", ctx.Err())
		}
	}
	var winner, loser *Engine
	for _, result := range joined {
		if result.err == nil {
			if winner != nil {
				t.Fatal("two publication winners")
			}
			winner = result.target
		} else {
			var diagnostic *sqlstate.Error
			if !errors.As(result.err, &diagnostic) || diagnostic.Code != "42P04" {
				t.Fatal(result.err)
			}
			loser = result.target
		}
	}
	if winner == nil || loser == nil {
		t.Fatal("publication did not produce one winner and one loser")
	}
	winnerOID := winner.Catalog.DatabaseAddress().ObjectID
	if e.Cluster.Targets["contended"] != winner || winnerOID < frontier || e.Cluster.NextOID != winnerOID+1 || loser.Cluster != nil {
		t.Fatal("contention exposed an orphan or consumed a losing identity")
	}
	want := map[string]ids.OID{e.DatabaseName: e.Catalog.DatabaseAddress().ObjectID, "contended": winnerOID}
	for _, target := range []*Engine{e, winner} {
		if got := databaseCreateUnitRows(t, target); !reflect.DeepEqual(got, want) {
			t.Fatal(got, want)
		}
	}
}

// Err is the helper's final admission boundary, after all candidate roots have
// been constructed. Cancel there rather than racing a fast copy with a timer.
type databaseCreateCancelContext struct {
	context.Context
	cancel   context.CancelFunc
	admitted bool
}

func (c *databaseCreateCancelContext) Err() error {
	c.admitted = true
	c.cancel()
	return c.Context.Err()
}

func TestDatabaseCreateCancellation(t *testing.T) {
	e, seed := New(), New()
	databaseCreateUnitInstall(t, e, seed, "existing")
	target := databaseCreateUnitTarget(t, e, seed, "canceled")
	unchanged := databaseCreateUnitUnchanged(t, e)
	root, address := target.Catalog.State, target.Catalog.DatabaseAddress()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &databaseCreateCancelContext{Context: base, cancel: cancel}
	if _, err := databaseCreateUnitPublish(e, ctx, "canceled", target); !errors.Is(err, context.Canceled) || !ctx.admitted {
		t.Fatal("final cancellation was not honored", err)
	}
	unchanged()
	if target.Cluster != nil || target.Catalog.State != root || target.Catalog.DatabaseAddress() != address {
		t.Fatal("cancellation partially installed the private target")
	}
	if selected, err := e.SelectDatabase(context.Background(), "canceled"); selected != nil || err == nil {
		t.Fatal("canceled target can be selected", selected, err)
	}
}

func TestDatabaseCreateProvenance(t *testing.T) {
	e, seed, foreign := New(), New(), New()
	local := databaseCreateUnitInstall(t, e, seed, "local")
	for name, candidate := range map[string]*Engine{
		"foreign":    databaseCreateUnitTarget(t, foreign, seed, "foreign"),
		"wrong_name": databaseCreateUnitTarget(t, e, seed, "different_name"),
	} {
		t.Run(name, func(t *testing.T) {
			unchanged := databaseCreateUnitUnchanged(t, e)
			if _, err := databaseCreateUnitPublish(e, context.Background(), name, candidate); err == nil {
				t.Fatal("invalid target accepted")
			}
			unchanged()
		})
	}
	candidate := databaseCreateUnitTarget(t, e, seed, "foreign_owner")
	unchanged := databaseCreateUnitUnchanged(t, e)
	e.Mu.Lock()
	_, err := e.publishDatabaseTargetLocked(context.Background(), "foreign_owner", candidate, foreign.Roles().LookupRef("postgres"))
	e.Mu.Unlock()
	if err == nil {
		t.Fatal("equal numeric foreign principal accepted")
	}
	unchanged()
	// Builtins deliberately have equal numeric addresses in every local domain.
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259}
	for _, source := range []*Engine{e, foreign} {
		view := source.CatalogRead()
		ref := source.Catalog.Domain.Ref(view.Epoch(), address)
		if err := local.ValidateObjectRef(local.CatalogRead(), ref); err == nil {
			t.Fatal("cross-target object reference accepted", ref)
		}
	}
	view := local.CatalogRead()
	if err := local.ValidateObjectRef(view, local.Catalog.Domain.Ref(view.Epoch(), address)); err != nil {
		t.Fatal("own reference rejected", err)
	}
}

func TestDatabaseCreateSharedAuthority(t *testing.T) {
	e, seed := New(), New()
	left := databaseCreateUnitInstall(t, e, seed, "authority_left")
	right := databaseCreateUnitInstall(t, e, seed, "authority_right")
	admin, done := roleOperation(t, left, "postgres")
	defer done()
	old := right.Roles()
	actor := roleAttributeCreate(t, left, admin, "shared_actor", true, parser.RoleOptions{Present: parser.RoleCreateDB, CreateDB: true})
	if _, ok := old.Lookup(actor.Name); ok {
		t.Fatal("retained role capture changed")
	}
	for _, target := range []*Engine{e, left, right} {
		got, ok := target.Roles().Lookup(actor.Name)
		if !ok || got != actor {
			t.Fatal("committed shared role is not visible", got)
		}
		if ref, err := target.ResolveLogin(actor.Name); err != nil || ref != actor.Ref {
			t.Fatal("shared role cannot authenticate", ref, err)
		}
	}
	held, heldDone := roleOperation(t, right, actor.Name)
	defer heldDone()
	roleAttributeApply(t, left, admin, roleAttributeAlter(actor.Name, parser.RoleOptions{Present: parser.RoleCreateDB, CreateDB: false}))
	right.Mu.Lock()
	auth, err := right.AuthorizationLocked(held)
	right.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	current, err := auth.Roles.Descriptor(actor.Ref)
	if err != nil || current.CreateDB {
		t.Fatal("held operation retained stale shared authority", current, err)
	}
	// Reverse direction proves a child does not merely receive a one-time copy.
	otherAdmin, otherDone := roleOperation(t, right, "postgres")
	defer otherDone()
	roleAttributeApply(t, right, otherAdmin, roleAttributeAlter(actor.Name, parser.RoleOptions{Present: parser.RoleCreateDB, CreateDB: true}))
	for _, target := range []*Engine{e, left, right} {
		current, err := target.Roles().Descriptor(actor.Ref)
		if err != nil || !current.CreateDB {
			t.Fatal("reverse publication not visible", current, err)
		}
	}
	// Role authority is shared, so an object in another database keeps the
	// role alive until that object is gone.
	dependent := roleAttributeCreate(t, left, admin, "shared_dependency", false, parser.RoleOptions{})
	roleExecute(t, left, admin, "CREATE SCHEMA shared_dependency_schema AUTHORIZATION shared_dependency")
	drop := &parser.DropRole{Names: []string{dependent.Name}}
	_, err = right.Execute(otherAdmin, drop, 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Detail != "1 object in database authority_left" {
		t.Fatal("role retirement did not retain the other database's dependency", err)
	}
	if _, err := right.Roles().Descriptor(dependent.Ref); err != nil {
		t.Fatal("failed retirement removed the shared role", err)
	}
	roleExecute(t, left, admin, "DROP SCHEMA shared_dependency_schema")
	if _, err := right.Execute(otherAdmin, drop, 0, nil); err != nil {
		t.Fatal("role remained dependent after the other database's object was dropped", err)
	}
	for _, target := range []*Engine{e, left, right} {
		if _, ok := target.Roles().Lookup(dependent.Name); ok {
			t.Fatal("role retirement was not shared")
		}
	}
}
