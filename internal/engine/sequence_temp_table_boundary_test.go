package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/parser"
)

func TestSequenceTemporaryTableBoundaryAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, destination := range []string{"Own", "Peer", "Path", "OwnExisting", "PeerExisting", "PathExisting"} {
			t.Run(destination+map[bool]string{false: "/Direct", true: "/Prepared"}[prepared], func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE SCHEMA boundary_a")
				sessionSQL(t, a, "CREATE SCHEMA boundary_b")
				sessionSQL(t, a, "CREATE SEQUENCE boundary_b.s START 10 CACHE 7")
				sessionSQL(t, a, "SET search_path TO boundary_a,boundary_b,public")
				ast, held := sequencePreparedDescription(t, e, a, "SELECT nextval('s')")
				r, err := sequencePreparedExecute(e, a, ast, &held)
				if err != nil {
					t.Fatal(err)
				}
				if got := tuples(r); len(got) != 1 || got[0][0] != int64(10) {
					t.Fatal(got)
				}
				r.Rows.Close()
				sessionSQL(t, a, "CREATE SEQUENCE boundary_a.s START 100")
				sessionSQL(t, a, "CREATE TEMP SEQUENCE seed START 31 CACHE 5")
				sessionSQL(t, b, "CREATE TEMP SEQUENCE seed START 81 CACHE 5")
				sequenceScalar(t, a, "SELECT nextval('seed')", 31)
				sequenceScalar(t, b, "SELECT nextval('seed')", 81)
				own := e.state.descriptors[a.temporaryNamespace].(*namespaceDescriptor).name
				peer := e.state.descriptors[b.temporaryNamespace].(*namespaceDescriptor).name
				target := own + ".forbidden"
				switch destination {
				case "Peer":
					target = peer + ".forbidden"
				case "Path", "PathExisting":
					sessionSQL(t, a, "SET search_path TO pg_temp")
					target = "forbidden"
				case "OwnExisting":
					target = own + ".seed"
				case "PeerExisting":
					target = peer + ".seed"
				}
				if destination == "PathExisting" {
					target = "seed"
				}
				prefix := "CREATE TABLE "
				if destination == "OwnExisting" || destination == "PeerExisting" || destination == "PathExisting" {
					prefix += "IF NOT EXISTS "
				}
				catalog, retained, stores := e.state, cloneCatalog(e.state), len(e.storage)
				cell, last, peerLast := held.sequences, a.lastSequence, b.lastSequence
				generators := make(map[ObjectAddress]sequenceGenerator)
				memories := make(map[ObjectAddress]sequenceMemory)
				for address, g := range e.sequences {
					generators[address] = *g
				}
				for address, m := range a.sequences {
					memories[address] = *m
				}
				peerMemory := *b.sequences[b.lastSequence]
				err = sequenceTempMoveCommand(t, e, a, prefix+target+"(v INT)", prepared)
				sequenceOwnerDiagnostic(t, err, "0A000", "temporary schema creation is unsupported", "")
				if e.state != catalog || !reflect.DeepEqual(e.state, retained) || len(e.storage) != stores || held.sequences != cell || a.lastSequence != last || b.lastSequence != peerLast || *b.sequences[peerLast] != peerMemory || len(e.sequences) != len(generators) || len(a.sequences) != len(memories) {
					t.Fatal("table rejection published state or disturbed retained analysis")
				}
				for address, want := range generators {
					if *e.sequences[address] != want {
						t.Fatal("generator changed", address)
					}
				}
				for address, want := range memories {
					if *a.sequences[address] != want {
						t.Fatal("cache changed", address)
					}
				}
				sequenceScalar(t, a, "SELECT currval('seed')", 31)
				sequenceScalar(t, a, "SELECT lastval()", 31)
				sequenceScalar(t, a, "SELECT nextval('seed')", 32)
				sequenceScalar(t, b, "SELECT nextval('seed')", 82)
				sessionSQL(t, a, "CREATE TABLE public.allowed(v INT)")
			})
		}
	}
}

// The shared ordinary creator must not depend on the executor's early guard.
func TestSequenceTemporaryTableBoundarySharedCreator(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TEMP SEQUENCE seed")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	raw := statement(t, "CREATE TABLE IF NOT EXISTS seed(v INT)").(*parser.CreateTable)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { scope.Finish(err) }()
	if err = scope.gate(raw); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.catalog)
	before := e.state
	err = e.schemaTableLocked(op, auth, stage, raw, s.temporaryNamespace, auth.state.CurrentUser, scope.input)
	e.mu.Unlock()
	sequenceOwnerDiagnostic(t, err, "0A000", "temporary schema creation is unsupported", "")
	if e.state != before || len(stage.descriptors) != 0 || len(stage.names) != 0 {
		t.Fatal("shared creator staged a temporary table")
	}
	e.DiscardCatalogStage(stage)
}
