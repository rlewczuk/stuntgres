package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

type alterSchemaCandidateProvider struct {
	CatalogProvider
	inspect func(CatalogRead)
}

func (p alterSchemaCandidateProvider) Validate(v CatalogRead, d Descriptor) error {
	p.inspect(v)
	return p.CatalogProvider.Validate(v, d)
}

func alterSchemaPublishDescriptor(t *testing.T, e *Engine, d *relationDescriptor) {
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
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func TestAlterSchemaWholeCandidate(t *testing.T) {
	e := New()
	alterSchemaEngineFixture(t, e)
	source := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s1"})
	destination := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s2"})
	addresses := []ids.ObjectAddress{}
	for _, name := range []string{"t", "q1", "q2"} {
		addresses = append(addresses, NameAddressForTempMove(t, e, source, name))
	}
	calls := 0
	provider := e.catalog.providers[ids.RelationClassID]
	e.catalog.providers[ids.RelationClassID] = alterSchemaCandidateProvider{CatalogProvider: provider, inspect: func(v CatalogRead) {
		calls++
		for _, address := range addresses {
			d, ok := v.Descriptor(address)
			if !ok || d.Namespace() != destination {
				t.Fatal("provider saw partial namespace closure", address, d)
			}
			if e.catalog.state.Get(address).Namespace() != source {
				t.Fatal("candidate escaped before validation")
			}
		}
	}}
	run(t, e, "ALTER TABLE s1.t SET SCHEMA s2")
	e.catalog.providers[ids.RelationClassID] = provider
	if calls == 0 {
		t.Fatal("candidate was not validated")
	}
	frontier := e.catalog.nextRevision
	run(t, e, "COMMENT ON TABLE s2.t IS 'new comment'")
	ref := roleTableRef(t, e, "s2.t")
	if e.catalog.state.Get(ref.Address).Revision() < frontier {
		t.Fatal("move failed to advance allocator")
	}
}

func TestAlterSchemaOverflowAndSkip(t *testing.T) {
	for _, field := range []string{"table_revision", "sequence_revision", "sequence_stamp"} {
		t.Run(field, func(t *testing.T) {
			e := New()
			alterSchemaEngineFixture(t, e)
			source := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s1"})
			name := "q2"
			if field == "table_revision" {
				name = "t"
			}
			address := NameAddressForTempMove(t, e, source, name)
			d := *e.catalog.state.MustRelation(address)
			if field == "sequence_stamp" {
				d.SequencePlanStamp = ids.DefinitionRevision(^uint64(0))
			} else {
				d.revision = ids.DefinitionRevision(^uint64(0))
			}
			alterSchemaPublishDescriptor(t, e, &d)
			before, frontier := e.catalog.state, e.catalog.nextRevision
			_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE s1.t SET SCHEMA s2"), nil)
			if err == nil || err.Error() != "stuntgres: definition revision exhausted" {
				t.Fatal(err)
			}
			if e.catalog.state != before || e.catalog.nextRevision != frontier {
				t.Fatal("overflow changed catalog/frontier")
			}
			lockEmpty(t, e)
		})
	}
	e := New()
	alterSchemaEngineFixture(t, e)
	before, frontier := e.catalog.state, e.catalog.nextRevision
	run(t, e, "ALTER TABLE IF EXISTS absent.missing SET SCHEMA absent")
	run(t, e, "ALTER TABLE s1.t SET SCHEMA s1")
	if e.catalog.state != before || e.catalog.nextRevision != frontier {
		t.Fatal("skip consumed catalog/frontier")
	}
}

func TestAlterSchemaGrantTimeDefinition(t *testing.T) {
	e := New()
	alterSchemaEngineFixture(t, e)
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	ref := roleTableRef(t, e, "s1.t")
	old := e.catalog.state.MustRelation(ref.Address)
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "LOCK TABLE s1.t IN ACCESS EXCLUSIVE MODE")
	done := lockAsync(e, b, context.Background(), "ALTER TABLE s1.t SET SCHEMA s2", false)
	lockPending(t, e, 1)
	next := *old
	next.revision++
	next.Comment = "grant-time comment"
	alterSchemaPublishDescriptor(t, e, &next)
	transactionEnd(t, a, tx, false)
	lockAwait(t, done, nil)
	moved := e.catalog.state.MustRelation(ref.Address)
	if moved.Comment != "grant-time comment" || moved.revision <= next.revision || roleTableRef(t, e, "s2.t") != ref {
		t.Fatal("prewait descriptor used", moved)
	}
	lockEmpty(t, e)
}

func TestAlterSchemaActualTemporaryDestination(t *testing.T) {
	e := New()
	alterSchemaEngineFixture(t, e)
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TEMP SEQUENCE temporary_probe")
	destination := e.catalog.state.MustNamespace(s.temporaryNamespace).name
	before, frontier := e.catalog.state, e.catalog.nextRevision
	err := sequenceTempMoveCommand(t, e, s, "ALTER TABLE s1.t SET SCHEMA "+destination, true)
	renameWantError(t, err, "0A000", "temporary schema creation is unsupported")
	if e.catalog.state != before || e.catalog.nextRevision != frontier {
		t.Fatal("temporary destination changed closure")
	}
	lockEmpty(t, e)
}

func TestAlterSchemaDependentGrantTimeName(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(map[bool]string{false: "move", true: "collision"}[collision], func(t *testing.T) {
			e := New()
			alterSchemaEngineFixture(t, e)
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			source := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s1"})
			address := NameAddressForTempMove(t, e, source, "q2")
			ref := e.catalog.domain.Ref(e.catalog.state.epoch, address)
			tx := transactionBegin(t, a)
			if err := e.locks.Acquire(context.Background(), a.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
				t.Fatal(err)
			}
			if collision {
				run(t, e, "CREATE SEQUENCE s2.grant_q2")
			}
			done := lockAsync(e, b, context.Background(), "ALTER TABLE s1.t SET SCHEMA s2", false)
			lockPending(t, e, 1)
			next := *e.catalog.state.MustRelation(address)
			next.name = "grant_q2"
			next.revision++
			next.SequencePlanStamp++
			alterSchemaPublishDescriptor(t, e, &next)
			before, frontier := e.catalog.state, e.catalog.nextRevision
			transactionEnd(t, a, tx, false)
			if collision {
				renameWantError(t, <-done, "42P07", `relation "grant_q2" already exists in schema "s2"`)
				if e.catalog.state != before || e.catalog.nextRevision != frontier {
					t.Fatal("grant-time collision changed closure")
				}
			} else {
				lockAwait(t, done, nil)
				moved := e.catalog.state.MustRelation(address)
				if moved.name != "grant_q2" || moved.Sequence != next.Sequence || moved.SequencePlanStamp <= next.SequencePlanStamp {
					t.Fatal("stale dependency definition", moved)
				}
				destination := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s2"})
				if NameAddressForTempMove(t, e, destination, "grant_q2") != address {
					t.Fatal("dependency identity changed")
				}
			}
			lockEmpty(t, e)
		})
	}
}
