package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// A phase borrows the same pools, recorder and physical connection. Only its
// request context changes; cumulative matrix work must not consume the next
// operation's budget. Retained rows must be consumed/closed before fn returns.
func regtypeDisplacementPhase(t *testing.T, p *fixture.Pair, label string, fn func(*fixture.Pair)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	start := time.Now()
	defer func() {
		if t.Failed() {
			t.Logf("mixed replay phase %q elapsed=%s context=%v", label, time.Since(start), ctx.Err())
		}
	}()
	phase := *p
	phase.Ctx = ctx
	fn(&phase)
	if err := ctx.Err(); err != nil {
		t.Fatalf("mixed replay phase %q exhausted after %s: %v", label, time.Since(start), err)
	}
}

func TestReferenceRegtypeArrayDisplacementMixedReplay(t *testing.T) {
	var emitted []byte
	fixture.SchemaReferenceCleanup(t, "regtype_displacement")
	p := fixture.NewScenario(t)
	run := func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		var cases []regtypeDisplacementCase
		var dataRelations []string
		kinds := []string{"table", "query_table", "domain", "enum", "view"}
		for _, baseKind := range kinds {
			for _, namedKind := range kinds {
				if baseKind == namedKind {
					continue
				}
				for _, first := range []bool{true, false} {
					base := fmt.Sprintf("mixed_%s_%s_%t", baseKind, namedKind, first)
					c := regtypeDisplacementCase{base: base, named: "_" + base, arrayFirst: first}
					baseCreator, namedCreator := c, c
					baseCreator.kind, namedCreator.kind = baseKind, namedKind
					for _, creator := range []regtypeDisplacementCase{baseCreator, namedCreator} {
						if creator.kind == "query_table" || creator.kind == "view" {
							name := c.base
							if creator.kind == namedKind {
								name = c.named
							}
							dataRelations = append(dataRelations, name)
						}
					}
					regtypeDisplacementPhase(t, p, "source create "+base, func(phase *fixture.Pair) {
						if first {
							c.baseArray, c.namedArray = "__"+base, "__"+base+"_1"
							fixture.RunStep(t, phase.Ctx, conn, recorder, regtypeDisplacementCreate(baseCreator, c.base))
							regtypeDisplacementReferences(t, phase, conn, recorder, c)
							fixture.RunStep(t, phase.Ctx, conn, recorder, regtypeDisplacementCreate(namedCreator, c.named))
						} else {
							c.baseArray, c.namedArray = "_"+base+"_1", "__"+base
							fixture.RunStep(t, phase.Ctx, conn, recorder, regtypeDisplacementCreate(namedCreator, c.named))
							fixture.RunStep(t, phase.Ctx, conn, recorder, regtypeDisplacementCreate(baseCreator, c.base))
							regtypeDisplacementReferences(t, phase, conn, recorder, c)
						}
						namedReference := c
						namedReference.base = c.named
						regtypeDisplacementReferences(t, phase, conn, recorder, namedReference)
					})
					cases = append(cases, c)
				}
			}
		}
		check := func(label string, source bool) {
			var identities [][4]int64
			regtypeDisplacementPhase(t, p, label+" identities", func(phase *fixture.Pair) {
				identities = regtypeDisplacementIdentities(t, phase, conn, cases)
			})
			for i, c := range cases {
				regtypeDisplacementPhase(t, p, label+" "+c.base, func(phase *fixture.Pair) {
					if source {
						baseID, arrayID := identities[i][0], identities[i][1]
						regtypeDisplacementCheck(t, phase, backend, conn, recorder, c, baseID, arrayID)
						regtypeDisplacementReferenceCheck(t, phase, conn, recorder, c, arrayID, 1)
					} else {
						regtypeDisplacementLogicalCheck(t, phase, backend, conn, recorder, c, identities[i])
					}
					namedReference := c
					namedReference.base = c.named
					regtypeDisplacementReferenceCheck(t, phase, conn, recorder, namedReference, identities[i][3], 1)
				})
			}
			for _, name := range dataRelations {
				regtypeDisplacementPhase(t, p, label+" data "+name, func(phase *fixture.Pair) {
					fixture.RegtypeBoth(t, phase, conn, recorder, fixture.Select("SELECT v FROM "+fixture.RegtypeDisplacementQualified(name), []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		}
		check("source", true)
		if backend.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native mixed-creator Dump required")
			}
			regtypeDisplacementPhase(t, p, "PostgreSQL emitted replay", func(phase *fixture.Pair) {
				fixture.RunStep(t, phase.Ctx, conn, recorder, fixture.Exec("DROP SCHEMA regtype_displacement CASCADE", 0))
				if _, err := conn.ExecContext(phase.Ctx, string(emitted)); err != nil {
					t.Fatal("actual mixed-creator PostgreSQL replay", err)
				}
				fixture.RunStep(t, phase.Ctx, conn, recorder, fixture.Exec("SET search_path TO regtype_displacement,public", 0))
			})
			check("PostgreSQL restored", false)
			return
		}
		var dump bytes.Buffer
		regtypeDisplacementPhase(t, p, "native Dump", func(phase *fixture.Pair) {
			if err := stuntgres.Dump(phase.Ctx, phase.Native, &dump); err != nil {
				t.Fatal("mixed-creator native Dump", err)
			}
		})
		emitted = bytes.Clone(dump.Bytes())
		regtypeDisplacementPhase(t, p, "native replacement and retained cursor", func(phase *fixture.Pair) {
			retained, err := conn.QueryContext(phase.Ctx, "SELECT r FROM "+fixture.RegtypeDisplacementQualified("v_"+cases[0].base))
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Reset(phase.Ctx, phase.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(phase.Ctx, phase.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal("mixed-creator native Restore", err)
			}
			// Consume the old snapshot before canceling its query context, not
			// after the unrelated full restored-catalog matrix.
			fixture.RegtypeAssertRows(t, retained, [][]any{{cases[0].base + "[]"}}, false)
		})
		check("native restored", false)
		regtypeDisplacementForeignReplay(t, emitted, cases, dataRelations)
	}
	// The shared displacement wrapper uses its original Pair.Ctx before
	// entering the body. This long matrix also needs phase-local borrowing
	// and setup when native-first replay has already consumed that lifetime.
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			var conn *sql.Conn
			var recorder *fixture.Recorder
			regtypeDisplacementPhase(t, p, backend.Name+" setup", func(phase *fixture.Pair) {
				_, conn, recorder = fixture.NoticeReferencePool(t, phase, backend.PG, &notices)
				for _, q := range []string{"CREATE SCHEMA regtype_displacement", "SET search_path TO regtype_displacement,public"} {
					fixture.RunStep(t, phase.Ctx, conn, recorder, fixture.Exec(q, 0))
				}
			})
			run(t, p, backend, conn, recorder)
		})
	}
}
