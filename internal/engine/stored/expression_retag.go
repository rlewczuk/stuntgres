package stored

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Replacement can retain TEMP sequence builtin generations. Rebind persisted
// scalar trees to those types without evaluating them or changing captured
// values, positions or expression properties.
func RetagTypes(view catalog.Read, node expr.Expr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...typedef.CollationRemapper) (expr.Expr, bool, error) {
	retainedType = analyze.RecordTypeMapper(retainedType, collations...)
	return RetagTypesWithQuery(view, node, retainedType, func(root plan.Node) (plan.Node, error) {
		if (view.State() == nil || view.State().ACLItemImport() == nil) && (len(collations) == 0 || collations[0] == nil) && !analyze.QueryDescriptorsChanged(root, retainedType) && !analyze.QueryRoutineCarriersChanged(view, root) {
			return root, nil
		}
		return remapViewQueryMapped(view, root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, retainedType, collations...)
	}, collations...)
}

func RetagTypesWithQuery(view catalog.Read, node expr.Expr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, queryMapper func(plan.Node) (plan.Node, error), collations ...typedef.CollationRemapper) (expr.Expr, bool, error) {
	return MapExpression(view, node, retainedType, queryMapper, nil, nil, collations...)
}

// MapExpression shares the complete copy-on-change traversal with catalog
// retagging. A leaf rewrite runs before children, without evaluating expressions.
func MapExpression(view catalog.Read, node expr.Expr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, queryMapper func(plan.Node) (plan.Node, error), recipe expr.RoutineRecipeReference, leaf func(expr.Expr) (expr.Expr, bool, error), collations ...typedef.CollationRemapper) (expr.Expr, bool, error) {
	return MapExpressionReader(view, nil, node, retainedType, queryMapper, recipe, false, leaf, collations...)
}

// substituteSources is only for equivalent-child query-stage rewrites. Catalog
// generation remapping keeps Rebuild/Retag and their provider validation.
func MapExpressionReader(view catalog.Read, reader catalog.DefinitionReader, node expr.Expr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, queryMapper func(plan.Node) (plan.Node, error), recipe expr.RoutineRecipeReference, substituteSources bool, leaf func(expr.Expr) (expr.Expr, bool, error), collations ...typedef.CollationRemapper) (expr.Expr, bool, error) {
	// This entry is a structural type rewrite, not catalog reprovenance.
	// Replacement callers supply their actual original-source policy below.
	retain := func(original *catalog.Procedure) (*catalog.Procedure, error) { return original, nil }
	return mapExpressionWithProcedure(view, reader, node, retainedType, queryMapper, recipe, substituteSources, leaf, retain, collations...)
}

