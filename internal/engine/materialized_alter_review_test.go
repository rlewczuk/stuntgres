package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestMaterializedAlterQueuedPredecessor(t *testing.T) {
	for _, phase := range []string{"unrelated_column", "superuser_owner", "name", "schema"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			admin, adminDone := roleOperation(t, e, "postgres")
			defer adminDone()
			actor := roleAttributeCreate(t, e, admin, "mv_queue_successor", true, parser.RoleOptions{})
			roleExecute(t, e, admin, "CREATE SCHEMA queue_destination")
			roleExecute(t, e, admin, "CREATE TABLE queue_source(i INT,s TEXT)")
			roleExecute(t, e, admin, "INSERT INTO queue_source VALUES(1,'a'),(2,NULL)")
			roleExecute(t, e, admin, "CREATE MATERIALIZED VIEW queued AS SELECT i,s FROM queue_source")
			address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "queued"})
			d := e.catalog.state.MustRelation(address)
			index := materializedAlterIndex(t, e, admin, d)
			d = e.catalog.state.MustRelation(address)
			store, root := e.storage.Relations[address], e.storage.IndexRoots[index.Address()]
			entries := slices.Clone(root.Entries)
			firstSuffix := "RENAME COLUMN s TO label"
			wantName, wantSecond, wantNamespace, wantOwner := d.Name(), "label", d.Namespace(), d.Owner
			switch phase {
			case "superuser_owner":
				firstSuffix = "OWNER TO mv_queue_successor"
				wantSecond = "s"
				wantOwner = actor.Ref
			case "name":
				firstSuffix = "RENAME TO renamed_queue_mv"
				wantName = "renamed_queue_mv"
				wantSecond = "s"
			case "schema":
				firstSuffix = "SET SCHEMA queue_destination"
				wantSecond = "s"
				wantNamespace = e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "queue_destination"})
			}
			holder, firstWorker, secondWorker := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			tx := transactionBegin(t, holder)
			released := false
			result := sessionSQL(t, holder, "SELECT i,s FROM queued")
			if err := result.Rows.Close(); err != nil {
				transactionEnd(t, holder, tx, false)
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var first, second <-chan error
			firstJoined, secondJoined := false, false
			defer func() {
				if !released {
					transactionEnd(t, holder, tx, false)
				}
				cancel()
				for _, worker := range []struct {
					done   <-chan error
					joined bool
				}{{first, firstJoined}, {second, secondJoined}} {
					if worker.done != nil && !worker.joined {
						select {
						case <-worker.done:
						case <-time.After(5 * time.Second):
							t.Error("queued ALTER worker did not join")
						}
					}
				}
			}()
			first = lockAsync(e, firstWorker, ctx, "ALTER MATERIALIZED VIEW queued "+firstSuffix, false)
			lockPending(t, e, 1)
			second = lockAsync(e, secondWorker, ctx, "ALTER MATERIALIZED VIEW queued RENAME COLUMN i TO key", false)
			lockPending(t, e, 2)
			transactionEnd(t, holder, tx, false)
			released = true
			select {
			case err := <-first:
				firstJoined = true
				if err != nil {
					t.Fatal("first genuine queued ALTER", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case err := <-second:
				secondJoined = true
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Errorf("second ALTER adopted changed admitted authority: %v, want %v", err, ids.ErrCatalogReplaced)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			current := e.catalog.state.MustRelation(address)
			attached := e.catalog.state.MustRelation(index.Address())
			if current == d || current.Name() != wantName || current.Namespace() != wantNamespace || current.Owner != wantOwner || current.Columns[0].Name != "i" || current.Columns[1].Name != wantSecond || !reflect.DeepEqual(current.Attributes, d.Attributes) || current.View != d.View || current.RowType != d.RowType || e.storage.Relations[address] != store || e.storage.IndexRoots[index.Address()] != root || !reflect.DeepEqual(root.Entries, entries) || attached.Owner != wantOwner || attached.Namespace() != wantNamespace || attached.SQLIndex != index.SQLIndex {
				t.Fatal("rejected queued ALTER changed first publication or stored/index authority")
			}
			if d.Name() != "queued" || d.Columns[0].Name != "i" || d.Columns[1].Name != "s" || index.Owner != d.Owner || index.Namespace() != d.Namespace() {
				t.Fatal("captured predecessor mutated")
			}
			lockEmpty(t, e)
		})
	}
}

