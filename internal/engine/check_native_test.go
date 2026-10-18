package engine

import (
	"context"
	"strings"
	"testing"
)

func TestCheckRetainedTemporaryBuiltinReplacement(t *testing.T) {
	for _, mode := range []string{"ReplaceCatalog", "Restore"} {
		t.Run(mode, func(t *testing.T) {
			target := New()
			session := sessionStart(t, target, nil)
			sessionSQL(t, session, "CREATE TEMP SEQUENCE kept START 30")
			sequenceScalar(t, session, "SELECT nextval('kept')", 30)
			_, description := sequencePreparedDescription(t, target, session, "SELECT nextval('kept')")
			tempRef := description.sequences.constants[0].ref
			temp := target.catalog.state.descriptors[tempRef.Address]
			const script = "CREATE TABLE imported(n BIGINT DEFAULT CAST(1 AS BIGINT),b BOOL DEFAULT TRUE,CONSTRAINT c CHECK(n=CAST(1 AS BIGINT) AND b))"
			if mode == "Restore" {
				if err := target.Restore(context.Background(), strings.NewReader(script)); err != nil {
					t.Fatal(err)
				}
			} else {
				source := New()
				peer := sessionStart(t, source, nil)
				sessionSQL(t, peer, script)
				ref := catalogRef(t, source, source.CatalogRead(), "imported")
				old := source.catalog.state.descriptors[ref.Address]
				if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
					t.Fatal(err)
				}
				if source.catalog.state.descriptors[ref.Address] != old {
					t.Fatal("replacement mutated source")
				}
			}
			view := target.CatalogRead()
			ref := catalogRef(t, target, view, "imported")
			descriptor := view.state.descriptors[ref.Address].(*relationDescriptor)
			if err := validateChecks(view, descriptor, map[string]bool{}); err != nil {
				t.Fatal(err)
			}
			if target.catalog.state.descriptors[tempRef.Address] != temp {
				t.Fatal("replacement changed pinned TEMP descriptor")
			}
			sessionSQL(t, session, "INSERT INTO imported DEFAULT VALUES")
			sequenceScalar(t, session, "SELECT nextval('kept')", 31)
		})
	}
}

func TestCheckFailedRestorePreservesEpochAndStage(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE sentinel(a INT)")
	sessionSQL(t, s, "INSERT INTO sentinel VALUES(7)")
	before := e.CatalogRead()
	ref := catalogRef(t, e, before, "sentinel")
	storage := e.storage[ref.Address]
	stage := e.BeginCatalogStage(before)
	for _, script := range []string{"CREATE TABLE replacement(a INT CHECK(a))", "CREATE TABLE replacement(a INT CHECK(a=1));INSERT INTO replacement VALUES(2)"} {
		if err := e.Restore(context.Background(), strings.NewReader(script)); err == nil {
			t.Fatal("bad Restore succeeded")
		}
		after := e.CatalogRead()
		if after.state != before.state || after.Epoch() != before.Epoch() || e.storage[ref.Address] != storage {
			t.Fatal("failed Restore replaced destination")
		}
		if err := e.ValidateObjectRef(after, ref); err != nil {
			t.Fatal("old reference invalidated", err)
		}
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err != nil {
		t.Fatal("failed Restore invalidated prior stage", err)
	}
	e.DiscardCatalogStage(stage)
	transactionRows(t, s, "SELECT a FROM sentinel", 7)
}

func TestCheckAbsentValidatorHasNoAllocation(t *testing.T) {
	table := &relationDescriptor{}
	eval := &EvalContext{Context: context.Background()}
	if allocations := testing.AllocsPerRun(100, func() {
		constraint, err := validateCheckRow(table, nil, eval)
		if constraint != nil || err != nil {
			panic("unexpected validator failure")
		}
	}); allocations != 0 {
		t.Fatal("no-CHECK validator allocations", allocations)
	}
}
