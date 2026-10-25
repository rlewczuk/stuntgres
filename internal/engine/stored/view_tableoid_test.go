package stored

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func storedTableOIDFixture(t *testing.T) (catalog.Read, *RoutineProgram, analyze.ViewLiteralPath) {
	t.Helper()
	read, relation, _, _ := routineCaptureFixture(t)
	oid, ok := read.LookupType(sqltype.Oid)
	if !ok {
		t.Fatal("OID fixture type")
	}
	ref := read.Domain().Ref(read.Epoch(), relation.Address())
	pos := parser.Position{Offset: 7, Character: 8}
	raw := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "tableoid", Pos: pos}}}, Pos: pos}
	syntax := &parser.Select{Targets: []parser.Expr{raw}, From: &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: relation.Name()}}}}}
	n := plan.TableOIDExpr{Info: expr.Info{Usage: oid.Usage(), Pos: pos, Property: expr.Properties{RowDependent: true}}, Owner: ref, Scope: 1, Frame: 1, QueryOccurrence: 1}
	source := &plan.SourceNode{QuerySchema: relation.Columns, Ref: ref, Occurrence: 0, RangeName: relation.Name()}
	project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "tableoid", Usage: oid.Usage()}}, Expressions: []expr.Expr{n}, Child: source, Frame: 1, Syntax: syntax}
	query := &plan.Query{Root: project}
	path := analyze.ViewLiteralPath{Query: 1, Clause: analyze.ViewTargetLiteral}
	attribute := ref
	attribute.Address.SubID = -6
	dependencies := []catalog.ExpressionDependency{{Ref: attribute, Revision: relation.Revision()}}
	v := NewView(ViewParts{Query: query, Syntax: syntax, Frames: map[uint32]*plan.ProjectNode{1: project}, Queries: map[*parser.Select]*plan.ProjectNode{syntax: project}, Sources: []ViewLeaf{{Ref: ref, Attributes: relation.Attributes, TableOIDRead: true}}, Dependencies: dependencies, Columns: map[analyze.ViewLiteralPath]analyze.ViewColumn{path: {TableOID: true, Owner: ref, Scope: 1, Frame: 1, Occurrence: 1}}})
	binding := analyze.Binding{Query: query, QuerySources: []exec.Leaf{{Ref: ref, Relation: relation, TableOIDRead: true}}, Dependencies: dependencies}
	return read, NewRoutineProgram([]RuleAction{NewRuleAction(RuleActionParts{Syntax: syntax, Binding: binding, Presentation: v})}), path
}

func TestStoredTableOIDCaptureValidation(t *testing.T) {
	for _, kind := range []string{"missing", "extra", "mode", "scope", "frame", "occurrence", "owner", "foreign", "stale", "admission"} {
		t.Run(kind, func(t *testing.T) {
			read, p, path := storedTableOIDFixture(t)
			if err := p.Validate(read); err != nil {
				t.Fatal("valid tableoid capture", err)
			}
			v := p.actions[0].presentation
			c := v.columns[path]
			switch kind {
			case "missing":
				delete(v.columns, path)
			case "extra":
				extra := path
				extra.Node = 99
				v.columns[extra] = c
			case "mode":
				c.TableOID = false
			case "scope":
				c.Scope = 0
			case "frame":
				c.Frame++
			case "occurrence":
				c.Occurrence++
			case "owner":
				c.Owner.Address.SubID = -6
			case "foreign":
				c.Owner = new(ids.Domain).Ref(c.Owner.Epoch, c.Owner.Address)
			case "stale":
				c.Owner = read.Domain().Ref(c.Owner.Epoch+1, c.Owner.Address)
			case "admission":
				v.sources[0].TableOIDRead = false
			}
			if kind != "missing" && kind != "extra" {
				v.columns[path] = c
			}
			if err := p.Validate(read); err == nil {
				t.Fatal("corrupt capture published")
			}
		})
	}
	read, p, _ := storedTableOIDFixture(t)
	n := p.actions[0].binding.Query.Root.(*plan.ProjectNode).Expressions[0]
	refs, err := ExpressionDependRefs(context.Background(), read, n)
	if err != nil || len(refs) != 1 || refs[0].SubID != -6 || refs[0].ObjectID != p.actions[0].binding.QuerySources[0].Ref.Address.ObjectID {
		t.Fatal("exact system dependency", refs, err)
	}
	for _, kind := range []string{"valid", "missing", "extra", "wrong-source", "ordinary-slot", "not-correlated"} {
		t.Run("function_"+kind, func(t *testing.T) {
			read, p, _ := storedTableOIDFixture(t)
			system := p.actions[0].binding.Query.Root.(*plan.ProjectNode).Expressions[0].(plan.TableOIDExpr)
			array, ok := read.LookupArray(system.ResultUsage().Type)
			if !ok {
				t.Fatal("missing OID array")
			}
			call := plan.UnnestExpr{Info: system.Info, Args: []expr.Expr{plan.ArrayExpr{Info: expr.Info{Usage: array.Usage()}, Elements: []expr.Expr{system}}}}
			capture := plan.FunctionCorrelation{Frame: system.Frame, QueryOccurrence: system.QueryOccurrence, Pos: system.Pos}
			node := &plan.FunctionFromNode{Frame: 1, Correlated: true, Calls: []expr.Expr{call}, Captures: []plan.FunctionCorrelation{capture}}
			switch kind {
			case "missing":
				node.Captures = nil
			case "extra":
				node.Captures = append(node.Captures, capture)
			case "wrong-source":
				node.Captures[0].QueryOccurrence++
			case "ordinary-slot":
				node.Captures[0].Slot = 1
			case "not-correlated":
				node.Correlated = false
			}
			err := validateTableOIDFunctionCaptures(node)
			if (err == nil) != (kind == "valid") {
				t.Fatal("function capture completeness", kind, err)
			}
		})
	}
}

