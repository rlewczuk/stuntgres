package reference_test

import "testing"

func TestReferenceViewJoinedDefinition(t *testing.T) {
	for _, leading := range []bool{true, false} {
		name := "root_leading"
		if !leading {
			name = "legacy_compact"
		}
		t.Run(name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					for _, s := range referenceViewJoinSeed(leading) {
						run(s)
					}
					for phase := range 2 {
						if phase == 1 {
							for _, name := range referenceViewJoinNames(leading) {
								referenceViewCopy(t, p, b, name, name+"_copy")
							}
						}
						for _, name := range referenceViewJoinNames(leading) {
							suffixes := []string{""}
							if phase > 0 {
								suffixes = append(suffixes, "_copy")
							}
							for _, suffix := range suffixes {
								s := referenceViewJoinRead(name, suffix, leading)
								run(s)
								run(referencePrepared(s))
							}
						}
					}
					run(referenceExec("ALTER TABLE renamed_a ADD COLUMN y TEXT DEFAULT 'different'", 0))
					referenceViewCopy(t, p, b, "natural_view", "frozen_keys")
					s := referenceSelect("SELECT id,x,y FROM frozen_keys ORDER BY id", []string{"id", "x", "y"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{int64(1), "a", "u"}, {int64(1), "a", "v"}, {int64(3), "c", nil}})
					s.bag = true
					run(s)
					run(referencePrepared(s))
				})
			}
		})
	}
}

func TestReferenceViewJoinedNamespaceReplay(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, s := range referenceViewNamespaceSeed() {
				run(s)
			}
			names := []string{"self_sorted", "converted_sorted", "alias_sorted"}
			for phase := range 2 {
				if phase == 1 {
					for _, name := range names {
						referenceViewCopy(t, p, b, name, name+"_copy")
					}
				}
				for _, name := range names {
					suffixes := []string{""}
					if phase > 0 {
						suffixes = append(suffixes, "_copy")
					}
					for _, suffix := range suffixes {
						s := referenceViewNamespaceRead(name, suffix)
						run(s)
						run(referencePrepared(s))
					}
				}
			}
		})
	}
	dump := referenceViewDump(t, p)
	t.Run("native_destination", func(t *testing.T) {
		destination := newReferenceFixture(t, false, true)
		referenceViewRestore(t, destination, dump)
		for _, name := range []string{"self_sorted", "converted_sorted", "alias_sorted"} {
			for _, suffix := range []string{"", "_copy"} {
				s := referenceViewNamespaceRead(name, suffix)
				referenceRunStep(t, destination.ctx, destination.memory, destination.memoryRecord, s)
				referenceRunStep(t, destination.ctx, destination.memory, destination.memoryRecord, referencePrepared(s))
			}
		}
	})
}
