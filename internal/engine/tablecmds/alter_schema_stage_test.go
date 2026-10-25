package tablecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

type alterSchemaCandidateProvider struct {
	catalog.Provider
	inspect func(catalog.Read)
}

func (p alterSchemaCandidateProvider) Validate(v catalog.Read, d catalog.Descriptor) error {
	p.inspect(v)
	return p.Provider.Validate(v, d)
}

func alterSchemaPublishDescriptor(t *testing.T, e *engine.Engine, d *catalog.Relation) {
	t.Helper()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(d); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func TestAlterSchemaWholeCandidate(t *testing.T) {
	e := engine.New()
	alterSchemaEngineFixture(t, e)
	source := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s1"})
	destination := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s2"})
	addresses := []ids.ObjectAddress{}
	for _, name := range []string{"t", "q1", "q2"} {
		addresses = append(addresses, NameAddressForTempMove(t, e, source, name))
	}
	calls := 0
	provider := e.Catalog.Providers[ids.RelationClassID]
	e.Catalog.Providers[ids.RelationClassID] = alterSchemaCandidateProvider{Provider: provider, inspect: func(v catalog.Read) {
		calls++
		for _, address := range addresses {
			d, ok := v.Descriptor(address)
			if !ok || d.Namespace() != destination {
				t.Fatal("provider saw partial namespace closure", address, d)
			}
			if e.Catalog.State.Get(address).Namespace() != source {
				t.Fatal("candidate escaped before validation")
			}
		}
	}}
	enginetest.Run(t, e, "ALTER TABLE s1.t SET SCHEMA s2")
	e.Catalog.Providers[ids.RelationClassID] = provider
	if calls == 0 {
		t.Fatal("candidate was not validated")
	}
	frontier := e.Catalog.NextRevision()
	enginetest.Run(t, e, "COMMENT ON TABLE s2.t IS 'new comment'")
	ref := enginetest.RoleTableRef(t, e, "s2.t")
	if e.Catalog.State.Get(ref.Address).Revision() < frontier {
		t.Fatal("move failed to advance allocator")
	}
}

func TestAlterSchemaOverflowAndSkip(t *testing.T) {
	for _, field := range []string{"table_revision", "sequence_revision", "sequence_stamp"} {
		t.Run(field, func(t *testing.T) {
			e := engine.New()
			alterSchemaEngineFixture(t, e)
			source := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s1"})
			name := "q2"
			if field == "table_revision" {
				name = "t"
			}
			address := NameAddressForTempMove(t, e, source, name)
			d := *e.Catalog.State.MustRelation(address)
			if field == "sequence_stamp" {
				d.SequencePlanStamp = ids.DefinitionRevision(^uint64(0))
			} else {
				d.ObjectRevision = ids.DefinitionRevision(^uint64(0))
			}
			alterSchemaPublishDescriptor(t, e, &d)
			before, frontier := e.Catalog.State, e.Catalog.NextRevision()
			_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "ALTER TABLE s1.t SET SCHEMA s2"), nil)
			if err == nil || err.Error() != "stuntgres: definition revision exhausted" {
				t.Fatal(err)
			}
			if e.Catalog.State != before || e.Catalog.NextRevision() != frontier {
				t.Fatal("overflow changed catalog/frontier")
			}
			enginetest.LockEmpty(t, e)
		})
	}
	e := engine.New()
	alterSchemaEngineFixture(t, e)
	before, frontier := e.Catalog.State, e.Catalog.NextRevision()
	enginetest.Run(t, e, "ALTER TABLE IF EXISTS absent.missing SET SCHEMA absent")
	enginetest.Run(t, e, "ALTER TABLE s1.t SET SCHEMA s1")
	if e.Catalog.State != before || e.Catalog.NextRevision() != frontier {
		t.Fatal("skip consumed catalog/frontier")
	}
}

func TestAlterSchemaGrantTimeDefinition(t *testing.T) {
	e := engine.New()
	alterSchemaEngineFixture(t, e)
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	ref := enginetest.RoleTableRef(t, e, "s1.t")
	old := e.Catalog.State.MustRelation(ref.Address)
	tx := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "LOCK TABLE s1.t IN ACCESS EXCLUSIVE MODE")
	done := enginetest.LockAsync(e, b, context.Background(), "ALTER TABLE s1.t SET SCHEMA s2", false)
	enginetest.LockPending(t, e, 1)
	next := *old
	next.ObjectRevision++
	next.Comment = "grant-time comment"
	alterSchemaPublishDescriptor(t, e, &next)
	enginetest.TransactionEnd(t, a, tx, false)
	enginetest.LockAwait(t, done, nil)
	moved := e.Catalog.State.MustRelation(ref.Address)
	if moved.Comment != "grant-time comment" || moved.Revision() <= next.Revision() || enginetest.RoleTableRef(t, e, "s2.t") != ref {
		t.Fatal("prewait descriptor used", moved)
	}
	enginetest.LockEmpty(t, e)
}

func TestAlterSchemaActualTemporaryDestination(t *testing.T) {
	e := engine.New()
	alterSchemaEngineFixture(t, e)
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEMP SEQUENCE temporary_probe")
	destination := e.Catalog.State.MustNamespace(s.TemporaryNamespace).Name()
	before, frontier := e.Catalog.State, e.Catalog.NextRevision()
	err := sequenceTempMoveCommand(t, e, s, "ALTER TABLE s1.t SET SCHEMA "+destination, true)
	renameWantError(t, err, "0A000", "temporary schema creation is unsupported")
	if e.Catalog.State != before || e.Catalog.NextRevision() != frontier {
		t.Fatal("temporary destination changed closure")
	}
	enginetest.LockEmpty(t, e)
}

func TestAlterSchemaDependentGrantTimeName(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(map[bool]string{false: "move", true: "collision"}[collision], func(t *testing.T) {
			e := engine.New()
			alterSchemaEngineFixture(t, e)
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			source := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s1"})
			address := NameAddressForTempMove(t, e, source, "q2")
			ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
			tx := enginetest.TransactionBegin(t, a)
			if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
				t.Fatal(err)
			}
			if collision {
				enginetest.Run(t, e, "CREATE SEQUENCE s2.grant_q2")
			}
			done := enginetest.LockAsync(e, b, context.Background(), "ALTER TABLE s1.t SET SCHEMA s2", false)
			enginetest.LockPending(t, e, 1)
			next := *e.Catalog.State.MustRelation(address)
			next.ObjectName = "grant_q2"
			next.ObjectRevision++
			next.SequencePlanStamp++
			alterSchemaPublishDescriptor(t, e, &next)
			before, frontier := e.Catalog.State, e.Catalog.NextRevision()
			enginetest.TransactionEnd(t, a, tx, false)
			if collision {
				renameWantError(t, <-done, "42P07", `relation "grant_q2" already exists in schema "s2"`)
				if e.Catalog.State != before || e.Catalog.NextRevision() != frontier {
					t.Fatal("grant-time collision changed closure")
				}
			} else {
				enginetest.LockAwait(t, done, nil)
				moved := e.Catalog.State.MustRelation(address)
				if moved.Name() != "grant_q2" || moved.Sequence != next.Sequence || moved.SequencePlanStamp <= next.SequencePlanStamp {
					t.Fatal("stale dependency definition", moved)
				}
				destination := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s2"})
				if NameAddressForTempMove(t, e, destination, "grant_q2") != address {
					t.Fatal("dependency identity changed")
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
