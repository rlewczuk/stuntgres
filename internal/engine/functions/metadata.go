// Package functions owns builtin-specific analysis, immutable calls and aggregate states.
package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
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

// Info preserves expression metadata without knowing the engine's property type
// or catalog authority. Callers supply generation-owned usages and provenance.
type Info[P any] struct {
	Usage                         sqltype.Usage
	Pos                           parser.Position
	Property                      P
	CollationConflict             bool
	CollationExplicit             parser.Position
	CollationLeft, CollationRight sqltype.CollationRef
	CollationConflictPos          parser.Position
	RoutineReference              RoutineReference
	RoutineRecipeReference        RoutineRecipeReference
}

func (i Info[P]) ResultUsage() sqltype.Usage                         { return i.Usage }
func (i Info[P]) SourcePosition() parser.Position                    { return i.Pos }
func (i Info[P]) Properties() P                                      { return i.Property }
func (i Info[P]) CollationIndeterminate() bool                       { return i.CollationConflict }
func (i Info[P]) ExplicitCollation() parser.Position                 { return i.CollationExplicit }
func (i Info[P]) CallRoutineReference() RoutineReference             { return i.RoutineReference }
func (i Info[P]) CallRoutineRecipeReference() RoutineRecipeReference { return i.RoutineRecipeReference }
func (i Info[P]) ImplicitCollationConflict() (sqltype.CollationRef, sqltype.CollationRef, parser.Position) {
	return i.CollationLeft, i.CollationRight, i.CollationConflictPos
}

type Context interface{ FunctionContext() context.Context }
type Expression[C any] interface {
	ResultUsage() sqltype.Usage
	Eval(C) (sqltype.Value, error)
}
type LiteralContext[E any] interface {
	Context
	FunctionLiteralNull(E) bool
}

func evaluationContext[C Context](c C) (context.Context, error) {
	ctx := c.FunctionContext()
	if ctx == nil {
		return nil, fmt.Errorf("stuntgres: missing expression context")
	}
	return ctx, ctx.Err()
}
func sqlError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

type Resolution uint8

const (
	Resolved Resolution = iota
	Missing
	Ambiguous
	UnknownPolymorphic
)

type IntrinsicPropertyRule uint8

const (
	ExactStable IntrinsicPropertyRule = iota
	ExactVolatile
	VolatileOnly
	ExactImmutable
)
