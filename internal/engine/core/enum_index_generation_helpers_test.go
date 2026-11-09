package core_test

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func enumIndexGenerationRows(rows []storage.RowVersion) []storage.RowVersion {
	next := slices.Clone(rows)
	for i, row := range rows {
		next[i].Values = slices.Clone(row.Values)
		next[i].IndexOutputs = maps.Clone(row.IndexOutputs)
		for address, output := range row.IndexOutputs {
			output.Keys = slices.Clone(output.Keys)
			next[i].IndexOutputs[address] = output
		}
		var history *storage.HeapPredecessor
		tail := &history
		for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
			copy := *prior
			copy.Version = enumIndexGenerationRows([]storage.RowVersion{prior.Version})[0]
			copy.Next = nil
			*tail = &copy
			tail = &copy.Next
		}
		next[i].PrivateHistory = history
	}
	return next
}

func enumIndexGenerationRelation(relation *catalog.Relation) catalog.Relation {
	copy := *relation
	copy.Columns = slices.Clone(relation.Columns)
	copy.Attributes = slices.Clone(relation.Attributes)
	if relation.SQLIndex != nil {
		definition := *relation.SQLIndex
		definition.Keys = slices.Clone(definition.Keys)
		definition.Include = slices.Clone(definition.Include)
		copy.SQLIndex = &definition
	}
	return copy
}

type enumIndexGenerationReceipt struct {
	engine                 *core.Engine
	private                catalog.Read
	descriptorVersion      catalog.DescriptorVersion
	journal, journalCopy   *session.Journal
	parent, descriptor     *catalog.Relation
	parentCopy, indexCopy  catalog.Relation
	store                  *storage.Relation
	versions, writes       []storage.RowVersion
	versionCopy, writeCopy []storage.RowVersion
	root                   *storage.IndexRoot
	entries                []storage.IndexEntry
	construction           any
	candidate              index.RootCandidate
	codec                  *sqltype.Descriptor
	members                []sqltype.EnumMember
	ends                   *storage.DeletionRoot
	endCopy                *storage.DeletionRoot
	deletes                *visibility.PrivateDeletes
	deleteCopy             *visibility.PrivateDeletes
}

// The projection cache may be replaced under Engine.mu as the published base
// changes. Snapshot every journal authority field under that same lock; retain
// the actual immutable cached inventory separately rather than its mutable slot.
func enumIndexGenerationJournalSnapshot(e *core.Engine, journal *session.Journal) *session.Journal {
	e.Mu.Lock()
	snapshot := journal.Clone()
	e.Mu.Unlock()
	snapshot.InvalidatePrivateRead()
	return snapshot
}

func enumIndexGenerationCapture(t *testing.T, e *core.Engine, s *session.Session) enumIndexGenerationReceipt {
	t.Helper()
	journal := s.Transactions.Catalog
	if journal == nil {
		t.Fatal("fixture has no admitted private catalog owner")
	}
	e.Mu.Lock()
	view := e.PrivateCatalogLocked(journal)
	e.Mu.Unlock()
	ref := enginetest.CatalogRef(t, e, view, "enum_generation_idx")
	descriptor := view.MustRelation(ref.Address)
	parent := view.MustRelation(descriptor.SQLIndex.Parent)
	store := e.PrivateStorageLocked(journal, parent.Address())
	j := index.JournalIndexesOrZero(journal)
	candidate, ok := j.Candidates[ref.Address]
	root := j.UniqueRoots[ref.Address]
	if !ok || root == nil || candidate.Root != root || candidate.Index != descriptor || !candidate.MatchesIndex(descriptor, store) {
		t.Fatalf("admitted authority present=%t root=%p candidateRoot=%p owner=%p want=%p storageMatch=%t",
			ok, root, candidate.Root, candidate.Index, descriptor, candidate.MatchesIndex(descriptor, store))
	}
	writes := s.Transactions.Writes[parent.Address()]
	codec := parent.Columns[0].Usage.Type
	return enumIndexGenerationReceipt{
		engine: e, private: view, descriptorVersion: view.CaptureDescriptorVersion(),
		journal: journal, journalCopy: enumIndexGenerationJournalSnapshot(e, journal), parent: parent, descriptor: descriptor,
		parentCopy: enumIndexGenerationRelation(parent), indexCopy: enumIndexGenerationRelation(descriptor),
		store: store, versions: store.Versions, versionCopy: enumIndexGenerationRows(store.Versions),
		writes: writes, writeCopy: enumIndexGenerationRows(writes), root: root,
		entries: transactionalIndexSnapshot(root), construction: root.Construction,
		candidate: candidate, codec: codec, members: codec.EnumMembers(),
		ends: store.Ends, endCopy: store.Ends.Clone(),
		deletes: s.Transactions.Deletes[parent.Address()], deleteCopy: s.Transactions.Deletes[parent.Address()].Clone(),
	}
}

