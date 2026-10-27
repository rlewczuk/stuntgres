package partition

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func pruningProofFixture(t *testing.T) (pruneProof, plan.ColumnExpr, *sqltype.Descriptor) {
	t.Helper()
	var integer, boolean *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
		}
		if typ.ID() == sqltype.Bool {
			boolean = typ
		}
	}
	ref := ids.ObjectRef{Epoch: 1, Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}}
	relation := &catalog.Relation{ObjectAddress: ref.Address, ObjectName: "p", Columns: []catalog.Column{{Name: "k", Usage: integer.Usage()}}, Attributes: []int32{1}, Index: map[string]int{"k": 0}}
	columnRef := ref
	columnRef.Address.SubID = 1
	column := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Origin: columnRef, Scope: 1, Frame: 1, Slot: 0}
	state := catalog.NewState(1)
	catalog.InstallBuiltinAccessMethods(state)
	handlers := routine.NativeAccessMethodProcedures(authorization.PrincipalRef{})
	for i := range handlers {
		state.SetDescriptor(handlers[i].Address(), &handlers[i])
		state.BindName(catalog.DescriptorNameKey(&handlers[i]), handlers[i].Address())
	}
	catalog.InstallBuiltinFamilies(state, authorization.PrincipalRef{})
	catalog.InstallBuiltinClasses(state, authorization.PrincipalRef{})
	procedures := routine.BuiltinProcedures(authorization.PrincipalRef{})
	for i := range procedures {
		state.SetDescriptor(procedures[i].Address(), &procedures[i])
	}
	view := catalog.NewRead(nil, state)
	evaluation := &exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}}
	return newPruneProof(context.Background(), evaluation, view, relation, ref), column, boolean
}
func pruningComparison(t *testing.T, column plan.ColumnExpr, boolean *sqltype.Descriptor, op string, n int64) expr.Expr {
	t.Helper()
	operator, ok := pruneOperator(op)
	if !ok {
		t.Fatal(op)
	}
	provider, err := sqltype.BindComparisonPredicate(operator, column.Usage.Type, column.Usage.Type, boolean, column.Usage.Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	value, err := sqltype.Integer(column.Usage.Type, n)
	if err != nil {
		t.Fatal(err)
	}
	return plan.ComparisonExpr{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Left: column, Right: plan.LiteralExpr{Info: expr.Info{Usage: column.Usage}, Value: value}, Predicate: provider, Operator: op}
}
func pruningPossible(t *testing.T, p *pruneProof, query, check expr.Expr) bool {
	t.Helper()
	root, err := p.normalize(query, false)
	if err != nil {
		t.Fatal(err)
	}
	if check != nil {
		fact, err := p.normalize(check, true)
		if err != nil {
			t.Fatal(err)
		}
		root = p.join("and", root, fact)
	}
	domains := make([]pruneDomain, len(p.terms))
	for i := range domains {
		domains[i] = pruneDomain{null: true, nonnull: true}
	}
	possible, err := p.possible(&pruneTask{node: root}, domains, func([]pruneDomain) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	return possible
}

type pruningForbiddenExpr struct {
	expr.Info
	calls *int
}

func (e pruningForbiddenExpr) Eval(expr.Context) (sqltype.Value, error) {
	*e.calls++
	return sqltype.Value{}, errors.New("forbidden proof probe")
}

func TestPartitionPruningProof(t *testing.T) {
	for _, checkNull := range []bool{false, true} {
		p, column, boolean := pruningProofFixture(t)
		null := plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Child: column}
		comparison := pruningComparison(t, column, boolean, ">", 0)
		if checkNull {
			if !pruningPossible(t, &p, null, comparison) {
				t.Fatal("CHECK UNKNOWN was treated as FALSE")
			}
		} else {
			query := plan.LogicalExpr{Info: comparison.(plan.ComparisonExpr).Info, Op: parser.LogicalAnd, Left: null, Right: comparison}
			if pruningPossible(t, &p, query, nil) {
				t.Fatal("strict comparison accepted NULL in WHERE")
			}
		}
	}
	for _, op := range []parser.LogicalOperator{parser.LogicalAnd, parser.LogicalOr} {
		p, column, boolean := pruningProofFixture(t)
		query := plan.LogicalExpr{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Op: op, Left: pruningComparison(t, column, boolean, ">", 3), Right: pruningComparison(t, column, boolean, "<", 3)}
		if got := pruningPossible(t, &p, query, nil); got != (op == parser.LogicalOr) {
			t.Fatal("AND/OR refutation", op, got)
		}
	}
	p, column, boolean := pruningProofFixture(t)
	calls := 0
	query := pruningComparison(t, column, boolean, "=", 1).(plan.ComparisonExpr)
	query.Right = pruningForbiddenExpr{Info: expr.Info{Usage: column.Usage, Property: expr.Properties{Volatility: expr.Volatile, DomainDependent: true}}, calls: &calls}
	if !pruningPossible(t, &p, query, nil) || calls != 0 {
		t.Fatal("unsupported probe was evaluated", calls)
	}
	// A different lexical owner/frame/slot cannot supply metadata facts.
	for _, change := range []func(*plan.ColumnExpr){func(c *plan.ColumnExpr) { c.Frame++ }, func(c *plan.ColumnExpr) { c.Scope++ }, func(c *plan.ColumnExpr) { c.Slot++ }, func(c *plan.ColumnExpr) { c.Origin.Epoch++ }, func(c *plan.ColumnExpr) { c.Origin.Address.ObjectID++ }} {
		other := column
		change(&other)
		if p.term(other) >= 0 {
			t.Fatal("foreign identity became a proof term", other)
		}
	}
	// Same input INT4 with an OID comparator has unsigned partition ordering.
	// A signed SQL comparison must not be substituted for that provider.
	fact, ok := p.view.LookupOperatorClass(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, 403, "oid_ops")
	if !ok {
		t.Fatal("missing admitted OID class")
	}
	class, ok := p.view.IndexClass(fact.Address().ObjectID)
	if !ok {
		t.Fatal("missing current OID class")
	}
	ordering, err := routine.BindIndexOrdering(p.view, column.Usage, class)
	if err != nil {
		t.Fatal(err)
	}
	p.named.Partition = &catalog.PartitionDefinition{Strategy: parser.PartitionRange, Keys: []catalog.PartitionKeyEntry{{Attribute: 1, Usage: column.Usage, Class: class, Comparison: ordering}}}
	p.term(column)
	terms, err := p.keyTerms(p.named)
	if err != nil || terms[0] != -1 {
		t.Fatal("incompatible comparator supplied a key fact", terms, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.ctx = ctx
	if _, err := p.normalize(query, false); !errors.Is(err, context.Canceled) {
		t.Fatal("proof suppressed cancellation", err)
	}
}

func TestPlannerSettingsPruningSelection(t *testing.T) {
	for _, kind := range []string{"range","list","default","hash"} {
		t.Run(kind,func(t *testing.T) {
			p,column,boolean:=pruningProofFixture(t)
			parent:=*p.named
			state:=catalog.NewState(1)
			for address,d:=range p.view.Descriptors() { state.SetDescriptor(address,d) }
			for key,address:=range p.view.Names() { state.BindName(key,address) }
			physical:=parent
			physical.ObjectAddress.ObjectID++
			physical.ObjectName="p_child"
			one,_:=sqltype.Integer(column.Usage.Type,1)
			ten,_:=sqltype.Integer(column.Usage.Type,10)
			twenty,_:=sqltype.Integer(column.Usage.Type,20)
			// The fixture must capture the same real support declarations as
			// partition binding, including ordering for RANGE/LIST membership.
			class,ok:=p.view.UniqueDefaultIndexClass(403,column.Usage.Type); if !ok { t.Fatal("missing ordering class") }
			ordering,err:=routine.BindIndexOrdering(p.view,column.Usage,class); if err!=nil { t.Fatal(err) }
			key:=catalog.PartitionKeyEntry{Attribute:1,Usage:column.Usage,Class:class,Comparison:ordering}
			if kind=="hash" {
				class,ok=p.view.UniqueDefaultIndexClass(405,column.Usage.Type); if !ok { t.Fatal("missing hash class") }
				hash,err:=routine.BindPartitionHash(p.view,column.Usage,class); if err!=nil { t.Fatal(err) }
				key.Class,key.Hash=class,hash
			}
			parent.Partition=&catalog.PartitionDefinition{Keys:[]catalog.PartitionKeyEntry{key},Children:[]ids.ObjectAddress{physical.Address()}}
			physical.Membership=&catalog.PartitionMembership{Parent:parent.Address()}
			switch kind {
			case "range":
				parent.Partition.Strategy=parser.PartitionRange
				physical.Membership.Lower=[]catalog.PartitionDatum{{Value:ten}}
				physical.Membership.Upper=[]catalog.PartitionDatum{{Value:twenty}}
			case "list":
				parent.Partition.Strategy=parser.PartitionList
				physical.Membership.Values=[]catalog.PartitionDatum{{Value:ten}}
			case "default":
				parent.Partition.Strategy=parser.PartitionList
				physical.Membership.DefaultBound=true
				sibling:=physical; sibling.ObjectAddress.ObjectID++; sibling.ObjectName="p_sibling"
				sibling.Membership=&catalog.PartitionMembership{Parent:parent.Address(),Strategy:parser.PartitionList,Values:[]catalog.PartitionDatum{{Value:one}}}
				parent.Partition.Children=append(parent.Partition.Children,sibling.Address())
				state.SetDescriptor(sibling.Address(),&sibling)
			case "hash":
				parent.Partition.Strategy=parser.PartitionHash
				physical.Membership.Modulus=2
				physical.Membership.Remainder=1
			}
			physical.Membership.Strategy=parent.Partition.Strategy
			state.SetDescriptor(parent.Address(),&parent)
			state.SetDescriptor(physical.Address(),&physical)
			view:=catalog.NewRead(nil,state)
			source:=&exec.Source{Ref:p.ref,Anonymous:[][]sqltype.Value{{one}},Inheritance:[]exec.InheritanceQuerySegment{{Leaf:exec.Leaf{Relation:&physical}}},InheritanceEnds:[]int{1}}
			pruner:=CapturedPruner{view:view,named:&parent,source:source,mode:"on"}
			query:=pruningComparison(t,column,boolean,"=",1)
			for _, mode:=range []string{"on","partition","off"} {
				pruner.mode=mode
				for _, membership:=range []bool{true,false} {
					selected,err:=pruner.Select(context.Background(),p.evaluation,query,membership)
					want := exec.QueryBranchSelection{Applied: membership}
					if err!=nil || !reflect.DeepEqual(selected,want) { t.Fatalf("mode=%s membership=%t selected=%+v want=%+v err=%v",mode,membership,selected,want,err) }
				}
			}
			// Constraint-only coverage below deliberately has no membership:
			// a bound must not hide an independently forbidden CHECK proof.
			ctx,cancel:=context.WithCancel(context.Background()); cancel()
			if _,err:=pruner.Select(ctx,p.evaluation,query,false); !errors.Is(err,context.Canceled) { t.Fatal(err) }
		})
	}
	for _, topology := range []string{"ordinary", "only-child", "inherited"} {
		t.Run(topology, func(t *testing.T) {
			for _, fact := range []string{"check", "check-null", "not-null", "false"} {
				t.Run(fact, func(t *testing.T) {
					p, column, boolean := pruningProofFixture(t)
					named := *p.named
					physical := named
					if topology == "inherited" {
						physical.ObjectAddress.ObjectID++
						physical.ObjectName = "p_child"
					}
					if topology != "ordinary" {
						parentAddress := named.Address()
						if topology == "only-child" {
							parentAddress.ObjectID += 10
						}
						physical.Inheritance = &catalog.TableInheritance{Parents: []catalog.InheritanceParent{{Relation: parentAddress, Seqno: 1}}}
					}
					checkColumn := column
					checkColumn.Origin.Address = physical.Address()
					checkColumn.Origin.Address.SubID = 1
					query := pruningComparison(t, column, boolean, "=", 1)
					switch fact {
					case "check", "check-null":
						physical.Constraints.Check = []catalog.CheckConstraint{{Expression: pruningComparison(t, checkColumn, boolean, ">=", 10)}}
					case "not-null":
						physical.Constraints.NotNull = []catalog.NotNullConstraint{{Attribute: 1}}
					}
					if fact == "check-null" || fact == "not-null" {
						query = plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Child: column}
					}
					if fact == "false" {
						query = plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}
					}
					source := &exec.Source{Ref: p.ref, Anonymous: make([][]sqltype.Value, 3)}
					if topology == "inherited" {
						// Exact nonadjacent retained intervals prove the parent's
						// occurrence and the unconstrained trailing sibling survive.
						sibling := named
						sibling.ObjectAddress.ObjectID += 2
						source.Anonymous = make([][]sqltype.Value, 7)
						source.Inheritance = []exec.InheritanceQuerySegment{{Leaf: exec.Leaf{Relation: &named}}, {Leaf: exec.Leaf{Relation: &physical}}, {Leaf: exec.Leaf{Relation: &sibling}}}
						source.InheritanceEnds = []int{2, 5, 7}
					} else {
						named = physical
					}
					for _, mode := range []string{"on", "partition", "off"} {
						for _, membership := range []bool{true, false} {
							pruner := CapturedPruner{view: p.view, named: &named, source: source, mode: mode}
							excluded := fact == "false" || fact != "check-null" && (mode == "on" || mode == "partition" && topology == "inherited")
							want := exec.QueryBranchSelection{Applied: excluded}
							if excluded && topology == "inherited" && fact != "false" {
								want.Intervals = []exec.QueryBranchInterval{{Start: 0, End: 2}, {Start: 5, End: 7}}
							}
							got, err := pruner.Select(context.Background(), p.evaluation, query, membership)
							if err != nil || !reflect.DeepEqual(got, want) {
								t.Fatalf("mode=%s membership=%t got=%+v want=%+v err=%v", mode, membership, got, want, err)
							}
							proof, err := NewBranchProof(context.Background(), p.evaluation, p.view, &named, p.ref, query, mode, topology == "inherited")
							if err != nil {
								t.Fatal(err)
							}
							possible, err := proof.Possible(&physical, membership)
							if err != nil || possible == excluded {
								t.Fatalf("metadata feasibility disagrees: possible=%t excluded=%t err=%v", possible, excluded, err)
							}
						}
					}
				})
			}
		})
	}
}
