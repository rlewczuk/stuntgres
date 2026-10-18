package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func sequenceTempMoveCommand(t *testing.T, e *Engine, s *Session, query string, prepared bool) error {
	t.Helper()
	if prepared {
		ast, description := sequencePreparedDescription(t, e, s, query)
		_, err := sequencePreparedExecute(e, s, ast, &description)
		return err
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	_, err = e.Execute(op, statement(t, query), 0, nil)
	return err
}

// The held analysis and real cache are consumers of the same catalog authority:
// rejection must leave them intact, not merely retain the relation's name.
func TestSequenceTemporaryMoveAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, destination := range []string{"TempToPublic", "OwnAlias", "OwnIdentity", "PeerIdentity", "PeerCreate", "UninitializedAlias", "TempSameIdentity"} {
			mode := map[bool]string{false: "Direct", true: "Prepared"}[prepared]
			t.Run(destination+"/"+mode, func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE SEQUENCE public.persistent START 10 CACHE 7")
				sessionSQL(t, b, "CREATE TEMP SEQUENCE peer_temp START 40 CACHE 5")
				if destination != "UninitializedAlias" {
					sessionSQL(t, a, "CREATE TEMP SEQUENCE own_temp START 30 CACHE 7")
				}
				source, target := "public.persistent", "pg_temp"
				switch destination {
				case "TempToPublic":
					source, target = "own_temp", "public"
				case "OwnIdentity":
					target = e.state.descriptors[a.temporaryNamespace].(*namespaceDescriptor).name
				case "PeerIdentity", "PeerCreate":
					target = e.state.descriptors[b.temporaryNamespace].(*namespaceDescriptor).name
				case "TempSameIdentity":
					source = "own_temp"
					target = e.state.descriptors[a.temporaryNamespace].(*namespaceDescriptor).name
				}
				ast, held := sequencePreparedDescription(t, e, a, "SELECT nextval('"+source+"')")
				ref := held.sequences.constants[0].ref
				first := int64(10)
				if source == "own_temp" {
					first = 30
				}
				r, err := sequencePreparedExecute(e, a, ast, &held)
				if err != nil {
					t.Fatal(err)
				}
				got := tuples(r)
				r.Rows.Close()
				if len(got) != 1 || got[0][0] != first {
					t.Fatal("initial held allocation", got)
				}
				snapshot := sessionSQL(t, a, "SELECT last_value FROM "+source)
				catalog := e.state
				descriptor := catalog.descriptors[ref.Address].(*relationDescriptor)
				generator := *e.sequences[ref.Address]
				memory := *a.sequences[ref.Address]
				last, namespace, peerNamespace := a.lastSequence, a.temporaryNamespace, b.temporaryNamespace
				cell := held.sequences
				peerDescriptor := catalog.descriptors[peerNamespace]
				peerGenerator := *e.sequences[NameAddressForTempMove(t, e, peerNamespace, "peer_temp")]
				query := "ALTER SEQUENCE " + source + " SET SCHEMA " + target
				if destination == "PeerCreate" {
					query = "CREATE SEQUENCE " + target + ".forbidden_new"
				}
				err = sequenceTempMoveCommand(t, e, a, query, prepared)
				if destination == "PeerIdentity" || destination == "PeerCreate" {
					sequenceOwnerDiagnostic(t, err, "42P16", "cannot create relations in temporary schemas of other sessions", "")
				} else {
					sequenceOwnerDiagnostic(t, err, "0A000", "cannot move objects into or out of temporary schemas", "")
				}
				if e.state != catalog || e.state.descriptors[ref.Address] != descriptor || *e.sequences[ref.Address] != generator || *a.sequences[ref.Address] != memory || a.lastSequence != last || a.temporaryNamespace != namespace || b.temporaryNamespace != peerNamespace || held.sequences != cell {
					t.Fatal("rejected move changed catalog, identity, stamp, generator, cache, held binding or session ownership")
				}
				if e.state.descriptors[peerNamespace] != peerDescriptor || *e.sequences[NameAddressForTempMove(t, e, peerNamespace, "peer_temp")] != peerGenerator {
					t.Fatal("rejected move changed peer ownership or generator")
				}
				r, err = sequencePreparedExecute(e, a, ast, &held)
				if err != nil {
					t.Fatal("held source invalidated by rejected move", err)
				}
				got = tuples(r)
				r.Rows.Close()
				if len(got) != 1 || got[0][0] != first+1 || held.sequences != cell {
					t.Fatal("rejected move disturbed held cache progression", got)
				}
				a.RequestEnd(CleanClose)
				if err := a.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
				if namespace != (ObjectAddress{}) {
					assertTemporaryNamespacePair(t, e, a.token, namespace)
					for address, d := range catalog.descriptors {
						if d.Namespace() == namespace && (e.state.descriptors[address] != nil || e.sequences[address] != nil || e.storage[address] != nil) {
							t.Fatal("physical end leaked owned temporary object", address)
						}
					}
				}
				if a.temporaryNamespace != (ObjectAddress{}) || len(a.sequences) != 0 || a.lastSequence != (ObjectAddress{}) {
					t.Fatal("physical end retained session namespace or memory")
				}
				if e.state.descriptors[peerNamespace] != peerDescriptor {
					t.Fatal("physical end removed peer namespace")
				}
				sequenceScalar(t, b, "SELECT nextval('peer_temp')", 40)
				wantPersistent := int64(10)
				if source == "public.persistent" {
					wantPersistent = 17 // owner cache reserved 10..16; physical end loses its tail.
				}
				sequenceScalar(t, b, "SELECT nextval('public.persistent')", wantPersistent)
				if got := tuples(snapshot); len(got) != 1 || got[0][0] != first+6 {
					t.Fatal("retained row changed after rejection or physical cleanup", got)
				}
				if catalog.descriptors[ref.Address] != descriptor {
					t.Fatal("cleanup mutated retained catalog")
				}
			})
		}
	}
}

