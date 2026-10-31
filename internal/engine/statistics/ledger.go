package statistics

import "maps"

// TupleCounts are unresolved transaction counters, not reportable writer events.
type TupleCounts struct{ Inserts, Updates, Deletes int64 }

// Account is one relation's current nesting frame. Parents are immutable values;
// cloning a ledger therefore never shares mutable current or checkpoint state.
// The first truncate saves this frame's original counters, not a flat outer sum.
type Account struct {
	Fact
	Level          int
	BeforeTruncate TupleCounts
	parent         *Account
}

// Ledger belongs beside writes. No catalog journal or published map owns it.
type Ledger map[Key]Account

func (l Ledger) Clone() Ledger { return maps.Clone(l) }

func counts(f Fact) TupleCounts { return TupleCounts{f.Inserts, f.Updates, f.Deletes} }
func addCounts(a, b TupleCounts) TupleCounts {
	return TupleCounts{a.Inserts + b.Inserts, a.Updates + b.Updates, a.Deletes + b.Deletes}
}

// CurrentCounts reconciles a later statement suffix with the owner's live
// nesting frames without releasing, mutating or allocating accounting roots.
// ANALYZE uses current triples, never the counters saved before a truncate.
func (l Ledger) CurrentCounts(key Key, suffix Ledger) TupleCounts {
	a, hasA := l[key]
	b, hasB := suffix[key]
	var left, right *Account
	if hasA {
		left = &a
	}
	if hasB {
		right = &b
	}
	var total TupleCounts
	for left != nil || right != nil {
		switch {
		case right == nil || left != nil && left.Level > right.Level:
			total = addCounts(total, counts(left.Fact))
			left = left.parent
		case left == nil || right.Level > left.Level:
			total = addCounts(total, counts(right.Fact))
			right = right.parent
		default:
			current := counts(right.Fact)
			if !right.Truncated {
				current = addCounts(counts(left.Fact), current)
			}
			total = addCounts(total, current)
			left, right = left.parent, right.parent
		}
	}
	return total
}

// combine joins consecutive segments at the SAME nesting level. A segment's
// first pre-truncate work belongs to that level and must join its earlier prefix.
func (a Account) combine(b Account) Account {
	if b.Truncated {
		if !a.Truncated {
			a.BeforeTruncate = addCounts(counts(a.Fact), b.BeforeTruncate)
		}
		a.Fact = b.Fact
	} else {
		a.Fact = merge(a.Fact, b.Fact)
	}
	return a
}

// join retains the unresolved frames of independent statement segments. It is
// deliberately not subtransaction commit: that has different truncate rules.
func join(a, b Account) Account {
	if a.Level == b.Level {
		merged := a.combine(b)
		if b.parent != nil {
			parent := *b.parent
			if a.parent != nil {
				parent = join(*a.parent, parent)
			}
			merged.parent = &parent
		}
		return merged
	}
	if b.Level > a.Level {
		parent := a
		if b.parent != nil {
			parent = join(a, *b.parent)
		}
		b.parent = &parent
		return b
	}
	parent := b
	if a.parent != nil {
		parent = join(*a.parent, b)
	}
	a.parent = &parent
	return a
}

func (l *Ledger) Record(key Key, event Fact, level int) {
	if *l == nil {
		*l = make(Ledger)
	}
	next := Account{Fact: event, Level: level}
	if before, ok := (*l)[key]; ok {
		next = join(before, next)
	}
	(*l)[key] = next
}

// release performs real successful child transitions. A truncated child replaces
// the parent's current counters, but its discarded pre-truncate work is NOT
// added to the parent's first-pre state. Missing immediate parents promote the
// frame intact, as PostgreSQL's relation subtransaction owner does.
func (a Account) release(level int) Account {
	for a.Level > level {
		if a.parent == nil || a.parent.Level < a.Level-1 {
			a.Level--
			continue
		}
		parent := *a.parent
		if a.Truncated {
			if !parent.Truncated {
				parent.BeforeTruncate = counts(parent.Fact)
			}
			parent.Fact = a.Fact
		} else {
			parent.Fact = merge(parent.Fact, a.Fact)
		}
		a = parent
	}
	return a
}
func (l Ledger) Release(level int) {
	for key, account := range l {
		l[key] = account.release(level)
	}
}

// Committed folds successful children without publishing them independently.
func (a Account) Committed() Fact { return a.release(0).Fact }

// AbortedFrom reports only completed aborted frames. Each restores its OWN first
// pre-truncate triple. Surviving parent counters remain private. Inserts since
// vacuum counts all reported inserts, including aborts; modifications do not.
func (a Account) AbortedFrom(level int) Fact {
	var event Fact
	for {
		if a.Level < level {
			break
		}
		current := counts(a.Fact)
		if a.Truncated {
			current = a.BeforeTruncate
		}
		event.Inserts += current.Inserts
		event.Updates += current.Updates
		event.Deletes += current.Deletes
		event.Dead += current.Inserts + current.Updates
		event.InsertsSince += current.Inserts
		if a.parent == nil {
			break
		}
		a = *a.parent
	}
	return event
}

// Take consumes accepted private ownership; retained marks own cloned maps.
func (l *Ledger) Take(segment *Ledger) {
	if len(*segment) == 0 {
		return
	}
	if *l == nil {
		*l = *segment
	} else {
		for key, event := range *segment {
			if before, ok := (*l)[key]; ok {
				event = join(before, event)
			}
			(*l)[key] = event
		}
	}
	*segment = nil
}
