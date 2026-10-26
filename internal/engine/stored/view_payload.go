package stored

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ViewOf returns d's concrete view definition, or nil when d stores none.
func ViewOf(d *catalog.Relation) *View {
	v, _ := d.View.(*View)
	return v
}

// ViewPayload stores v as a payload; a nil definition stays a nil payload,
// so d.view == nil keeps meaning "no stored definition".
func ViewPayload(v *View) catalog.ViewPayload {
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
	ActionSyntax      parser.Statement
	ExpressionSyntax  parser.Expr
	MutationFrame     bool
	Sources           []ViewLeaf
	Dependencies      []catalog.ExpressionDependency
	SequenceRefs      map[int]ids.ObjectRef
	Literals          map[analyze.ViewLiteralPath]plan.LiteralExpr
	CastUsages        map[analyze.ViewLiteralPath]sqltype.Usage
	RoutineInputs     []sqltype.Usage
	RoutineRefs       map[analyze.ViewLiteralPath]ids.ObjectRef
	OperatorRefs      map[analyze.ViewLiteralPath]ids.ObjectRef
	Composites        map[analyze.ViewLiteralPath]analyze.TypeOccurrence
	SimilarSubstrings map[analyze.ViewLiteralPath]bool
	PosixSubstrings   map[analyze.ViewLiteralPath]bool
	Columns           map[analyze.ViewLiteralPath]analyze.ViewColumn
	Stars             map[analyze.ViewLiteralPath][]analyze.ViewColumn
	Frames            map[uint32]*plan.ProjectNode
	Queries           map[*parser.Select]*plan.ProjectNode
	Namespaces        []analyze.ViewNamespace
	Barrier           bool
	Invoker           bool
	CheckOption       CheckMode
	Options           []string
	Rule              ids.OID
	Consumer          ids.ObjectAddress
	Temporary         ids.SessionToken
	References        map[ids.ObjectRef]catalog.Descriptor
	Collations        map[analyze.ViewLiteralPath]uint32
	QuoteRevision     ids.DefinitionRevision
}

// NewView builds a definition from p.
func NewView(p ViewParts) *View {
	return &View{
		query:             p.Query,
		syntax:            p.Syntax,
		actionSyntax:      p.ActionSyntax,
		expressionSyntax:  p.ExpressionSyntax,
		mutationFrame:     p.MutationFrame,
		sources:           p.Sources,
		dependencies:      p.Dependencies,
		sequenceRefs:      p.SequenceRefs,
		literals:          p.Literals,
		castUsages:        p.CastUsages,
		routineInputs:     p.RoutineInputs,
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
func (v *View) Parts() ViewParts {
	return ViewParts{
		Query:             v.query,
		Syntax:            v.syntax,
		ActionSyntax:      v.actionSyntax,
		ExpressionSyntax:  v.expressionSyntax,
		MutationFrame:     v.mutationFrame,
		Sources:           v.sources,
		Dependencies:      v.dependencies,
		SequenceRefs:      v.sequenceRefs,
		Literals:          v.literals,
		CastUsages:        v.castUsages,
		RoutineInputs:     v.routineInputs,
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
func (v *View) With(edit func(*ViewParts)) *View {
	p := v.Parts()
	edit(&p)
	return NewView(p)
}

func (v *View) RuleOID() ids.OID { return v.rule }

// The accessors below read a frozen definition for code outside the stored
// presentation. Results share the published payload; callers never mutate them.

// Sources returns the stored source leaves in SQL occurrence order.
func (v *View) Sources() []ViewLeaf { return v.sources }

// Dependencies returns the frozen expression dependencies.
func (v *View) Dependencies() []catalog.ExpressionDependency { return v.dependencies }

// Invoker reports whether the view runs with the invoker's permissions.
func (v *View) Invoker() bool { return v.invoker }

// Options returns the view's reloptions as "name=value" strings.
func (v *View) Options() []string { return v.options }

// References returns the descriptors a TEMP consumer captured, by reference.
func (v *View) References() map[ids.ObjectRef]catalog.Descriptor { return v.references }

// Literals returns the captured literals by view literal path.
func (v *View) Literals() map[analyze.ViewLiteralPath]plan.LiteralExpr { return v.literals }

// Query returns the analyzed query.
func (v *View) Query() *plan.Query { return v.query }

// Syntax returns the frozen SELECT syntax.
func (v *View) Syntax() *parser.Select { return v.syntax }

// CastUsages returns the captured cast usages by view literal path.
func (v *View) CastUsages() map[analyze.ViewLiteralPath]sqltype.Usage { return v.castUsages }

// RoutineRefs returns the captured routine references by view literal path.
func (v *View) RoutineRefs() map[analyze.ViewLiteralPath]ids.ObjectRef {
	return v.routineRefs
}

// Columns returns the captured column occurrences by view literal path.
func (v *View) Columns() map[analyze.ViewLiteralPath]analyze.ViewColumn { return v.columns }

// Frames returns the analyzed projection of each lexical frame.
func (v *View) Frames() map[uint32]*plan.ProjectNode { return v.frames }

// Queries returns the analyzed projection of each nested SELECT.
func (v *View) Queries() map[*parser.Select]*plan.ProjectNode { return v.queries }

// Namespaces returns the captured namespaces.
func (v *View) Namespaces() []analyze.ViewNamespace { return v.namespaces }

// Barrier returns whether the view is a security barrier.
func (v *View) Barrier() bool { return v.barrier }

// CheckOption returns the WITH CHECK OPTION mode.
func (v *View) CheckOption() CheckMode { return v.checkOption }

// Consumer returns the TEMP consumer that owns the definition.
func (v *View) Consumer() ids.ObjectAddress { return v.consumer }

// Temporary returns the TEMP owner session token.
func (v *View) Temporary() ids.SessionToken { return v.temporary }

// QuoteRevision returns the frozen defining revision used for quoting.
func (v *View) QuoteRevision() ids.DefinitionRevision { return v.quoteRevision }

// DependsOn projects hard definition edges without discarding the complete
// execution manifest. A written support call has its own definition capture.
func (v *View) DependsOn(target ids.ObjectAddress) bool {
	for _, leaf := range v.sources {
		if target.SubID == 0 && leaf.Ref.Address == target {
			return true
		}
	}
	for _, dependency := range v.dependencies {
		if dependency.Purpose != catalog.DefinitionDependency {
			continue
		}
		a := dependency.Ref.Address
		if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
			return true
		}
	}
	return false
}

func (v *View) TemporaryConsumer() (ids.ObjectAddress, ids.SessionToken) {
	return v.consumer, v.temporary
}

func (v *View) CapturedReference(ref ids.ObjectRef) (catalog.Descriptor, bool) {
	d, ok := v.references[ref]
	return d, ok
}