func NameAddressForTempMove(t *testing.T, e *Engine, namespace ObjectAddress, name string) ObjectAddress {
	t.Helper()
	a, ok := e.state.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: name}]
	if !ok {
		t.Fatal("missing fixture relation", name)
	}
	return a
}

func TestSequenceTemporaryMovePrecedenceAndPersistentMove(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE SCHEMA moved", "CREATE TABLE owner_table(c INT)", "CREATE SEQUENCE linked OWNED BY owner_table.c", "CREATE TEMP SEQUENCE own_temp", "CREATE SEQUENCE persistent CACHE 7"} {
				sessionSQL(t, s, q)
			}
			for _, destination := range []string{"absent", "pg_temp"} {
				err := sequenceTempMoveCommand(t, e, s, "ALTER SEQUENCE linked SET SCHEMA "+destination, prepared)
				sequenceOwnerDiagnostic(t, err, "0A000", "cannot move an owned sequence into another schema", `Sequence "linked" is linked to table "owner_table".`)
			}
			err := sequenceTempMoveCommand(t, e, s, "ALTER SEQUENCE own_temp SET SCHEMA absent", prepared)
			sequenceOwnerDiagnostic(t, err, "3F000", `schema "absent" does not exist`, "")
			ast, held := sequencePreparedDescription(t, e, s, "SELECT nextval('persistent')")
			ref := held.sequences.constants[0].ref
			sequenceScalar(t, s, "SELECT nextval('persistent')", 1)
			before := e.state.descriptors[ref.Address].(*relationDescriptor)
			generator, memory := *e.sequences[ref.Address], *s.sequences[ref.Address]
			if err := sequenceTempMoveCommand(t, e, s, "ALTER SEQUENCE persistent SET SCHEMA moved", prepared); err != nil {
				t.Fatal("ordinary persistent move", err)
			}
			after := e.state.descriptors[ref.Address].(*relationDescriptor)
			if after.address != before.address || after.namespace == before.namespace || after.sequencePlanStamp == before.sequencePlanStamp || !reflect.DeepEqual(after.sequence, before.sequence) || *e.sequences[ref.Address] != generator || *s.sequences[ref.Address] != memory {
				t.Fatal("persistent move changed identity/options/cache or failed to invalidate")
			}
			_, err = sequencePreparedExecute(e, s, ast, &held)
			state(t, err, "42P01")
			sequenceScalar(t, s, "SELECT nextval('moved.persistent')", 2)
		})
	}
}

func TestSequenceTemporaryMoveCreatePermissionPrecedence(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE USER mover")
			sessionSQL(t, s, "CREATE SCHEMA denied")
			ns := e.state.names[NameKey{ClassID: NamespaceClassID, Name: "denied"}]
			if err := e.SetObjectACL(context.Background(), ObjectRef{owner: e.domain, Epoch: e.state.epoch, Address: ns}, authorization.ACLValue{Explicit: true}); err != nil {
				t.Fatal(err)
			}
			sessionSQL(t, s, "SET SESSION AUTHORIZATION mover")
			sessionSQL(t, s, "CREATE TEMP SEQUENCE own_temp CACHE 7")
			sequenceScalar(t, s, "SELECT nextval('own_temp')", 1)
			_, held := sequencePreparedDescription(t, e, s, "SELECT nextval('own_temp')")
			ref := held.sequences.constants[0].ref
			catalog, namespace := e.state, s.temporaryNamespace
			generator, memory := *e.sequences[ref.Address], *s.sequences[ref.Address]
			err := sequenceTempMoveCommand(t, e, s, "ALTER SEQUENCE own_temp SET SCHEMA denied", prepared)
			sequenceOwnerDiagnostic(t, err, "42501", "permission denied for schema denied", "")
			if e.state != catalog || s.temporaryNamespace != namespace || *e.sequences[ref.Address] != generator || *s.sequences[ref.Address] != memory {
				t.Fatal("destination permission rejection published effects")
			}
			sequenceScalar(t, s, "SELECT nextval('own_temp')", 2)
		})
	}
}
