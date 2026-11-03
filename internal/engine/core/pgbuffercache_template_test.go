package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Inspect the detached statement capability by database and storage identity;
// equal local relfilenodes in different databases must never merge ownership.
func bufferUnitDatabaseFact(t *testing.T, e *core.Engine, observer *session.Session, database, node ids.OID, usage int64) int {
	t.Helper()
	cursor := bufferUnitCursor(t, e, observer)
	defer cursor.Close()
	found, occupied := 0, 0
	for {
		row, present, err := cursor.Next(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		if row.CompositeField(1).IsNull() {
			continue
		}
		occupied++
		actualNode, err := row.CompositeField(1).OID()
		if err != nil {
			t.Fatal(err)
		}
		actualDatabase, err := row.CompositeField(3).OID()
		if err != nil {
			t.Fatal(err)
		}
		if actualNode != uint32(node) || actualDatabase != uint32(database) {
			continue
		}
		found++
		actualUsage, err := row.CompositeField(7).Integer()
		if err != nil {
			t.Fatal(err)
		}
		pins, err := row.CompositeField(8).Integer()
		if err != nil || actualUsage != usage || pins != 0 {
			t.Fatal("template cache owner counters", actualUsage, pins, usage, err)
		}
	}
	want := 1
	if usage == 0 {
		want = 0
	}
	if found != want {
		t.Fatal("template cache owner identity", database, node, found, want)
	}
	return occupied
}

// Private replacement selects the destination's provider, but validates the
// projected source Read before remapping. Its owner remains the real template.
type bufferTemplateFailure struct {
	catalog.Provider
	source   *catalog.Owner
	relation ids.ObjectAddress
	once     sync.Once
	seen     bool
	failure  error
	cancel   context.CancelFunc
}

func (p *bufferTemplateFailure) Validate(view catalog.Read, descriptor catalog.Descriptor) error {
	if view.Owner() == p.source && descriptor.Address() == p.relation {
		p.once.Do(func() {
			p.seen = true
			if p.cancel != nil {
				p.cancel()
			}
		})
		if p.cancel == nil {
			return p.failure
		}
	}
	return p.Provider.Validate(view, descriptor)
}

func TestPgBuffercacheTemplateOwnership(t *testing.T) {
	// engine.New deliberately uses an unnamed database; this SQL template
	// fixture needs a named owner rather than assuming a postgres database.
	e := engine.NewWithSettings(nil, "buffer_template_source", false)
	enginetest.Run(t, e, "CREATE EXTENSION pg_buffercache")
	enginetest.Run(t, e, "CREATE TABLE buffer_template_heap(k integer)")
	enginetest.Run(t, e, "INSERT INTO buffer_template_heap VALUES(1)")
	enginetest.Run(t, e, "SELECT k FROM buffer_template_heap")
	node := bufferUnitNode(t, e, "buffer_template_heap")
	database := e.Catalog.DatabaseAddress().ObjectID
	observer := enginetest.StartSession(t, e, nil)
	bufferUnitDatabaseFact(t, e, observer, database, node, 1)
	// Real template admission excludes other physical sessions. A detached
	// cache snapshot and an ended observer must not obstruct that lifecycle.
	retained := bufferUnitCursor(t, e, observer)
	defer retained.Close()
	observer.RequestEnd(session.CleanClose)
	ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
	defer cancel()
	if err := observer.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}

	// This drives the genuine newDatabaseRuntime -> ReplaceDatabaseTemplate
	// (Cluster nil) -> publishDatabaseTargetLocked attachment boundary.
	sourceDatabase := e.CatalogRead().MustDatabase(e.Catalog.DatabaseAddress())
	enginetest.Run(t, e, "CREATE DATABASE buffer_template_copy TEMPLATE "+catalog.QuoteIdentifier(sourceDatabase.Name()))
	target, err := e.SelectDatabase(t.Context(), "buffer_template_copy")
	if err != nil {
		t.Fatal(err)
	}
	if target.Cluster != e.Cluster || target.Mu != e.Mu || target.RoleStore != e.RoleStore || target.Catalog.Domain == e.Catalog.Domain {
		t.Fatal("template destination did not acquire its genuine cluster/local domain")
	}
	targetNode := bufferUnitNode(t, target, "buffer_template_heap")
	targetDatabase := target.Catalog.DatabaseAddress().ObjectID
	if targetDatabase == database {
		t.Fatal("template reused source database identity")
	}
	live := enginetest.StartSession(t, e, nil)
	bufferUnitDatabaseFact(t, e, live, database, node, 1)
	bufferUnitDatabaseFact(t, e, live, targetDatabase, targetNode, 0)
	enginetest.Run(t, target, "SELECT k FROM buffer_template_heap")
	bufferUnitDatabaseFact(t, e, live, targetDatabase, targetNode, 1)
	bufferUnitDatabaseFact(t, e, live, database, node, 1)
	for _, mode := range []string{"validation_failure", "canceled_private_copy"} {
		t.Run(mode, func(t *testing.T) {
			population := bufferUnitDatabaseFact(t, e, live, database, node, 1)
			root, templateRoot := e.Catalog.State, target.Catalog.State
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sentinel := errors.New("buffer template validation failure")
			templateRead := target.CatalogRead()
			templateRelation := enginetest.CatalogRef(t, target, templateRead, "buffer_template_heap")
			original := e.Catalog.Providers[ids.RelationClassID]
			provider := &bufferTemplateFailure{Provider: original, source: templateRead.Owner(), relation: templateRelation.Address, failure: sentinel}
			want := error(sentinel)
			if mode == "canceled_private_copy" {
				provider.cancel, want = cancel, context.Canceled
			}
			e.Catalog.Providers[ids.RelationClassID] = provider
			defer func() { e.Catalog.Providers[ids.RelationClassID] = original }()
			templateDatabase := templateRead.MustDatabase(target.Catalog.DatabaseAddress())
			failedName := "buffer_private_" + mode
			_, err := enginetest.Execute(e, ctx, enginetest.Statement(t, "CREATE DATABASE "+catalog.QuoteIdentifier(failedName)+" TEMPLATE "+catalog.QuoteIdentifier(templateDatabase.Name())), nil)
			if !provider.seen || !errors.Is(err, want) {
				t.Fatal("private-copy failure frontier", provider.seen, err, want)
			}
			if e.Cluster.Targets[failedName] != nil || e.Catalog.State != root || target.Catalog.State != templateRoot {
				t.Fatal("failed private copy published or replaced a live owner")
			}
			if got := bufferUnitDatabaseFact(t, e, live, database, node, 1); got != population {
				t.Fatal("failed private copy leaked occupancy", got, population)
			}
			bufferUnitDatabaseFact(t, e, live, targetDatabase, targetNode, 1)
		})
	}

	// A separate live cluster neither borrowed template occupancy nor receives
	// the destination's subsequent actual reads.
	independent := bufferUnitEngine(t)
	other := enginetest.StartSession(t, independent, nil)
	bufferUnitDatabaseFact(t, independent, other, targetDatabase, targetNode, 0)
}
