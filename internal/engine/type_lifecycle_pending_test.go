package engine

import (
	"fmt"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestTypeLifecyclePendingProtection(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE TYPE public.mood AS ENUM ('a')", "CREATE DOMAIN public.amount AS integer", "CREATE TYPE public.pair AS(v integer)", "CREATE SCHEMA other", "CREATE TYPE other.mood AS ENUM ('a')", "CREATE TYPE public._mood AS ENUM ('shadow')"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	table := domainUnitTable(t, e, op, "CREATE TABLE public.rows(v integer)")
	for _, name := range []string{"cube", "intarray", "citext", "earthdistance"} {
		domainUnitPublish(t, e, cubeUnitStage(t, e, op, name, ""))
	}
	view := e.CatalogRead()
	path := []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}
	otherNS := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "other"})
	unrelated := view.MustType(view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: otherNS, Name: "mood"}))
	for _, name := range []string{"mood", "amount", "pair"} {
		base := domainUnitType(t, view, name)
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
		if name == "mood" && array.Name() == "_mood" {
			t.Fatal("displaced companion precondition missing")
		}
		owner, err := catalog.ArrayTypeOwner(view, array)
		if err != nil || owner != base {
			t.Fatal("canonical owner not resolved", err)
		}
		foreignOwner := *base
		for _, pending := range []map[ids.ObjectAddress]*catalog.Type{nil, {array.Address(): array}, {unrelated.Address(): unrelated}, {base.Address(): &foreignOwner}} {
			err := catalog.TypeDropProtection(view, array, path, pending)
			domainUnitDiagnostic(t, err, "2BP01", "cannot drop type "+name+"[] because type "+name+" requires it")
		}
		pending := map[ids.ObjectAddress]*catalog.Type{array.Address(): array, base.Address(): base}
		if err := catalog.TypeDropProtection(view, array, path, pending); err != nil {
			t.Fatal("genuine pending internal owner not deferred", err)
		}
		if err := catalog.TypeDropProtection(view, base, path, pending); err != nil {
			t.Fatal("ordinary owner not droppable", err)
		}
		foreignArray := *array
		if err := catalog.TypeDropProtection(view, &foreignArray, path, pending); err == nil {
			t.Fatal("foreign same-OID companion descriptor authorized deferral")
		}
	}
	for _, tc := range []struct{ extension, typ string }{{"cube", "cube"}, {"intarray", "query_int"}, {"citext", "citext"}, {"earthdistance", "earth"}} {
		base := domainUnitType(t, view, tc.typ)
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
		pending := map[ids.ObjectAddress]*catalog.Type{array.Address(): array, base.Address(): base}
		if err := catalog.TypeDropProtection(view, array, path, pending); err != nil {
			t.Fatal("installed companion did not defer to genuine pending element", err)
		}
		domainUnitDiagnostic(t, catalog.TypeDropProtection(view, base, path, pending), "2BP01", "cannot drop type "+tc.typ+" because extension "+tc.extension+" requires it")
	}
	integer := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23})
	integerArray := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(integer.Array)})
	pinned := map[ids.ObjectAddress]*catalog.Type{integer.Address(): integer, integerArray.Address(): integerArray}
	if err := catalog.TypeDropProtection(view, integerArray, path, pinned); err != nil {
		t.Fatal("pinned companion did not defer", err)
	}
	domainUnitDiagnostic(t, catalog.TypeDropProtection(view, integer, path, pinned), "2BP01", "cannot drop type integer because it is required by the database system")
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(table.RowType)})
	rowArray := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	owned := map[ids.ObjectAddress]*catalog.Type{row.Address(): row, rowArray.Address(): rowArray}
	if err := catalog.TypeDropProtection(view, rowArray, path, owned); err != nil {
		t.Fatal("row companion did not defer", err)
	}
	domainUnitDiagnostic(t, catalog.TypeDropProtection(view, row, path, owned), "2BP01", "cannot drop type rows because table rows requires it")
	if e.CatalogRead().State() != view.State() || view.MustType(integer.Address()) != integer || view.MustType(row.Address()) != row {
		t.Fatal("protection changed the immutable graph")
	}
}

func TestTypeLifecyclePendingDropClosure(t *testing.T) {
	for _, family := range []struct{ name, create string }{{"mood", "CREATE TYPE public.mood AS ENUM ('a')"}, {"amount", "CREATE DOMAIN public.amount AS integer"}, {"pair", "CREATE TYPE public.pair AS(v integer)"}} {
		for _, arrayFirst := range []bool{false, true} {
			for _, dependent := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/array_first=%t/dependent=%t", family.name, arrayFirst, dependent), func(t *testing.T) {
					e := New()
					op, done := roleOperation(t, e, "postgres")
					defer done()
					domainUnitPublish(t, e, domainUnitStage(t, e, op, family.create))
					if dependent {
						domainUnitTable(t, e, op, "CREATE TABLE public.holder(keep integer,v public."+family.name+")")
					}
					before := e.CatalogRead()
					base := domainUnitType(t, before, family.name)
					array := before.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
					first, last := base.Name(), array.Name()
					if arrayFirst {
						first, last = last, first
					}
					q := "DROP TYPE public." + first + ",public." + last + ",public." + first + ",public." + last + " RESTRICT"
					stage := domainUnitStage(t, e, op, q)
					defer e.DiscardCatalogStage(stage)
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					defer scope.Finish(nil)
					if err := scope.gate(statement(t, q)); err != nil {
						_ = scope.Finish(err)
						t.Fatal(err)
					}
					err = scope.stageDomainDependents(stage, parser.DropRestrict)
					if dependent {
						domainUnitDiagnostic(t, err, "2BP01", "cannot drop desired object(s) because other objects depend on them")
						if stage.RemovalCount() != 0 || e.CatalogRead().State() != before.State() {
							t.Fatal("blocked batch staged/published partial retirement")
						}
						_ = scope.Finish(err)
						return
					}
					if err != nil {
						_ = scope.Finish(err)
						t.Fatal(err)
					}
					for _, address := range []ids.ObjectAddress{base.Address(), array.Address()} {
						found := false
						for removed := range stage.Removals() {
							found = found || removed == address
						}
						if !found {
							t.Fatal("genuine batch member missing from retirement", address)
						}
					}
					if err := scope.Finish(nil); err != nil {
						t.Fatal(err)
					}
					domainUnitPublish(t, e, stage)
					if _, ok := e.CatalogRead().Type(base.Address()); ok {
						t.Fatal("element survived actual publication")
					}
					if _, ok := e.CatalogRead().Type(array.Address()); ok {
						t.Fatal("companion survived actual publication")
					}
					if before.MustType(base.Address()) != base || before.MustType(array.Address()) != array {
						t.Fatal("retained graph changed")
					}
				})
			}
		}
	}
}
