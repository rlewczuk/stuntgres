package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// viewOf returns d's concrete view definition, or nil when d stores none.
func ViewOf(d *catalog.Relation) *viewDefinition {
	v, _ := d.View.(*viewDefinition)
	return v
}

// viewPayloadOf stores v as a payload; a nil definition stays a nil payload,
// so d.view == nil keeps meaning "no stored definition".
func ViewPayload(v *viewDefinition) catalog.ViewPayload {
	if v == nil {
		return nil
	}
	return v
}

// ViewParts lists every field of a frozen view definition. Builders fill it
// and call NewView; code editing an unpublished definition uses Parts or
// With. Maps and slices are shared, never copied.
type ViewParts struct {
	Query             *plan.Query
	Syntax            *parser.Select
	ExpressionSyntax  parser.Expr
	Sources           []viewLeaf
	Dependencies      []catalog.ExpressionDependency
	SequenceRefs      map[int]ids.ObjectRef
	Literals          map[analyze.ViewLiteralPath]plan.LiteralExpr
	CastUsages        map[analyze.ViewLiteralPath]sqltype.Usage
	RoutineRefs       map[analyze.ViewLiteralPath]ids.ObjectRef
	OperatorRefs      map[analyze.ViewLiteralPath]ids.ObjectRef
	Composites        map[analyze.ViewLiteralPath]analyze.CompositeOccurrence
	SimilarSubstrings map[analyze.ViewLiteralPath]bool
	PosixSubstrings   map[analyze.ViewLiteralPath]bool
	Columns           map[analyze.ViewLiteralPath]analyze.ViewColumn
	Stars             map[analyze.ViewLiteralPath][]analyze.ViewColumn
	Frames            map[uint32]*plan.ProjectNode
	Queries           map[*parser.Select]*plan.ProjectNode
	Namespaces        []analyze.ViewNamespace
	Barrier           bool
	Invoker           bool
	CheckOption       viewCheckMode
	Options           []string
	Rule              ids.OID
	Consumer          ids.ObjectAddress
	Temporary         ids.SessionToken
	References        map[ids.ObjectRef]catalog.Descriptor
	Collations        map[analyze.ViewLiteralPath]uint32
	QuoteRevision     ids.DefinitionRevision
}

// NewView builds a definition from p.
func NewView(p ViewParts) *viewDefinition {
	return &viewDefinition{
		query:             p.Query,
		syntax:            p.Syntax,
		expressionSyntax:  p.ExpressionSyntax,
		sources:           p.Sources,
		dependencies:      p.Dependencies,
		sequenceRefs:      p.SequenceRefs,
		literals:          p.Literals,
		castUsages:        p.CastUsages,
		routineRefs:       p.RoutineRefs,
		operatorRefs:      p.OperatorRefs,
		composites:        p.Composites,
		similarSubstrings: p.SimilarSubstrings,
		posixSubstrings:   p.PosixSubstrings,
		columns:           p.Columns,
		stars:             p.Stars,
		frames:            p.Frames,
		queries:           p.Queries,
		namespaces:        p.Namespaces,
		barrier:           p.Barrier,
		invoker:           p.Invoker,
		checkOption:       p.CheckOption,
		options:           p.Options,
		rule:              p.Rule,
		consumer:          p.Consumer,
		temporary:         p.Temporary,
		references:        p.References,
		collations:        p.Collations,
		quoteRevision:     p.QuoteRevision,
	}
}

// Parts returns v's fields; the definition itself is not changed.
func (v *viewDefinition) Parts() ViewParts {
	return ViewParts{
		Query:             v.query,
		Syntax:            v.syntax,
		ExpressionSyntax:  v.expressionSyntax,
		Sources:           v.sources,
		Dependencies:      v.dependencies,
		SequenceRefs:      v.sequenceRefs,
		Literals:          v.literals,
		CastUsages:        v.castUsages,
		RoutineRefs:       v.routineRefs,
		OperatorRefs:      v.operatorRefs,
		Composites:        v.composites,
		SimilarSubstrings: v.similarSubstrings,
		PosixSubstrings:   v.posixSubstrings,
		Columns:           v.columns,
		Stars:             v.stars,
		Frames:            v.frames,
		Queries:           v.queries,
		Namespaces:        v.namespaces,
		Barrier:           v.barrier,
		Invoker:           v.invoker,
		CheckOption:       v.checkOption,
		Options:           v.options,
		Rule:              v.rule,
		Consumer:          v.consumer,
		Temporary:         v.temporary,
		References:        v.references,
		Collations:        v.collations,
		QuoteRevision:     v.quoteRevision,
	}
}

