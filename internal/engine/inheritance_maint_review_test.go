package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func inheritanceMaintReviewGraphSnapshot(g *catalog.TableInheritance) *catalog.TableInheritance {
	copy := catalog.CloneInheritance(g)
	for a, p := range copy.Columns {
		p.Origins = slices.Clone(p.Origins)
		copy.Columns[a] = p
	}
	for a, p := range copy.NotNull {
		p.Origins, p.Retired = slices.Clone(p.Origins), slices.Clone(p.Retired)
		copy.NotNull[a] = p
	}
	for name, p := range copy.Checks {
		p.Origins, p.Retired = slices.Clone(p.Origins), slices.Clone(p.Retired)
		copy.Checks[name] = p
	}
	return copy
}

func TestInheritanceMaintReviewTemporaryHistory(t *testing.T) {
	for _, replacement := range []string{"Reset", "Restore"} {
		t.Run(replacement, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			// SQL only constructs authentic physical TEMP/history providers. The
			// witnesses below exercise replacement, retained authority, candidate
			// immutability and dependency closure directly, not SQL responses.
			for _, sql := range []string{
				"CREATE TEMP TABLE review_temp_p(a INT CONSTRAINT nn NOT NULL,b INT,CONSTRAINT ck CHECK(a>0))",
				"CREATE TEMP TABLE review_temp_c(a INT) INHERITS(review_temp_p)",
				"CREATE TEMP TABLE review_temp_leaf() INHERITS(review_temp_c)",
				"INSERT INTO review_temp_c VALUES(2,20)",
				"ALTER TABLE review_temp_p DROP a",
			} {
				roleExecute(t, e, op, sql)
			}
			parent := retiredUnitTable(t, e, "review_temp_p")
			child := retiredUnitTable(t, e, "review_temp_c")
			leaf := retiredUnitTable(t, e, "review_temp_leaf")
			before := e.CatalogRead()
			childGraph := inheritanceMaintReviewGraphSnapshot(child.Inheritance)
			leafGraph := inheritanceMaintReviewGraphSnapshot(leaf.Inheritance)
			physical := e.Storage.Relations[child.Address()]
			var dump bytes.Buffer
			if replacement == "Reset" {
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := e.Dump(context.Background(), &dump); err != nil {
					t.Fatal(err)
				}
				if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			current := e.CatalogRead()
			if current.Epoch() == before.Epoch() || current.Get(parent.Address()) != parent || current.Get(child.Address()) != child || current.Get(leaf.Address()) != leaf || e.Storage.Relations[child.Address()] != physical {
				t.Fatal("fixture did not perform real replacement retaining exact TEMP providers")
			}
			if err := table.ValidateInheritance(current, child); err != nil {
				t.Fatal("incumbent retained-reference authority rejected valid history", err)
			}
			admitted := current.Domain().Ref(current.Epoch(), parent.Address())
			if child.Inheritance.NotNull[1].Retired[0].Parent == admitted {
				t.Fatal("fixture lacks an authentic historical/current epoch distinction")
			}
			candidate, err := table.DetachInheritance(current, child, parent, admitted)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(childGraph, child.Inheritance) || !reflect.DeepEqual(leafGraph, leaf.Inheritance) || !reflect.DeepEqual(child.Constraints, candidate.Constraints) || e.CatalogRead().Get(child.Address()) != child {
				t.Fatal("candidate mutated retained source graph, facts or publication")
			}
			for name, fact := range map[string]catalog.InheritedConstraint{"nn": candidate.Inheritance.NotNull[1], "ck": candidate.Inheritance.Checks["ck"]} {
				if fact.Local || fact.Count() != 1 || len(fact.Origins) != 0 || len(fact.Retired) != 1 || !fact.Retired[0].Detached {
					t.Fatal("detach did not archive valid retained-TEMP history", name, fact)
				}
				old := child.Inheritance.NotNull[1].Retired[0]
				if name == "ck" {
					old = child.Inheritance.Checks["ck"].Retired[0]
				}
				expected := old
				expected.Detached = true
				if fact.Retired[0] != expected {
					t.Fatal("archive changed historical identity beyond detached state", name)
				}
			}
			if candidate.Inheritance.HasParent(parent.Address()) || len(candidate.Inheritance.Parents) != 0 {
				t.Fatal("live edge survived detach")
			}
			if err := table.ValidateInheritance(current, candidate); err != nil {
				t.Fatal("detached historical candidate is invalid", err)
			}
			stage := e.BeginCatalogStage(current)
			defer e.DiscardCatalogStage(stage)
			beforeDetach, err := DomainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{parent.Address()})
			if err != nil || !slices.ContainsFunc(beforeDetach, func(edge table.DomainDependency) bool {
				return edge.Address == child.Address() && edge.Target == parent.Address() && !edge.Automatic
			}) {
				t.Fatal("fixture lacks its real pre-detach child dependency", beforeDetach, err)
			}
			if err := stage.Replace(candidate); err != nil {
				t.Fatal(err)
			}
			edges, err := DomainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{parent.Address()})
			if err != nil {
				t.Fatal(err)
			}
			rowType := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(parent.RowType)}
			for _, edge := range edges {
				array, exists := current.Type(edge.Address)
				// The former parent's own row-type array is automatically
				// collected by DROP. It is not a surviving child/history edge.
				if !edge.Automatic || edge.Root != parent.Address() || edge.Target != rowType || !exists || !array.SQLType.IsArray() || ids.OID(array.SQLType.Element().ID()) != rowType.ObjectID {
					t.Fatal("archived history retained an external former-parent drop dependency", edge)
				}
			}
			if !reflect.DeepEqual(childGraph, child.Inheritance) || !reflect.DeepEqual(leafGraph, leaf.Inheritance) || e.Storage.Relations[child.Address()] != physical {
				t.Fatal("dependency observation mutated retained source authority")
			}
		})
	}
}