func TestStoredTableOIDRemap(t *testing.T) {
	for _, bad := range []string{"", "foreign", "stale", "nil-domain"} {
		t.Run("source_"+bad, func(t *testing.T) {
			read, p, path := storedTableOIDFixture(t)
			a := p.actions[0].binding.QuerySources[0].Ref.Address
			b, c := a, a
			b.ObjectID++
			c.ObjectID += 2
			for _, address := range []ids.ObjectAddress{b, c} {
				copy := *read.MustRelation(a)
				copy.ObjectAddress = address
				read.State().SetDescriptor(address, &copy)
			}
			if bad != "" {
				project := p.actions[0].binding.Query.Root.(*plan.ProjectNode)
				n := project.Expressions[0].(plan.TableOIDExpr)
				switch bad {
				case "foreign":
					n.Owner = new(ids.Domain).Ref(read.Epoch(), a)
				case "stale":
					n.Owner = read.Domain().Ref(read.Epoch()+1, a)
				case "nil-domain":
					n.Owner = ids.ObjectRef{Address: a, Epoch: read.Epoch()}
				}
				project.Expressions[0] = n
			}
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{a: b, b: c}
			next, err := p.Rebind(read, mapping, NewRoutineRemap(read, mapping, nil, nil, false))
			if bad != "" {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("source lifetime escaped before mapping", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			mapped := next.(*RoutineProgram).actions[0]
			n := mapped.binding.Query.Root.(*plan.ProjectNode).Expressions[0].(plan.TableOIDExpr)
			capture := mapped.presentation.columns[path]
			if n.Owner.Address != b || capture.Owner != n.Owner || n.Frame != 1 || n.QueryOccurrence != 1 || capture.Occurrence != 1 {
				t.Fatal("reference remapped more than once or coordinates lost", n, capture)
			}
			for _, dep := range mapped.binding.Dependencies {
				if dep.Ref.Address.SubID == -6 && dep.Ref.Address.ObjectID != b.ObjectID {
					t.Fatal("negative reference lost remap", dep)
				}
			}
			if original := p.actions[0].binding.Query.Root.(*plan.ProjectNode).Expressions[0].(plan.TableOIDExpr); original.Owner.Address != a {
				t.Fatal("original mutated")
			}
		})
	}
	t.Run("function_coordinates", func(t *testing.T) {
		read, p, _ := storedTableOIDFixture(t)
		project := p.actions[0].binding.Query.Root.(*plan.ProjectNode)
		system := project.Expressions[0].(plan.TableOIDExpr)
		array, ok := read.LookupArray(system.ResultUsage().Type)
		if !ok {
			t.Fatal("missing OID array")
		}
		call := plan.UnnestExpr{Info: system.Info, Args: []expr.Expr{plan.ArrayExpr{Info: expr.Info{Usage: array.Usage()}, Elements: []expr.Expr{system}}}}
		capture := plan.FunctionCorrelation{Frame: 1, QueryOccurrence: 1, Pos: system.Pos}
		function := &plan.FunctionFromNode{Frame: 1, Correlated: true, Calls: []expr.Expr{call}, Captures: []plan.FunctionCorrelation{capture}}
		join := &plan.JoinNode{Left: project.Child, Right: function, Frame: 1, CorrelatedRight: true, CorrelationIdentityPrefix: true, Captures: []plan.FunctionCorrelation{capture}}
		mapper := NewRuleTreeMapper(read, func(frame uint32) uint32 { return frame + 3 }, 5, nil)
		node, err := mapper.Query(join)
		if err != nil {
			t.Fatal(err)
		}
		mapped := node.(*plan.JoinNode)
		mappedFunction := mapped.Right.(*plan.FunctionFromNode)
		mappedSystem := mappedFunction.Calls[0].(plan.UnnestExpr).Args[0].(plan.ArrayExpr).Elements[0].(plan.TableOIDExpr)
		if mapped.Frame != 4 || mappedFunction.Frame != 4 || mapped.Captures[0].Frame != 4 || mapped.Captures[0].QueryOccurrence != 6 || mappedFunction.Captures[0].QueryOccurrence != 6 || mappedSystem.QueryOccurrence != 6 || mappedSystem.Frame != 4 || mappedSystem.Owner != system.Owner || !mapped.CorrelationIdentityPrefix || mapped.Left.(*plan.SourceNode).Occurrence != 5 {
			t.Fatal("function source coordinate remap lost provenance", mapped, mappedFunction, mappedSystem)
		}
		if join.Frame != 1 || join.Captures[0] != capture || function.Captures[0] != capture || project.Child.(*plan.SourceNode).Occurrence != 0 {
			t.Fatal("function coordinate remap mutated original")
		}
	})
}
