package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestInheritanceMaintCandidate(t *testing.T) {
	e := New()
	p := inheritanceUnitRelation(e, "p", 22000, "a")
	q := inheritanceUnitRelation(e, "q", 22001, "a")
	c := inheritanceUnitRelation(e, "c", 22002, "extra", "a")
	p.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23000, Name: "pn", Attribute: 1}}
	q.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23001, Name: "qn", Attribute: 1}}
	c.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23002, Name: "cn", Attribute: 2}}
	p.Columns[0].Nullable, q.Columns[0].Nullable, c.Columns[1].Nullable = false, false, false
	view := inheritanceUnitView(e, p, q, c)
	beforeColumns := slices.Clone(c.Columns)
	first, err := table.AttachInheritance(context.Background(), view, c, p)
	if err != nil {
		t.Fatal(err)
	}
	if first == c || c.Inheritance != nil || !reflect.DeepEqual(beforeColumns, c.Columns) || &first.Columns[0] != &c.Columns[0] || first.Constraints.NotNull[0].OID != 23002 {
		t.Fatal("attach copied physical roots, reminted fact or mutated source")
	}
	if edge := first.Inheritance.Parents[0]; edge.Relation != p.Address() || edge.Seqno != 1 {
		t.Fatal(edge)
	}
	original := catalog.CloneInheritance(first.Inheritance)
	second, err := table.AttachInheritance(context.Background(), view, first, q)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Inheritance, original) || len(second.Inheritance.NotNull[2].Origins) != 2 || !second.Inheritance.NotNull[2].Local {
		t.Fatal("nested append mutated old provenance")
	}
	detached, err := table.DetachInheritance(view, second, p, view.Domain().Ref(view.Epoch(), p.Address()))
	if err != nil {
		t.Fatal(err)
	}
	if !detached.Inheritance.HasParent(q.Address()) || detached.Inheritance.HasParent(p.Address()) || detached.Inheritance.Parents[0].Seqno != 2 || second.Inheritance.Parents[0].Relation != p.Address() {
		t.Fatal("detach renumbered or mutated source edge")
	}
	last, err := table.DetachInheritance(view, detached, q, view.Domain().Ref(view.Epoch(), q.Address()))
	if err != nil || last.Inheritance != nil {
		t.Fatal("last ordinary detach retained obsolete graph", err)
	}
	bad := *p
	bad.Columns = nil
	bad.Attributes = nil
	bad.Index = map[string]int{}
	// Real cycle/duplicate and mismatch checks are candidate calls, not SQL wrappers.
	if _, err := table.AttachInheritance(context.Background(), view, first, p); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := table.AttachInheritance(context.Background(), view, &bad, c); err == nil {
		t.Fatal("missing source column accepted")
	}
	loop := *p
	loop.Inheritance = &catalog.TableInheritance{Parents: inheritanceUnitParents(c.Address())}
	if _, err := table.AttachInheritance(context.Background(), inheritanceUnitView(e, &loop, c), c, &loop); err == nil {
		t.Fatal("cycle accepted")
	}
	if err := table.ValidateInheritance(view, first); err != nil {
		t.Fatal(err)
	}
}

func TestInheritanceMaintOrdinals(t *testing.T) {
	e := New()
	p := inheritanceUnitRelation(e, "p", 22000, "a")
	q := inheritanceUnitRelation(e, "q", 22001, "a")
	c := inheritanceUnitRelation(e, "c", 22002, "a")
	view := inheritanceUnitView(e, p, q, c)
	first, err := table.AttachInheritance(context.Background(), view, c, p)
	if err != nil {
		t.Fatal(err)
	}
	first.Inheritance.Parents[0].Seqno = 3
	next, err := table.AttachInheritance(context.Background(), view, first, q)
	if err != nil || next.Inheritance.Parents[1].Seqno != 4 {
		t.Fatal("max-live ordinal not used", err)
	}
	mapped := table.RemapInheritance(next.Inheritance, func(a ids.ObjectAddress) ids.ObjectAddress { a.ObjectID += 100; return a }, func(o catalog.InheritanceOrigin) ids.OID { return o.Constraint })
	if mapped.Parents[0].Seqno != 3 || mapped.Parents[1].Seqno != 4 || next.Inheritance.Parents[0].Relation != p.Address() {
		t.Fatal("remap lost sparse ordinals or mutated source")
	}
	for _, seqno := range []int32{0, -1} {
		bad := *first
		bad.Inheritance = catalog.CloneInheritance(first.Inheritance)
		bad.Inheritance.Parents[0].Seqno = seqno
		if table.ValidateInheritance(view, &bad) == nil {
			t.Fatal("invalid ordinal admitted", seqno)
		}
	}
	overflow := *first
	overflow.Inheritance = catalog.CloneInheritance(first.Inheritance)
	overflow.Inheritance.Parents[0].Seqno = int32(1<<31 - 1)
	_, err = table.AttachInheritance(context.Background(), view, &overflow, q)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54000" || overflow.Inheritance.Parents[0].Seqno != int32(1<<31-1) {
		t.Fatal("ordinal overflow mutated source", err)
	}
}

