package core_test

import (
 "errors"
 "reflect"
 "testing"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/core/contrib"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/storage"
)

func TestPgPrewarmSequence(t *testing.T) {
 e, _, n := prewarmOwnerEngine(t)
 enginetest.Run(t, e, "CREATE UNLOGGED SEQUENCE prewarm_sequence")
 ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "prewarm_sequence")
 node := bufferUnitNode(t, e, "prewarm_sequence")
 if node == 0 { t.Fatal("genuine sequence has no storage locator") }
 actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
 // Do not let the new explicit locator silently change ordinary source reads.
 enginetest.SessionSQL(t, actor, "SELECT last_value FROM prewarm_sequence")
 if got := prewarmOwnerCoordinates(t, e, observer, node); len(got) != 0 { t.Fatal("ordinary sequence source acquired a new cache frontier", got) }
 scope, finish := prewarmOwnerScope(t, t.Context(), e, actor)
 host := core.HostOf(scope).(contrib.Host[core.AuthorizationView])
 host.ReacquireOwnerEngine()
 generator := host.SequenceGeneratorLocked(ref.Address)
 if generator == nil { host.ReleaseOwnerEngineLocked(); t.Fatal("missing real generator") }
 original := *generator
 host.ReleaseOwnerEngineLocked()
 for _, fork := range []int16{0,3} {
  count, err := contrib.PgPrewarm[core.AuthorizationView](host, n, uint32(ref.Address.ObjectID), storage.PrewarmRequest{Fork: fork, DefaultFirst: true, DefaultLast: true})
  if err != nil || count != 1 { t.Fatal("genuine sequence/init preload", fork, count, err) }
 }
 host.ReacquireOwnerEngine()
 unchanged := host.SequenceGeneratorLocked(ref.Address) == generator && reflect.DeepEqual(*generator, original)
 host.ReleaseOwnerEngineLocked()
 if !unchanged { t.Fatal("preload changed generator state/authority") }
 if err := finish(nil); err != nil { t.Fatal(err) }
 want := map[[2]int64][2]int64{{0,0}: {1,0}, {3,0}: {1,0}}
 if got := prewarmOwnerCoordinates(t, e, observer, node); !reflect.DeepEqual(got, want) { t.Fatal("sequence count-only preload or leaked pins", got, want) }
 // Name/ACL and real counter mutations retain the allocated storage family.
 for _, query := range []string{"ALTER SEQUENCE prewarm_sequence RENAME TO prewarm_sequence_kept", "GRANT SELECT ON SEQUENCE prewarm_sequence_kept TO PUBLIC", "SELECT nextval('prewarm_sequence_kept')", "SELECT setval('prewarm_sequence_kept',17,false)"} {
  enginetest.SessionSQL(t, actor, query)
  if current := bufferUnitNode(t, e, "prewarm_sequence_kept"); current != node { t.Fatal("metadata/counter reminted locator", query, current, node) }
  if got := prewarmOwnerCoordinates(t, e, observer, node); !reflect.DeepEqual(got, want) { t.Fatal("metadata/counter changed coordinates", query, got, want) }
 }
 e.Mu.Lock()
 kept := e.Storage.Sequences[ref.Address]
 counterKept := kept == generator && kept.Generation == original.Generation && kept.Last == 17 && !kept.Called
 e.Mu.Unlock()
 if !counterKept { t.Fatal("counter/metadata replaced genuine generator authority") }
 warm := func(t *testing.T, name string, init bool) {
  t.Helper()
  enginetest.TransactionRows(t, actor, "SELECT pg_prewarm('"+name+"','buffer','main',0,0)", 1)
  if init { enginetest.TransactionRows(t, actor, "SELECT pg_prewarm('"+name+"','buffer','init',0,0)", 1) }
 }
 replaced := func(t *testing.T, name, query string, init bool) {
  t.Helper()
  previous := bufferUnitNode(t, e, name)
  address := enginetest.CatalogRef(t, e, e.CatalogRead(), name).Address
  e.Mu.Lock()
  before := e.Storage.Sequences[address]
  generation := before.Generation
  e.Mu.Unlock()
  enginetest.SessionSQL(t, actor, query)
  current := bufferUnitNode(t, e, name)
  e.Mu.Lock()
  after := e.Storage.Sequences[address]
  changed := after != nil && after != before && after.Generation != generation
  e.Mu.Unlock()
  if !changed || current == 0 || current == previous { t.Fatal("semantic generator replacement retained incarnation", query, previous, current, changed) }
  if got := prewarmOwnerCoordinates(t, e, observer, previous); len(got) != 0 { t.Fatal("semantic replacement retained old coordinates", query, got) }
  if got := prewarmOwnerCoordinates(t, e, observer, current); len(got) != 0 { t.Fatal("semantic replacement inherited occupancy", query, got) }
  warm(t, name, init)
  expected := map[[2]int64][2]int64{{0,0}: {1,0}}
  if init { expected[[2]int64{3,0}] = [2]int64{1,0} }
  if got := prewarmOwnerCoordinates(t, e, observer, current); !reflect.DeepEqual(got, expected) { t.Fatal("replacement did not genuinely rewarm without pins", query, got, expected) }
 }
 replaced(t, "prewarm_sequence_kept", "ALTER SEQUENCE prewarm_sequence_kept RESTART WITH 9", true)
 replaced(t, "prewarm_sequence_kept", "ALTER SEQUENCE prewarm_sequence_kept SET LOGGED", false)
 node = bufferUnitNode(t, e, "prewarm_sequence_kept")
 beforePrivate := prewarmOwnerCoordinates(t, e, observer, node)
 enginetest.SessionSQL(t, actor, "BEGIN")
 defer func() { if actor.TransactionState() != session.TxIdle { enginetest.SessionSQL(t, actor, "ROLLBACK") } }()
 enginetest.SessionSQL(t, actor, "SAVEPOINT before_sequence_restart")
 enginetest.SessionSQL(t, actor, "ALTER SEQUENCE prewarm_sequence_kept RESTART WITH 23")
 actor.Transactions.Mu.Lock()
 e.Mu.Lock()
 privateNode := e.PrivateCatalogLocked(actor.Transactions.Catalog).MustRelation(ref.Address).StorageLocator
 e.Mu.Unlock()
 actor.Transactions.Mu.Unlock()
 if privateNode == 0 || privateNode == node { t.Fatal("private generator has no fresh authored locator", privateNode, node) }
 warm(t, "prewarm_sequence_kept", false)
 if got := prewarmOwnerCoordinates(t, e, observer, privateNode); len(got) != 0 { t.Fatal("private sequence acquired global occupancy", got) }
 if got := prewarmOwnerCoordinates(t, e, observer, node); !reflect.DeepEqual(got, beforePrivate) { t.Fatal("private sequence retired committed occupancy", got, beforePrivate) }
 enginetest.SessionSQL(t, actor, "ROLLBACK TO before_sequence_restart")
 actor.Transactions.Mu.Lock()
 e.Mu.Lock()
 rolledBack := e.PrivateCatalogLocked(actor.Transactions.Catalog).MustRelation(ref.Address).EffectiveStorageLocator(e.PrivateCatalogLocked(actor.Transactions.Catalog))
 e.Mu.Unlock()
 actor.Transactions.Mu.Unlock()
 if rolledBack != node { t.Fatal("savepoint retained abandoned sequence locator", rolledBack, node) }
 enginetest.SessionSQL(t, actor, "ALTER SEQUENCE prewarm_sequence_kept RESTART WITH 29")
 actor.Transactions.Mu.Lock()
 e.Mu.Lock()
 allocated := e.PrivateCatalogLocked(actor.Transactions.Catalog).MustRelation(ref.Address).StorageLocator
 privateGenerator := actor.Transactions.Catalog.Generators[ref.Address]
 e.Mu.Unlock()
 actor.Transactions.Mu.Unlock()
 if allocated == 0 || allocated == node || allocated == privateNode || privateGenerator == nil { t.Fatal("later sequence rewrite reused abandoned allocation", allocated, node, privateNode) }
 enginetest.SessionSQL(t, actor, "COMMIT")
 e.Mu.Lock()
 retainedGenerator := e.Storage.Sequences[ref.Address] == privateGenerator && privateGenerator.Last == 29 && !privateGenerator.Called
 e.Mu.Unlock()
 if current := bufferUnitNode(t, e, "prewarm_sequence_kept"); current != allocated || !retainedGenerator { t.Fatal("commit reminted admitted sequence storage", current, allocated, retainedGenerator) }
 if got := prewarmOwnerCoordinates(t, e, observer, node); len(got) != 0 { t.Fatal("commit retained predecessor sequence", got) }
 if got := prewarmOwnerCoordinates(t, e, observer, allocated); len(got) != 0 { t.Fatal("committed sequence inherited private occupancy", got) }
 warm(t, "prewarm_sequence_kept", false)
 node = bufferUnitNode(t, e, "prewarm_sequence_kept")
 enginetest.SessionSQL(t, actor, "DROP SEQUENCE prewarm_sequence_kept")
 if got := prewarmOwnerCoordinates(t, e, observer, node); len(got) != 0 { t.Fatal("drop retained sequence coordinates", got) }
 enginetest.SessionSQL(t, actor, "CREATE UNLOGGED SEQUENCE prewarm_sequence_kept")
 recreated := enginetest.CatalogRef(t, e, e.CatalogRead(), "prewarm_sequence_kept")
 if recreated.Address == ref.Address || bufferUnitNode(t, e, "prewarm_sequence_kept") == node { t.Fatal("recreation reused retired sequence identity") }
 warm(t, "prewarm_sequence_kept", true)
 node = bufferUnitNode(t, e, "prewarm_sequence_kept")
 if _, err := e.ReplaceCatalog(t.Context(), e, e.CatalogRead()); err != nil { t.Fatal(err) }
 if got := prewarmOwnerCoordinates(t, e, observer, node); len(got) != 0 { t.Fatal("native replacement retained sequence coordinates", got) }
 freshNode := bufferUnitNode(t, e, "prewarm_sequence_kept")
 if got := prewarmOwnerCoordinates(t, e, observer, freshNode); len(got) != 0 { t.Fatal("native replacement did not start cold", got) }
 warm(t, "prewarm_sequence_kept", true)
 if got := prewarmOwnerCoordinates(t, e, observer, freshNode); !reflect.DeepEqual(got, want) { t.Fatal("remapped sequence failed real preload", got, want) }
 t.Run("identity_generator_authors", func(t *testing.T) {
  enginetest.SessionSQL(t, actor, "CREATE UNLOGGED TABLE prewarm_identity(k integer GENERATED ALWAYS AS IDENTITY)")
  name := "prewarm_identity_k_seq"
  warm(t, name, true)
  replaced(t, name, "ALTER TABLE prewarm_identity ALTER COLUMN k RESTART WITH 7", true)
  replaced(t, name, "TRUNCATE prewarm_identity RESTART IDENTITY", true)
  replaced(t, name, "ALTER TABLE prewarm_identity SET LOGGED", false)
 })
 t.Run("captured_generator_replacement", func(t *testing.T) {
  current, _, call := prewarmOwnerEngine(t)
  enginetest.Run(t, current, "CREATE UNLOGGED SEQUENCE prewarm_capture_sequence")
  target := enginetest.CatalogRef(t, current, current.CatalogRead(), "prewarm_capture_sequence")
  oldNode := bufferUnitNode(t, current, "prewarm_capture_sequence")
  caller, peer := enginetest.StartSession(t, current, nil), enginetest.StartSession(t, current, nil)
  scope, finish := prewarmOwnerScope(t, t.Context(), current, caller)
  counting := &prewarmCountingHost{Host: core.HostOf(scope).(contrib.Host[core.AuthorizationView])}
  changed := false
  captured := &prewarmReleaseHost{prewarmCountingHost: counting, afterCapture: func() {
   changed = true
   enginetest.Run(t, current, "ALTER SEQUENCE prewarm_capture_sequence RESTART WITH 11")
  }}
  count, err := contrib.PgPrewarm[core.AuthorizationView](captured, call, uint32(target.Address.ObjectID), storage.PrewarmRequest{Fork: 0, DefaultFirst: true, DefaultLast: true})
  if !changed || count != 0 || counting.completed != 0 || !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("stale real generator admitted a coordinate", changed, count, counting.completed, err) }
  finish(err)
  if now := bufferUnitNode(t, current, "prewarm_capture_sequence"); now == oldNode { t.Fatal("capture witness did not genuinely replace storage", now) }
  if got := prewarmOwnerCoordinates(t, current, peer, oldNode); len(got) != 0 { t.Fatal("stale sequence warmed retired incarnation", got) }
 })
}