func (r enumIndexGenerationReceipt) immutable(t *testing.T) {
	t.Helper()
	if !reflect.DeepEqual(enumIndexGenerationJournalSnapshot(r.engine, r.journal), r.journalCopy) || r.private.CaptureDescriptorVersion() != r.descriptorVersion || !reflect.DeepEqual(*r.parent, r.parentCopy) || !reflect.DeepEqual(*r.descriptor, r.indexCopy) {
		t.Fatal("transport mutated retained journal or descriptor backing")
	}
	if !reflect.DeepEqual(r.root.Entries, r.entries) || r.root.Construction != r.construction || !reflect.DeepEqual(r.writes, r.writeCopy) || !reflect.DeepEqual(r.versions, r.versionCopy) {
		t.Fatal("transport mutated retained keys, produced outputs, predecessor history or physical rows")
	}
	if !reflect.DeepEqual(r.ends, r.endCopy) || !reflect.DeepEqual(r.codec.EnumMembers(), r.members) {
		t.Fatal("transport mutated retained deletion root or enum generation")
	}
	if !reflect.DeepEqual(r.deletes, r.deleteCopy) {
		t.Fatal("transport mutated retained private deletion backing")
	}
	j := index.JournalIndexesOrZero(r.journal)
	if j.UniqueRoots[r.descriptor.Address()] != r.root || j.Candidates[r.descriptor.Address()] != r.candidate {
		t.Fatal("transport retargeted retained root authority")
	}
}

func enumIndexGenerationPhysical(t *testing.T, descriptor, parent *catalog.Relation, root *storage.IndexRoot, store *storage.Relation) {
	t.Helper()
	if descriptor.SQLIndex.Parent != parent.Address() || descriptor.Columns[0].Usage != parent.Columns[0].Usage || !index.ConstructionMatches(root, descriptor) {
		t.Fatal("index lost parent/key/construction identity")
	}
	seen := make(map[ids.VersionID]bool)
	for _, entry := range root.Entries {
		if entry.Position < 0 || entry.Position >= len(store.Versions) {
			t.Fatal("index invented a physical position", entry)
		}
		row := store.Versions[entry.Position]
		if row.Row != entry.Row || row.Version != entry.Version || seen[entry.Version] || len(entry.Keys) != 1 || entry.Keys[0].Type() != parent.Columns[0].Usage.Type || !reflect.DeepEqual(entry.Keys[0], row.Values[0]) {
			t.Fatalf("physical index entry position=%d row=%d/%d version=%d/%d duplicate=%t keys=%d",
				entry.Position, entry.Row, row.Row, entry.Version, row.Version, seen[entry.Version], len(entry.Keys))
		}
		seen[entry.Version] = true
	}
}