func TestMaterializedAlterIndexColumnLabel(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE MATERIALIZED VIEW label_parent AS SELECT 1 AS i,'a'::text AS s")
	roleExecute(t, e, op, "CREATE UNIQUE INDEX label_index ON label_parent(i) INCLUDE(s)")
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	parentAddress := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "label_parent"})
	indexAddress := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "label_index"})
	parent := e.catalog.state.MustRelation(parentAddress)
	captured := e.catalog.state.MustRelation(indexAddress)
	store, root := e.storage.Relations[parentAddress], e.storage.IndexRoots[indexAddress]
	entries := slices.Clone(root.Entries)
	definition := captured.SQLIndex
	if definition.Parent != parentAddress || len(definition.Keys) != 1 || definition.Keys[0].Attribute != parent.Attributes[0] || len(definition.Include) != 1 || definition.Include[0] != parent.Attributes[1] || len(root.Entries) != 1 {
		t.Fatal("fixture lacks genuine parent/key/include/root")
	}
	for _, transition := range []struct{ from, to string }{{"i", "key"}, {"key", "i"}} {
		before := e.catalog.state.MustRelation(indexAddress)
		if err := extensionUnitExecute(t, e, op, statement(t, "ALTER MATERIALIZED VIEW label_index RENAME COLUMN "+transition.from+" TO "+transition.to)); err != nil {
			t.Fatal(err)
		}
		current := e.catalog.state.MustRelation(indexAddress)
		if current == before || current.Columns[0].Name != transition.to || current.Columns[1].Name != "s" || !reflect.DeepEqual(current.Index, map[string]int{transition.to: 0, "s": 1}) || before.Columns[0].Name != transition.from || before.Index[transition.from] != 0 {
			t.Fatal("selected index column action did not immutably relabel descriptor")
		}
		if _, exists := before.Index[transition.to]; exists {
			t.Fatal("captured index name-to-slot map mutated")
		}
		if current.SQLIndex != definition || !reflect.DeepEqual(current.Attributes, captured.Attributes) || e.catalog.state.Get(parentAddress) != parent || parent.Columns[0].Name != "i" || parent.Columns[1].Name != "s" || e.storage.Relations[parentAddress] != store || e.storage.IndexRoots[indexAddress] != root || !reflect.DeepEqual(root.Entries, entries) {
			t.Fatal("descriptor relabel retargeted parent/key/include/root/storage")
		}
	}
	if captured.Columns[0].Name != "i" || !reflect.DeepEqual(captured.Index, map[string]int{"i": 0, "s": 1}) {
		t.Fatal("original index descriptor mutated")
	}
	lockEmpty(t, e)
}

