package session

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestJournalCaptureObserve(t *testing.T) {
	schema := journalTestSchema(20001, "s")
	table := journalTestRelation(20010, "t", schema.Address())
	committed := journalTestState(schema, table)
	c := NewJournal()
	capture := JournalCapture{Journal: c, Committed: committed, Current: committed}
	if err := capture.Observe(table.Address(), table); err != nil || c.Originals[table.Address()] != table {
		t.Fatal("an observation did not record the committed original", err)
	}
	c.Originals[schema.Address()] = nil
	if err := capture.Observe(schema.Address(), schema); err != nil || c.Originals[schema.Address()] != nil {
		t.Fatal("an observation replaced the journal's own original", err)
	}
	stale := *table
	if err := capture.Observe(table.Address(), &stale); !isConflict(err) {
		t.Fatalf("stale observation: got %v, want a conflict", err)
	}

	database := schema.Address()
	if err := capture.ObserveDatabase(database, table, schema); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("changed authority: got %v, want a replaced catalog", err)
	}
	c.Originals[database] = table
	if err := capture.ObserveDatabase(database, schema, schema); !isConflict(err) {
		t.Fatalf("other original: got %v, want a conflict", err)
	}
	delete(c.Originals, database)
	if err := capture.ObserveDatabase(database, schema, schema); err != nil || c.Originals[database] != schema {
		t.Fatal("the database predecessor was not recorded as the original", err)
	}

	t.Run("temporary_namespace_predecessor", func(t *testing.T) {
		for _, change := range []string{
			"none", "missing_original", "nonnil_original", "descriptor_copy", "descriptor_type", "base_copy",
			"ordinary_namespace", "foreign_session", "missing_source", "foreign_domain", "detached_source",
			"missing_name", "wrong_name", "base_name", "current_object", "committed_object", "current_name", "committed_name",
			"appeared_label", "replaced_label", "pending", "pending_copy", "base_label_copy", "missing_pending",
			"missing_label_original", "nonnil_label_original", "committed_label", "nonzero_subid",
		} {
			t.Run(change, func(t *testing.T) {
				domain := &ids.Domain{}
				owner := &catalog.Owner{Domain: domain}
				source := domain.Session(1)
				ns := journalTestSchema(20001, "pg_temp_1")
				ns.Temporary = source
				committed := journalTestState()
				journal := NewJournal()
				journal.RecordDescriptor(committed, ns.Address(), ns)
				key := catalog.SecurityLabelKey{Object: ns.Address(), Provider: "selinux"}
				switch change {
				case "pending", "pending_copy", "base_label_copy", "missing_pending", "missing_label_original", "nonnil_label_original", "replaced_label":
					journal.SecurityLabels = map[catalog.SecurityLabelKey]*catalog.SecurityLabelRecord{key: {Label: "P"}}
					journal.LabelOriginals = map[catalog.SecurityLabelKey]*catalog.SecurityLabelRecord{key: nil}
					journal.LabelReads = map[catalog.SecurityLabelKey]*catalog.SecurityLabelRecord{key: nil}
				}
				base := journal.PrivateRead(owner, committed)
				candidate := catalog.NewCandidate(base)
				table := journalTestRelation(20010, "t", ns.Address())
				if err := candidate.Create(table); err != nil {
					t.Fatal(err)
				}
				candidate.ObserveSecurityLabel(key)
				candidate.RecordSecurityLabel(catalog.SecurityLabelKey{Object: table.Address(), Provider: "selinux"}, &catalog.SecurityLabelRecord{Label: "P"})
				capture := JournalCapture{Journal: journal, Candidate: &candidate, Committed: committed, Current: committed, SourceSession: source}
				name := catalog.DescriptorNameKey(ns)
				switch change {
				case "missing_original":
					delete(journal.Originals, ns.Address())
				case "nonnil_original":
					journal.Originals[ns.Address()] = ns
				case "descriptor_copy":
					copy := *ns
					journal.Descriptors[ns.Address()] = &copy
					journal.InvalidatePrivateRead()
				case "descriptor_type":
					wrong := journalTestRelation(ns.Address().ObjectID, ns.Name(), ids.ObjectAddress{})
					wrong.ObjectAddress = ns.Address()
					journal.Descriptors[ns.Address()] = wrong
					journal.InvalidatePrivateRead()
				case "base_copy":
					copy := *ns
					candidate.Base = catalog.NewRead(owner, journalTestState(&copy))
				case "ordinary_namespace":
					ns.Temporary = ids.SessionToken{}
				case "foreign_session":
					ns.Temporary = domain.Session(2)
				case "missing_source":
					capture.SourceSession = ids.SessionToken{}
				case "foreign_domain":
					capture.SourceSession = (&ids.Domain{}).Session(1)
					ns.Temporary = capture.SourceSession
				case "detached_source":
					capture.SourceSession = domain.DetachedSession(1)
					ns.Temporary = capture.SourceSession
				case "missing_name":
					delete(journal.Names, name)
				case "wrong_name":
					journal.Names[name] = ns.Address()
				case "base_name":
					state := catalog.CloneState(base.State())
					state.BindName(name, table.Address())
					candidate.Base = catalog.NewRead(owner, state)
				case "current_object", "committed_object":
					copy := *ns
					state := journalTestState()
					state.SetDescriptor(ns.Address(), &copy)
					if change == "current_object" {
						capture.Current = state
					} else {
						capture.Committed = state
					}
				case "current_name", "committed_name":
					state := journalTestState()
					state.BindName(name, table.Address())
					if change == "current_name" {
						capture.Current = state
					} else {
						capture.Committed = state
					}
				case "appeared_label", "replaced_label":
					state := catalog.CloneState(committed)
					state.SetSecurityLabel(key, &catalog.SecurityLabelRecord{Label: "P"})
					capture.Current = state
				case "pending_copy":
					copy := *journal.SecurityLabels[key]
					journal.SecurityLabels[key] = &copy
					journal.InvalidatePrivateRead()
				case "base_label_copy":
					state := catalog.CloneState(base.State())
					copy := *state.SecurityLabel(key)
					state.SetSecurityLabel(key, &copy)
					candidate.Base = catalog.NewRead(owner, state)
				case "missing_pending":
					delete(journal.SecurityLabels, key)
					journal.InvalidatePrivateRead()
				case "missing_label_original":
					delete(journal.LabelOriginals, key)
				case "nonnil_label_original":
					journal.LabelOriginals[key] = &catalog.SecurityLabelRecord{Label: "P"}
				case "committed_label":
					state := catalog.CloneState(committed)
					state.SetSecurityLabel(key, &catalog.SecurityLabelRecord{Label: "P"})
					capture.Committed = state
				case "nonzero_subid":
					sub := key
					sub.Object.SubID = 1
					candidate.ObserveSecurityLabel(sub)
				}
				currentCount, committedCount := capture.Current.DescriptorCount(), capture.Committed.DescriptorCount()
				currentNames, committedNames := capture.Current.NameCount(), capture.Committed.NameCount()
				currentLabels, committedLabels := capture.Current.SecurityLabelRoot(), capture.Committed.SecurityLabelRoot()
				if change == "none" || change == "pending" {
					if !isConflict(candidate.DefaultPreflight(capture.Current)) {
						t.Fatal("ordinary preflight adopted prospective namespace authority")
					}
					if err := capture.DefaultPreflight(); err != nil {
						t.Fatal("exact source intent failed preflight", err)
					}
					if err := capture.ObserveCandidate(); err != nil {
						t.Fatal("exact source intent failed shared namespace observer", err)
					}
					journal.RecordCandidateSecurityLabels(committed, &candidate)
					original, exists := journal.Originals[ns.Address()]
					label, observed := journal.LabelReads[key]
					if !exists || original != nil || journal.Names[name] != (ids.ObjectAddress{}) || !observed || label != nil {
						t.Fatal("capture refreshed original object/name/label absence fences")
					}
					peer := catalog.CloneState(committed)
					peer.SetSecurityLabel(key, &catalog.SecurityLabelRecord{Label: "P"})
					if catalog.SecurityLabelsUnchanged(peer, journal.LabelReads) {
						t.Fatal("peer label appearance passed the retained final read fence")
					}
					if change == "pending" {
						original, exists := journal.LabelOriginals[key]
						if !exists || original != nil || journal.SecurityLabels[key] != base.State().SecurityLabel(key) {
							t.Fatal("capture replaced original absence or lost exact own-pending label")
						}
					}
				} else {
					if err := capture.DefaultPreflight(); !isConflict(err) {
						t.Fatal("invalid source fact passed preflight", err)
					}
					switch change {
					case "appeared_label", "replaced_label", "pending_copy", "base_label_copy", "missing_pending", "missing_label_original", "nonnil_label_original", "committed_label", "nonzero_subid":
						// Record provenance is checked by source-purpose preflight;
						// the shared observer separately owns descriptor/name intent.
					default:
						if err := capture.ObserveCandidate(); !isConflict(err) {
							t.Fatal("invalid source fact passed shared namespace observer", err)
						}
					}
				}
				if capture.Current.DescriptorCount() != currentCount || capture.Committed.DescriptorCount() != committedCount ||
					capture.Current.NameCount() != currentNames || capture.Committed.NameCount() != committedNames ||
					capture.Current.SecurityLabelRoot() != currentLabels || capture.Committed.SecurityLabelRoot() != committedLabels ||
					capture.Current.Get(table.Address()) != nil || capture.Committed.Get(table.Address()) != nil ||
					capture.Current.NameAddress(catalog.DescriptorNameKey(table)) != (ids.ObjectAddress{}) ||
					capture.Committed.SecurityLabel(catalog.SecurityLabelKey{Object: table.Address(), Provider: "selinux"}) != nil ||
					committed.DescriptorCount() != 0 || committed.NameCount() != 0 || committed.SecurityLabelCount() != 0 {
					t.Fatal("capture published partial catalog/name/label data or mutated the retained predecessor")
				}
			})
		}
	})

	t.Run("ordinary_label_descriptor_and_removal_fences", func(t *testing.T) {
		domain := &ids.Domain{}
		owner := &catalog.Owner{Domain: domain}
		schema := journalTestSchema(20001, "ordinary")
		table := journalTestRelation(20010, "t", schema.Address())
		base := journalTestState(schema, table)
		candidate := catalog.NewCandidate(catalog.NewRead(owner, base))
		key := catalog.SecurityLabelKey{Object: table.Address(), Provider: "selinux"}
		candidate.ObserveSecurityLabel(key)
		replaced := catalog.CloneState(base)
		copy := *table
		replaced.SetDescriptor(table.Address(), &copy)
		capture := JournalCapture{Journal: NewJournal(), Candidate: &candidate, Committed: base, Current: replaced, SourceSession: domain.Session(1)}
		if err := capture.DefaultPreflight(); !isConflict(err) {
			t.Fatal("nonnamespace label owner mismatch was waived", err)
		}
		candidate.RecordRemoval(domain.Ref(base.Epoch(), table.Address()), table)
		appeared := catalog.CloneState(base)
		other := key
		other.Object.SubID = 1
		appeared.SetSecurityLabel(other, &catalog.SecurityLabelRecord{Label: "P"})
		capture.Current = appeared
		if err := capture.DefaultPreflight(); !isConflict(err) {
			t.Fatal("unseen label on removal was waived", err)
		}
	})
}

