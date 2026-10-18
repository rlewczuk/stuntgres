package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// Exercise the admission seam itself, not merely a predicate-mapper callback.
// Every case uses valid numeric slots; lexical ownership is what must bar it.
func TestPGSequencesAdmissionFrames(t *testing.T) {
	for _, boundary := range []string{"derived", "stored_project", "stored_input"} {
		for _, invalid := range []string{"foreign_frame", "foreign_scope", "unproven_frame", "mixed_and", "foreign_projection", "mixed_projection"} {
			if boundary == "stored_input" && (invalid == "foreign_projection" || invalid == "mixed_projection") {
				continue // An input layout has no expression projection.
			}
			t.Run(boundary+"/"+invalid, func(t *testing.T) {
				e := New()
				d := e.catalog.state.descriptors[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12048}].(*relationDescriptor)
				provider := &pgSequencesSource{}
				leaf := &querySource{pgSequences: provider}
				child := &sourceNode{querySchema: d.columns, occurrence: 0}
				inner := columnExpression{scope: 1, frame: 2, slot: 1}
				project := &projectNode{querySchema: d.columns[:1], child: child, expressions: []BoundExpr{inner}, frame: 2}
				column := columnExpression{scope: 1, frame: 1, slot: 0}
				switch invalid {
				case "foreign_frame", "mixed_and":
					column.frame = 9
				case "foreign_scope":
					column.scope = 2
				case "unproven_frame":
					column.frame = 0
				case "foreign_projection":
					inner.frame = 9
					project.expressions[0] = inner
				}
				var predicate BoundExpr = nullTestExpression{child: column}
				if invalid == "mixed_and" {
					predicate = logicalExpression{op: parser.LogicalAnd, left: nullTestExpression{child: columnExpression{scope: 1, frame: 1, slot: 0}}, right: predicate}
				}
				if invalid == "mixed_projection" {
					foreign := inner
					foreign.frame = 9
					project.expressions = append(project.expressions, foreign)
					project.querySchema = d.columns[:2]
					predicate = logicalExpression{op: parser.LogicalAnd,
						left:  nullTestExpression{child: column},
						right: nullTestExpression{child: columnExpression{scope: 1, frame: 1, slot: 1}},
					}
				}
				switch boundary {
				case "derived":
					node := &derivedNode{querySchema: d.columns[:1], query: &BoundQuery{Root: project}, scanFrame: 1}
					admitPGSequencesPredicate(node, leaf, predicate, 0, 1)
				case "stored_project":
					source := &querySource{nestedRoot: project, nestedInputs: leaf}
					admitPGSequencesSource(source, predicate, 1)
				case "stored_input":
					source := &querySource{input: leaf, layout: []int{1}}
					admitPGSequencesSource(source, predicate, 1)
				}
				if len(provider.predicates) != 0 {
					t.Fatalf("%s admitted %d predicates with %s ownership; want an admission barrier", boundary, len(provider.predicates), invalid)
				}
			})
		}
	}
}

// Proven local columns still cross aliases, reordered projections and join
// offsets; a frame guard must not become a blanket pushdown prohibition.
func TestPGSequencesAdmissionLocalFrames(t *testing.T) {
	e := New()
	d := e.catalog.state.descriptors[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12048}].(*relationDescriptor)
	for _, boundary := range []string{"derived", "stored_project", "stored_input"} {
		t.Run(boundary, func(t *testing.T) {
			provider := &pgSequencesSource{}
			leaf := &querySource{pgSequences: provider}
			child := &sourceNode{querySchema: d.columns, occurrence: 0}
			project := &projectNode{querySchema: d.columns[:2], child: child, expressions: []BoundExpr{
				columnExpression{scope: 1, frame: 2, slot: 1},
				columnExpression{scope: 1, frame: 2, slot: 0},
			}, frame: 2}
			column := columnExpression{scope: 1, frame: 1, slot: 0}
			switch boundary {
			case "derived":
				column.slot = 3
				node := &derivedNode{querySchema: d.columns[:2], query: &BoundQuery{Root: project}, scanFrame: 1}
				admitPGSequencesPredicate(node, leaf, nullTestExpression{child: column}, 3, 1)
			case "stored_project":
				source := &querySource{nestedRoot: project, nestedInputs: leaf}
				admitPGSequencesSource(source, nullTestExpression{child: column}, 1)
			case "stored_input":
				source := &querySource{input: leaf, layout: []int{1, 0}}
				admitPGSequencesSource(source, nullTestExpression{child: column}, 1)
			}
			if len(provider.predicates) != 1 {
				t.Fatalf("local admission got %d predicates, want 1", len(provider.predicates))
			}
			mapped := provider.predicates[0].(nullTestExpression).child.(columnExpression)
			frame := uint32(2)
			if boundary == "stored_input" {
				frame = 1 // Layout adapts the row without entering another frame.
			}
			if mapped.scope != 1 || mapped.frame != frame || mapped.slot != 1 {
				t.Fatalf("mapped scope/frame/slot = %d/%d/%d, want 1/%d/1", mapped.scope, mapped.frame, mapped.slot, frame)
			}
		})
	}
}

// Bind the actual row-lock query shape: hand-built derived nodes with an
// explicitly supplied scanFrame cannot catch missing caller-frame capture.
func TestPGSequencesAdmissionDerivedRowLockFrame(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	raw := statement(t, "SELECT sequencename FROM (SELECT sequencename FROM pg_sequences) s WHERE sequencename='a' FOR UPDATE").(*parser.Select)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	binder := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	binder.queryAuth = &auth
	var query *BoundQuery
	if err == nil {
		query, err = binder.bindSelect(raw, false)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	project := query.Root.(*projectNode)
	rowLock := project.child.(*rowLockNode)
	filter := rowLock.child.(*filterNode)
	derived := filter.child.(*derivedNode)
	inner := derived.query.Root.(*projectNode)
	state := queryState{frame: project.frame}
	evaluation := state.evaluation(op.Context())
	if derived.scanFrame == 0 || derived.scanFrame != evaluation.Frame || inner.frame == evaluation.Frame {
		t.Fatalf("caller/evaluation/derived/inner frames = %d/%d/%d/%d", project.frame, evaluation.Frame, derived.scanFrame, inner.frame)
	}
	provider := &pgSequencesSource{}
	inputs := &querySource{pgSequences: provider}
	admitPGSequencesPredicate(rowLock, inputs, filter.predicate, 0, evaluation.Frame)
	if len(provider.predicates) != 1 {
		t.Fatalf("bound local row-lock predicate admitted %d predicates, want 1", len(provider.predicates))
	}
	mapped := provider.predicates[0].(comparisonExpression).left.(columnExpression)
	if mapped.scope != 1 || mapped.frame != inner.frame || mapped.slot != 1 {
		t.Fatalf("mapped scope/frame/slot = %d/%d/%d, want 1/%d/1", mapped.scope, mapped.frame, mapped.slot, inner.frame)
	}
	if !pgSequencesPredicateInFrame(filter.predicate, evaluation.Frame) {
		t.Fatal("admission changed the residual predicate's lexical ownership")
	}
}