func mapExpressionWithProcedure(view catalog.Read, reader catalog.DefinitionReader, node expr.Expr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, queryMapper func(plan.Node) (plan.Node, error), recipe expr.RoutineRecipeReference, substituteSources bool, leaf func(expr.Expr) (expr.Expr, bool, error), procedure catalog.ProcedureRemapper, collations ...typedef.CollationRemapper) (expr.Expr, bool, error) {
	if node == nil {
		return nil, false, nil
	}
	if leaf != nil {
		next, changed, err := leaf(node)
		if changed || err != nil {
			return next, changed, err
		}
	}
	if unresolved, ok := node.(plan.UnknownOutput); ok {
		// Retained contextual literal analysis owns no descriptor or value.
		// Executable query validation still rejects unresolved output nodes.
		literal, ok := unresolved.Raw.(parser.LiteralExpr)
		if !ok || unresolved.ResultUsage() != (sqltype.Usage{}) || literal.Value.Kind != parser.StringValue && literal.Value.Kind != parser.NullValue {
			return nil, false, fmt.Errorf("stuntgres: invalid contextual view output")
		}
		return node, false, nil
	}
	info := expr.InfoOf(node)
	if leaf != nil {
		info.Property = RewrittenIntrinsicProperties(node)
	}
	typ := retainedType(info.Usage.Type)
	if typ == nil && info.Usage.Type != nil {
		return nil, false, fmt.Errorf("stuntgres: invalid remapped expression type")
	}
	changed := typ != info.Usage.Type
	info.Usage.Type = typ
	if _, failure := exec.ExpressionRecipeDescriptor(node); failure != nil {
		return nil, false, failure
	}
	if recipe != nil && info.RoutineRecipeReference != recipe {
		info.RoutineRecipeReference = recipe
		changed = true
	}
	var err error
	originalProcedure := func(original *catalog.Procedure) (*catalog.Procedure, bool) {
		var next *catalog.Procedure
		next, err = procedure(original)
		return next, err == nil && next != nil
	}
	if info.RoutineReference != nil {
		captured, failure := exec.ExpressionRoutineDescriptor(node)
		if failure != nil {
			return nil, false, failure
		}
		if view.State() != nil {
			current, ok := originalProcedure(captured)
			if err != nil {
				return nil, false, err
			}
			if capture, installed := hstoreComparisonCaptureOf(node); ok && installed {
				if failure := remapHstoreComparisonRoutine(view, reader, captured, current, capture, retainedType); failure != nil {
					return nil, false, failure
				}
			}
			if capture, installed := ltreeComparisonCaptureOf(node); ok && installed {
				if failure := remapLtreeComparisonRoutine(view, reader, captured, current, capture, retainedType); failure != nil {
					return nil, false, failure
				}
			}
			if capture, isCube := cubeComparisonCaptureOf(node); ok && isCube {
				if failure := remapCubeComparisonRoutine(view, reader, captured, current, capture, retainedType); failure != nil {
					return nil, false, failure
				}
			}
			if capture, isCitext := citextComparisonCaptureOf(node); isCitext {
				if failure := remapCitextComparisonRoutine(view, reader, captured, current, capture, retainedType); failure != nil {
					return nil, false, failure
				}
			}
			if capture, isCitext := citextPatternCaptureOf(node); isCitext {
				if failure := remapCitextPatternRoutine(view, reader, captured, current, capture, retainedType); failure != nil {
					return nil, false, failure
				}
			}
			if call, isCitext := citextRegexpCallOf(node); isCitext {
				if failure := remapCitextRegexpRoutine(view, reader, captured, current, call, retainedType); failure != nil {
					return nil, false, failure
				}
			}
			if call, isHstore := node.(functions.HstoreRecord); isHstore {
				if failure := remapHstoreRecordRoutine(view, reader, captured, current, call, retainedType); failure != nil {
					return nil, false, failure
				}
			}
			if call, isHstore := node.(functions.HstoreConstruct); isHstore {
				if failure := remapHstoreConstructRoutine(view, reader, captured, current, call, retainedType); failure != nil {
					return nil, false, failure
				}
			}
			if call, isHstore := node.(functions.HstoreRows); isHstore {
				if failure := remapHstoreRowsRoutine(view, reader, captured, current, call, retainedType); failure != nil {
					return nil, false, failure
				}
			}
			if call, isHstore := node.(functions.HstoreArray); isHstore {
				if failure := remapHstoreArrayRoutine(view, reader, captured, current, call, retainedType); failure != nil {
					return nil, false, failure
				}
			}
			if call, isLtree := node.(functions.LtreeArray); isLtree {
				if failure := remapLtreeArrayRoutine(view, reader, captured, current, call, retainedType); failure != nil {
					return nil, false, failure
				}
			}
   if call, isLtree := node.(functions.LtreeLtxtquery); isLtree {
    if failure := remapLtreeLtxtqueryRoutine(view, reader, captured, current, call, retainedType); failure != nil { return nil, false, failure }
   }
			if call, isLtree := node.(functions.LtreeHierarchy); isLtree {
				if failure := remapLtreeHierarchyRoutine(view, reader, captured, current, call, retainedType); failure != nil { return nil, false, failure }
			}
   if call, isLtree := node.(functions.LtreePath); isLtree {
    if failure := remapLtreePathRoutine(view, reader, captured, current, call, retainedType); failure != nil { return nil, false, failure }
   }
   if call, isLtree := node.(functions.LtreeLquery); isLtree {
    if failure := remapLtreeLqueryRoutine(view, reader, captured, current, call, retainedType); failure != nil { return nil, false, failure }
   }
			if call, isHstore := node.(functions.HstoreJSON); isHstore {
				if failure := remapHstoreJSONRoutine(view, reader, captured, current, call, retainedType); failure != nil {
					return nil, false, failure
				}
			}
			if call, isCitext := citextStringCallOf(node); isCitext {
				if failure := remapCitextStringRoutine(view, reader, captured, current, call, retainedType); failure != nil {
					return nil, false, failure
				}
			}
			sameSignature := ok && current.Signature == captured.Signature
			if captured.RangeConstructor != nil {
				sameSignature = ok && current.RangeConstructor != nil && current.RangeConstructor.Form == captured.RangeConstructor.Form && current.RangeConstructor.Type == retainedType(captured.RangeConstructor.Type)
			}
			if ok && captured.SQL != nil {
				var collation typedef.CollationRemapper
				if len(collations) != 0 {
					collation = collations[0]
				}
				sameSignature, err = sameMappedSQLRoutine(current, captured, retainedType, collation)
				if err != nil {
					return nil, false, err
				}
			} else if ok && !sameSignature {
				// Symbolic type-address mapping may change a provider's numeric
				// signature. Validate the mapped ORIGINAL against the target;
				// do not replace it with the target's newer descriptor.
				descriptor, _ := catalog.DescriptorAt(view, reader, current.Address())
				target, _ := descriptor.(*catalog.Procedure)
				sameSignature = exec.SameRoutineDeclaration(target, current)
			}
			if cube, cubeCall := node.(plan.InstalledCubeExpr); cubeCall {
				sameSignature = ok && sameMappedCubeSignature(cube, current, retainedType)
			} else if earth, earthCall := node.(plan.InstalledEarthExpr); earthCall {
				sameSignature = ok && sameMappedEarthSignature(earth, current, retainedType)
			} else if installed, spatialCall := node.(plan.InstalledCubeSpatialExpr); spatialCall {
				mapped := retainedType(installed.Args[0].ResultUsage().Type)
				sameSignature = ok && extension.CubeProviderOf(current) == extension.StockCube && extension.CubeProviderOf(captured) == extension.StockCube && mapped != nil && mapped.IsCube() && current.Signature == extension.CubeBinarySignature(mapped.ID())
			}
			if !ok || !sameSignature || current.RoutineKind != captured.RoutineKind || current.Builtin != captured.Builtin || current.Implementation != captured.Implementation || current.AccessMethod != captured.AccessMethod || current.Conversion != captured.Conversion || current.Foreign != captured.Foreign {
				return nil, false, fmt.Errorf("stuntgres: missing remapped routine declaration")
			}
			changed = changed || current != captured
			info.RoutineReference = current
		}
		// A catalog-free structural rewrite preserves this already-owned
		// identity. It neither relocates the callable nor admits its execution.
	}
	if len(collations) != 0 && collations[0] != nil {
		info.Usage.Collation, err = collations[0](info.Usage.Collation)
		if err != nil {
			return nil, false, err
		}
		changed = changed || info.Usage.Collation != node.ResultUsage().Collation
		info.CollationLeft, err = collations[0](info.CollationLeft)
		if err != nil {
			return nil, false, err
		}
		info.CollationRight, err = collations[0](info.CollationRight)
		if err != nil {
			return nil, false, err
		}
		oldInfo := expr.InfoOf(node)
		changed = changed || info.CollationLeft != oldInfo.CollationLeft || info.CollationRight != oldInfo.CollationRight
	}
	comparisonCollation := func(ref sqltype.CollationRef) sqltype.CollationRef {
		if err != nil || len(collations) == 0 || collations[0] == nil {
			return ref
		}
		next, failure := collations[0](ref)
		err = failure
		changed = changed || next != ref
		return next
	}
	mapChild := func(old expr.Expr) (expr.Expr, bool, error) {
		next, replaced, failure := mapExpressionWithProcedure(view, reader, old, retainedType, queryMapper, recipe, substituteSources, leaf, procedure, collations...)
		changed = changed || replaced
		if next != nil && (leaf != nil || replaced) {
			info.Property = expr.MergeProperties(info.Property, next.Properties())
		}
		return next, replaced, failure
	}
	child := func(old expr.Expr) expr.Expr {
		if err != nil {
			return old
		}
		var next expr.Expr
		next, _, err = mapChild(old)
		return next
	}
	children := func(old []expr.Expr) []expr.Expr {
		var args []expr.Expr
		for i, arg := range old {
			next, replaced, failure := mapChild(arg)
			if failure != nil {
				err = failure
				return old
			}
			if replaced {
				if args == nil {
					args = append([]expr.Expr(nil), old...)
				}
				args[i] = next
			}
		}
		if args != nil {
			return args
		}
		return old
	}
	var rebound expr.Expr
	switch n := node.(type) {
	case plan.SQLRoutineExpr:
		n.Args = children(n.Args)
		inputs := make([]sqltype.Usage, len(n.Signature.Inputs))
		for i, usage := range n.Signature.Inputs {
			inputs[i] = usage
			inputs[i].Type = retainedType(usage.Type)
			inputs[i].Collation = comparisonCollation(usage.Collation)
			changed = changed || inputs[i] != usage
		}
		n.Signature.Inputs, n.Signature.Result = inputs, info.Usage
		n.InputCollation = comparisonCollation(n.InputCollation)
		if err != nil {
			return nil, false, err
		}
		rebound, err = n.Rebuild(info, n.Args)
	case plan.AssignmentValueExpr:
		n.Base, n.Value = child(n.Base), child(n.Value)
		targets, failure := MapRuleAssignmentTargets([]*plan.ArrayAssignmentTarget{n.Target}, mapChild, func(usage sqltype.Usage) (sqltype.Usage, error) {
			usage.Type = retainedType(usage.Type)
			usage.Collation = comparisonCollation(usage.Collation)
			return usage, err
		})
		if failure != nil {
			return nil, false, failure
		}
		changed = changed || targets[0] != n.Target
		n.Target = targets[0]
		n.Info = info
		rebound = n
	case plan.UnnestExpr:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		n.Info = info
		if n.Procedure != nil {
			proc, ok := originalProcedure(n.Procedure)
			if err != nil {
				return nil, false, err
			}
			if !ok || extension.IntaggProviderOf(proc) != extension.StockIntagg {
				return nil, false, fmt.Errorf("stuntgres: missing remapped intagg enumerator")
			}
			changed = changed || n.Procedure != proc
			n.Procedure = proc
			if _, err := currentIntaggEnumerator(view, n); err != nil {
				return nil, false, err
			}
		}
		rebound = n
	case plan.InstalledEarthExpr:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		proc, ok := originalProcedure(n.Procedure)
		if !ok || extension.EarthdistanceProviderOf(proc) != extension.StockEarthdistance {
			return nil, false, fmt.Errorf("missing remapped earth function")
		}
		earth := retainedType(n.Plan.Earth())
		changed = changed || proc != n.Procedure || earth != n.Plan.Earth()
		n.EarthCoordinate, err = functions.NewEarthCoordinate(info, n.Plan.Operation(), earth, n.Args)
		if err != nil {
			return nil, false, err
		}
		n.Procedure = proc
		if err := ValidateInstalledEarth(n, view); err != nil {
			return nil, false, err
		}
		rebound = n
	case plan.InstalledCubeExpr:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		proc, ok := originalProcedure(n.Procedure)
		if err != nil {
			return nil, false, err
		}
		if !ok || extension.CubeProviderOf(proc) != extension.StockCube {
			return nil, false, fmt.Errorf("stuntgres: missing remapped cube function")
		}
		changed = changed || n.Procedure != proc || n.CubeType != retainedType(n.CubeType)
		n.Procedure, n.CubeType = proc, retainedType(n.CubeType)
		n.Info = info
		if err = ValidateInstalledCube(n, view); err != nil {
			return nil, false, err
		}
		rebound = n
	case plan.CitextComparisonExpr:
		n.Args = [2]expr.Expr{child(n.Args[0]), child(n.Args[1])}
		if err != nil {
			return node, false, err
		}
		if len(collations) != 0 && collations[0] != nil && n.Collation.ID != 0 {
			mapped, failure := collations[0](n.Collation)
			if failure != nil {
				return nil, false, failure
			}
			changed = changed || mapped != n.Collation
			n.Collation = mapped
		}
		if changed {
			rebound, err = n.Rebuild(info, n.Args[:])
		}
	case plan.CitextPatternExpr:
		n.Args = [2]expr.Expr{child(n.Args[0]), child(n.Args[1])}
		if err != nil {
			return node, false, err
		}
		if len(collations) != 0 && collations[0] != nil && n.Collation.ID != 0 {
			mapped, failure := collations[0](n.Collation)
			if failure != nil {
				return nil, false, failure
			}
			changed = changed || mapped != n.Collation
			n.Collation = mapped
		}
		text := retainedType(n.Text.Type)
		changed = changed || text != n.Text.Type
		n.Text.Type = text
		if changed {
			rebound, err = n.Rebuild(info, n.Args[:])
		}
	case plan.HstoreComparisonExpr:
		source := n
		if captured, ok := n.RoutineReference.(*catalog.Procedure); ok && captured != nil {
			// Reference relocation precedes declaration/type remapping.
			source.Ref.Address = captured.Address()
		}
		if err := source.ValidateStoredProvider(); err != nil {
			return node, false, err
		}
		capture, _ := hstoreComparisonCaptureOf(n)
		result, ok := view.LookupType(capture.result)
		if !ok || info.Usage.Type != result {
			return node, false, fmt.Errorf("stuntgres: foreign hstore comparison result")
		}
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		if changed {
			rebound, err = n.Rebuild(info, n.Args)
		}
	case plan.CubeComparisonExpr:
		n.Args = [2]expr.Expr{child(n.Args[0]), child(n.Args[1])}
		if err != nil {
			return node, false, err
		}
		if changed {
			rebound, err = n.Rebuild(info, n.Args[:])
		}
	case plan.InstalledPageinspectExpr:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		proc, ok := originalProcedure(n.Procedure)
		if !ok || extension.PageinspectProviderOf(proc) != extension.StockPageinspect {
			return nil, false, fmt.Errorf("stuntgres: missing remapped pageinspect function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure, info.RoutineReference = proc, proc
		n.Info = info
		if err = ValidateInstalledPageinspect(n, view); err != nil {
			return nil, false, err
		}
		rebound = n
	case plan.InstalledAmcheckExpr:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		proc, ok := originalProcedure(n.Procedure)
		if !ok || extension.AmcheckProviderOf(proc) != extension.StockAmcheck {
			return nil, false, fmt.Errorf("stuntgres: missing remapped amcheck function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure, info.RoutineReference = proc, proc
		n.Info = info
		if err = ValidateInstalledAmcheck(n, view); err != nil {
			return nil, false, err
		}
		rebound = n
	case plan.InstalledSoundexExpr:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		proc, ok := originalProcedure(n.Procedure)
		if !ok || extension.FuzzystrmatchProviderOf(proc) != extension.StockFuzzystrmatch {
			return nil, false, fmt.Errorf("stuntgres: missing remapped Soundex function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure = proc
		n.Soundex, err = n.Reconstruct(info, n.Args)
		if err == nil {
			err = ValidateInstalledSoundex(n, view)
		}
		rebound = n
	case plan.InstalledMetaphoneExpr:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		proc, ok := originalProcedure(n.Procedure)
		if !ok || extension.FuzzystrmatchProviderOf(proc) != extension.StockFuzzystrmatch {
			return nil, false, fmt.Errorf("stuntgres: missing remapped Metaphone function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure = proc
		n.Metaphone, err = n.Reconstruct(info, n.Args)
		if err == nil {
			err = ValidateInstalledMetaphone(n, view)
		}
		rebound = n
	case plan.InstalledLevenshteinExpr:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		proc, ok := originalProcedure(n.Procedure)
		if !ok || extension.FuzzystrmatchProviderOf(proc) != extension.StockFuzzystrmatch {
			return nil, false, fmt.Errorf("stuntgres: missing remapped Levenshtein function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure = proc
		n.Levenshtein, err = n.Reconstruct(info, n.Args)
		if err == nil {
			err = ValidateInstalledLevenshtein(n, view)
		}
		rebound = n
	case plan.InstalledUUIDOsspExpr:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		proc, ok := originalProcedure(n.Procedure)
		if !ok || extension.UUIDOsspProviderOf(proc) != extension.StockUUIDOssp {
			return nil, false, fmt.Errorf("stuntgres: missing remapped uuid-ossp function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure = proc
		n.UUIDGeneration, err = n.Reconstruct(info, n.Args, nil)
		if err == nil {
			err = ValidateInstalledUUIDOssp(n, view)
		}
		rebound = n
	case plan.InstalledCryptoExpr:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		proc, ok := originalProcedure(n.Procedure)
		if !ok || extension.PgcryptoProviderOf(proc) != extension.StockPgcrypto {
			return nil, false, fmt.Errorf("stuntgres: missing remapped crypto function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure = proc
		n.Pgcrypto, err = n.Reconstruct(info, n.Args)
		if err == nil {
			err = ValidateInstalledCrypto(n, view)
		}
		rebound = n
	case plan.InstalledEarthPointExpr:
		n.Args = [2]expr.Expr{child(n.Args[0]), child(n.Args[1])}
		if err != nil {
			return node, false, err
		}
		proc, ok := originalProcedure(n.Procedure)
		if err != nil {
			return nil, false, err
		}
		if !ok || extension.EarthdistanceProviderOf(proc) != extension.StockEarthdistance {
			return nil, false, fmt.Errorf("stuntgres: missing remapped earth point function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure = proc
		n.EarthPoint, err = functions.NewEarthPoint(info, n.Args)
		if err == nil {
			err = ValidateInstalledEarthPoint(n, view)
		}
		rebound = n
	case plan.InstalledCubeSpatialExpr:
		n.Args = [2]expr.Expr{child(n.Args[0]), child(n.Args[1])}
		if err != nil {
			return node, false, err
		}
		proc, ok := originalProcedure(n.Procedure)
		if err != nil {
			return nil, false, err
		}
		if !ok || extension.CubeProviderOf(proc) != extension.StockCube {
			return nil, false, fmt.Errorf("stuntgres: missing remapped cube spatial function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure = proc
		n.CubeSpatial, err = functions.NewCubeSpatial(info, n.Plan.Operation(), n.Args)
		if err == nil {
			err = ValidateInstalledCubeSpatial(n, view)
		}
		rebound = n
	case plan.InstalledTextExpr:
		n.Arg = child(n.Arg)
		proc, ok := originalProcedure(n.Procedure)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: missing remapped installed function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure = proc
		n.Info = info
		if ValidateInstalledText(n, view) != nil {
			return nil, false, fmt.Errorf("stuntgres: invalid remapped installed function")
		}
		rebound = n
	case plan.InstalledConnectbyExpr:
		args := make([]expr.Expr, len(n.Args))
		for i, arg := range n.Args {
			args[i] = child(arg)
		}
		n.Args = args
		proc, ok := originalProcedure(n.Procedure)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: missing remapped connectby function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure, n.Info = proc, info
		err = ValidateInstalledConnectby(n, view)
		rebound = n
	case plan.InstalledTablefuncNormalExpr:
		for i, arg := range n.Args {
			n.Args[i] = child(arg)
		}
		proc, ok := originalProcedure(n.Procedure)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: missing remapped normal_rand function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure, n.Info = proc, info
		err = ValidateInstalledTablefuncNormal(n, view)
		rebound = n
	case plan.InstalledPgrowlocksExpr:
		n.Arg = child(n.Arg)
		proc, ok := originalProcedure(n.Procedure)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: missing remapped pgrowlocks function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure, n.Info = proc, info
		err = ValidateInstalledPgrowlocks(n, view)
		rebound = n
	case plan.InstalledTablefuncCrosstabExpr:
		n.Args = append([]expr.Expr(nil), n.Args...)
		for i, arg := range n.Args {
			n.Args[i] = child(arg)
		}
		proc, ok := originalProcedure(n.Procedure)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: missing remapped crosstab function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure, n.Info = proc, info
		err = ValidateInstalledTablefuncCrosstab(n, view)
		rebound = n
	case plan.InstalledVerifyHeapamExpr:
		for i, arg := range n.Args {
			n.Args[i] = child(arg)
		}
		proc, ok := originalProcedure(n.Procedure)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: missing remapped verify_heapam function")
		}
		changed = changed || n.Procedure != proc
		n.Procedure, n.Info = proc, info
		err = ValidateInstalledVerifyHeapam(n, view)
		rebound = n
	case plan.ViewQualificationExpr:
		mapped, replaced, failure := exec.MapViewQualification(n, mapChild)
		if failure != nil {
			return nil, false, failure
		}
		changed = changed || replaced
		n = mapped.(plan.ViewQualificationExpr)
		n.Info = info
		rebound = n
	case plan.RowExpr:
		var replaced bool
		n, replaced, err = plan.MapRowAnalysis(n, mapChild)
		changed = changed || replaced
		if err == nil {
			var projected bool
			n, projected, err = n.RetagFieldProjection(info.Usage.Type.BaseType())
			changed = changed || projected
		}
		n.Info = info
		rebound = n
	case plan.FieldExpr:
		n.Base = child(n.Base)
		if err != nil {
			return nil, false, err
		}
		index, ok := n.Base.ResultUsage().Type.BaseType().CompositeAttributeIndex(n.Attribute)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: retired remapped field")
		}
		if row, constructor := n.Base.(plan.RowExpr); constructor {
			index = row.FieldSourceIndex(index)
			if index < 0 {
				return nil, false, fmt.Errorf("stuntgres: missing retained row field")
			}
		}
		changed = changed || index != n.Index
		n.Index = index
		n.Info = info
		rebound = n
	case plan.ArrayExpr:
		var elements []expr.Expr
		for i, old := range n.Elements {
			next, replaced, failure := mapChild(old)
			if failure != nil {
				return nil, false, failure
			}
			if replaced {
				changed = true
				if elements == nil {
					elements = append([]expr.Expr(nil), n.Elements...)
				}
				elements[i] = next
			}
		}
		if !changed {
			return node, false, err
		}
		if elements != nil {
			n.Elements = elements
		}
		n.Info = info
		rebound = n
	case plan.SubscriptExpr:
		var replaced bool
		n, replaced, err = plan.MapSubscript(n, mapChild)
		if err != nil {
			return nil, false, err
		}
		changed = changed || replaced
		n.Info = info
		rebound = n
	case plan.TableOIDExpr:
		n.Info = info
		rebound = n
	case plan.SubqueryExpr:
		if queryMapper != nil {
			root, failure := queryMapper(n.QueryPlan.Root)
			if failure != nil {
				return nil, false, failure
			}
			if root != n.QueryPlan.Root {
				copy := *n.QueryPlan
				copy.Root = root
				n.QueryPlan = &copy
				changed = true
				if leaf != nil {
					plan.VisitCTEQuery(root, func(plan.Node) {}, func(expr expr.Expr) {
						properties := expr.Properties()
						info.Property.Volatility = max(info.Property.Volatility, properties.Volatility)
						info.Property.ParameterDependent = info.Property.ParameterDependent || properties.ParameterDependent
						info.Property.DomainDependent = info.Property.DomainDependent || properties.DomainDependent
						info.Property.RuntimeDomainConversion = info.Property.RuntimeDomainConversion || properties.RuntimeDomainConversion
					}, true)
				}
			}
		}
		var comparisons []plan.ComparisonExpr
		for i, old := range n.Comparisons {
			next := child(old)
			if err != nil {
				return nil, false, err
			}
			if changed && comparisons == nil {
				comparisons = append([]plan.ComparisonExpr(nil), n.Comparisons...)
			}
			if comparisons != nil {
				comparisons[i] = next.(plan.ComparisonExpr)
			}
		}
		var expansionChanged bool
		n.LeftExpansions, expansionChanged, err = plan.MapRowComparisonExpansions(n.LeftExpansions, mapChild)
		if err != nil {
			return nil, false, err
		}
		changed = changed || expansionChanged
		if n.LeftConstructor != nil {
			n.LeftConstructor = child(n.LeftConstructor)
			if err != nil {
				return nil, false, err
			}
		}
		if n.JSONArray != nil {
			copy := *n.JSONArray
			copy.Input = child(copy.Input)
			if err != nil {
				return nil, false, err
			}
			copy.Plan, err = sqltype.BindJSONArrayAggregate(copy.Input.ResultUsage(), info.Usage, true)
			if err != nil {
				return nil, false, err
			}
			n.JSONArray = &copy
		}
		if !changed {
			return node, false, nil
		}
		if comparisons != nil {
			n.Comparisons = comparisons
		}
		if len(n.Ordering) != 0 {
			n.Ordering = make([]sqltype.Comparison, len(n.Comparisons))
			for i, c := range n.Comparisons {
				ref, conflict := c.Predicate.InputCollation()
				n.Ordering[i], err = sqltype.BindComparison(c.Left.ResultUsage().Type, c.Right.ResultUsage().Type, ref, conflict)
				if err != nil {
					return nil, false, err
				}
			}
		}
		if len(n.Membership) != 0 {
			n.Membership = make([]sqltype.Grouping, len(n.Comparisons))
			for i, pair := range n.Comparisons {
				n.Membership[i], err = pair.Predicate.EqualityGrouping()
				if err != nil {
					return nil, false, err
				}
			}
		}
		n.Info = info
		rebound = n
	case plan.UpdateOutputExpr:
		n.Info = info
		rebound = n
	case plan.ParameterExpr:
		if !changed {
			return node, false, nil
		}
		n.Info = info
		rebound = n
	case plan.ColumnExpr:
		n.Virtual = child(n.Virtual)
		n.Info = info
		rebound = n
	case plan.ReturningColumnExpr:
		if n.Value != nil {
			n.Value = child(n.Value)
			n.Info = info
			rebound = n
			break
		}
		next := child(n.ColumnExpr)
		column, ok := next.(plan.ColumnExpr)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: invalid RETURNING column remap")
		}
		n.ColumnExpr = column
		rebound = n
	case plan.ReturningWholeRowExpr:
		var members []expr.Expr
		for i, member := range n.Members {
			next := child(member)
			if changed && members == nil {
				members = append([]expr.Expr(nil), n.Members...)
			}
			if members != nil {
				members[i] = next
			}
		}
		if members != nil {
			n.Members = members
		}
		n.Info = info
		rebound = n
	case plan.MergeActionExpr:
		n.Info = info
		rebound = n
	case plan.GroupExpr:
		n.Info = info
		rebound = n
	case plan.AggregateExpr:
		n.Info = info
		rebound = n
	case plan.GroupingExpr:
		var args []expr.Expr
		for i, arg := range n.Args {
			next, replaced, failure := mapChild(arg)
			if failure != nil {
				return nil, false, failure
			}
			if replaced {
				changed = true
				if args == nil {
					args = append([]expr.Expr(nil), n.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			n.Args = args
		}
		n.Info = info
		rebound = n
	case *plan.WindowExpr:
		if !changed {
			return node, false, nil
		}
		copy := *n
		copy.Info = info
		rebound = &copy
	case plan.CachedOrderExpr:
		n.Info = info
		rebound = n
	case plan.ExtensionPathsExpr:
		n.Arg = child(n.Arg)
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n.Info = info
		if err := n.Validate(); err != nil {
			return node, false, err
		}
		rebound = n
	case plan.CaseExpr:
		n.Test, n.Otherwise = child(n.Test), child(n.Otherwise)
		var arms []plan.CaseBranch
		for i, old := range n.Arms {
			arm := old
			arm.Test = child(arm.Test)
			arm.Condition, arm.Result = child(arm.Condition), child(arm.Result)
			if arm.Routine != nil && view.State() != nil {
				current, ok := originalProcedure(arm.Routine)
				if err != nil {
					return nil, false, err
				}
				if !ok {
					return nil, false, fmt.Errorf("stuntgres: missing remapped CASE equality declaration")
				}
				if old.TestUsage.Type.IsLtree() || old.Condition.ResultUsage().Type.IsLtree() {
					if failure := remapLtreeComparisonRoutine(view, reader, old.Routine, current, ltreeComparisonCapture{ref: old.RoutineRef, left: old.TestUsage.Type, right: old.Condition.ResultUsage().Type, name: "ltree_eq", result: sqltype.Bool}, retainedType); failure != nil {
						return nil, false, failure
					}
				} else if old.TestUsage.Type.IsHstore() || old.Condition.ResultUsage().Type.IsHstore() {
					if failure := remapHstoreComparisonRoutine(view, reader, old.Routine, current, hstoreComparisonCapture{ref: old.RoutineRef, left: old.TestUsage.Type, right: old.Condition.ResultUsage().Type, name: "hstore_eq", result: sqltype.Bool}, retainedType); failure != nil {
						return nil, false, failure
					}
				} else if old.TestUsage.Type.IsCube() || old.Condition.ResultUsage().Type.IsCube() || old.RoutineRef != (ids.ObjectRef{}) {
					if failure := remapCubeComparisonRoutine(view, reader, old.Routine, current, cubeComparisonCapture{ref: old.RoutineRef, left: old.TestUsage.Type, right: old.Condition.ResultUsage().Type, name: "cube_eq", result: sqltype.Bool}, retainedType); failure != nil {
						return nil, false, failure
					}
				}
				changed = changed || arm.Routine != current
				arm.Routine = current
			}
			ref, conflict := arm.Equality.InputCollation()
			ref = comparisonCollation(ref)
			if n.Test != nil && err == nil {
				arm.TestUsage.Type = retainedType(arm.TestUsage.Type)
				if len(collations) != 0 && collations[0] != nil {
					arm.TestUsage.Collation, err = collations[0](arm.TestUsage.Collation)
					if err != nil {
						return nil, false, err
					}
				}
				arm.Equality, err = sqltype.BindComparison(arm.TestUsage.Type, arm.Condition.ResultUsage().Type, ref, conflict)
			}
			if changed && arms == nil {
				arms = append([]plan.CaseBranch(nil), n.Arms...)
			}
			if arms != nil {
				arms[i] = arm
			}
		}
		if !changed {
			return node, false, err
		}
		if arms != nil {
			n.Arms = arms
		}
		n.Info = info
		if err := n.ValidateStoredProvider(); err != nil {
			return nil, false, err
		}
		rebound = n
	case plan.ConditionalExpr:
		ref, conflict := n.Comparison.InputCollation()
		ref = comparisonCollation(ref)
		var args []expr.Expr
		for i, old := range n.Args {
			next := child(old)
			if changed && args == nil {
				args = append([]expr.Expr(nil), n.Args...)
			}
			if args != nil {
				args[i] = next
			}
		}
		if !changed {
			return node, false, err
		}
		if args != nil {
			n.Args = args
		}
		if err == nil && n.Kind != parser.Coalesce {
			left, right := typ, typ
			if n.Kind == parser.NullIf {
				left, right = n.Args[0].ResultUsage().Type, n.Args[1].ResultUsage().Type
			}
			if n.Kind == parser.Greatest || n.Kind == parser.Least {
				n.Comparison, n.MissingOrdering, err = analyze.BindExtremaComparison(info.Usage, conflict)
			} else {
				n.Comparison, err = sqltype.BindComparison(left, right, ref, conflict)
			}
		}
		n.Info = info
		rebound = n
	case plan.EarthCheckExpr:
		if !n.Valid() {
			return nil, false, fmt.Errorf("stuntgres: invalid retagged earth CHECK")
		}
		mapped := child(n.Value)
		if err != nil {
			return nil, false, err
		}
		value, ok := mapped.(plan.DomainValueExpr)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: foreign earth CHECK child")
		}
		n.Value = value
		if !changed {
			return node, false, nil
		}
		n.Info = info
		if !n.Valid() {
			return nil, false, fmt.Errorf("stuntgres: invalid earth CHECK generation")
		}
		if _, err := earthCheckSQL(view, n); err != nil {
			return nil, false, err
		}
		rebound = n
	case plan.DomainValueExpr:
		if !changed {
			return node, false, nil
		}
		n.Info = info
		rebound = n
	case plan.LiteralExpr:
		info.UnknownNull = n.UnknownNull
		info.RegclassRef = n.RegclassRef
		if n.UnknownInput != nil {
			if err := n.UnknownInput.Validate(n.Usage, n.Value); err != nil {
				return nil, false, err
			}
			chain, chainChanged, failure := expr.RetagTransparentInputCasts(n.UnknownInput.Chain, retainedType)
			if failure != nil {
				return nil, false, failure
			}
			if chainChanged {
				marker := *n.UnknownInput
				marker.Chain = chain
				n.UnknownInput = &marker
				changed = true
			}
			if n.UnknownInput.CastType != nil {
				target := retainedType(n.UnknownInput.CastType)
				if target == nil {
					return nil, false, fmt.Errorf("stuntgres: missing remapped unknown cast target")
				}
				if target != n.UnknownInput.CastType {
					marker := *n.UnknownInput
					marker.CastType = target
					n.UnknownInput = &marker
					changed = true
				}
			}
		}
		if !changed && (view.State() == nil || view.State().ACLItemImport() == nil || !n.Value.Type().ContainsACLItem()) {
			return node, false, nil
		}
		var collation typedef.CollationRemapper
		if len(collations) != 0 {
			collation = collations[0]
		}
		previous := n.Value
		n.Value, err = typedef.ReownDomainDatum(n.Value, typ, collation, view.State().ScalarImports(), view.State().ACLItemImport())
		if err != nil {
			return nil, false, err
		}
		changed = changed || n.Value != previous
		if !changed {
			return node, false, nil
		}
		n.Info = info
		rebound = n
	case plan.CollationExpr:
		n.Child = child(n.Child)
		if !changed {
			return node, false, err
		}
		n.Info = info
		rebound = n
	case plan.CoercionExpr:
		if n.XMLSerializeResult {
			if _, _, failure := plan.XMLSerialization(n); failure != nil {
				return nil, false, failure
			}
		}
		if _, failure := n.LogicalInputCast(); failure != nil {
			return nil, false, failure
		}
		n.Child = child(n.Child)
		if n.UnknownInputCast != nil {
			chain, chainChanged, failure := expr.RetagTransparentInputCasts(n.UnknownInputCast.Transparent, retainedType)
			if failure != nil {
				return nil, false, failure
			}
			if chainChanged {
				marker := *n.UnknownInputCast
				marker.Transparent = chain
				n.UnknownInputCast = &marker
				changed = true
			}
			target := retainedType(n.UnknownInputCast.Target)
			if target == nil {
				return nil, false, fmt.Errorf("stuntgres: missing remapped logical input cast target")
			}
			if target != n.UnknownInputCast.Target || n.Child.ResultUsage() != n.UnknownInputCast.Source {
				marker := *n.UnknownInputCast
				marker.Target, marker.Source = target, n.Child.ResultUsage()
				n.UnknownInputCast = &marker
				changed = true
			}
		}
		if !changed {
			return node, false, err
		}
		// The bound expression already distinguishes semantic authority from
		// storage/formal destinations. Map that captured Usage and structure;
		// rederiving from the child would erase the destination contract.
		if changed && err == nil {
			n.Plan, err = n.Plan.Rebind(n.Child.ResultUsage(), info.Usage, n.Mode)
		}
		n.Info = info
		if err == nil {
			_, err = n.LogicalInputCast()
		}
		if err == nil && n.XMLSerializeResult {
			_, _, err = plan.XMLSerialization(n)
		}
		rebound = n
	case plan.BitwiseExpr:
		n.Left, n.Right = child(n.Left), child(n.Right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var rt *sqltype.Descriptor
			if n.Right != nil {
				rt = n.Right.ResultUsage().Type
			}
			n.Provider, n.Bits, n.Network, n.MAC = sqltype.Bitwise{}, sqltype.BitStringBitwise{}, sqltype.NetworkBitwise{}, sqltype.MACBitwise{}
			if info.Usage.Type.ID() == sqltype.Macaddr || info.Usage.Type.ID() == sqltype.Macaddr8 {
				n.MAC, err = sqltype.BindMACBitwise(n.Operator, n.Left.ResultUsage().Type, rt)
			} else if info.Usage.Type.ID() == sqltype.Inet {
				n.Network, err = sqltype.BindNetworkBitwise(n.Operator, n.Left.ResultUsage().Type, rt)
			} else if info.Usage.Type.ID() == sqltype.Bit {
				n.Bits, err = sqltype.BindBitStringBitwise(n.Operator, n.Left.ResultUsage().Type, rt)
			} else {
				n.Provider, err = sqltype.BindBitwise(n.Operator, n.Left.ResultUsage().Type, rt)
			}
		}
		n.Info = info
		rebound = n
	case plan.SelectedOperatorExpr:
		n.Provider = child(n.Provider)
		if view.State() != nil {
			if current, ok := view.Operator(n.Ref.Address); ok {
				changed = changed || current != n.Operator
				n.Operator = current
			}
		}
		if !changed {
			return node, false, err
		}
		n.Info = info
		rebound = n
	case plan.ArithmeticExpr:
		n.Left, n.Right = child(n.Left), child(n.Right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var rightType *sqltype.Descriptor
			if n.Right != nil {
				rightType = n.Right.ResultUsage().Type
			}
			n.Provider, err = sqltype.BindArithmetic(n.Operator, n.Left.ResultUsage().Type, rightType, info.Usage.Type)
		}
		n.Info = info
		rebound = n
	case plan.LikeExpr:
		n.Left, n.Right = child(n.Left), child(n.Right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var collation expr.Info
			collation, err = analyze.CollatedInfo(view, expr.Info{Usage: n.Left.ResultUsage(), Pos: info.Pos}, n.Left, n.Right)
			if err == nil {
				n.Provider, err = sqltype.BindLike(n.Operator, n.Left.ResultUsage(), n.Right.ResultUsage(), info.Usage, collation.CollationConflict)
			}
		}
		n.Info = info
		rebound = n
	case plan.RowComparisonExpr:
		var pairs []plan.ComparisonExpr
		for i, pair := range n.Comparisons {
			next := child(pair)
			if err != nil {
				return nil, false, err
			}
			if changed && pairs == nil {
				pairs = append([]plan.ComparisonExpr(nil), n.Comparisons...)
			}
			if pairs != nil {
				pairs[i] = next.(plan.ComparisonExpr)
			}
		}
		for side, expansions := range n.Expansions {
			var replaced bool
			n.Expansions[side], replaced, err = plan.MapRowComparisonExpansions(expansions, mapChild)
			if err != nil {
				return nil, false, err
			}
			changed = changed || replaced
		}
		if !changed {
			return node, false, nil
		}
		if pairs != nil {
			n.Comparisons = pairs
		}
		if len(n.Ordering) != 0 {
			n.Ordering = make([]sqltype.Comparison, len(n.Comparisons))
			for i, pair := range n.Comparisons {
				ref, conflict := pair.Predicate.InputCollation()
				n.Ordering[i], err = sqltype.BindComparison(pair.Left.ResultUsage().Type, pair.Right.ResultUsage().Type, ref, conflict)
				if err != nil {
					return nil, false, err
				}
			}
		}
		n.Info = info
		rebound = n
	case plan.ScalarArrayComparisonExpr:
		ref, conflict := n.Predicate.InputCollation()
		ref = comparisonCollation(ref)
		var replaced bool
		n, replaced, err = plan.MapScalarArrayChildren(n, mapChild)
		changed = changed || replaced
		if !changed {
			return node, false, err
		}
		if err == nil {
			operator, ok := analyze.ComparisonOperator(n.Operator)
			if !ok {
				return nil, false, fmt.Errorf("stuntgres: invalid array comparison operator")
			}
			n.Predicate, err = sqltype.BindComparisonPredicate(operator, n.Left.ResultUsage().Type, n.ElementType(), typ, ref, conflict)
		}
		n.Info = info
		rebound = n
	case plan.ComparisonExpr:
		ref, conflict := n.Predicate.InputCollation()
		ref = comparisonCollation(ref)
		n.Left, n.Right = child(n.Left), child(n.Right)
		if !changed {
			return node, false, err
		}
		if changed && err == nil {
			var operator sqltype.ComparisonOperator
			switch n.Operator {
			case "=":
				operator = sqltype.CompareEqual
			case "<>":
				operator = sqltype.CompareNotEqual
			case "<":
				operator = sqltype.CompareLess
			case "<=":
				operator = sqltype.CompareLessEqual
			case ">":
				operator = sqltype.CompareGreater
			case ">=":
				operator = sqltype.CompareGreaterEqual
			default:
				return nil, false, fmt.Errorf("stuntgres: invalid default comparison")
			}
			n.Predicate, err = sqltype.BindComparisonPredicate(operator, n.Left.ResultUsage().Type, n.Right.ResultUsage().Type, typ, ref, conflict)
		}
		n.Info = info
		rebound = n
	case plan.NullTestExpr:
		n.Child = child(n.Child)
		if !changed {
			return node, false, err
		}
		n.Info = info
		rebound = n
	case plan.TruthTestExpr:
		n.Child = child(n.Child)
		if !changed {
			return node, false, err
		}
		n.Info = info
		rebound = n
	case plan.DistinctExpr:
		ref, conflict := n.Equality.InputCollation()
		ref = comparisonCollation(ref)
		n.Left, n.Right = child(n.Left), child(n.Right)
		if !changed {
			return node, false, err
		}
		if changed && err == nil {
			n.Equality, err = sqltype.BindComparisonPredicate(sqltype.CompareEqual, n.Left.ResultUsage().Type, n.Right.ResultUsage().Type, typ, ref, conflict)
		}
		n.Info = info
		rebound = n
	case plan.LogicalExpr:
		n.Left, n.Right = child(n.Left), child(n.Right)
		if !changed {
			return node, false, err
		}
		n.Info = info
		rebound = n
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence.
		var mapped []expr.Expr
		mapped, err = mapCompositeChildren(n, mapChild)
		if err != nil {
			return node, false, err
		}
		if _, source := n.(expr.Source); substituteSources && source {
			if changed {
				rebound, err = expr.Substitute(n, info, mapped)
			}
		} else if hook, ok := n.(expr.Retaggable); ok {
			rebound, changed, err = hook.Retag(info, mapped, changed, retagEnvironment(view, retainedType, collations))
		} else if changed {
			rebound, err = n.Rebuild(info, mapped)
		}
	default:
		return nil, false, fmt.Errorf("stuntgres: unsupported stored default node")
	}
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return node, false, nil
	}
	return rebound, true, nil
}

// mapCompositeChildren maps a protocol node's children, copying the slice only
// when a child is replaced.
func mapCompositeChildren(node expr.Composite, mapChild func(expr.Expr) (expr.Expr, bool, error)) ([]expr.Expr, error) {
	children := node.Children()
	var mapped []expr.Expr
	for i, old := range children {
		next, replaced, err := mapChild(old)
		if err != nil {
			return nil, err
		}
		if replaced {
			if mapped == nil {
				mapped = append([]expr.Expr(nil), children...)
			}
			mapped[i] = next
		}
	}
	if mapped == nil {
		return children, nil
	}
	return mapped, nil
}

// retagEnvironment gives Retaggable nodes the retained descriptors, the stored
// collation remap and collation derivation over the catalog view.
func retagEnvironment(view catalog.Read, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, collations []typedef.CollationRemapper) expr.Retag {
	remap := func(ref sqltype.CollationRef) (sqltype.CollationRef, error) { return ref, nil }
	if len(collations) != 0 && collations[0] != nil {
		remap = collations[0]
	}
	return expr.Retag{
		Type:      retainedType,
		Collation: remap,
		Collated: func(info expr.Info, inputs ...expr.Expr) (expr.Info, error) {
			return analyze.CollatedInfo(view, info, inputs...)
		},
	}
}
