package schemacmds_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
		"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestLtreeComparisonTransport(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	source, sourceExtension, sourceType := ltreeUnitInstalled(t)
	sourceView := source.CatalogRead()
	value, err := sourceType.SQLType.Input(ctx, "a.b", -1)
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := sqltype.BindComparison(sourceType.SQLType, sourceType.SQLType, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Use the established genuine retained TEMP-provider import fixture.
	// Public Controls separately covers displaced destination replacement.
	target := engine.New()
	owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
	t.Cleanup(closeOwner)
	namespace := intarrayUnitTemporaryNamespace(t, target, owner)
	installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
	extensionUnitPublish(t, target, installer, cubeUnitStage(t, target, installer, "unaccent", namespace.Name()))
	closeInstaller()
	if _, err := target.ReplaceCatalog(ctx, source, sourceView); err != nil {
		t.Fatal(err)
	}
	view := target.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"}))
	imported := view.MustType(installation.Members[3])
	if imported.SQLType.ID() == sourceType.SQLType.ID() {
		t.Fatal("fixture did not remap the ltree signature type")
	}
	published := view.State()
	if len(published.ScalarImports()) != 0 {
		t.Fatal("publication retained candidate-only scalar import authority")
	}
	// ReplaceCatalog has already published and cleared its temporary scalar
	// translations. Retagging an independent source literal requires the same
	// explicit source/destination authority on an unpublished candidate.
	candidate := catalog.CloneState(published)
	candidate.SetScalarImport(sourceType.SQLType, imported.SQLType)
	view = view.WithState(candidate)
	mapType := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		if old == sourceType.SQLType {
			return imported.SQLType
		}
		current, _ := view.LookupType(old.ID())
		return current
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{sourceExtension.Address(): installation.Address()}
	for i, address := range sourceExtension.Members {
		mapping[address] = installation.Members[i]
	}
	mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sourceType.Array)}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(imported.Array)}
	sourceSupport,_:=sourceView.FamilySupport(sourceExtension.Members[extension.LtreeBtreeFamilySlot].ObjectID,ids.OID(sourceType.SQLType.ID()),ids.OID(sourceType.SQLType.ID()),1)
	targetSupport,_:=view.FamilySupport(installation.Members[extension.LtreeBtreeFamilySlot].ObjectID,ids.OID(imported.SQLType.ID()),ids.OID(imported.SQLType.ID()),1)
	mapping[sourceSupport.Address()]=targetSupport.Address()
	mapper := stored.ViewQueryRemapper{
		Catalog: view, Type: mapType,
		Ref: func(ref ids.ObjectRef) ids.ObjectRef {
			address := ref.Address
			if mapped, ok := mapping[address]; ok {
				address = mapped
			}
			return view.Owner().Domain.Ref(view.Epoch(), address)
		},
		Procedure: extension.ProcedureRemapper(mapping, replacementProcedurePolicyFixture(source, target), nil),
	}
	mapOriginal := func(node expr.Expr) (expr.Expr, error) {
		query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{node}}})
		if err != nil {
			return nil, err
		}
		return query.(*plan.ValuesNode).Rows[0][0], nil
	}
	boolean, _ := sourceView.LookupType(sqltype.Bool)
	equality := sourceView.MustProcedure(sourceExtension.Members[extension.LtreeComparisonProcedureStart])
	inequality := sourceView.MustProcedure(sourceExtension.Members[extension.LtreeComparisonProcedureStart+1])
	literal := plan.LiteralExpr{Info: expr.Info{Usage: sourceType.SQLType.Usage()}, Value: value}
	null := plan.LiteralExpr{Info: literal.Info, Value: sqltype.Null(sourceType.SQLType)}
	t.Run("captured_ordering",func(t *testing.T){
		class,exists,err:=sourceView.DefaultIndexClass(403,sourceType.SQLType)
		if err!=nil||!exists{t.Fatal("source installed default",err)}
		bound,err:=routine.BindClassSupport(sourceView,class);if err!=nil{t.Fatal(err)}
		order,capture,err:=routine.BindCapturedIndexOrdering(sourceView,sourceType.SQLType.Usage(),class);if err!=nil{t.Fatal(err)}
		key:=plan.SortKey{Expression:literal,Comparison:order.DatumComparison(),Ordering:capture}
		for _,mutate:=range []func(*sqltype.IndexSupport){
			func(s *sqltype.IndexSupport){s.Routine=uint32(sourceExtension.Members[extension.LtreeComparisonProcedureStart].ObjectID)},
			func(s *sqltype.IndexSupport){s.Member++},
			func(s *sqltype.IndexSupport){s.Revision++},
			func(s *sqltype.IndexSupport){s.ClassRevision++},
			func(s *sqltype.IndexSupport){s.FamilyRevision++},
			func(s *sqltype.IndexSupport){s.RoutineRevision++},
		}{
			stamp:=bound.Support(1);mutate(&stamp);wrongClass:=bound.WithSupport(1,stamp)
			wrong,err:=sqltype.BindIndexOrdering(sourceType.SQLType.Usage(),wrongClass);if err!=nil{t.Fatal("wrong-support fixture lost correct operand admission",err)}
			if !wrong.DatumComparison().OrderingFor(sourceType.SQLType.Usage()){t.Fatal("wrong-support fixture changed operand generation")}
			if _,err:=catalog.CaptureIndexOrdering(sourceView,sourceType.SQLType.Usage(),wrongClass,wrong);err==nil{t.Fatal("numeric support flags minted source authority")}
		}
		for _,kind:=range []string{"sort","window"}{t.Run(kind,func(t *testing.T){
			build:=func(k plan.SortKey)plan.Node{
				if kind=="sort"{return &plan.SortNode{Child:&plan.ValuesNode{},Keys:[]plan.SortKey{k}}}
				return &plan.WindowNode{Child:&plan.ValuesNode{},Specs:[]plan.WindowSpec{{Order:[]plan.SortKey{k},Frame:plan.WindowFrame{Unit:parser.FrameRows}}}}
			}
			extract:=func(node plan.Node)plan.SortKey{
				if kind=="sort"{return node.(*plan.SortNode).Keys[0]}
				return node.(*plan.WindowNode).Specs[0].Order[0]
			}
			original:=build(key)
			sourceDeps,err:=stored.QueryDependRefs(ctx,sourceView,nil,&plan.Query{Root:original});if err!=nil{t.Fatal("captured ordering dependencies",err)}
			for _,ref:=range capture.References(){found:=false;for _,address:=range sourceDeps{if address==ref.Address{found=true;break}};if !found{t.Fatal("lost captured support dependency",ref.Address)}}
			m:=mapper.Fresh();mapped,err:=m.Query(original);if err!=nil{t.Fatal("mapped installed ordering",err)}
			got:=extract(mapped)
			if got.Expression.ResultUsage().Type!=imported.SQLType||got.Comparison.DefaultBtreeOpclass()!=uint32(installation.Members[extension.LtreeBtreeClassSlot].ObjectID)||!got.Comparison.OrderingFor(imported.SQLType.Usage()){t.Fatal("lost mapped class/type ordering capture")}
			if got.Ordering==nil||got.Ordering==capture{t.Fatal("destination borrowed original source capture")}
			if err:=got.Ordering.Validate(imported.SQLType.Usage(),got.Comparison);err!=nil{t.Fatal("destination lost genuine support capture",err)}
			a,err:=imported.SQLType.Input(ctx,"a.b",-1);if err!=nil{t.Fatal(err)}
			b,err:=imported.SQLType.Input(ctx,"a-b",-1);if err!=nil{t.Fatal(err)}
			if n,err:=got.Comparison.Compare(ctx,a,b,sqltype.InputEnvironment{});err!=nil||n!=-1{t.Fatal("mapped typed order",n,err)}
			// The real reference-only scalar traversal cannot rebind types or
			// select a builtin/default class while moving nested query references.
			nested:=plan.SubqueryExpr{QueryPlan:&plan.Query{Root:original}}
			relocated,err:=stored.RemapExpression(nested,mapper.Ref,nil)
			if err!=nil||!extract(relocated.(plan.SubqueryExpr).QueryPlan.Root).Comparison.Equal(key.Comparison)||extract(relocated.(plan.SubqueryExpr).QueryPlan.Root).Ordering!=capture{t.Fatal("reference-only pass replaced source ordering",err)}
			if !extract(original).Comparison.Equal(key.Comparison)||extract(original).Ordering!=capture||extract(original).Expression.(plan.LiteralExpr).Value!=value{t.Fatal("ordering remap mutated source")}
			for _,cause:=range []string{"missing_class","hash_class","ownerless","bad_support","unbound_source","foreign_source_generation","missing_source_capture","new_source_generation","new_source_class_generation","source_member_mutation","source_routine_mutation","source_routine_revision","source_family_mutation","source_class_mutation","foreign_mapped_provenance","default_displaced"}{
				t.Run(cause,func(t *testing.T){
					bad:=mapper.Fresh();node:=original
					rollback:=func(){};defer func(){rollback()}()
					switch cause{
					case "missing_class","hash_class":
						bad.Ref=func(ref ids.ObjectRef)ids.ObjectRef{
							if ref.Address.ClassID==ids.OperatorClassCatalogID{
								address:=ids.ObjectAddress{}
								if cause=="hash_class"{address=installation.Members[extension.LtreeHashClassSlot]}
								return view.Owner().Domain.Ref(view.Epoch(),address)
							}
							return mapper.Ref(ref)
						}
					case "ownerless":bad.Catalog=catalog.NewRead(nil,view.State())
					case "bad_support":
						state:=catalog.CloneState(view.State());member,_:=view.FamilySupport(installation.Members[extension.LtreeBtreeFamilySlot].ObjectID,ids.OID(imported.SQLType.ID()),ids.OID(imported.SQLType.ID()),1)
						next:=*member;next.Function=installation.Members[extension.LtreeComparisonProcedureStart].ObjectID;state.SetDescriptor(next.Address(),&next);bad.Catalog=view.WithState(state)
					case "unbound_source":
						invalid:=key;invalid.Comparison,err=sqltype.BindComparison(sourceType.SQLType,sourceType.SQLType,sqltype.CollationRef{},false);if err!=nil{t.Fatal(err)};node=build(invalid)
					case "foreign_source_generation":
						foreign,err:=sqltype.Ltree(sourceType.SQLType.ID(),sourceType.Name(),sourceType.SQLType.SQLName());if err!=nil{t.Fatal(err)}
						wrong,err:=sqltype.BindIndexOrdering(foreign.Usage(),bound);if err!=nil{t.Fatal(err)}
						invalid:=key;invalid.Comparison=wrong.DatumComparison();node=build(invalid)
					case "missing_source_capture":
						invalid:=key;invalid.Ordering=nil;node=build(invalid)
					case "new_source_generation":
						state:=catalog.CloneState(sourceView.State());member:=*sourceSupport;member.ObjectRevision++;state.SetDescriptor(member.Address(),&member)
						_,newCapture,err:=routine.BindCapturedIndexOrdering(sourceView.WithState(state),sourceType.SQLType.Usage(),class);if err!=nil{t.Fatal("genuine new support generation",err)}
						invalid:=key;invalid.Ordering=newCapture;node=build(invalid)
					case "new_source_class_generation":
						state:=catalog.CloneState(sourceView.State());fact,_:=sourceView.OperatorClass(sourceExtension.Members[extension.LtreeBtreeClassSlot].ObjectID);changed:=*fact;changed.ObjectRevision++;state.SetDescriptor(changed.Address(),&changed)
						_,newCapture,err:=routine.BindCapturedIndexOrdering(sourceView.WithState(state),sourceType.SQLType.Usage(),class);if err!=nil{t.Fatal("genuine new class generation",err)}
						invalid:=key;invalid.Ordering=newCapture;node=build(invalid)
					case "source_member_mutation":
						old:=*sourceSupport;rollback=func(){*sourceSupport=old};sourceSupport.ObjectRevision++
					case "source_routine_mutation":
						procedure:=sourceView.MustProcedure(sourceExtension.Members[extension.LtreeComparisonProcedureStart+6]);old:=*procedure;rollback=func(){*procedure=old};procedure.Display.Source="ltree_eq"
					case "source_routine_revision":
						procedure:=sourceView.MustProcedure(sourceExtension.Members[extension.LtreeComparisonProcedureStart+6]);old:=*procedure;rollback=func(){*procedure=old};procedure.ObjectRevision++
					case "source_family_mutation":
						family,_:=sourceView.OperatorFamily(sourceExtension.Members[extension.LtreeBtreeFamilySlot]);old:=*family;rollback=func(){*family=old};family.ObjectRevision++
					case "source_class_mutation":
						fact,_:=sourceView.OperatorClass(sourceExtension.Members[extension.LtreeBtreeClassSlot].ObjectID);old:=*fact;rollback=func(){*fact=old};fact.ObjectRevision++
					case "foreign_mapped_provenance":
						bad.Ref=func(ref ids.ObjectRef)ids.ObjectRef{next:=mapper.Ref(ref);return sourceView.Owner().Domain.Ref(next.Epoch,next.Address)}
					case "default_displaced":
						state:=catalog.CloneState(view.State());installedClass,_:=view.OperatorClass(installation.Members[extension.LtreeBtreeClassSlot].ObjectID)
						retiredDefault:=*installedClass;retiredDefault.Default=false;retiredDefault.ObjectRevision++;state.SetDescriptor(retiredDefault.Address(),&retiredDefault)
						target.Mu.Lock();oid,allocationErr:=target.Catalog.AllocateOIDLocked();revision,revisionErr:=target.Catalog.RevisionLocked();target.Mu.Unlock();if allocationErr!=nil||revisionErr!=nil{t.Fatal(allocationErr,revisionErr)}
						alternative:=*installedClass;alternative.ObjectAddress.ObjectID=oid;alternative.ObjectRevision=revision;alternative.ObjectName="ltree_displaced_default";alternative.Extension=ids.ObjectAddress{};state.SetDescriptor(alternative.Address(),&alternative)
						state.BindName(catalog.DescriptorNameKey(&alternative),alternative.Address())
						displaced:=view.WithState(state)
						if err:=catalog.Validate(catalog.OperatorClassProvider{},displaced,&alternative);err!=nil{t.Fatal("genuine alternative class",err)}
						currentDefault,exists,err:=displaced.DefaultIndexClass(403,imported.SQLType);if err!=nil||!exists||currentDefault.OID()!=uint32(oid)||currentDefault.OID()==got.Comparison.DefaultBtreeOpclass(){t.Fatal("default displacement was not observable",err)}
						if err:=extension.StockLtree.ValidateExtension(displaced,installation);err==nil{t.Fatal("stock default displacement unexpectedly authorized")}
						bad.Catalog=displaced
					}
					if _,err:=bad.Query(node);err==nil{t.Fatal("unauthorized ordering capture accepted",cause)}
					rollback();rollback=func(){}
					if err:=capture.Validate(sourceType.SQLType.Usage(),key.Comparison);err!=nil{t.Fatal("rejection mutated immutable original capture",err)}
					if !extract(original).Comparison.Equal(key.Comparison)||extract(original).Ordering!=capture||extract(original).Expression.(plan.LiteralExpr).Value!=value{t.Fatal("rejected ordering remap mutated source")}
				})
			}
		})}
	})
	trueValue := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
	falseValue := plan.LiteralExpr{Info: trueValue.Info, Value: sqltype.Boolean(boolean, false)}
	equalInfo := expr.Info{Usage: boolean.Usage(), RoutineReference: equality}
	notEqualInfo := expr.Info{Usage: boolean.Usage(), RoutineReference: inequality}
	equalRef := sourceView.Owner().Domain.Ref(sourceView.Epoch(), equality.Address())
	notEqualRef := sourceView.Owner().Domain.Ref(sourceView.Epoch(), inequality.Address())
	equalPredicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, sourceType.SQLType, sourceType.SQLType, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	notEqualPredicate, err := sqltype.BindComparisonPredicate(sqltype.CompareNotEqual, sourceType.SQLType, sourceType.SQLType, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	pair := plan.ComparisonExpr{Info: equalInfo, RoutineRef: equalRef, Left: literal, Right: literal, Predicate: equalPredicate, Operator: "="}
	for _, tc := range []struct {
		name string
		node expr.Expr
		want bool
	}{
		{"shared_in", plan.ScalarArrayComparisonExpr{Info: equalInfo, RoutineRef: equalRef, Left: literal, Items: []expr.Expr{literal, null}, Predicate: equalPredicate, Operator: "="}, true},
		{"shared_not_in", plan.ScalarArrayComparisonExpr{Info: notEqualInfo, RoutineRef: notEqualRef, Left: literal, Items: []expr.Expr{literal, null}, Predicate: notEqualPredicate, Operator: "<>", All: true}, false},
		{"shared_nullif", plan.ConditionalExpr{Info: expr.Info{Usage: sourceType.SQLType.Usage(), RoutineReference: equality}, RoutineRef: equalRef, Kind: parser.NullIf, Args: []expr.Expr{literal, literal}, Comparison: comparison}, false},
		{"shared_case", plan.CaseExpr{Info: trueValue.Info, Test: literal, Arms: []plan.CaseBranch{{Test: plan.CaseTestExpr{Info: literal.Info}, TestUsage: literal.ResultUsage(), Condition: literal, Result: trueValue, Equality: comparison, Routine: equality, RoutineRef: equalRef}}, Otherwise: falseValue}, true},
		{"shared_comparison", pair, true},
		{"shared_distinct", plan.DistinctExpr{Info: equalInfo, RoutineRef: equalRef, Left: literal, Right: literal, Equality: equalPredicate}, false},
		{"shared_row", plan.RowComparisonExpr{Info: trueValue.Info, Comparisons: []plan.ComparisonExpr{pair, pair}, Operator: "="}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			procedure := equality
			member := extension.LtreeComparisonProcedureStart
			if tc.name == "shared_not_in" {
				procedure, member = inequality, extension.LtreeComparisonProcedureStart+1
			}
			current := view.MustProcedure(installation.Members[member])
			originalSQL, err := (stored.ScalarRenderer{}).SQL(ctx, sourceView, tc.node)
			if err != nil {
				t.Fatal("source rendering", err)
			}
			if tc.name == "shared_in" || tc.name == "shared_not_in" {
				if strings.Contains(originalSQL, "ARRAY[") || !strings.Contains(originalSQL, " IN (") {
					t.Fatal("scalar membership rendered as an unsupported public array", originalSQL)
				}
			}
			relocated, err := stored.RemapExpression(tc.node, func(ref ids.ObjectRef) ids.ObjectRef {
				if ref.Address != procedure.Address() {
					t.Fatal("unexpected selected reference", ref)
				}
				return view.Owner().Domain.Ref(view.Epoch(), current.Address())
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			mapped, err := mapOriginal(tc.node)
			if err != nil {
				t.Fatal("shared carrier relocation", err)
			}
			plan.WalkExpression(mapped, func(node expr.Expr) {
				if datum,ok:=node.(plan.LiteralExpr);ok&&datum.Value.Type()==imported.SQLType{
					back,err:=datum.Value.RemapLtree(sourceType.SQLType)
					want:=value;if datum.Value.IsNull(){want=sqltype.Null(sourceType.SQLType)}
					if err!=nil||back!=want{t.Fatal("transport copied or mutated immutable operand backing",err)}
				}
				if ref := expr.InfoOf(node).RoutineReference; ref != nil {
					captured, ok := ref.(*catalog.Procedure)
					if !ok || !exec.SameRoutineDeclaration(current, captured) || captured.Revision() != procedure.Revision() {
						t.Fatal("incoherent original routine", ref)
					}
				}
				if node.ResultUsage().Type == sourceType.SQLType {
					t.Fatal("source operand type retained")
				}
				if branch, ok := node.(plan.CaseExpr); ok {
					for _, arm := range branch.Arms {
						if !exec.SameRoutineDeclaration(current, arm.Routine) || arm.Routine.Revision() != procedure.Revision() || arm.RoutineRef != view.Owner().Domain.Ref(view.Epoch(), current.Address()) {
							t.Fatal("source CASE branch capture retained")
						}
					}
				}
			})
			collector := stored.DependRefs{Context: ctx, Catalog: view}
			collector.Expression(mapped)
			if _, found := collector.Addresses[current.Address()]; collector.Err != nil || !found {
				t.Fatal("missing selected procedure dependency", collector.Err)
			}
			if row, ok := mapped.(plan.RowComparisonExpr); ok {
				for _, pair := range row.Comparisons {
					captured, ok := pair.RoutineReference.(*catalog.Procedure)
					if !ok || !exec.SameRoutineDeclaration(current, captured) || captured.Revision() != procedure.Revision() || pair.RoutineRef != view.Owner().Domain.Ref(view.Epoch(), current.Address()) {
						t.Fatal("source row-pair routine capture retained")
					}
				}
				subquery := plan.SubqueryExpr{Comparisons: row.Comparisons, QueryPlan: &plan.Query{Root: &plan.SingletonNode{}}}
				queryDependencies := stored.DependRefs{Context: ctx, Catalog: view}
				queryDependencies.Expression(subquery)
				if _, found := queryDependencies.Addresses[current.Address()]; queryDependencies.Err != nil || !found {
					t.Fatal("missing selected subquery-pair procedure dependency", queryDependencies.Err)
				}
			}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: view}}
			result, err := mapped.Eval(&evaluation)
			if err != nil {
				t.Fatal("mapped execution", err)
			}
			if tc.name == "shared_nullif" {
				if !result.IsNull() || result.Type() != imported.SQLType {
					t.Fatal("NULLIF lost ltree result while equality returns BOOL", result)
				}
			} else if truth, err := result.Boolean(); err != nil || result.IsNull() || truth != tc.want {
				t.Fatal("mapped truth", result, err)
			}
			if after, err := (stored.ScalarRenderer{}).SQL(ctx, sourceView, tc.node); err != nil || after != originalSQL || procedure.Signature != ltreeUnitComparisonSignature(sourceType.SQLType.ID()) {
				t.Fatal("source capture mutated", after, err)
			}
			missing, err := stored.RemapExpression(tc.node, func(ids.ObjectRef) ids.ObjectRef { return ids.ObjectRef{} }, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := stored.RetagTypes(view, missing, mapType); err == nil {
				t.Fatal("recovered a missing mapped shared identity")
			}
			if _, _, err := stored.RetagTypes(catalog.NewRead(nil, view.State()), relocated, mapType); err == nil {
				t.Fatal("shared carrier admitted ownerless authority")
			}
			if _, _, err := stored.RetagTypes(view.WithState(published), relocated, mapType); err == nil {
				t.Fatal("shared source literals admitted without scalar import authority")
			}
			foreign, err := stored.RemapExpression(tc.node, func(ids.ObjectRef) ids.ObjectRef {
				return sourceView.Owner().Domain.Ref(view.Epoch(), current.Address())
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := stored.RetagTypes(view, foreign, mapType); err == nil {
				t.Fatal("shared carrier admitted a foreign mapped reference domain")
			}
			stale, err := stored.RemapExpression(tc.node, func(ids.ObjectRef) ids.ObjectRef {
				return view.Owner().Domain.Ref(view.Epoch()+1, current.Address())
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := stored.RetagTypes(view, stale, mapType); err == nil {
				t.Fatal("shared carrier admitted a stale mapped reference epoch")
			}
			invalidManifest := catalog.CloneState(view.State())
			invalidInstallation := *installation
			invalidInstallation.Version = "invalid"
			invalidManifest.SetDescriptor(installation.Address(), &invalidInstallation)
			if _, _, err := stored.RetagTypes(view.WithState(invalidManifest), relocated, mapType); err == nil {
				t.Fatal("shared carrier admitted an invalid stock manifest")
			}
			for _, corrupt := range []func(*catalog.Procedure){
				func(p *catalog.Procedure) { p.Signature = extension.LquerySignature(imported.SQLType.ID()) + ",25" },
				func(p *catalog.Procedure) { p.Signature = "25," + extension.LquerySignature(imported.SQLType.ID()) },
				func(p *catalog.Procedure) { p.Result = 23 },
				func(p *catalog.Procedure) { p.ObjectName = "ltree_lt" },
				func(p *catalog.Procedure) { p.Implementation = extension.StockCitext },
			} {
				bad := catalog.CloneState(view.State())
				p := *current
				corrupt(&p)
				bad.SetDescriptor(p.Address(), &p)
				if _, _, err := stored.RetagTypes(view.WithState(bad), relocated, mapType); err == nil {
					t.Fatal("shared carrier admitted a corrupt selected declaration")
				}
			}
		})
	}

}

func ltreeUnitComparisonSignature(id sqltype.ID) catalog.ProcedureSignature {s:=extension.LquerySignature(id);return s+","+s}