func TestInheritanceMaintReviewContributionLimit(t *testing.T) {
	for _, kind := range []string{"Column", "NotNull", "Check"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			child := inheritanceUnitRelation(e, "limit_child", 90000, "a")
			incoming := inheritanceUnitRelation(e, "limit_incoming", 90001, "a")
			nextParent := inheritanceUnitRelation(e, "limit_next", 90002, "a")
			child.Inheritance = table.LocalInheritance(child)
			var sources []*catalog.Relation
			count := 1
			if kind == "Column" {
				count = 32766
			}
			column := catalog.InheritedColumn{}
			for i := range count {
				parent := inheritanceUnitRelation(e, fmt.Sprintf("limit_source_%d", i), ids.OID(100000+i), "a")
				sources = append(sources, parent)
				child.Inheritance.Parents = append(child.Inheritance.Parents, catalog.InheritanceParent{Relation: parent.Address(), Seqno: int32(i + 1)})
				column.Origins = append(column.Origins, catalog.InheritanceOrigin{Parent: parent.Address(), Attribute: 1})
			}
			child.Inheritance.Columns[1] = column
			if kind == "NotNull" {
				for i, relation := range []*catalog.Relation{child, sources[0], incoming, nextParent} {
					relation.Columns[0].Nullable = false
					relation.Constraints.NotNull = []catalog.NotNullConstraint{{OID: ids.OID(200000 + i), Name: fmt.Sprintf("nn_%d", i), Attribute: 1}}
				}
			}
			if kind == "Check" {
				usage := testUsage(e, sqltype.Bool)
				value, err := usage.Type.Input(context.Background(), "true", usage.Typmod)
				if err != nil {
					t.Fatal(err)
				}
				for i, relation := range []*catalog.Relation{child, sources[0], incoming, nextParent} {
					relation.Constraints.Check = []catalog.CheckConstraint{{OID: ids.OID(200000 + i), Name: "ck", Expression: plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: value}}}
				}
			}
			relations := append(slices.Clone(sources), child, incoming, nextParent)
			view := inheritanceUnitView(e, relations...)
			if kind != "Column" {
				fact := catalog.InheritedConstraint{Origins: []catalog.InheritanceOrigin{{Parent: sources[0].Address(), Attribute: 1, Constraint: 200001}}}
				if kind == "Check" {
					fact.Origins[0].Attribute = 0
				}
				archive := view.Domain().Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 800000})
				// Unique authentic-shaped archival certificates plus one actual live
				// provider exercise totals without 32768 SQL tables or quadratic
				// whole-history validation; candidate guards consume these totals.
				retiredName := "nn"
				if kind == "Check" {
					retiredName = "ck"
				}
				for i := range 32765 {
					fact.Retired = append(fact.Retired, catalog.RetiredInheritanceOrigin{Parent: archive, Attribute: 1, Column: "a", Constraint: ids.OID(300000 + i), Own: 200000, Name: retiredName, Lifetime: uint64(view.Epoch()), Replayed: true, Archive: 1, Detached: true, Check: kind == "Check"})
				}
				if kind == "NotNull" {
					child.Inheritance.NotNull[1] = fact
				} else {
					child.Inheritance.Checks["ck"] = fact
				}
			}
			before := inheritanceMaintReviewGraphSnapshot(child.Inheritance)
			parentBefore := *incoming
			accepted, err := table.AttachInheritance(context.Background(), view, child, incoming)
			if err != nil {
				t.Fatal("last signed16 contribution rejected", err)
			}
			if !reflect.DeepEqual(before, child.Inheritance) || !reflect.DeepEqual(parentBefore, *incoming) || &accepted.Columns[0] != &child.Columns[0] || !reflect.DeepEqual(accepted.Constraints, child.Constraints) {
				t.Fatal("last allowed contribution mutated its source or physical identities")
			}
			total := len(accepted.Inheritance.Columns[1].Origins)
			if kind == "NotNull" {
				total = accepted.Inheritance.NotNull[1].Count()
			} else if kind == "Check" {
				total = accepted.Inheritance.Checks["ck"].Count()
			}
			if total != 32767 || accepted.Inheritance.Parents[len(accepted.Inheritance.Parents)-1].Seqno != int32(count+1) {
				t.Fatal("last permitted counter or ordinal is wrong", total)
			}
			limit := inheritanceMaintReviewGraphSnapshot(accepted.Inheritance)
			nextBefore := *nextParent
			rejected, err := table.AttachInheritance(context.Background(), view, accepted, nextParent)
			var diagnostic *sqlstate.Error
			if rejected != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "54000" || diagnostic.Message != "too many inheritance parents" {
				t.Fatal("signed16 overflow was not rejected by the genuine candidate", rejected, err)
			}
			if !reflect.DeepEqual(limit, accepted.Inheritance) || !reflect.DeepEqual(before, child.Inheritance) || !reflect.DeepEqual(nextBefore, *nextParent) || &accepted.Columns[0] != &child.Columns[0] || !reflect.DeepEqual(accepted.Constraints, child.Constraints) {
				t.Fatal("overflow mutated nested origins, retired history, facts, layout or ordinals")
			}
		})
	}
}
