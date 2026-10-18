package expr

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// RoutineReference is opaque selected-declaration metadata. The OID is only an
// identity/debug token; catalog lifetime and permission authority belong to the
// engine's existing captured-reference manifests and fresh admission view.
type RoutineReference interface {
	RoutineOID() uint32
}

// RoutineRecipeReference borrows the defining stored recipe of a grafted node.
// Its identity token is not execution or catalog permission authority.
type RoutineRecipeReference interface {
	RoutineRecipeOID() uint32
}

// Info is the metadata every bound expression carries. Nodes embed it to
// implement the metadata half of Expr. Callers supply generation-owned usages
// and provenance.
type Info struct {
	Usage       sqltype.Usage
	Pos         parser.Position
	Property    Properties
	UnknownNull bool
	// Only genuine scalar REGCLASS constants carry captured relation authority.
	RegclassRef                   ids.ObjectRef
	CollationConflict             bool
	CollationExplicit             parser.Position
	CollationLeft, CollationRight sqltype.CollationRef
	CollationConflictPos          parser.Position
	RoutineReference              RoutineReference
	RoutineRecipeReference        RoutineRecipeReference // Borrowed immutable defining recipe of a grafted stored node.
}

func (i Info) ResultUsage() sqltype.Usage                         { return i.Usage }
func (i Info) SourcePosition() parser.Position                    { return i.Pos }
func (i Info) Properties() Properties                             { return i.Property }
func (i Info) CollationIndeterminate() bool                       { return i.CollationConflict }
func (i Info) ExplicitCollation() parser.Position                 { return i.CollationExplicit }
func (i Info) CallRoutineReference() RoutineReference             { return i.RoutineReference }
func (i Info) CallRoutineRecipeReference() RoutineRecipeReference { return i.RoutineRecipeReference }
func (i Info) ImplicitCollationConflict() (sqltype.CollationRef, sqltype.CollationRef, parser.Position) {
	return i.CollationLeft, i.CollationRight, i.CollationConflictPos
}

// InfoOf copies the metadata of node, so a wrapper keeps its diagnostic
// provenance. Routine references are copied when node exposes them.
// UnknownNull and RegclassRef are not part of Expr and stay zero.
func InfoOf(node Expr) Info {
	left, right, position := node.ImplicitCollationConflict()
	info := Info{Usage: node.ResultUsage(), Pos: node.SourcePosition(), Property: node.Properties(), CollationConflict: node.CollationIndeterminate(), CollationExplicit: node.ExplicitCollation(), CollationLeft: left, CollationRight: right, CollationConflictPos: position}
	if call, ok := node.(interface {
		CallRoutineReference() RoutineReference
	}); ok {
		info.RoutineReference = call.CallRoutineReference()
	}
	if call, ok := node.(interface {
		CallRoutineRecipeReference() RoutineRecipeReference
	}); ok {
		info.RoutineRecipeReference = call.CallRoutineRecipeReference()
	}
	return info
}
