package core_test

import (
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestEnumIndexLayoutGenerationCaptureFences(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	tx := enginetest.TransactionBegin(t, s)
	for _, q := range []string{"CREATE TYPE enum_generation_mood AS ENUM('pending')", "CREATE TYPE enum_generation_foreign AS ENUM('pending','inhibited')", "CREATE TABLE enum_generation_heap(status enum_generation_mood,id INT)", "INSERT INTO enum_generation_heap VALUES('pending',1)", "CREATE INDEX enum_generation_idx ON enum_generation_heap(status)", "CREATE TABLE enum_generation_sibling(status enum_generation_foreign)", "CREATE INDEX enum_generation_sibling_idx ON enum_generation_sibling(status)"} {
		enginetest.SessionSQL(t, s, q)
	}
	original := enumIndexGenerationCapture(t, e, s)
	enginetest.SessionSQL(t, s, "ALTER TYPE enum_generation_mood ADD VALUE 'inhibited'")
	source := enumIndexGenerationCapture(t, e, s)
	enginetest.SessionSQL(t, s, "ALTER TABLE enum_generation_heap ADD COLUMN extra TEXT")
	target := enumIndexGenerationCapture(t, e, s)
	view := e.PrivateCatalogLocked(target.journal)
	siblingRef := enginetest.CatalogRef(t, e, view, "enum_generation_sibling_idx")
	sibling := view.MustRelation(siblingRef.Address)
	siblingParent := view.MustRelation(sibling.SQLIndex.Parent)
	siblingRoot := index.JournalIndexesOrZero(target.journal).UniqueRoots[siblingRef.Address]
	address := source.descriptor.Address()
	lineage := index.LayoutLineage{Source: source.descriptor, SourceParent: source.parent, Target: target.descriptor, TargetParent: target.parent}
	base := index.LayoutCaptureInput{
		Layouts: map[ids.ObjectAddress]index.LayoutLineage{address: lineage}, Target: view, Parent: target.parent,
		Committed: e.Catalog.State, CommittedRoots: e.Storage.IndexRoots, CommittedStore: e.Storage.Relations[source.parent.Address()],
		Changes: source.journal, SourceStore: source.store, TargetStore: target.store,
	}
	capture := func(in index.LayoutCaptureInput) ([]index.LayoutCapture, error) {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		return index.CaptureLayouts(in)
	}
	if source.codec == original.codec || source.codec.ID() != original.codec.ID() || index.SameIndexKeySemantics(source.descriptor, original.descriptor) {
		t.Fatal("positive fixture did not execute a real certified enum successor")
	}
	if captures, err := capture(base); err != nil || len(captures) != 1 || captures[0].Root != source.root || captures[0].Lineage.Source != source.descriptor || captures[0].Lineage.Target != target.descriptor {
		t.Fatal("real certified successor/layout lineage was rejected", captures, err)
	}
	staged := catalog.NewCandidate(view)
	if err := staged.Replace(target.descriptor); err != nil {
		t.Fatal(err)
	}
	if !index.CandidatesMatch(map[ids.ObjectAddress]index.RootCandidate{address: target.candidate}, &staged, e.Catalog.State, func(parent ids.ObjectAddress) *storage.Relation {
		if parent != target.parent.Address() {
			t.Fatal("candidate requested another parent", parent)
		}
		return target.store
	}) {
		t.Fatal("current genuine successor failed final exact candidate fence")
	}
	for _, substitution := range []string{"missing-candidate", "missing-root", "nil-candidate-root", "candidate-root-mismatch", "root-candidate-mismatch", "foreign-equal-content-storage", "stale-generation", "stale-ends", "stale-length", "wrong-index", "wrong-key", "wrong-parent", "wrong-target", "uncertified-generation", "foreign-enum", "same-oid-uncertified-enum"} {
		t.Run(substitution, func(t *testing.T) {
			in := base
			in.Layouts = maps.Clone(base.Layouts)
			in.Changes = source.journal.Clone()
			j := index.JournalIndexes(in.Changes)
			candidate := j.Candidates[address]
			edge := lineage
			switch substitution {
			case "missing-candidate":
				delete(j.Candidates, address)
			case "missing-root":
				delete(j.UniqueRoots, address)
			case "nil-candidate-root":
				candidate.Root = nil
			case "candidate-root-mismatch":
				candidate.Root = siblingRoot
			case "root-candidate-mismatch":
				j.UniqueRoots[address] = siblingRoot
			case "foreign-equal-content-storage":
				foreign := *source.store
				foreign.IndexSource = nil
				if !reflect.DeepEqual(foreign, *source.store) {
					t.Fatal("fixture did not substitute equal-content foreign storage")
				}
				in.SourceStore = &foreign
			case "stale-generation":
				candidate.Generation++
			case "stale-ends":
				candidate.Ends = &storage.DeletionRoot{}
			case "stale-length":
				candidate.Length++
			case "wrong-index":
				candidate.Index = sibling
			case "wrong-key":
				wrong := enumIndexGenerationRelation(source.descriptor)
				wrong.SQLIndex.Keys[0].Attribute = source.parent.Attributes[1]
				candidate.Index = &wrong
			case "wrong-parent":
				edge.SourceParent = siblingParent
			case "wrong-target":
				edge.Target = sibling
			case "uncertified-generation":
				edge.Source, edge.SourceParent = original.descriptor, original.parent
				in.SourceStore = original.store
			case "foreign-enum", "same-oid-uncertified-enum":
				wrong := enumIndexGenerationRelation(source.parent)
				codec := siblingParent.Columns[0].Usage.Type
				if substitution == "same-oid-uncertified-enum" {
					var err error
					codec, err = sqltype.EnumWithMembers(source.codec.ID(), source.codec.Name(), source.codec.SQLName(), source.codec.EnumMembers())
					if err != nil {
						t.Fatal(err)
					}
					if codec == source.codec || codec.ID() != source.codec.ID() {
						t.Fatal("fixture did not retain a distinct same-ID enum generation")
					}
				}
				wrong.Columns[0].Usage.Type = codec
				edge.SourceParent = &wrong
			}
			if substitution != "missing-candidate" {
				j.Candidates[address] = candidate
			}
			in.Layouts[address] = edge
			journalBefore := in.Changes.Clone()
			published := indexMaintenanceCapture(e)
			frontier, row, version := e.Visibility.CommitSeq(), e.Storage.RowFrontier(), e.Storage.VersionFrontier()
			captures, err := capture(in)
			if !errors.Is(err, ids.ErrCatalogReplaced) || captures != nil {
				t.Fatal("substituted source acquired layout authority", captures, err)
			}
			if !reflect.DeepEqual(in.Changes.Clone(), journalBefore) || s.Transactions.Catalog != target.journal || e.Visibility.CommitSeq() != frontier || e.Storage.RowFrontier() != row || e.Storage.VersionFrontier() != version {
				t.Fatal("rejected capture mutated destination, journal or physical frontiers")
			}
			indexMaintenanceUnchanged(t, e, published)
			original.immutable(t)
			source.immutable(t)
			target.immutable(t)
		})
	}
	// Engine ownership belongs to the catalog stage, not to equal metadata or
	// numeric OIDs. Keep its actual admitted source and candidate intact.
	foreign := engine.New()
	stage := e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	stage.Indexes().Candidates = map[ids.ObjectAddress]index.RootCandidate{address: target.candidate}
	beforeSource, beforeForeign := indexMaintenanceCapture(e), indexMaintenanceCapture(foreign)
	if _, err := foreign.ValidateCatalogStage(t.Context(), stage); err == nil {
		t.Fatal("foreign engine admitted a genuine source-owned stage", err)
	}
	indexMaintenanceUnchanged(t, e, beforeSource)
	indexMaintenanceUnchanged(t, foreign, beforeForeign)
	source.immutable(t)
	target.immutable(t)
	enginetest.TransactionEnd(t, s, tx, false)
	enginetest.LockEmpty(t, e)
}

func TestEnumIndexLayoutGenerationFinalCandidateFences(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	tx := enginetest.TransactionBegin(t, s)
	for _, q := range []string{"CREATE TYPE enum_generation_mood AS ENUM('pending')", "CREATE TABLE enum_generation_heap(status enum_generation_mood,id INT)", "INSERT INTO enum_generation_heap VALUES('pending',1)", "CREATE INDEX enum_generation_idx ON enum_generation_heap(status)"} {
		enginetest.SessionSQL(t, s, q)
	}
	predecessor := enumIndexGenerationCapture(t, e, s)
	enginetest.SessionSQL(t, s, "ALTER TYPE enum_generation_mood ADD VALUE 'inhibited'")
	enginetest.SessionSQL(t, s, "ALTER TABLE enum_generation_heap ADD COLUMN extra TEXT")
	receipt := enumIndexGenerationCapture(t, e, s)
	view := e.PrivateCatalogLocked(receipt.journal)
	for _, substitution := range []string{"none", "foreign-storage", "generation", "ends", "length", "predecessor-descriptor", "wrong-parent", "foreign-engine"} {
		t.Run(substitution, func(t *testing.T) {
			candidate := receipt.candidate
			store := receipt.store
			staged := catalog.NewCandidate(view)
			if err := staged.Replace(receipt.descriptor); err != nil {
				t.Fatal(err)
			}
			switch substitution {
			case "foreign-storage":
				copy := *store
				copy.IndexSource = nil
				store = &copy
			case "generation":
				candidate.Generation++
			case "ends":
				candidate.Ends = &storage.DeletionRoot{}
			case "length":
				candidate.Length++
			case "predecessor-descriptor":
				candidate.Index = predecessor.descriptor
			case "wrong-parent":
				wrong := enumIndexGenerationRelation(receipt.descriptor)
				definition := *wrong.SQLIndex
				definition.Parent = ids.ObjectAddress{}
				wrong.SQLIndex = &definition
				candidate.Index = &wrong
			case "foreign-engine":
				other := engine.New()
				owner := enginetest.StartSession(t, other, nil)
				enginetest.TransactionBegin(t, owner)
				for _, q := range []string{"CREATE TYPE enum_generation_mood AS ENUM('pending')", "CREATE TABLE enum_generation_heap(status enum_generation_mood,id INT)", "INSERT INTO enum_generation_heap VALUES('pending',1)", "CREATE INDEX enum_generation_idx ON enum_generation_heap(status)", "ALTER TYPE enum_generation_mood ADD VALUE 'inhibited'", "ALTER TABLE enum_generation_heap ADD COLUMN extra TEXT"} {
					enginetest.SessionSQL(t, owner, q)
				}
				candidate = enumIndexGenerationCapture(t, other, owner).candidate
			}
			before := indexMaintenanceCapture(e)
			matched := index.CandidatesMatch(map[ids.ObjectAddress]index.RootCandidate{receipt.descriptor.Address(): candidate}, &staged, e.Catalog.State, func(ids.ObjectAddress) *storage.Relation { return store })
			if matched != (substitution == "none") {
				t.Fatal("final exact descriptor/storage fence accepted substitution or rejected real successor", matched)
			}
			indexMaintenanceUnchanged(t, e, before)
			receipt.immutable(t)
			predecessor.immutable(t)
		})
	}
	enginetest.TransactionEnd(t, s, tx, false)
	if s.TransactionState() != session.TxIdle {
		t.Fatal("final-candidate fixture retained an owner")
	}
	enginetest.LockEmpty(t, e)
}
