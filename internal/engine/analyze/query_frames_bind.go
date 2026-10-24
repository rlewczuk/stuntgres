package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Lexical scopes share statement authority and parameter/dependency ownership.
// Only query-local namespace and aggregate state is saved at a query boundary.
type queryBindingScope struct {
	relation            *catalog.Relation
	namespace           *QueryNamespace
	hidden              []QueryQualifier
	frame               uint32
	allowed             bool
	Clause              string
	aggregates          []plan.AggregateCall
	groupingActive      bool
	groupSets           [][]int
	returning           *returningAliases
	nonLateral          bool
	rowDiagnosticMode   lock.Mode
	windowRaw           []parser.WindowDefinition
	windowDefinitions   map[string]plan.WindowSpec
	windowCalls         []plan.WindowCall
	argumentOwner       ArgumentWindowOwner
	stringFromForbidden bool
}

func (b *Binder) FrameID() uint32 {
	if b.Query.Frame == 0 {
		return 1
	}
	return b.Query.Frame
}

func (b *Binder) EnterQuery(disallowParent bool) func() {
	oldSetGuard, oldForbidden := b.Clause.StringSetGuard, b.Clause.stringFromForbidden
	oldRegexpClause := b.Clause.RegexpClause
	oldTargetSet := b.Clause.QueryHasTargetSet
	oldFromSet := b.Clause.FromSetObservation
	b.Clause.FromSetObservation = FromSetObservation{}
	b.Clause.QueryHasTargetSet = false
	b.Clause.StringSetGuard, b.Clause.stringFromForbidden = "", false
	b.Clause.RegexpClause = ""
	oldNamespace := b.Query.Namespace
	parent := queryBindingScope{namespace: b.Query.Namespace, hidden: b.Query.Hidden, frame: b.FrameID(), allowed: !disallowParent, Clause: b.Clause.AggregateClause, aggregates: b.Grouping.Aggregates, returning: b.Target.returningAliases, nonLateral: b.Query.NonLateral}
	parent.stringFromForbidden = oldForbidden
	parent.relation = b.Target.Relation
	parent.groupingActive, parent.groupSets = b.Grouping.active, b.Grouping.GroupSets
	parent.rowDiagnosticMode = b.Query.rowDiagnosticMode
	parent.windowRaw, parent.windowDefinitions, parent.windowCalls = b.Grouping.windowRaw, b.Grouping.windowDefinitions, b.Grouping.WindowCalls
	parent.argumentOwner = b.Grouping.ArgumentOwner
	oldFrame := b.Query.Frame
	if b.Query.NextQueryFrame == 0 {
		b.Query.NextQueryFrame = 1
	}
	if oldFrame != 0 || b.Target.Relation != nil {
		b.Query.NextQueryFrame++
		b.Query.Frame = b.Query.NextQueryFrame
	} else {
		b.Query.Frame = 1
	}
	index := len(b.Query.Scopes)
	b.Query.Scopes = append(b.Query.Scopes, parent)
	b.Query.Namespace, b.Query.Hidden = nil, nil
	b.Grouping.Aggregates, b.Clause.AggregateClause = nil, ""
	b.Grouping.active, b.Grouping.GroupSets = false, nil
	b.Grouping.windowRaw, b.Grouping.windowDefinitions, b.Grouping.WindowCalls = nil, nil, nil
	b.Grouping.ArgumentOwner = ArgumentWindowOwner{}
	b.Target.returningAliases = nil
	b.Query.NonLateral = disallowParent
	b.Query.rowDiagnosticMode = 0
	return func() {
		b.Clause.StringSetGuard, b.Clause.stringFromForbidden = oldSetGuard, oldForbidden
		b.Clause.RegexpClause = oldRegexpClause
		b.Clause.QueryHasTargetSet = oldTargetSet
		b.Clause.FromSetObservation = oldFromSet
		parent := b.Query.Scopes[index]
		b.Query.Scopes = b.Query.Scopes[:index]
		b.Query.Namespace, b.Query.Hidden = oldNamespace, parent.hidden
		b.Grouping.Aggregates, b.Clause.AggregateClause = parent.aggregates, parent.Clause
		b.Grouping.active, b.Grouping.GroupSets = parent.groupingActive, parent.groupSets
		b.Grouping.windowRaw, b.Grouping.windowDefinitions, b.Grouping.WindowCalls = parent.windowRaw, parent.windowDefinitions, parent.windowCalls
		b.Grouping.ArgumentOwner = parent.argumentOwner
		b.Target.returningAliases, b.Query.NonLateral = parent.returning, parent.nonLateral
		b.Query.rowDiagnosticMode = parent.rowDiagnosticMode
		b.Query.Frame = oldFrame
	}
}

func (q *QueryScopeState) outerQualifiers() []QueryQualifier {
	var out []QueryQualifier
	for i := len(q.Scopes) - 1; i >= 0; i-- {
		s := &q.Scopes[i]
		if len(s.hidden) != 0 {
			out = append(out, s.hidden...)
		} else if s.namespace != nil {
			out = append(out, s.namespace.Qualifiers...)
		}
	}
	return out
}

func (b *Binder) queryColumnInFrame(c QueryColumn, pos parser.Position, frame uint32) expr.Expr {
	expr := b.QueryColumnAt(c, pos).(plan.ColumnExpr)
	expr.Frame = frame
	expr.MergeSource = c.Leaf != QueryTargetLeaf && b.mergeReturningFrame(frame)
	return expr
}

func (b *Binder) queryTargetRelation() *catalog.Relation {
	if b.Target.Relation != nil {
		return b.Target.Relation
	}
	for i := len(b.Query.Scopes) - 1; i >= 0; i-- {
		if d := b.Query.Scopes[i].relation; d != nil {
			return d
		}
	}
	return nil
}