func TestJournalMergeCandidate(t *testing.T) {
	owner := &catalog.Owner{}
	schema := journalTestSchema(20001, "s")
	table := journalTestRelation(20010, "t", schema.Address())
	committed := journalTestState(schema, table)
	c := NewJournal()
	private := *table
	private.ObjectName = "p"
	c.RecordDescriptor(committed, table.Address(), &private)
	observed := c.PrivateRead(owner, committed).State()
	candidate := catalog.NewCandidate(catalog.NewRead(owner, observed))
	next := private
	next.ObjectName = "q"
	if err := candidate.Replace(&next); err != nil {
		t.Fatal(err)
	}

	moved := catalog.CloneState(committed)
	concurrent := *table
	moved.SetDescriptor(table.Address(), &concurrent)
	if err := c.MergeCandidate(moved, observed, &candidate); !isConflict(err) || c.Descriptors[table.Address()] != &private {
		t.Fatal("a concurrent commit did not fail the merge before it recorded anything", err)
	}
	if err := c.MergeCandidate(committed, observed, &candidate); err != nil {
		t.Fatal(err)
	}
	if c.Descriptors[table.Address()] != &next || c.Originals[table.Address()] != table {
		t.Fatal("the merge did not record the change against the journal's original")
	}
}

func TestJournalCaptureFenceNamesWithoutIndexes(t *testing.T) {
	owner := &catalog.Owner{}
	schema := journalTestSchema(20001, "s")
	table := journalTestRelation(20010, "before", schema.Address())
	committed := journalTestState(schema, table)
	candidate := catalog.NewCandidate(catalog.NewRead(owner, committed))
	next := *table
	next.ObjectName = "after"
	if err := candidate.Replace(&next); err != nil {
		t.Fatal(err)
	}
	journal := NewJournal()
	capture := JournalCapture{Journal: journal, Candidate: &candidate, Committed: committed, Current: committed}
	if err := capture.FenceNames(nil); err != nil {
		t.Fatal("a name-only stage needs no index-parent iterator", err)
	}
	before, after := catalog.DescriptorNameKey(table), catalog.DescriptorNameKey(&next)
	if len(journal.Names) != 2 || journal.Names[before] != table.Address() || journal.Names[after] != (ids.ObjectAddress{}) {
		t.Fatal("omitting index parents lost committed source/absence fences", journal.Names)
	}
	changed := catalog.CloneState(committed)
	changed.BindName(after, table.Address())
	capture.Current = changed
	if err := capture.FenceNames(nil); !isConflict(err) {
		t.Fatal("omitting index parents bypassed actual name conflict", err)
	}
}
