package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
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
				peerNamespace := b.TemporaryNamespace
				peerName := e.catalog.state.MustNamespace(peerNamespace).Name()
				ownName := ""
				if !fresh {
					ownName = e.catalog.state.MustNamespace(a.TemporaryNamespace).Name()
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
				catalogSnapshot, namespace, cell := e.catalog.state, a.TemporaryNamespace, held.sequences
				retained := catalog.CloneState(catalogSnapshot)
				generators := make(map[ids.ObjectAddress]storage.SequenceGenerator, len(e.storage.Sequences))
				for address, g := range e.storage.Sequences {
					generators[address] = *g
				}
				memories := make(map[ids.ObjectAddress]sequence.Memory, payloadOf(a).sequences.Len())
				for address, m := range payloadOf(a).sequences.All() {
					memories[address] = *m
				}
				peerAddress := NameAddressForTempMove(t, e, peerNamespace, "seed")
				peerDescriptor, peerMemory := catalogSnapshot.Get(peerAddress), *payloadOf(b).sequences.Memory(peerAddress)
				last, peerLast := payloadOf(a).sequences.Last(), payloadOf(b).sequences.Last()
				err = sequenceTempMoveCommand(t, e, a, query, prepared)
				if !success {
					sequenceOwnerDiagnostic(t, err, code, message, "")
					if e.catalog.state != catalogSnapshot || !reflect.DeepEqual(catalogSnapshot, retained) || a.TemporaryNamespace != namespace || b.TemporaryNamespace != peerNamespace || payloadOf(a).sequences.Last() != last || payloadOf(b).sequences.Last() != peerLast || held.sequences != cell || len(e.storage.Sequences) != len(generators) || payloadOf(a).sequences.Len() != len(memories) {
						t.Fatal("creation rejection published catalog, namespace event, generator, memory or held analysis")
					}
					for address, want := range generators {
						if e.storage.Sequences[address] == nil || *e.storage.Sequences[address] != want {
							t.Fatal("creation rejection changed generator", address)
						}
					}
					for address, want := range memories {
						if payloadOf(a).sequences.Memory(address) == nil || *payloadOf(a).sequences.Memory(address) != want {
							t.Fatal("creation rejection changed cached tail", address)
						}
					}
				} else {
					if err != nil {
						t.Fatal("valid own temporary creation", err)
					}
					ns := e.catalog.state.MustNamespace(a.TemporaryNamespace)
					if ns.Temporary != a.SessionToken || ns.TemporaryToast || ns.Owner.ID() != 10 || (namespace != (ids.ObjectAddress{}) && ns.Address() != namespace) {
						t.Fatal("creation lost exact namespace/session/bootstrap ownership")
					}
					assertTemporaryNamespacePair(t, e, a.SessionToken, ns.Address())
					address := NameAddressForTempMove(t, e, ns.Address(), "created")
					d := e.catalog.state.MustRelation(address)
					want := catalog.SequenceOptions{SQLType: sqltype.Int8, Increment: 1, Min: 1, Max: 9223372036854775807, Start: 60, Cache: 3, Persistence: "temporary"}
					g := e.storage.Sequences[address]
					if d.Namespace() != ns.Address() || d.Owner != e.Roles().LookupRef("postgres") || d.RelationKind != catalog.SequenceRelation || d.Address() == last || !reflect.DeepEqual(*d.Sequence, want) || g.Last != 60 || g.Called || g.Generation == 0 || g.Generation != d.Revision() || d.SequencePlanStamp != d.Revision() {
						t.Fatal("creation identity/options/generator mismatch", d, g)
					}
					wantGeneration := catalogSnapshot.NamespaceGeneration()
					if fresh {
						wantGeneration++
					}
					if e.catalog.state.NamespaceGeneration() != wantGeneration {
						t.Fatal("wrong namespace insertion event", e.catalog.state.NamespaceGeneration(), wantGeneration)
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
				ownedNamespace := a.TemporaryNamespace
				a.RequestEnd(session.CleanClose)
				if err := a.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
				for address, d := range e.catalog.state.Descriptors() {
					if ownedNamespace != (ids.ObjectAddress{}) && d.Namespace() == ownedNamespace {
						t.Fatal("physical end leaked owned temporary child", address)
					}
				}
				if ownedNamespace != (ids.ObjectAddress{}) {
					assertTemporaryNamespacePair(t, e, a.SessionToken, ownedNamespace)
				}
				if a.TemporaryNamespace != (ids.ObjectAddress{}) || payloadOf(a).sequences.Len() != 0 || payloadOf(a).sequences.Last() != (ids.ObjectAddress{}) || e.catalog.state.Get(peerAddress) != peerDescriptor || b.TemporaryNamespace != peerNamespace || *payloadOf(b).sequences.Memory(peerAddress) != peerMemory {
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
				ns := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "denied"})
				if err := e.SetObjectACL(context.Background(), e.catalog.Domain.Ref(e.catalog.state.Epoch(), ns), authorization.ACLValue{Explicit: true}); err != nil {
					t.Fatal(err)
				}
				sessionSQL(t, s, "SET SESSION AUTHORIZATION creator")
				sessionSQL(t, s, "CREATE TEMP SEQUENCE own_temp CACHE 7")
				sequenceScalar(t, s, "SELECT nextval('own_temp')", 1)
				_, held := sequencePreparedDescription(t, e, s, "SELECT nextval('own_temp')")
				ref := held.sequences.Constants[0].Ref
				catalog, namespace, cell := e.catalog.state, s.TemporaryNamespace, held.sequences
				generator, memory := *e.storage.Sequences[ref.Address], *payloadOf(s).sequences.Memory(ref.Address)
				query := "CREATE TEMP SEQUENCE denied.created"
				if existing {
					query = "CREATE TEMP SEQUENCE IF NOT EXISTS denied.seed"
				}
				err := sequenceTempMoveCommand(t, e, s, query, prepared)
				sequenceOwnerDiagnostic(t, err, "42501", "permission denied for schema denied", "")
				if e.catalog.state != catalog || s.TemporaryNamespace != namespace || held.sequences != cell || *e.storage.Sequences[ref.Address] != generator || *payloadOf(s).sequences.Memory(ref.Address) != memory {
					t.Fatal("CREATE permission rejection changed authority/state")
				}
				sequenceScalar(t, s, "SELECT nextval('own_temp')", 2)
			})
		}
	}
}
