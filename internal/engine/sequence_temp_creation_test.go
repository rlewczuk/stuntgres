package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSequenceTemporaryCreationAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, branch := range []string{
			"ImplicitTemp", "ActualOwnTemp", "LoggedAlias", "LoggedActualOwn", "LoggedPath",
			"UnloggedFreshAlias", "UnloggedFreshPath", "UnloggedAlias", "UnloggedOwn", "UnloggedPeer",
			"UnloggedAliasExisting", "UnloggedOwnExisting", "UnloggedPeerExisting",
			"TempPeer", "TempPeerExisting", "LoggedPeer", "TempPersistent", "TempPersistentExisting", "TempMissing",
		} {
			mode := map[bool]string{false: "Direct", true: "Prepared"}[prepared]
			t.Run(branch+"/"+mode, func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE SEQUENCE public.persistent START 10 CACHE 7")
				sessionSQL(t, b, "CREATE TEMP SEQUENCE seed START 40 CACHE 5")
				fresh := branch == "ImplicitTemp" || branch == "UnloggedFreshAlias" || branch == "UnloggedFreshPath"
				if !fresh {
					sessionSQL(t, a, "CREATE TEMP SEQUENCE seed START 30 CACHE 7")
					sequenceScalar(t, a, "SELECT nextval('seed')", 30)
				}
				sequenceScalar(t, b, "SELECT nextval('seed')", 40)
				peerNamespace := b.temporaryNamespace
				peerName := e.state.descriptors[peerNamespace].(*namespaceDescriptor).name
				ownName := ""
				if !fresh {
					ownName = e.state.descriptors[a.temporaryNamespace].(*namespaceDescriptor).name
				}
				query := "CREATE TEMP SEQUENCE created START 60 CACHE 3"
				code, message := "42P16", "only temporary relations may be created in temporary schemas"
				success := false
				switch branch {
				case "ImplicitTemp":
					success = true
				case "ActualOwnTemp":
					query, success = "CREATE TEMP SEQUENCE "+ownName+".created START 60 CACHE 3", true
				case "LoggedAlias":
					query, success = "CREATE SEQUENCE pg_temp.created START 60 CACHE 3", true
				case "LoggedActualOwn":
					query, success = "CREATE SEQUENCE "+ownName+".created START 60 CACHE 3", true
				case "LoggedPath":
					sessionSQL(t, a, "SET search_path TO pg_temp,public")
					query, success = "CREATE SEQUENCE created START 60 CACHE 3", true
				case "UnloggedFreshAlias", "UnloggedAlias":
					query = "CREATE UNLOGGED SEQUENCE pg_temp.created"
				case "UnloggedFreshPath":
					sessionSQL(t, a, "SET search_path TO pg_temp,public")
					query = "CREATE UNLOGGED SEQUENCE created"
				case "UnloggedOwn":
					query = "CREATE UNLOGGED SEQUENCE " + ownName + ".created"
				case "UnloggedPeer":
					query = "CREATE UNLOGGED SEQUENCE " + peerName + ".created"
				case "UnloggedAliasExisting":
					query = "CREATE UNLOGGED SEQUENCE IF NOT EXISTS pg_temp.seed"
				case "UnloggedOwnExisting":
					query = "CREATE UNLOGGED SEQUENCE IF NOT EXISTS " + ownName + ".seed"
				case "UnloggedPeerExisting":
					query = "CREATE UNLOGGED SEQUENCE IF NOT EXISTS " + peerName + ".seed"
				case "TempPeer", "TempPeerExisting", "LoggedPeer":
					message = "cannot create relations in temporary schemas of other sessions"
					query = "CREATE TEMP SEQUENCE " + peerName + ".created"
					if branch == "TempPeerExisting" {
						query = "CREATE TEMP SEQUENCE IF NOT EXISTS " + peerName + ".seed"
					} else if branch == "LoggedPeer" {
						query = "CREATE SEQUENCE " + peerName + ".created"
					}
				case "TempPersistent", "TempPersistentExisting":
					message = "cannot create temporary relation in non-temporary schema"
					query = "CREATE TEMP SEQUENCE public.created"
					if branch == "TempPersistentExisting" {
						query = "CREATE TEMP SEQUENCE IF NOT EXISTS public.persistent"
					}
				case "TempMissing":
					query, code, message = "CREATE TEMP SEQUENCE absent.created", "3F000", `schema "absent" does not exist`
				}
				ast, held := sequencePreparedDescription(t, e, a, "SELECT nextval('public.persistent')")
				r, err := sequencePreparedExecute(e, a, ast, &held)
				if err != nil {
					t.Fatal(err)
				}
				if got := tuples(r); len(got) != 1 || got[0][0] != int64(10) {
					t.Fatal("initial held allocation", got)
				}
				r.Rows.Close()
				catalog, namespace, cell := e.state, a.temporaryNamespace, held.sequences
				retained := cloneCatalog(catalog)
				generators := make(map[ObjectAddress]sequenceGenerator, len(e.sequences))
				for address, g := range e.sequences {
					generators[address] = *g
				}
				memories := make(map[ObjectAddress]sequenceMemory, len(a.sequences))
				for address, m := range a.sequences {
					memories[address] = *m
				}
				peerAddress := NameAddressForTempMove(t, e, peerNamespace, "seed")
				peerDescriptor, peerMemory := catalog.descriptors[peerAddress], *b.sequences[peerAddress]
				last, peerLast := a.lastSequence, b.lastSequence
				err = sequenceTempMoveCommand(t, e, a, query, prepared)
				if !success {
					sequenceOwnerDiagnostic(t, err, code, message, "")
					if e.state != catalog || !reflect.DeepEqual(catalog, retained) || a.temporaryNamespace != namespace || b.temporaryNamespace != peerNamespace || a.lastSequence != last || b.lastSequence != peerLast || held.sequences != cell || len(e.sequences) != len(generators) || len(a.sequences) != len(memories) {
						t.Fatal("creation rejection published catalog, namespace event, generator, memory or held analysis")
					}
					for address, want := range generators {
						if e.sequences[address] == nil || *e.sequences[address] != want {
							t.Fatal("creation rejection changed generator", address)
						}
					}
					for address, want := range memories {
						if a.sequences[address] == nil || *a.sequences[address] != want {
							t.Fatal("creation rejection changed cached tail", address)
						}
					}
				} else {
					if err != nil {
						t.Fatal("valid own temporary creation", err)
					}
					ns := e.state.descriptors[a.temporaryNamespace].(*namespaceDescriptor)
					if ns.temporary != a.token || ns.owner != e.state.descriptors[last].(*relationDescriptor).owner || (namespace != (ObjectAddress{}) && ns.address != namespace) {
						t.Fatal("creation lost exact namespace/session/role ownership")
					}
					address := NameAddressForTempMove(t, e, ns.address, "created")
					d := e.state.descriptors[address].(*relationDescriptor)
					want := sequenceOptions{typ: sqltype.Int8, increment: 1, min: 1, max: 9223372036854775807, start: 60, cache: 3, persistence: "temporary"}
					g := e.sequences[address]
					if d.namespace != ns.address || d.owner != ns.owner || d.relationKind != sequenceRelation || d.address == last || !reflect.DeepEqual(*d.sequence, want) || g.last != 60 || g.called || g.generation == 0 || g.generation != d.revision || d.sequencePlanStamp != d.revision {
						t.Fatal("creation identity/options/generator mismatch", d, g)
					}
					wantGeneration := catalog.namespaceGeneration
					if fresh {
						wantGeneration++
					}
					if e.state.namespaceGeneration != wantGeneration {
						t.Fatal("wrong namespace insertion event", e.state.namespaceGeneration, wantGeneration)
					}
					sequenceScalar(t, a, "SELECT nextval('pg_temp.created')", 60)
				}
				r, err = sequencePreparedExecute(e, a, ast, &held)
				if err != nil {
					t.Fatal(err)
				}
				if got := tuples(r); len(got) != 1 || got[0][0] != int64(11) || (!success && held.sequences != cell) {
					t.Fatal("creation disturbed held cache progression", got)
				}
				r.Rows.Close()
				ownedNamespace := a.temporaryNamespace
				a.RequestEnd(CleanClose)
				if err := a.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
				for address, d := range e.state.descriptors {
					if ownedNamespace != (ObjectAddress{}) && (address == ownedNamespace || d.Namespace() == ownedNamespace) {
						t.Fatal("physical end leaked owned temporary identity", address)
					}
				}
				if a.temporaryNamespace != (ObjectAddress{}) || len(a.sequences) != 0 || a.lastSequence != (ObjectAddress{}) || e.state.descriptors[peerAddress] != peerDescriptor || b.temporaryNamespace != peerNamespace || *b.sequences[peerAddress] != peerMemory {
					t.Fatal("cleanup retained own state or changed peer identity/cache")
				}
				sequenceScalar(t, b, "SELECT nextval('seed')", 41)
				sequenceScalar(t, b, "SELECT nextval('public.persistent')", 17)
			})
		}
	}
}

