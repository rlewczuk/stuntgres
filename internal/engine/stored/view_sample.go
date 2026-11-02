package stored

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func (v *View) sampleProcedure(read catalog.Read, path analyze.ViewLiteralPath) (*catalog.Procedure, error) {
	ref, exists := v.routineRefs[path]
	consumer, _ := read.Relation(v.consumer)
	if !exists || ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || !ViewReferenceValid(read, consumer, ref) || ref.Epoch != read.Epoch() && (consumer == nil || ViewOf(consumer) != v) {
		return nil, fmt.Errorf("stuntgres: missing or stale frozen sampling reference")
	}
	project := v.frames[path.Query]
	if project == nil {
		return nil, fmt.Errorf("stuntgres: missing sampling frame")
	}
	source := viewSourceAt(analyze.ViewFromRoot(project), path.Target)
	if source == nil || source.Sample == nil || source.Sample.Handler != ref || source.Sample.Procedure == nil || source.Sample.Procedure.Address() != ref.Address {
		return nil, fmt.Errorf("stuntgres: mismatched frozen sampling occurrence")
	}
	procedure, ok := read.Procedure(ref.Address)
	if !ok || !exec.SameRoutineDeclaration(procedure, source.Sample.Procedure) {
		return nil, ids.ErrCatalogReplaced
	}
	if err := extension.SystemRowsCapability(read, procedure); err != nil {
		return nil, err
	}
	for _, dependency := range v.dependencies {
		if dependency.Ref == ref && dependency.Revision != 0 && dependency.Purpose == catalog.DefinitionDependency {
			return procedure, nil
		}
	}
	return nil, fmt.Errorf("stuntgres: missing frozen sampling dependency")
}

func sampleSyntax(item parser.FromItem) *parser.TableSample {
	for {
		switch n := item.(type) {
		case *parser.TableReference:
			return n.Sample
		case *parser.AliasReference:
			item = n.Input
		default:
			return nil
		}
	}
}

func (v *QueryRenderer) sampleSQL(ctx context.Context, read catalog.Read, source *plan.SourceNode, raw *parser.TableSample) (string, error) {
	if raw == nil && source.Sample == nil {
		return "", nil
	}
	if raw == nil || source.Sample == nil || len(raw.Arguments) != 1 || raw.Repeatable != nil {
		return "", fmt.Errorf("stuntgres: invalid frozen sampling syntax")
	}
	path := analyze.ViewLiteralPath{Query: v.Frame, Clause: analyze.ViewSampleHandler, Target: source.Occurrence}
	procedure, err := v.sampleProcedure(read, path)
	if err != nil {
		return "", err
	}
	name, err := v.providerProcedureName(read, procedure)
	if err != nil {
		return "", err
	}
	path.Clause = analyze.ViewSampleLiteral
	count, err := v.ExpressionSQL(ctx, read, raw.Arguments[0], path)
	if err != nil {
		return "", err
	}
	return " TABLESAMPLE " + name + "(" + count + ")", nil
}
