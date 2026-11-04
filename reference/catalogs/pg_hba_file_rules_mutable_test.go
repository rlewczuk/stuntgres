package reference_test

import (
	"reflect"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Invoked only as native phases inside shared reference definitions.
func hbaMutableLifecycle(t *testing.T, p *fixture.Pair) {
	t.Helper()
	for _, d := range []*stuntgres.Driver{stuntgres.NewDriver(), new(stuntgres.Driver)} {
		_, b := hbaNativePool(t, d, "hba-late-install")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		stmt, err := conn.PrepareContext(p.Ctx, hbaSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: hbaSelect}
		hbaRead(t, p, b, held, hbaSelect, nil, true)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("PREPARE hba_late AS "+hbaSelect, 0))
		defer conn.ExecContext(p.Ctx, "DEALLOCATE hba_late")
		input := []stuntgres.HBARule{hbaLocalFact("late-label", "peer", "map=owned")}
		d.SetHBARules(input)
		want := [][]any{hbaLocal(1, "late-label", 1, "peer", "map=owned")}
		old, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		// All caller-owned layers can change after publication.
		*input[0].RuleNumber = 99
		*input[0].FileName = "caller-mutated-label"
		*input[0].LineNumber = 88
		*input[0].Type = "caller-mutated-type"
		input[0].Database[0], input[0].UserName[0] = "caller-db", "caller-user"
		*input[0].AuthMethod = "caller-method"
		input[0].Options[0] = "map=caller"
		input[0] = stuntgres.HBARule{}
		hbaRead(t, p, b, b.DB, hbaSelect, want, false)
		hbaRead(t, p, b, held, hbaSelect, want, true)
		hbaRead(t, p, b, conn, "EXECUTE hba_late", want, false)
		d.SetHBARules(nil)
		hbaRead(t, p, b, held, hbaSelect, nil, true)
		hbaRead(t, p, b, conn, "EXECUTE hba_late", nil, false)
		d.SetHBARules([]stuntgres.HBARule{hbaLocalFact("reinstalled-label", "trust")})
		next := [][]any{hbaLocal(1, "reinstalled-label", 1, "trust")}
		hbaRead(t, p, b, held, hbaSelect, next, true)
		hbaRead(t, p, b, conn, "EXECUTE hba_late", next, false)
		hbaConsume(t, old, want, false)
		_, isolated := hbaNativePool(t, stuntgres.NewDriver(), "hba-late-install")
		hbaRead(t, p, isolated, isolated.DB, hbaSelect, nil, false)
	}
}

func hbaConcurrentBatches(t *testing.T, p *fixture.Pair) {
	t.Helper()
	d := stuntgres.NewDriver()
	_, b := hbaNativePool(t, d, "hba-complete-batches")
	// Independent inputs and expectations: duplicated rows/arrays must never
	// combine the A label with B options or expose a partial batch.
	a := []stuntgres.HBARule{hbaLocalFact("batch-A", "peer", "map=A"), hbaLocalFact("batch-A", "peer", "map=A")}
	z := []stuntgres.HBARule{hbaLocalFact("batch-B", "trust"), hbaLocalFact("batch-B", "trust"), hbaLocalFact("batch-B", "trust")}
	wantA := [][]any{hbaLocal(1, "batch-A", 1, "peer", "map=A"), hbaLocal(1, "batch-A", 1, "peer", "map=A")}
	wantB := [][]any{hbaLocal(1, "batch-B", 1, "trust"), hbaLocal(1, "batch-B", 1, "trust"), hbaLocal(1, "batch-B", 1, "trust")}
	var writers sync.WaitGroup
	ticks := make([]chan struct{}, 0, 3)
	for _, rules := range [][]stuntgres.HBARule{a, z, nil} {
		tick := make(chan struct{})
		ticks = append(ticks, tick)
		writers.Add(1)
		go func() {
			defer writers.Done()
			for range tick {
				for range 16 {
					d.SetHBARules(rules)
				}
			}
		}()
	}
	defer func() {
		for _, tick := range ticks {
			close(tick)
		}
		writers.Wait()
	}()
	for range 64 {
		// Keep every publisher alive across the full reader phase rather than
		// letting one startup burst finish before SQL has begun.
		for _, tick := range ticks {
			tick <- struct{}{}
		}
		rows, err := b.DB.QueryContext(p.Ctx, hbaSelect)
		if err != nil {
			t.Fatal(err)
		}
		got := hbaCollect(t, rows, false)
		if got != nil && !reflect.DeepEqual(got, wantA) && !reflect.DeepEqual(got, wantB) {
			t.Fatal("torn HBA batch", got)
		}
	}
}