func enumIndexGenerationOutputs(t *testing.T, receipt enumIndexGenerationReceipt, wantHeads, wantHistory int) {
	t.Helper()
	heads, histories := 0, 0
	check := func(row storage.RowVersion, history bool) bool {
		output, ok := row.IndexOutputs[receipt.descriptor.Address()]
		// Plain nonunique B-tree DML does not require insertion outputs.
		// CREATE INDEX attaches actual outputs to its then-current private
		// heads, but later INSERT/UPDATE versions legitimately have none.
		if !ok {
			return false
		}
		matches := index.OutputMatches(output, receipt.descriptor)
		typed, equal := false, false
		if len(output.Keys) == 1 && len(row.Values) != 0 {
			typed = output.Keys[0].Type() == receipt.codec
			equal = reflect.DeepEqual(output.Keys[0], row.Values[0])
		}
		if output.Index != receipt.descriptor || !output.Member || !matches || !typed || !equal || row.Row == 0 || row.Version == 0 {
			t.Fatalf("produced output row=%d version=%d history=%t deleted=%t owner=%p want=%p matches=%t member=%t keys=%d currentType=%t rowKey=%t",
				row.Row, row.Version, history, index.UniqueRowDeleted(row, receipt.deletes), output.Index, receipt.descriptor, matches, output.Member, len(output.Keys), typed, equal)
		}
		return true
	}
	for _, row := range receipt.writes {
		if check(row, false) {
			heads++
		}
		for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
			if check(prior.Version, true) {
				histories++
			}
		}
	}
	if heads != wantHeads || histories != wantHistory {
		t.Fatal("wrong produced head/history ownership coverage", heads, histories, wantHeads, wantHistory)
	}
}

// Transport preserves actual output presence and authority, including history.
func enumIndexGenerationOutputTransport(t *testing.T, before, after enumIndexGenerationReceipt) {
	t.Helper()
	rows := func(writes []storage.RowVersion) map[ids.VersionID]storage.RowVersion {
		all := make(map[ids.VersionID]storage.RowVersion)
		for _, row := range writes {
			all[row.Version] = row
			for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
				all[prior.Version.Version] = prior.Version
			}
		}
		return all
	}
	targets := rows(after.writes)
	for version, row := range rows(before.writes) {
		target, retained := targets[version]
		if !retained {
			t.Fatalf("output transport lost version=%d", version)
		}
		address := before.descriptor.Address()
		old, existed := row.IndexOutputs[address]
		next, present := target.IndexOutputs[address]
		if existed != present {
			t.Fatalf("output transport changed presence version=%d before=%t after=%t", version, existed, present)
		}
		if !existed {
			continue
		}
		if !index.OutputMatches(old, before.descriptor) {
			if !reflect.DeepEqual(old, next) {
				t.Fatalf("output transport certified/changed stale history version=%d owner=%p next=%p", version, old.Index, next.Index)
			}
			continue
		}
		if next.Index != after.descriptor || !index.OutputMatches(next, after.descriptor) || next.Member != old.Member || len(next.Keys) != len(old.Keys) {
			t.Fatalf("output transport lost current authority version=%d owner=%p want=%p", version, next.Index, after.descriptor)
		}
		for key, value := range old.Keys {
			member, enum := value.EnumMemberID()
			mapped, mappedEnum := next.Keys[key].EnumMemberID()
			if enum && (!mappedEnum || mapped != member || next.Keys[key].Type() != after.codec) || !enum && !reflect.DeepEqual(value, next.Keys[key]) {
				t.Fatalf("output transport changed produced key version=%d key=%d", version, key)
			}
		}
	}
}

