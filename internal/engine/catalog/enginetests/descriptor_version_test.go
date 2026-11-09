package enginetests_test

import (
	"fmt"
	"iter"
	"runtime"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func descriptorVersionInventory(t *testing.T, entries iter.Seq2[ids.ObjectAddress, catalog.Descriptor], current catalog.Read) {
	t.Helper()
	seen := make(map[ids.ObjectAddress]catalog.Descriptor)
	for address, descriptor := range entries {
		if _, duplicate := seen[address]; duplicate || current.Get(address) != descriptor {
			t.Fatal("reconciliation did not return each actual current descriptor once", address)
		}
		seen[address] = descriptor
	}
	if len(seen) != current.DescriptorCount() {
		t.Fatalf("reconciliation entries %d, want actual inventory %d", len(seen), current.DescriptorCount())
	}
}

func descriptorVersionEmpty(t *testing.T, version catalog.DescriptorVersion, current catalog.Read) {
	t.Helper()
	if !version.Matches(current) {
		t.Fatal("exact frozen backing/owner/domain/epoch did not match")
	}
	for address := range version.DescriptorsToReconcile(current) {
		t.Fatal("unchanged immutable backing enumerated a descriptor", address)
	}
}

func TestTruncateDescriptorVersionOwnership(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	original := base.Get(address)
	if original == nil {
		t.Fatal("missing real public namespace")
	}
	state := catalog.CloneState(base.State())
	read := catalog.NewRead(base.Owner(), state)
	version := read.CaptureDescriptorVersion()
	descriptorVersionEmpty(t, version, read)
	descriptorVersionEmpty(t, version, base.WithState(catalog.CloneState(state)))

	// Invocation captures the genuine inventory, not a later writable State.
	var zero catalog.DescriptorVersion
	retained := zero.DescriptorsToReconcile(read)
	next := *state.MustNamespace(address)
	next.ObjectRevision++
	state.SetDescriptor(address, &next)
	if version.Get(address) != original || version == read.CaptureDescriptorVersion() {
		t.Fatal("alias write changed captured baseline or counterfeited frozen equality")
	}
	if version.Matches(read) {
		t.Fatal("aliased descriptor write authenticated the old backing")
	}
	descriptorVersionInventory(t, version.DescriptorsToReconcile(read), read)
	descriptorVersionInventory(t, retained, base)
	state.RemoveDescriptor(address)
	if version.Get(address) != original || read.Get(address) != nil {
		t.Fatal("alias removal changed the admitted baseline")
	}
	if version.Matches(read) {
		t.Fatal("aliased descriptor removal authenticated the old backing")
	}
	descriptorVersionInventory(t, version.DescriptorsToReconcile(read), read)

	t.Run("candidate_and_private", func(t *testing.T) {
		candidate := catalog.NewCandidate(base)
		if err := candidate.Replace(&next); err != nil {
			t.Fatal(err)
		}
		candidateRead := base.WithState(candidate.State())
		if version.Matches(candidateRead) {
			t.Fatal("candidate replacement authenticated the admitted backing")
		}
		descriptorVersionInventory(t, version.DescriptorsToReconcile(candidateRead), candidateRead)
		journal := session.NewJournal()
		journal.RecordDescriptor(base.State(), address, &next)
		private := journal.PrivateRead(base.Owner(), base.State())
		if version.Matches(private) {
			t.Fatal("private descriptor replacement authenticated the admitted backing")
		}
		descriptorVersionInventory(t, version.DescriptorsToReconcile(private), private)
		descriptorVersionEmpty(t, private.CaptureDescriptorVersion(), journal.Clone().PrivateRead(base.Owner(), base.State()))
		if version.Get(address) != original || base.Get(address) != original {
			t.Fatal("private application mutated the original generation")
		}
	})

	t.Run("owner_domain_epoch", func(t *testing.T) {
		owner := *base.Owner()
		foreignOwner := catalog.NewRead(&owner, base.State())
		if version.Matches(foreignOwner) {
			t.Fatal("foreign owner authenticated shared backing")
		}
		descriptorVersionInventory(t, version.DescriptorsToReconcile(foreignOwner), foreignOwner)
		// Change the captured owner's domain in place: pointer identity alone
		// must not authenticate a different object domain.
		ownerRead := catalog.NewRead(&owner, base.State())
		owned := ownerRead.CaptureDescriptorVersion()
		owner.Domain = &ids.Domain{}
		if owned.Matches(ownerRead) {
			t.Fatal("mutated owner domain authenticated shared backing")
		}
		descriptorVersionInventory(t, owned.DescriptorsToReconcile(ownerRead), ownerRead)
		epoch := catalog.CloneState(base.State())
		epoch.SetEpoch(base.Epoch() + 1)
		epochRead := base.WithState(epoch)
		if version.Matches(epochRead) || zero.Matches(base) {
			t.Fatal("foreign epoch or zero version authenticated current backing")
		}
		descriptorVersionInventory(t, version.DescriptorsToReconcile(epochRead), epochRead)
		descriptorVersionInventory(t, zero.DescriptorsToReconcile(base), base)
		for _, uncertified := range []catalog.Read{
			catalog.NewRead(nil, base.State()),
			catalog.NewRead(&catalog.Owner{}, base.State()),
			base.WithState(func() *catalog.State {
				state := catalog.CloneState(base.State())
				state.SetEpoch(0)
				return state
			}()),
		} {
			captured := uncertified.CaptureDescriptorVersion()
			if captured.Matches(uncertified) {
				t.Fatal("uncertified owner/domain/epoch authenticated current backing")
			}
			descriptorVersionInventory(t, captured.DescriptorsToReconcile(uncertified), uncertified)
			if captured.Get(address) != original {
				t.Fatal("uncertified capture lost immutable baseline lookup")
			}
		}
		if (catalog.Read{}).CaptureDescriptorVersion() != zero {
			t.Fatal("missing state certified a descriptor generation")
		}
	})

	t.Run("bulk_and_equivalent_rebuild", func(t *testing.T) {
		bulk := catalog.CloneState(base.State())
		bulk.BeginBulkLoad(base.DescriptorCount())
		bulkRead := base.WithState(bulk)
		if version.Matches(bulkRead) {
			t.Fatal("mutable bulk state authenticated frozen backing")
		}
		descriptorVersionInventory(t, version.DescriptorsToReconcile(bulkRead), bulkRead)
		func() {
			defer func() {
				if recover() == nil {
					t.Error("mutable bulk state certified a frozen descriptor version")
				}
			}()
			bulkRead.CaptureDescriptorVersion()
		}()
		bulk.SetDescriptor(address, &next)
		bulk.EndBulkLoad()
		if version.Matches(bulkRead) {
			t.Fatal("bulk refill authenticated original backing")
		}
		descriptorVersionInventory(t, version.DescriptorsToReconcile(bulkRead), bulkRead)
		rebuilt := catalog.NewState(base.Epoch())
		rebuilt.BeginBulkLoad(base.DescriptorCount())
		for a, d := range base.Descriptors() {
			rebuilt.SetDescriptor(a, d)
		}
		rebuilt.EndBulkLoad()
		if version.Matches(base.WithState(rebuilt)) {
			t.Fatal("equivalent rebuilt inventory authenticated distinct backing")
		}
		descriptorVersionInventory(t, version.DescriptorsToReconcile(base.WithState(rebuilt)), base.WithState(rebuilt))
	})

	t.Run("typed_backing_lifetime", func(t *testing.T) {
		frozen := func() catalog.DescriptorVersion {
			private := catalog.CloneState(base.State())
			private.SetDescriptor(address, &next)
			return base.WithState(private).CaptureDescriptorVersion()
		}()
		runtime.GC()
		if frozen.Get(address) != &next || version.Get(address) != original {
			t.Fatal("retained typed backing lost its generation")
		}
	})
}

func TestTruncateDescriptorReplayBounded(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(fmt.Sprintf("private_%v", private), func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE schema_migrations(version BIGINT NOT NULL, dirty BOOLEAN NOT NULL)")
			tx := enginetest.TransactionBegin(t, s)
			if private {
				enginetest.SessionSQL(t, s, "CREATE TABLE private_before(id INT)")
			}
			current := func() catalog.Read {
				e.Mu.Lock()
				defer e.Mu.Unlock()
				return e.PrivateCatalogLocked(s.Transactions.Catalog)
			}
			version := current().CaptureDescriptorVersion()
			original := version
			for marker := 1; marker <= 256; marker++ {
				enginetest.SessionSQL(t, s, "TRUNCATE schema_migrations")
				enginetest.SessionSQL(t, s, fmt.Sprintf("INSERT INTO schema_migrations(version,dirty) VALUES(%d,false)", marker))
				if private && marker == 16 {
					before := current()
					enginetest.SessionSQL(t, s, "ALTER TABLE private_before ADD note TEXT")
					changed := current()
					descriptorVersionInventory(t, version.DescriptorsToReconcile(changed), changed)
					descriptorVersionEmpty(t, version, before)
					version = changed.CaptureDescriptorVersion()
				}
				if marker == 32 || marker == 64 || marker == 128 || marker == 256 {
					view := current()
					descriptorVersionEmpty(t, version, view)
					if private && len(s.Transactions.Catalog.Descriptors) == 0 {
						t.Fatal("marker proof accidentally exercised an empty private journal")
					}
					if private {
						descriptorVersionInventory(t, original.DescriptorsToReconcile(view), view)
					}
					e.Mu.Lock()
					err := e.CatalogPreflightLocked(s.Transactions.Catalog)
					e.Mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			enginetest.TransactionRows(t, s, "SELECT version FROM schema_migrations", 256)
			enginetest.TransactionEnd(t, s, tx, false)
		})
	}
}
