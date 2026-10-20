package engine

import (
	"stuntgres/internal/engine/expr"
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
				d := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12048})
				provider := &pgSequencesSource{}
				leaf := &querySource{pgSequences: provider}
				child := &sourceNode{QuerySchema: d.Columns, Occurrence: 0}
				inner := columnExpression{Scope: 1, Frame: 2, Slot: 1}
				project := &projectNode{QuerySchema: d.Columns[:1], Child: child, Expressions: []expr.Expr{inner}, Frame: 2}
				column := columnExpression{Scope: 1, Frame: 1, Slot: 0}
				switch invalid {
				case "foreign_frame", "mixed_and":
					column.Frame = 9
				case "foreign_scope":
					column.Scope = 2
				case "unproven_frame":
					column.Frame = 0
				case "foreign_projection":
					inner.Frame = 9
					project.Expressions[0] = inner
				}
				var predicate expr.Expr = nullTestExpression{Child: column}
				if invalid == "mixed_and" {
					predicate = logicalExpression{Op: parser.LogicalAnd, Left: nullTestExpression{Child: columnExpression{Scope: 1, Frame: 1, Slot: 0}}, Right: predicate}
				}
				if invalid == "mixed_projection" {
					foreign := inner
					foreign.Frame = 9
					project.Expressions = append(project.Expressions, foreign)
					project.QuerySchema = d.Columns[:2]
					predicate = logicalExpression{Op: parser.LogicalAnd,
						Left:  nullTestExpression{Child: column},
						Right: nullTestExpression{Child: columnExpression{Scope: 1, Frame: 1, Slot: 1}},
					}
				}
				switch boundary {
				case "derived":
					node := &derivedNode{QuerySchema: d.Columns[:1], Query: &BoundQuery{Root: project}, ScanFrame: 1}
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
	d := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12048})
	for _, boundary := range []string{"derived", "stored_project", "stored_input"} {
		t.Run(boundary, func(t *testing.T) {
			provider := &pgSequencesSource{}
			leaf := &querySource{pgSequences: provider}
			child := &sourceNode{QuerySchema: d.Columns, Occurrence: 0}
			project := &projectNode{QuerySchema: d.Columns[:2], Child: child, Expressions: []expr.Expr{
				columnExpression{Scope: 1, Frame: 2, Slot: 1},
				columnExpression{Scope: 1, Frame: 2, Slot: 0},
			}, Frame: 2}
			column := columnExpression{Scope: 1, Frame: 1, Slot: 0}
			switch boundary {
			case "derived":
				column.Slot = 3
				node := &derivedNode{QuerySchema: d.Columns[:2], Query: &BoundQuery{Root: project}, ScanFrame: 1}
				admitPGSequencesPredicate(node, leaf, nullTestExpression{Child: column}, 3, 1)
			case "stored_project":
				source := &querySource{nestedRoot: project, nestedInputs: leaf}
				admitPGSequencesSource(source, nullTestExpression{Child: column}, 1)
			case "stored_input":
				source := &querySource{input: leaf, layout: []int{1, 0}}
				admitPGSequencesSource(source, nullTestExpression{Child: column}, 1)
			}
			if len(provider.predicates) != 1 {
				t.Fatalf("local admission got %d predicates, want 1", len(provider.predicates))
			}
			mapped := provider.predicates[0].(nullTestExpression).Child.(columnExpression)
			frame := uint32(2)
			if boundary == "stored_input" {
				frame = 1 // Layout adapts the row without entering another frame.
			}
			if mapped.Scope != 1 || mapped.Frame != frame || mapped.Slot != 1 {
				t.Fatalf("mapped scope/frame/slot = %d/%d/%d, want 1/%d/1", mapped.Scope, mapped.Frame, mapped.Slot, frame)
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
	binder.query.auth = &auth
	var query *BoundQuery
	if err == nil {
		query, err = binder.bindSelect(raw, false)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	project := query.Root.(*projectNode)
	rowLock := project.Child.(*rowLockNode)
	filter := rowLock.Child.(*filterNode)
	derived := filter.Child.(*derivedNode)
	inner := derived.Query.Root.(*projectNode)
	state := queryState{frame: project.Frame}
	evaluation := state.evaluation(op.Context())
	if derived.ScanFrame == 0 || derived.ScanFrame != evaluation.Frame || inner.Frame == evaluation.Frame {
		t.Fatalf("caller/evaluation/derived/inner frames = %d/%d/%d/%d", project.Frame, evaluation.Frame, derived.ScanFrame, inner.Frame)
	}
	provider := &pgSequencesSource{}
	inputs := &querySource{pgSequences: provider}
	admitPGSequencesPredicate(rowLock, inputs, filter.Predicate, 0, evaluation.Frame)
	if len(provider.predicates) != 1 {
		t.Fatalf("bound local row-lock predicate admitted %d predicates, want 1", len(provider.predicates))
	}
	mapped := provider.predicates[0].(comparisonExpression).Left.(columnExpression)
	if mapped.Scope != 1 || mapped.Frame != inner.Frame || mapped.Slot != 1 {
		t.Fatalf("mapped scope/frame/slot = %d/%d/%d, want 1/%d/1", mapped.Scope, mapped.Frame, mapped.Slot, inner.Frame)
	}
	if !pgSequencesPredicateInFrame(filter.Predicate, evaluation.Frame) {
		t.Fatal("admission changed the residual predicate's lexical ownership")
	}
}