// With returns a new definition: v's parts after edit. It never changes v,
// so a published definition stays immutable.
func (v *viewDefinition) With(edit func(*ViewParts)) *viewDefinition {
	p := v.Parts()
	edit(&p)
	return NewView(p)
}

func (v *viewDefinition) RuleOID() ids.OID { return v.rule }

// The accessors below read a frozen definition for code outside the stored
// presentation. Results share the published payload; callers never mutate them.

// Sources returns the stored source leaves in SQL occurrence order.
func (v *viewDefinition) Sources() []viewLeaf { return v.sources }

// Dependencies returns the frozen expression dependencies.
func (v *viewDefinition) Dependencies() []catalog.ExpressionDependency { return v.dependencies }

// Invoker reports whether the view runs with the invoker's permissions.
func (v *viewDefinition) Invoker() bool { return v.invoker }

// Options returns the view's reloptions as "name=value" strings.
func (v *viewDefinition) Options() []string { return v.options }

// References returns the descriptors a TEMP consumer captured, by reference.
func (v *viewDefinition) References() map[ids.ObjectRef]catalog.Descriptor { return v.references }

// Literals returns the captured literals by view literal path.
func (v *viewDefinition) Literals() map[analyze.ViewLiteralPath]plan.LiteralExpr { return v.literals }

// Query returns the analyzed query.
func (v *viewDefinition) Query() *plan.Query { return v.query }

// Syntax returns the frozen SELECT syntax.
func (v *viewDefinition) Syntax() *parser.Select { return v.syntax }

// CastUsages returns the captured cast usages by view literal path.
func (v *viewDefinition) CastUsages() map[analyze.ViewLiteralPath]sqltype.Usage { return v.castUsages }

// RoutineRefs returns the captured routine references by view literal path.
func (v *viewDefinition) RoutineRefs() map[analyze.ViewLiteralPath]ids.ObjectRef {
	return v.routineRefs
}

// Columns returns the captured column occurrences by view literal path.
func (v *viewDefinition) Columns() map[analyze.ViewLiteralPath]analyze.ViewColumn { return v.columns }

// Frames returns the analyzed projection of each lexical frame.
func (v *viewDefinition) Frames() map[uint32]*plan.ProjectNode { return v.frames }

// Queries returns the analyzed projection of each nested SELECT.
func (v *viewDefinition) Queries() map[*parser.Select]*plan.ProjectNode { return v.queries }

// Namespaces returns the captured namespaces.
func (v *viewDefinition) Namespaces() []analyze.ViewNamespace { return v.namespaces }

// Barrier returns whether the view is a security barrier.
func (v *viewDefinition) Barrier() bool { return v.barrier }

// CheckOption returns the WITH CHECK OPTION mode.
func (v *viewDefinition) CheckOption() viewCheckMode { return v.checkOption }

// Consumer returns the TEMP consumer that owns the definition.
func (v *viewDefinition) Consumer() ids.ObjectAddress { return v.consumer }

// Temporary returns the TEMP owner session token.
func (v *viewDefinition) Temporary() ids.SessionToken { return v.temporary }

// QuoteRevision returns the frozen defining revision used for quoting.
func (v *viewDefinition) QuoteRevision() ids.DefinitionRevision { return v.quoteRevision }

func (v *viewDefinition) DependsOn(target ids.ObjectAddress) bool {
	for _, leaf := range v.sources {
		if target.SubID == 0 && leaf.Ref.Address == target {
			return true
		}
	}
	for _, dependency := range v.dependencies {
		a := dependency.Ref.Address
		if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
			return true
		}
	}
	return false
}

func (v *viewDefinition) TemporaryConsumer() (ids.ObjectAddress, ids.SessionToken) {
	return v.consumer, v.temporary
}

func (v *viewDefinition) CapturedReference(ref ids.ObjectRef) (catalog.Descriptor, bool) {
	d, ok := v.references[ref]
	return d, ok
}