func TestInheritanceMaintHistory(t *testing.T) {
	e := New()
	p := inheritanceUnitRelation(e, "p", 22000, "b")
	c := inheritanceUnitRelation(e, "c", 22001, "a", "b")
	view := inheritanceUnitView(e, p, c)
	nn := catalog.NotNullConstraint{OID: 23000, Name: "nn", Attribute: 1}
	c.Constraints.NotNull = []catalog.NotNullConstraint{nn}
	c.Columns[0].Nullable = false
	history := catalog.RetiredInheritanceOrigin{Parent: view.Domain().Ref(view.Epoch(), p.Address()), Attribute: 1, Column: "a", Name: "nn", Constraint: 23001, Lifetime: uint64(view.Epoch()), Own: nn.OID, Replayed: true, Archive: 1}
	c.Inheritance = table.LocalInheritance(c)
	c.Inheritance.Parents = inheritanceUnitParents(p.Address())
	c.Inheritance.NotNull[1] = catalog.InheritedConstraint{Retired: []catalog.RetiredInheritanceOrigin{history}}
	c.Inheritance.Columns[2] = catalog.InheritedColumn{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}}
	original := catalog.CloneInheritance(c.Inheritance)
	next, err := table.DetachInheritance(view, c, p, history.Parent)
	if err != nil {
		t.Fatal(err)
	}
	if next.Inheritance == nil || !next.Inheritance.NotNull[1].Retired[0].Detached || c.Inheritance.NotNull[1].Retired[0].Detached || !reflect.DeepEqual(original, c.Inheritance) {
		t.Fatal("detach lost history or mutated captured certificate")
	}
	withoutSource := inheritanceUnitView(e, next)
	if err := table.ValidateInheritance(withoutSource, next); err != nil {
		t.Fatal("archive still requires live source", err)
	}
	clean := table.ReleaseRetiredTypeConstraints(withoutSource, next, []parser.AlterTableAction{{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "a"}}})
	proof, exists := clean.Constraints.PhysicalNotNull[1]
	if !exists || !catalog.PhysicalNotNullStandaloneCause(proof.Cause) || len(clean.Constraints.NotNull) != 0 || clean.Columns[0].Nullable || len(next.Constraints.NotNull) != 1 {
		t.Fatal("no-key detached TYPE did not issue exact physical certificate")
	}
	if err := table.ValidatePhysicalNotNull(withoutSource, clean, table.PhysicalOrdinaryNotNull(clean)); err != nil {
		t.Fatal(err)
	}
	duplicate := *proof.Cause
	duplicate.Self = &duplicate
	duplicate.History = append(slices.Clone(duplicate.History), duplicate.History[0])
	if catalog.PhysicalNotNullStandaloneCause(&duplicate) {
		t.Fatal("duplicate TYPE certificate admitted")
	}
	cycle := *proof.Cause
	cycle.Self, cycle.Source, cycle.Kind = &cycle, &cycle, catalog.PhysicalNotNullInheritedTypeCleanup
	if catalog.PhysicalNotNullStandaloneCause(&cycle) {
		t.Fatal("cyclic cleanup source admitted")
	}
	unchanged := table.ReleaseRetiredTypeConstraints(withoutSource, next, []parser.AlterTableAction{{Action: parser.SetNotNull, Column: parser.Identifier{Text: "a"}}})
	if unchanged != next {
		t.Fatal("non-TYPE action minted a cleanup cause")
	}
	for _, mutate := range []func(*catalog.RetiredInheritanceOrigin){
		func(h *catalog.RetiredInheritanceOrigin) { h.Own++ },
		func(h *catalog.RetiredInheritanceOrigin) { h.Lifetime = 0 },
		func(h *catalog.RetiredInheritanceOrigin) { h.Detached = false },
	} {
		bad := *next
		bad.Inheritance = catalog.CloneInheritance(next.Inheritance)
		fact := bad.Inheritance.NotNull[1]
		fact.Retired = slices.Clone(fact.Retired)
		mutate(&fact.Retired[0])
		bad.Inheritance.NotNull[1] = fact
		if table.ValidateInheritance(inheritanceUnitView(e, &bad), &bad) == nil {
			t.Fatal("malformed archive admitted")
		}
		if _, issued := table.PhysicalNotNullFromType(inheritanceUnitView(e, &bad), &bad, &bad, nn); issued {
			t.Fatal("malformed archive issued a physical TYPE certificate")
		}
	}
}

func TestInheritanceMaintHistorySyntax(t *testing.T) {
	sql := retiredRepairSQL("c", "nn", "n", false, true, 0, retiredRow("n", "0:7", "77"))
	sql = sql[:strings.Index(sql, " AND EXISTS ")]
	sql = strings.Replace(sql, "'public.p'::regclass::text", "'detached:22000'::text", 1)
	parsed, err := table.RestoreRetiredSyntax(retiredRaw(t, sql))
	if err != nil || len(parsed.Rows) != 1 || parsed.Rows[0].Detached != 22000 || len(parsed.Rows[0].Parent.Parts) != 0 || !parsed.BeforeLocal || parsed.Before != 0 {
		t.Fatal("canonical detached archive was name-resolved or lost pairing", parsed, err)
	}
	for _, value := range []string{"detached:0", "detached:022000", "detached:-1", "detached:4294967296", "public.p"} {
		bad := strings.Replace(sql, "detached:22000", value, 1)
		if _, err := table.RestoreRetiredSyntax(retiredRaw(t, bad)); err == nil {
			t.Fatal("malformed archive token admitted", value)
		}
	}
	legacy := retiredRepairSQL("c", "nn", "n", false, false, 1, strings.Replace(retiredRow("n", "0:7", "77"), "::regclass::text", "::regclass", 1))
	if _, err := table.RestoreRetiredSyntax(retiredRaw(t, legacy)); err == nil {
		t.Fatal("obsolete mixed-type source carrier retained as a shim")
	}
}
