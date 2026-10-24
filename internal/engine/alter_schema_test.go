package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"sync"
	"testing"
)

func alterSchemaEngineFixture(t *testing.T, e *Engine) {
	t.Helper()
	for _, q := range []string{"CREATE SCHEMA s1", "CREATE SCHEMA s2", "CREATE TABLE s1.t(id INT CONSTRAINT nn_id NOT NULL,note TEXT DEFAULT 'fallback')", "INSERT INTO s1.t VALUES(7,'keep'),(9,NULL)", "COMMENT ON TABLE s1.t IS 'table comment'", "COMMENT ON COLUMN s1.t.id IS 'id comment'", "CREATE SEQUENCE s1.q1 START 10 CACHE 7 OWNED BY s1.t.id", "CREATE SEQUENCE s1.q2 START 100 CACHE 7 OWNED BY s1.t.id", "CREATE SEQUENCE s1.free", "CREATE VIEW v AS SELECT id,note FROM s1.t"} {
		run(t, e, q)
	}
}

func TestAlterSchemaIdentityAndCache(t *testing.T) {
	e := New()
	alterSchemaEngineFixture(t, e)
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	source := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s1"})
	dest := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s2"})
	tableAddress := NameAddressForTempMove(t, e, source, "t")
	table := e.catalog.state.MustRelation(tableAddress)
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: table.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}
	for _, subID := range []int32{0, table.Attributes[0]} {
		address := tableAddress
		address.SubID = subID
		if err := e.SetObjectACL(context.Background(), e.catalog.Domain.Ref(e.catalog.state.Epoch(), address), acl); err != nil {
			t.Fatal(err)
		}
	}
	addresses := make([]ids.ObjectAddress, 0, 3)
	before := make([]*catalog.Relation, 0, 3)
	for _, name := range []string{"t", "q1", "q2"} {
		a := NameAddressForTempMove(t, e, source, name)
		addresses = append(addresses, a)
		before = append(before, e.catalog.state.MustRelation(a))
	}
	storage := e.storage.Relations[addresses[0]]
	sequenceScalar(t, s, "SELECT nextval('s1.q1')", 10)
	generator := e.storage.Sequences[addresses[1]]
	generatorValue := *generator
	memory := payloadOf(s).sequences.Memory(addresses[1])
	memoryValue := *memory
	sequenceScalar(t, peer, "SELECT nextval('s1.q1')", 17)
	peerMemory := payloadOf(peer).sequences.Memory(addresses[1])
	peerValue := *peerMemory
	generatorValue = *generator
	free := NameAddressForTempMove(t, e, source, "free")
	freeDescriptor := e.catalog.state.Get(free)
	if err := sequenceTempMoveCommand(t, e, s, "ALTER TABLE s1.t SET SCHEMA s2", true); err != nil {
		t.Fatal(err)
	}
	for i, a := range addresses {
		after := e.catalog.state.MustRelation(a)
		if after.Namespace() != dest || after.Revision() <= before[i].Revision() {
			t.Fatal("namespace/revision", before[i], after)
		}
		copy := *after
		copy.ObjectNamespace, copy.ObjectRevision = before[i].Namespace(), before[i].Revision()
		if i != 0 {
			if after.SequencePlanStamp <= before[i].SequencePlanStamp {
				t.Fatal("missing sequence invalidation")
			}
			copy.SequencePlanStamp = before[i].SequencePlanStamp
		}
		if !reflect.DeepEqual(&copy, before[i]) {
			t.Fatal("move changed nonnamespace definition", before[i], after)
		}
	}
	if e.storage.Relations[addresses[0]] != storage || e.storage.Sequences[addresses[1]] != generator || *generator != generatorValue || payloadOf(s).sequences.Memory(addresses[1]) != memory || *memory != memoryValue || e.catalog.state.Get(free) != freeDescriptor {
		t.Fatal("move replaced data/generator/cache/independent sequence")
	}
	if payloadOf(peer).sequences.Memory(addresses[1]) != peerMemory || *peerMemory != peerValue {
		t.Fatal("move replaced peer cache")
	}
	sequenceScalar(t, peer, "SELECT nextval('s2.q1')", 18)
	sequenceScalar(t, s, "SELECT nextval('s2.q1')", 11)
	frontier := e.catalog.nextRevision
	if err := sequenceTempMoveCommand(t, e, s, "ALTER TABLE s2.t SET SCHEMA s2", false); err != nil {
		t.Fatal(err)
	}
	if e.catalog.nextRevision != frontier {
		t.Fatal("same-schema no-op consumed revision")
	}
}

func TestAlterSchemaPublicationFences(t *testing.T) {
	for _, kind := range []string{"table_collision", "later_sequence_collision", "cancel", "reset", "authority"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			alterSchemaEngineFixture(t, e)
			source := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s1"})
			dest := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s2"})
			original := make(map[ids.ObjectAddress]catalog.Descriptor)
			for _, name := range []string{"t", "q1", "q2", "free"} {
				a := NameAddressForTempMove(t, e, source, name)
				original[a] = e.catalog.state.Get(a)
			}
			frontier := e.catalog.nextRevision
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var once sync.Once
			provider := e.catalog.Providers[ids.RelationClassID]
			e.catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: provider, callback: func() {
				once.Do(func() {
					e.catalog.Providers[ids.RelationClassID] = provider
					switch kind {
					case "table_collision":
						run(t, e, "CREATE TABLE s2.t(id INT)")
					case "later_sequence_collision":
						run(t, e, "CREATE SEQUENCE s2.q2")
					case "cancel":
						cancel()
					case "reset":
						if err := e.Reset(context.Background()); err != nil {
							t.Fatal(err)
						}
					case "authority":
						run(t, e, "CREATE USER schema_changed_role")
					}
				})
			}}
			_, err := testExecute(e, ctx, statement(t, "ALTER TABLE s1.t SET SCHEMA s2"), nil)
			switch kind {
			case "table_collision", "later_sequence_collision":
				// Both destination bindings were absent when the move was
				// captured. A publisher committing before name admission
				// produces the pinned pg_class uniqueness diagnostic, not
				// the feature's initial already-present-name error.
				name := "t"
				if kind == "later_sequence_collision" {
					name = "q2"
				}
				want := sqlstate.Error{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505",
					Message: `duplicate key value violates unique constraint "pg_class_relname_nsp_index"`,
					Detail:  fmt.Sprintf("Key (relname, relnamespace)=(%s, %d) already exists.", name, dest.ObjectID),
					Schema:  "pg_catalog", Table: "pg_class", Constraint: "pg_class_relname_nsp_index",
				}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatal(err, diagnostic, want)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "reset":
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
			case "authority":
				if err == nil {
					t.Fatal("authority fence absent")
				}
			}
			if kind != "reset" {
				for a, d := range original {
					if e.catalog.state.Get(a) != d {
						t.Fatal("partial publication", a)
					}
				}
			}
			if kind == "cancel" && e.catalog.nextRevision != frontier {
				t.Fatal("canceled move consumed frontier")
			}
		})
	}
}
