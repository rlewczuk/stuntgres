package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewJoinedDefinition(t *testing.T) {
	for _, leading := range []bool{true, false} {
		name := "root_leading"
		if !leading {
			name = "legacy_compact"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, s := range referenceViewJoinSeed(leading) {
						run(s)
					}
					for phase := range 2 {
						if phase == 1 {
							for _, name := range referenceViewJoinNames(leading) {
								fixture.ViewCopy(t, p, b, name, name+"_copy")
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
								run(fixture.Prepared(s))
							}
						}
					}
					run(fixture.Exec("ALTER TABLE renamed_a ADD COLUMN y TEXT DEFAULT 'different'", 0))
					fixture.ViewCopy(t, p, b, "natural_view", "frozen_keys")
					s := fixture.Select("SELECT id,x,y FROM frozen_keys ORDER BY id", []string{"id", "x", "y"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{int64(1), "a", "u"}, {int64(1), "a", "v"}, {int64(3), "c", nil}})
					s.Bag = true
					run(s)
					run(fixture.Prepared(s))
				})
			}
		})
	}
}

func TestReferenceViewJoinedNamespaceReplay(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range referenceViewNamespaceSeed() {
				run(s)
			}
			names := []string{"self_sorted", "converted_sorted", "alias_sorted"}
			for phase := range 2 {
				if phase == 1 {
					for _, name := range names {
						fixture.ViewCopy(t, p, b, name, name+"_copy")
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
						run(fixture.Prepared(s))
					}
				}
			}
		})
	}
	dump := fixture.ViewDump(t, p)
	t.Run("native_destination", func(t *testing.T) {
		destination := fixture.NewFixture(t, false, true)
		fixture.ViewRestore(t, destination, dump)
		for _, name := range []string{"self_sorted", "converted_sorted", "alias_sorted"} {
			for _, suffix := range []string{"", "_copy"} {
				s := referenceViewNamespaceRead(name, suffix)
				fixture.RunStep(t, destination.Ctx, destination.Memory, destination.MemoryRecord, s)
				fixture.RunStep(t, destination.Ctx, destination.Memory, destination.MemoryRecord, fixture.Prepared(s))
			}
		}
	})
}