func enumIndexGenerationSamePhysical(t *testing.T, before, after []storage.RowVersion) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatal("generation/layout transport changed physical length")
	}
	valuesUnchanged := func(old, next storage.RowVersion) {
		if len(next.Values) < len(old.Values) {
			t.Fatal("transport removed a retained physical value")
		}
		for slot, value := range old.Values {
			current := next.Values[slot]
			if member, enum := value.EnumMemberID(); enum {
				mapped, present := current.EnumMemberID()
				if !present || mapped != member || value.Type().ID() != current.Type().ID() {
					t.Fatal("transport changed enum member/type identity", slot)
				}
			} else if !reflect.DeepEqual(value, current) {
				t.Fatal("transport changed an unrelated physical value", slot)
			}
		}
	}
	for i, row := range before {
		next := after[i]
		if row.Row != next.Row || row.Version != next.Version || row.Base != next.Base || row.Born != next.Born || row.InsertFullXID != next.InsertFullXID || row.DeleteFullXID != next.DeleteFullXID {
			t.Fatalf("transport changed physical identity position=%d row=%d/%d version=%d/%d base=%d/%d born=%d/%d xmin=%d/%d xmax=%d/%d",
				i, row.Row, next.Row, row.Version, next.Version, row.Base, next.Base, row.Born, next.Born, row.InsertFullXID, next.InsertFullXID, row.DeleteFullXID, next.DeleteFullXID)
		}
		valuesUnchanged(row, next)
		oldHistory, nextHistory := row.PrivateHistory, next.PrivateHistory
		for oldHistory != nil && nextHistory != nil {
			if oldHistory.Version.Row != nextHistory.Version.Row || oldHistory.Version.Version != nextHistory.Version.Version || oldHistory.End != nextHistory.End || oldHistory.EndFullXID != nextHistory.EndFullXID {
				t.Fatal("transport reminted predecessor identity/end provenance")
			}
			valuesUnchanged(oldHistory.Version, nextHistory.Version)
			oldHistory, nextHistory = oldHistory.Next, nextHistory.Next
		}
		if oldHistory != nil || nextHistory != nil {
			t.Fatal("transport lost physical predecessor history")
		}
	}
}

func enumIndexGenerationCreatedAfter(before, after []storage.RowVersion) []storage.RowVersion {
	known := make(map[ids.VersionID]bool)
	for _, row := range before {
		known[row.Version] = true
		for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
			known[prior.Version.Version] = true
		}
	}
	var created []storage.RowVersion
	add := func(row storage.RowVersion) {
		if !known[row.Version] {
			created = append(created, row)
			known[row.Version] = true
		}
	}
	for _, row := range after {
		add(row)
		for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
			add(prior.Version)
		}
	}
	return created
}

// Retiring a private heap must not move its slots into any surviving authority.
func enumIndexGenerationRetiredAbsent(t *testing.T, e *core.Engine, s *session.Session, owner enumIndexGenerationReceipt, created []storage.RowVersion) {
	t.Helper()
	versions := make(map[ids.VersionID]bool, len(created))
	address := owner.parent.Address()
	journal := s.Transactions.Catalog
	for _, row := range created {
		if row.Version == 0 || row.Born != 0 || row.InsertFullXID == 0 || versions[row.Version] {
			t.Fatal("retired witness lost genuine distinct private version provenance", row.Version)
		}
		versions[row.Version] = true
		for _, store := range []*storage.Relation{e.Storage.Relations[address], e.PrivateStorageLocked(journal, address)} {
			if store != nil {
				if _, _, found := store.PhysicalVersion(row.Version, -1); found {
					t.Fatal("rollback rebound a retired slot into surviving heap storage", row.Version)
				}
			}
		}
	}
	for _, row := range enumIndexGenerationCreatedAfter(nil, s.Transactions.Writes[address]) {
		if versions[row.Version] {
			t.Fatal("rollback retained a retired slot in current writes/history", row.Version)
		}
	}
	var private index.Journal
	if journal != nil {
		private = index.JournalIndexesOrZero(journal)
		for _, batch := range journal.DiscardedHeaps {
			for prior := batch.Versions; prior != nil; prior = prior.Next {
				if versions[prior.Version.Version] {
					t.Fatal("rollback rebound a retired slot into deferred heap debris", prior.Version.Version)
				}
			}
		}
	}
	for _, root := range []*storage.IndexRoot{e.Storage.IndexRoots[owner.descriptor.Address()], private.UniqueRoots[owner.descriptor.Address()]} {
		if root != nil {
			for _, entry := range root.Entries {
				if versions[entry.Version] {
					t.Fatal("rollback retained a retired slot in current index authority", entry.Version)
				}
			}
		}
	}
}
