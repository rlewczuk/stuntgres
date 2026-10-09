package reference_test

import "testing"

func TestReferenceViewAnalyzedLiteralPersistence(t *testing.T) {
	for _, seed := range referenceViewAnalyzedSeeds() {
		t.Run(seed.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					for _, s := range seed.setup {
						run(s)
					}
					for _, v := range seed.views {
						run(referenceExec("CREATE VIEW "+v.name+"("+v.projection+") AS "+v.body, 0))
					}
					for phase := range 3 {
						if phase == 1 {
							for _, v := range seed.views {
								referenceViewCopy(t, p, b, v.name, v.name+"_copy")
							}
						}
						if phase == 2 && !b.pg {
							run(referenceExec("CREATE TEMPORARY SEQUENCE retained", 0))
							referenceViewRestore(t, p, referenceViewDump(t, p))
						}
						for _, v := range seed.views {
							want := v.native
							if b.pg {
								want = v.postgres
							}
							suffixes := []string{""}
							if phase > 0 {
								suffixes = append(suffixes, "_copy")
							}
							for _, suffix := range suffixes {
								s := referenceSelect("SELECT "+v.projection+" FROM "+v.name+suffix, v.columns, v.types, want)
								run(s)
								run(referencePrepared(s))
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
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					for _, s := range seed.setup {
						run(s)
					}
					for _, v := range seed.views {
						run(referenceExec("CREATE VIEW "+v.name+"("+v.projection+") AS "+v.body, 0))
					}
					for phase := range 3 {
						if phase == 1 {
							for _, v := range seed.views {
								referenceViewCopy(t, p, b, v.name, v.name+"_copy")
							}
						}
						if phase == 2 && !b.pg {
							run(referenceExec("CREATE TEMPORARY SEQUENCE retained", 0))
							referenceViewRestore(t, p, referenceViewDump(t, p))
						}
						for _, v := range seed.views {
							want := v.native
							if b.pg {
								want = v.postgres
							}
							suffixes := []string{""}
							if phase > 0 {
								suffixes = append(suffixes, "_copy")
							}
							for _, suffix := range suffixes {
								s := referenceSelect("SELECT "+v.projection+" FROM "+v.name+suffix, v.columns, v.types, want)
								run(s)
								run(referencePrepared(s))
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
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					for _, s := range seed.setup {
						run(s)
					}
					for _, v := range seed.views {
						run(referenceExec("CREATE VIEW "+v.name+"("+v.projection+") AS "+v.body, 0))
					}
					for phase := range 3 {
						if phase == 1 {
							for _, v := range seed.views {
								referenceViewCopy(t, p, b, v.name, v.name+"_copy")
							}
						}
						if phase == 2 && !b.pg {
							run(referenceExec("CREATE TEMPORARY SEQUENCE retained", 0))
							referenceViewRestore(t, p, referenceViewDump(t, p))
						}
						for _, v := range seed.views {
							want := v.native
							if b.pg {
								want = v.postgres
							}
							suffixes := []string{""}
							if phase > 0 {
								suffixes = append(suffixes, "_copy")
							}
							for _, suffix := range suffixes {
								s := referenceSelect("SELECT "+v.projection+" FROM "+v.name+suffix, v.columns, v.types, want)
								run(s)
								run(referencePrepared(s))
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
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					for _, s := range seed.setup {
						run(s)
					}
					for _, v := range seed.views {
						run(referenceExec("CREATE VIEW "+v.name+"("+v.projection+") AS "+v.body, 0))
					}
					for phase := range 3 {
						if phase == 1 {
							for _, v := range seed.views {
								referenceViewCopy(t, p, b, v.name, v.name+"_copy")
							}
						}
						if phase == 2 && !b.pg {
							run(referenceExec("CREATE TEMPORARY SEQUENCE retained", 0))
							referenceViewRestore(t, p, referenceViewDump(t, p))
						}
						for _, v := range seed.views {
							want := v.native
							if b.pg {
								want = v.postgres
							}
							suffixes := []string{""}
							if phase > 0 {
								suffixes = append(suffixes, "_copy")
							}
							for _, suffix := range suffixes {
								s := referenceSelect("SELECT "+v.projection+" FROM "+v.name+suffix, v.columns, v.types, want)
								run(s)
								run(referencePrepared(s))
							}
						}
					}
				})
			}
		})
	}
}
