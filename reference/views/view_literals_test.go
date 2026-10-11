package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewAnalyzedLiteralPersistence(t *testing.T) {
	for _, seed := range referenceViewAnalyzedSeeds() {
		t.Run(seed.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, s := range seed.setup {
						run(s)
					}
					for _, v := range seed.views {
						run(fixture.Exec("CREATE VIEW "+v.name+"("+v.projection+") AS "+v.body, 0))
					}
					for phase := range 3 {
						if phase == 1 {
							for _, v := range seed.views {
								fixture.ViewCopy(t, p, b, v.name, v.name+"_copy")
							}
						}
						if phase == 2 && !b.PG {
							run(fixture.Exec("CREATE TEMPORARY SEQUENCE retained", 0))
							fixture.ViewRestore(t, p, fixture.ViewDump(t, p))
						}
						for _, v := range seed.views {
							want := v.native
							if b.PG {
								want = v.postgres
							}
							suffixes := []string{""}
							if phase > 0 {
								suffixes = append(suffixes, "_copy")
							}
							for _, suffix := range suffixes {
								s := fixture.Select("SELECT "+v.projection+" FROM "+v.name+suffix, v.columns, v.types, want)
								run(s)
								run(fixture.Prepared(s))
							}
						}
					}
				})
			}
		})
	}
}

func TestReferenceViewLiteralOccurrencesBeforeGrouping(t *testing.T) {
	for _, seed := range referenceViewGroupingSeeds() {
		t.Run(seed.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, s := range seed.setup {
						run(s)
					}
					for _, v := range seed.views {
						run(fixture.Exec("CREATE VIEW "+v.name+"("+v.projection+") AS "+v.body, 0))
					}
					for phase := range 3 {
						if phase == 1 {
							for _, v := range seed.views {
								fixture.ViewCopy(t, p, b, v.name, v.name+"_copy")
							}
						}
						if phase == 2 && !b.PG {
							run(fixture.Exec("CREATE TEMPORARY SEQUENCE retained", 0))
							fixture.ViewRestore(t, p, fixture.ViewDump(t, p))
						}
						for _, v := range seed.views {
							want := v.native
							if b.PG {
								want = v.postgres
							}
							suffixes := []string{""}
							if phase > 0 {
								suffixes = append(suffixes, "_copy")
							}
							for _, suffix := range suffixes {
								s := fixture.Select("SELECT "+v.projection+" FROM "+v.name+suffix, v.columns, v.types, want)
								run(s)
								run(fixture.Prepared(s))
							}
						}
					}
				})
			}
		})
	}
}

func TestReferenceViewTemporalEndpointAndTypedNullPersistence(t *testing.T) {
	for _, seed := range referenceViewEndpointSeeds() {
		t.Run(seed.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, s := range seed.setup {
						run(s)
					}
					for _, v := range seed.views {
						run(fixture.Exec("CREATE VIEW "+v.name+"("+v.projection+") AS "+v.body, 0))
					}
					for phase := range 3 {
						if phase == 1 {
							for _, v := range seed.views {
								fixture.ViewCopy(t, p, b, v.name, v.name+"_copy")
							}
						}
						if phase == 2 && !b.PG {
							run(fixture.Exec("CREATE TEMPORARY SEQUENCE retained", 0))
							fixture.ViewRestore(t, p, fixture.ViewDump(t, p))
						}
						for _, v := range seed.views {
							want := v.native
							if b.PG {
								want = v.postgres
							}
							suffixes := []string{""}
							if phase > 0 {
								suffixes = append(suffixes, "_copy")
							}
							for _, suffix := range suffixes {
								s := fixture.Select("SELECT "+v.projection+" FROM "+v.name+suffix, v.columns, v.types, want)
								run(s)
								run(fixture.Prepared(s))
							}
						}
					}
				})
			}
		})
	}
}

func TestReferenceViewContextualScalarLiteralNativeRoundTrip(t *testing.T) {
	for _, seed := range referenceViewScalarSeeds() {
		t.Run(seed.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, s := range seed.setup {
						run(s)
					}
					for _, v := range seed.views {
						run(fixture.Exec("CREATE VIEW "+v.name+"("+v.projection+") AS "+v.body, 0))
					}
					for phase := range 3 {
						if phase == 1 {
							for _, v := range seed.views {
								fixture.ViewCopy(t, p, b, v.name, v.name+"_copy")
							}
						}
						if phase == 2 && !b.PG {
							run(fixture.Exec("CREATE TEMPORARY SEQUENCE retained", 0))
							fixture.ViewRestore(t, p, fixture.ViewDump(t, p))
						}
						for _, v := range seed.views {
							want := v.native
							if b.PG {
								want = v.postgres
							}
							suffixes := []string{""}
							if phase > 0 {
								suffixes = append(suffixes, "_copy")
							}
							for _, suffix := range suffixes {
								s := fixture.Select("SELECT "+v.projection+" FROM "+v.name+suffix, v.columns, v.types, want)
								run(s)
								run(fixture.Prepared(s))
							}
						}
					}
				})
			}
		})
	}
}
