package partition

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func TestPartitionPruningSearchBudget(t *testing.T) {
	t.Run("refutation_before_exhaustion", func(t *testing.T) {
		p, _, _ := pruningProofFixture(t)
		p.remaining = 1
		possible, err := p.possible(&pruneTask{node: 1}, nil, func([]pruneDomain) (bool, error) {
			t.Fatal("FALSE reached finish")
			return true, nil
		})
		if err != nil || possible || p.remaining != 0 {
			t.Fatalf("last allowed refutation was lost: possible=%v remaining=%d err=%v", possible, p.remaining, err)
		}
		later, err := p.branch(1, p.named, true)
		if err != nil || !later || possible {
			t.Fatalf("fallback must retain later branch without changing prior refutation: earlier=%v later=%v err=%v", possible, later, err)
		}
	})

	t.Run("independent_alternatives_late_membership", func(t *testing.T) {
		p, column, _ := pruningProofFixture(t)
		key := p.term(column)
		integer := func(n int64) sqltype.Value {
			v, err := sqltype.Integer(column.Usage.Type, n)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		query := 0
		for i := range 12 {
			// Separate admitted terms stand for the independent a_i columns.
			independent := p.terms[key]
			c := column
			c.Slot = i + 1
			c.Origin.Address.SubID = int32(i + 2)
			independent.expression = c
			term := len(p.terms)
			p.terms = append(p.terms, independent)
			alternatives := p.join("or", p.atom(term, "=", integer(0)), p.atom(term, "=", integer(1)))
			query = p.join("and", query, alternatives)
		}
		query = p.join("and", query, p.atom(key, "=", integer(99)))
		p.named.Partition = &catalog.PartitionDefinition{Keys: []catalog.PartitionKeyEntry{{Usage: column.Usage}}}
		bound := &catalog.PartitionMembership{
			Lower: []catalog.PartitionDatum{{Value: integer(1)}},
			Upper: []catalog.PartitionDatum{{Value: integer(3)}},
		}
		var terms [32]int
		terms[0] = key
		root := p.join("and", query, p.rangeMembership(p.named, bound, terms))
		domains := make([]pruneDomain, len(p.terms))
		for i := range domains {
			domains[i] = pruneDomain{null: true, nonnull: true}
		}
		finishes := 0
		possible, err := p.possible(&pruneTask{node: root}, domains, func([]pruneDomain) (bool, error) {
			finishes++
			return true, nil
		})
		if err != nil || !possible || p.remaining != 0 || finishes != 0 {
			t.Fatalf("late contradiction must exhaust exactly %d entries and keep: possible=%v remaining=%d finishes=%d err=%v", pruneSearchLimit, possible, p.remaining, finishes, err)
		}
		for i, domain := range domains {
			if !domain.null || !domain.nonnull || domain.hasLower || domain.hasUpper || domain.point {
				t.Fatalf("fallback failed to unwind term %d: %+v", i, domain)
			}
		}
		// Later physical branches do not replenish the allowance or construct
		// more facts, even when their query would trivially refute them.
		nodes := len(p.nodes)
		possible, err = p.branch(1, p.named, true)
		if err != nil || !possible || p.remaining != 0 || len(p.nodes) != nodes {
			t.Fatalf("later branch renewed exhausted search: possible=%v remaining=%d nodes=%d err=%v", possible, p.remaining, len(p.nodes), err)
		}
		fresh, _, _ := pruningProofFixture(t)
		if fresh.remaining != pruneSearchLimit {
			t.Fatal("new selection did not receive fresh allowance", fresh.remaining)
		}
	})

	t.Run("terminal_entry_and_original_error", func(t *testing.T) {
		p, _, _ := pruningProofFixture(t)
		p.remaining = 2 // TRUE node and its terminal finish entry.
		sentinel := errors.New("original membership provider error")
		calls := 0
		possible, err := p.possible(&pruneTask{node: 0}, nil, func([]pruneDomain) (bool, error) {
			calls++
			return false, sentinel
		})
		if possible || !errors.Is(err, sentinel) || p.remaining != 0 || calls != 1 {
			t.Fatalf("last allowed finish lost error/accounting: possible=%v remaining=%d calls=%d err=%v", possible, p.remaining, calls, err)
		}
		possible, err = p.possible(nil, nil, func([]pruneDomain) (bool, error) {
			calls++
			return false, sentinel
		})
		if !possible || err != nil || calls != 1 || p.remaining != 0 {
			t.Fatalf("exhaustion must retain without another provider call: possible=%v remaining=%d calls=%d err=%v", possible, p.remaining, calls, err)
		}
	})

	t.Run("cancellation_precedes_exhaustion", func(t *testing.T) {
		p, _, _ := pruningProofFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p.ctx, p.remaining = ctx, 0
		finish := func([]pruneDomain) (bool, error) {
			t.Fatal("exhausted canceled search reached provider")
			return true, nil
		}
		if _, err := p.possible(nil, nil, finish); !errors.Is(err, context.Canceled) {
			t.Fatal("search exhaustion suppressed cancellation", err)
		}
		if _, err := p.branch(1, p.named, true); !errors.Is(err, context.Canceled) {
			t.Fatal("branch exhaustion suppressed cancellation", err)
		}
	})
}