func TestMaterializedAlterQueuedRefreshCompatible(t *testing.T) {
	for _, noData := range []bool{false, true} {
		t.Run(map[bool]string{false: "data", true: "no_data"}[noData], func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			roleExecute(t, e, op, "CREATE TABLE refresh_queue_source(i INT,s TEXT)")
			roleExecute(t, e, op, "INSERT INTO refresh_queue_source VALUES(1,'old')")
			roleExecute(t, e, op, "CREATE MATERIALIZED VIEW refreshed AS SELECT i,s FROM refresh_queue_source")
			address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "refreshed"})
			parent := e.catalog.state.MustRelation(address)
			index := materializedAlterIndex(t, e, op, parent)
			parent = e.catalog.state.MustRelation(address)
			store, root := e.storage.Relations[address], e.storage.IndexRoots[index.Address()]
			roleExecute(t, e, op, "INSERT INTO refresh_queue_source VALUES(2,'new')")
			holder, firstWorker, secondWorker := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			tx := transactionBegin(t, holder)
			released := false
			result := sessionSQL(t, holder, "SELECT i,s FROM refreshed")
			if err := result.Rows.Close(); err != nil {
				transactionEnd(t, holder, tx, false)
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var first, second <-chan error
			firstJoined, secondJoined := false, false
			defer func() {
				if !released {
					transactionEnd(t, holder, tx, false)
				}
				cancel()
				for _, worker := range []struct {
					done   <-chan error
					joined bool
				}{{first, firstJoined}, {second, secondJoined}} {
					if worker.done != nil && !worker.joined {
						select {
						case <-worker.done:
						case <-time.After(5 * time.Second):
							t.Error("queued refresh/ALTER worker did not join")
						}
					}
				}
			}()
			refresh := "REFRESH MATERIALIZED VIEW refreshed"
			if noData {
				refresh += " WITH NO DATA"
			}
			first = lockAsync(e, firstWorker, ctx, refresh, false)
			lockPending(t, e, 1)
			second = lockAsync(e, secondWorker, ctx, "ALTER MATERIALIZED VIEW refreshed RENAME COLUMN i TO key", false)
			lockPending(t, e, 2)
			transactionEnd(t, holder, tx, false)
			released = true
			select {
			case err := <-first:
				firstJoined = true
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case err := <-second:
				secondJoined = true
				if err != nil {
					t.Fatal("compatible refresh was treated as defining-authority replacement", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			current := e.catalog.state.MustRelation(address)
			wantCount := 2
			if noData {
				wantCount = 0
			}
			if current.Columns[0].Name != "key" || current.Columns[1].Name != "s" || current.Populated == noData || current.View != parent.View || current.RowType != parent.RowType || current.Owner != parent.Owner || !reflect.DeepEqual(current.Attributes, parent.Attributes) || e.storage.Relations[address] == store || len(e.storage.Relations[address].Versions) != wantCount || e.storage.IndexRoots[index.Address()] == root || len(e.storage.IndexRoots[index.Address()].Entries) != wantCount || e.catalog.state.MustRelation(index.Address()).SQLIndex != index.SQLIndex {
				t.Fatal("queued metadata operation overwrote compatible refresh publication")
			}
			if parent.Columns[0].Name != "i" || !parent.Populated || len(store.Versions) != 1 || len(root.Entries) != 1 {
				t.Fatal("captured refresh predecessor mutated")
			}
			if noData {
				checkOp, err := holder.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				rejected, err := e.Execute(checkOp, statement(t, "SELECT key,s FROM refreshed"), 0, nil)
				if rejected.Rows != nil {
					rejected.Rows.Close()
				}
				checkOp.Done()
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "55000" {
					t.Fatal("NO DATA relation lost demanded-read diagnostic after queued rename", err)
				}
				// A later genuine producer run must populate the exposed positional label.
				roleExecute(t, e, op, "REFRESH MATERIALIZED VIEW refreshed")
			}
			result = sessionSQL(t, holder, "SELECT key,s FROM refreshed ORDER BY key")
			rows := tuples(result)
			if err := result.Rows.Close(); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 || len(rows[0]) != 2 || len(rows[1]) != 2 || rows[0][0] != int64(1) || rows[0][1] != "old" || rows[1][0] != int64(2) || rows[1][1] != "new" {
				t.Fatal("compatible refresh bag/positional output", rows)
			}
			lockEmpty(t, e)
		})
	}
}

func TestMaterializedAlterQueuedDefinition(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	d := refreshUnitRelation(t, e)
	index := materializedAlterIndex(t, e, op, d)
	d = e.catalog.state.MustRelation(d.Address())
	store, root := e.storage.Relations[d.Address()], e.storage.IndexRoots[index.Address()]
	roleExecute(t, e, op, "CREATE MATERIALIZED VIEW producer_fixture AS SELECT 9 AS i")
	producer := e.catalog.state.MustRelation(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: "producer_fixture"}))
	holder, worker := sessionStart(t, e, nil), sessionStart(t, e, nil)
	tx := transactionBegin(t, holder)
	released, joined := false, false
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	var pending <-chan error
	defer func() {
		if !released {
			transactionEnd(t, holder, tx, false)
		}
		cancel()
		if pending != nil && !joined {
			select {
			case <-pending:
			case <-time.After(5 * time.Second):
				t.Error("queued defining-authority worker did not join")
			}
		}
	}()
	result := sessionSQL(t, holder, "SELECT i FROM unit_mv")
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	pending = lockAsync(e, worker, ctx, "ALTER MATERIALIZED VIEW unit_mv RENAME COLUMN i TO key", false)
	lockPending(t, e, 1)
	// Replace only with a genuine bound, type-compatible producer. Preserve
	// the original MV's valid rule, rowtype, exposed attributes and stale rows.
	// Validation/publication, rather than a direct descriptor-map mutation,
	// establishes this defining predecessor while the real ALTER is pending.
	next := *d
	next.ObjectRevision++
	definition := ViewOf(producer).With(func(p *ViewParts) {
		p.Rule = ViewOf(d).RuleOID()
		p.Consumer = ViewOf(d).Consumer()
		p.QuoteRevision = next.Revision()
	})
	next.View = definition
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	transactionEnd(t, holder, tx, false)
	released = true
	select {
	case err := <-pending:
		joined = true
		if !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("queued ALTER adopted a genuine changed producer", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	current := e.catalog.state.MustRelation(d.Address())
	if ViewOf(current) != definition || current.Columns[0].Name != "i" || current.RowType != d.RowType || !reflect.DeepEqual(current.Attributes, d.Attributes) || e.storage.Relations[d.Address()] != store || e.storage.IndexRoots[index.Address()] != root || ViewOf(current).Query() != ViewOf(producer).Query() || ViewOf(current).RuleOID() != ViewOf(d).RuleOID() {
		t.Fatal("rejected queued ALTER changed validated producer predecessor")
	}
	result = sessionSQL(t, holder, "SELECT i FROM unit_mv ORDER BY i")
	rows := tuples(result)
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0][0] != int64(1) || rows[1][0] != int64(2) || rows[2][0] != int64(3) {
		t.Fatal("metadata producer replacement reevaluated stored rows", rows)
	}
	roleExecute(t, e, op, "REFRESH MATERIALIZED VIEW unit_mv")
	result = sessionSQL(t, holder, "SELECT i FROM unit_mv")
	rows = tuples(result)
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0][0] != int64(9) || len(e.storage.IndexRoots[index.Address()].Entries) != 1 || len(store.Versions) != 3 || len(root.Entries) != 3 {
		t.Fatal("validated replacement did not retain old roots and execute actual new producer", rows)
	}
	lockEmpty(t, e)
}

func TestMaterializedAlterDefiningAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	d := refreshUnitRelation(t, e)
	roleExecute(t, e, op, "CREATE MATERIALIZED VIEW authority_fixture AS SELECT 9 AS other")
	other := e.catalog.state.MustRelation(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: "authority_fixture"}))
	if d.View == other.View || d.RowType == other.RowType || &d.Columns[0] == &other.Columns[0] || &d.Attributes[0] == &other.Attributes[0] {
		t.Fatal("fixture lacks distinct genuine defining authorities")
	}
	if !catalog.RelationDefiningAuthorityCompatible(d, d) || catalog.RelationDefiningAuthorityCompatible(d, nil) {
		t.Fatal("defining-authority identity/live contract")
	}
	for _, axis := range []string{"producer", "rowtype", "columns", "attributes"} {
		t.Run(axis, func(t *testing.T) {
			// Comparison-only projections isolate each frozen authority axis
			// using real captured descriptors. These are never published as
			// catalog fixtures, nor assigned fabricated OIDs/attribute values.
			expected := *d
			switch axis {
			case "producer":
				expected.View = other.View
			case "rowtype":
				expected.RowType = other.RowType
			case "columns":
				expected.Columns = other.Columns
			case "attributes":
				expected.Attributes = other.Attributes
			}
			if catalog.RelationDefiningAuthorityCompatible(&expected, d) {
				t.Fatal("compatibility waived isolated genuine defining authority", axis)
			}
		})
	}
	// Exercise a genuine foreign replacement lifecycle as the coherent
	// rowtype/attribute replacement complement; no hybrid stage is published.
	target := New()
	if _, err := target.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	imported := target.catalog.state.MustRelation(target.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: d.Name()}))
	row := target.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(imported.RowType)})
	if row.OwningRelation != imported.Address() || len(imported.Attributes) != 1 || imported.Attributes[0] != d.Attributes[0] || len(target.storage.Relations[imported.Address()].Versions) != 3 || e.catalog.state.Get(d.Address()) != d {
		t.Fatal("real replacement lost coherent rowtype/attributes/stored authority")
	}
}