func TestSequenceTemporaryCreationPermissionPrecedence(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, existing := range []bool{false, true} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/New", true: "/Existing"}[existing], func(t *testing.T) {
				e := New()
				s := sessionStart(t, e, nil)
				sessionSQL(t, s, "CREATE USER creator")
				sessionSQL(t, s, "CREATE SCHEMA denied")
				sessionSQL(t, s, "CREATE SEQUENCE denied.seed CACHE 7")
				ns := e.state.names[NameKey{ClassID: NamespaceClassID, Name: "denied"}]
				if err := e.SetObjectACL(context.Background(), ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: ns}, authorization.ACLValue{Explicit: true}); err != nil {
					t.Fatal(err)
				}
				sessionSQL(t, s, "SET SESSION AUTHORIZATION creator")
				sessionSQL(t, s, "CREATE TEMP SEQUENCE own_temp CACHE 7")
				sequenceScalar(t, s, "SELECT nextval('own_temp')", 1)
				_, held := sequencePreparedDescription(t, e, s, "SELECT nextval('own_temp')")
				ref := held.sequences.constants[0].ref
				catalog, namespace, cell := e.state, s.temporaryNamespace, held.sequences
				generator, memory := *e.sequences[ref.Address], *s.sequences[ref.Address]
				query := "CREATE TEMP SEQUENCE denied.created"
				if existing {
					query = "CREATE TEMP SEQUENCE IF NOT EXISTS denied.seed"
				}
				err := sequenceTempMoveCommand(t, e, s, query, prepared)
				sequenceOwnerDiagnostic(t, err, "42501", "permission denied for schema denied", "")
				if e.state != catalog || s.temporaryNamespace != namespace || held.sequences != cell || *e.sequences[ref.Address] != generator || *s.sequences[ref.Address] != memory {
					t.Fatal("CREATE permission rejection changed authority/state")
				}
				sequenceScalar(t, s, "SELECT nextval('own_temp')", 2)
			})
		}
	}
}
