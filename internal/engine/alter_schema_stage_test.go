package engine

import (
	"context"
	"testing"
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
	source := e.state.names[NameKey{ClassID: NamespaceClassID, Name: "s1"}]
	destination := e.state.names[NameKey{ClassID: NamespaceClassID, Name: "s2"}]
	addresses := []ObjectAddress{}
	for _, name := range []string{"t", "q1", "q2"} {
		addresses = append(addresses, NameAddressForTempMove(t, e, source, name))
	}
	calls := 0
	provider := e.providers[RelationClassID]
	e.providers[RelationClassID] = alterSchemaCandidateProvider{CatalogProvider: provider, inspect: func(v CatalogRead) {
		calls++
		for _, address := range addresses {
			d, ok := v.Descriptor(address)
			if !ok || d.Namespace() != destination {
				t.Fatal("provider saw partial namespace closure", address, d)
			}
			if e.state.descriptors[address].Namespace() != source {
				t.Fatal("candidate escaped before validation")
			}
		}
	}}
	run(t, e, "ALTER TABLE s1.t SET SCHEMA s2")
	e.providers[RelationClassID] = provider
	if calls == 0 {
		t.Fatal("candidate was not validated")
	}
	frontier := e.nextRevision
	run(t, e, "COMMENT ON TABLE s2.t IS 'new comment'")
	ref := roleTableRef(t, e, "s2.t")
	if e.state.descriptors[ref.Address].Revision() < frontier {
		t.Fatal("move failed to advance allocator")
	}
}

func TestAlterSchemaOverflowAndSkip(t *testing.T) {
	for _, field := range []string{"table_revision", "sequence_revision", "sequence_stamp"} {
		t.Run(field, func(t *testing.T) {
			e := New()
			alterSchemaEngineFixture(t, e)
			source := e.state.names[NameKey{ClassID: NamespaceClassID, Name: "s1"}]
			name := "q2"
			if field == "table_revision" {
				name = "t"
			}
			address := NameAddressForTempMove(t, e, source, name)
			d := *e.state.descriptors[address].(*relationDescriptor)
			if field == "sequence_stamp" {
				d.sequencePlanStamp = DefinitionRevision(^uint64(0))
			} else {
				d.revision = DefinitionRevision(^uint64(0))
			}
			alterSchemaPublishDescriptor(t, e, &d)
			before, frontier := e.state, e.nextRevision
			_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE s1.t SET SCHEMA s2"), nil)
			if err == nil || err.Error() != "stuntgres: definition revision exhausted" {
				t.Fatal(err)
			}
			if e.state != before || e.nextRevision != frontier {
				t.Fatal("overflow changed catalog/frontier")
			}
			lockEmpty(t, e)
		})
	}
	e := New()
	alterSchemaEngineFixture(t, e)
	before, frontier := e.state, e.nextRevision
	run(t, e, "ALTER TABLE IF EXISTS absent.missing SET SCHEMA absent")
	run(t, e, "ALTER TABLE s1.t SET SCHEMA s1")
	if e.state != before || e.nextRevision != frontier {
		t.Fatal("skip consumed catalog/frontier")
	}
}

func TestAlterSchemaGrantTimeDefinition(t *testing.T) {
	e := New()
	alterSchemaEngineFixture(t, e)
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	ref := roleTableRef(t, e, "s1.t")
	old := e.state.descriptors[ref.Address].(*relationDescriptor)
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "LOCK TABLE s1.t IN ACCESS EXCLUSIVE MODE")
	done := lockAsync(e, b, context.Background(), "ALTER TABLE s1.t SET SCHEMA s2", false)
	lockPending(t, e, 1)
	next := *old
	next.revision++
	next.comment = "grant-time comment"
	alterSchemaPublishDescriptor(t, e, &next)
	transactionEnd(t, a, tx, false)
	lockAwait(t, done, nil)
	moved := e.state.descriptors[ref.Address].(*relationDescriptor)
	if moved.comment != "grant-time comment" || moved.revision <= next.revision || roleTableRef(t, e, "s2.t") != ref {
		t.Fatal("prewait descriptor used", moved)
	}
	lockEmpty(t, e)
}

func TestAlterSchemaActualTemporaryDestination(t *testing.T) {
	e := New()
	alterSchemaEngineFixture(t, e)
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TEMP SEQUENCE temporary_probe")
	destination := e.state.descriptors[s.temporaryNamespace].(*namespaceDescriptor).name
	before, frontier := e.state, e.nextRevision
	err := sequenceTempMoveCommand(t, e, s, "ALTER TABLE s1.t SET SCHEMA "+destination, true)
	renameWantError(t, err, "0A000", "temporary schema creation is unsupported")
	if e.state != before || e.nextRevision != frontier {
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
			source := e.state.names[NameKey{ClassID: NamespaceClassID, Name: "s1"}]
			address := NameAddressForTempMove(t, e, source, "q2")
			ref := ObjectRef{owner: e.domain, Epoch: e.state.epoch, Address: address}
			tx := transactionBegin(t, a)
			if err := e.locks.acquire(context.Background(), a.transactions.ticket, lockResource{ObjectRef: ref}, accessExclusive); err != nil {
				t.Fatal(err)
			}
			if collision {
				run(t, e, "CREATE SEQUENCE s2.grant_q2")
			}
			done := lockAsync(e, b, context.Background(), "ALTER TABLE s1.t SET SCHEMA s2", false)
			lockPending(t, e, 1)
			next := *e.state.descriptors[address].(*relationDescriptor)
			next.name = "grant_q2"
			next.revision++
			next.sequencePlanStamp++
			alterSchemaPublishDescriptor(t, e, &next)
			before, frontier := e.state, e.nextRevision
			transactionEnd(t, a, tx, false)
			if collision {
				renameWantError(t, <-done, "42P07", `relation "grant_q2" already exists in schema "s2"`)
				if e.state != before || e.nextRevision != frontier {
					t.Fatal("grant-time collision changed closure")
				}
			} else {
				lockAwait(t, done, nil)
				moved := e.state.descriptors[address].(*relationDescriptor)
				if moved.name != "grant_q2" || moved.sequence != next.sequence || moved.sequencePlanStamp <= next.sequencePlanStamp {
					t.Fatal("stale dependency definition", moved)
				}
				destination := e.state.names[NameKey{ClassID: NamespaceClassID, Name: "s2"}]
				if NameAddressForTempMove(t, e, destination, "grant_q2") != address {
					t.Fatal("dependency identity changed")
				}
			}
			lockEmpty(t, e)
		})
	}
}
