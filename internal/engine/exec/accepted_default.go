package exec

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// DefaultOccurrence is an original command occurrence, not a fresh bound node.
// Attribute and default identity are recorded separately on the owned recipe.
type DefaultOccurrence struct {
	Statement parser.Statement
	Expansion int
	Kind      uint8 // 0 direct column, 1 UPDATE assignment, 2 normalized rule row.
	Row, Slot int
}

// AcceptedDefaultSet belongs to one accepted binding. Candidates are transferred
// only after final Bind admission; retirement never changes their immutable data.
type AcceptedDefaultSet struct {
	self            *AcceptedDefaultSet
	entries         []*AcceptedDefault
	generation      uint64
	sealed, retired bool
}

func NewAcceptedDefaultSet() *AcceptedDefaultSet {
	s := &AcceptedDefaultSet{}
	s.self = s
	return s
}
func (s *AcceptedDefaultSet) Seal(generation uint64) error {
	if s == nil || s.self != s || s.retired || s.sealed {
		return ids.ErrCatalogReplaced
	}
	s.generation, s.sealed = generation, true
	return nil
}
func (s *AcceptedDefaultSet) Retire() {
	if s != nil {
		s.retired = true
	}
}
func (s *AcceptedDefaultSet) Live() bool { return s != nil && s.self == s && s.sealed && !s.retired }
func (s *AcceptedDefaultSet) MatchesGeneration(generation uint64) bool {
	return s.Live() && s.generation == generation
}

// AcceptedDefault contains only an owned scalar closure and original immutable
// source/dependency evidence. It holds no operation, authority, frame or graph.
type AcceptedDefault struct {
	self         *AcceptedDefault
	owner        *AcceptedDefaultSet
	occurrence   DefaultOccurrence
	relation     *catalog.Relation
	ref          ids.ObjectRef
	attribute    int32
	definition   catalog.ColumnDefault
	dependencies []catalog.ExpressionDependency
	descriptors  []catalog.Descriptor // Exact original provider/type evidence, never grants.
	root         *acceptedDefaultRoot
	calls        []*acceptedDefaultCall
}

func (a *AcceptedDefault) Valid() bool {
	return a != nil && a.self == a && a.owner.Live() && a.root != nil && a.root.artifact == a && a.root.self == a.root
}
func (a *AcceptedDefault) Source() (*catalog.Relation, ids.ObjectRef, int32, catalog.ColumnDefault) {
	if a == nil || a.self != a {
		return nil, ids.ObjectRef{}, 0, catalog.ColumnDefault{}
	}
	return a.relation, a.ref, a.attribute, a.definition
}

// Dependencies returns borrowed immutable original evidence.
func (a *AcceptedDefault) Dependencies() []catalog.ExpressionDependency { return a.dependencies }
func (a *AcceptedDefault) SourceDescriptor(ref ids.ObjectRef) catalog.Descriptor {
	if a == nil || a.self != a {
		return nil
	}
	for i, dependency := range a.dependencies {
		if dependency.Ref == ref && i < len(a.descriptors) {
			return a.descriptors[i]
		}
	}
	return nil
}
func (a *AcceptedDefault) Expression() expr.Expr {
	if a == nil || a.self != a {
		return nil
	}
	return a.root
}

func (s *AcceptedDefaultSet) Lookup(occurrence DefaultOccurrence, ref ids.ObjectRef, attribute int32) (*AcceptedDefault, error) {
	if !s.Live() {
		return nil, ids.ErrCatalogReplaced
	}
	for _, a := range s.entries {
		if a.occurrence == occurrence && a.ref == ref && a.attribute == attribute {
			if !a.Valid() {
				return nil, ids.ErrCatalogReplaced
			}
			return a, nil
		}
	}
	return nil, nil
}

// defaultInvocationMode is source-owner evidence, never a caller-selected
// dispatch policy. Zero and unknown values cannot authorize any occurrence.
type defaultInvocationMode uint8

const (
	defaultPredecessor defaultInvocationMode = iota + 1
	defaultCurrentPL
)

func currentPLDefault(d *catalog.Procedure) bool {
	return d != nil && d.SQL != nil && d.SQL == d.RoutineDefinition() &&
		d.SQL.Kind == proc.PLpgSQLBody && d.RoutineKind == catalog.FunctionRoutine &&
		!d.SQL.Procedure && d.SQL.Invocation == proc.OrdinaryInvocation && !d.SQL.ReturnsSet
}

// valid binds mode to both ends of the actual source-bound preparation. It
// does not replace provider validation or permit an independently supplied tree.
func (mode defaultInvocationMode) valid(definition *proc.Definition, source, prepared plan.SQLRoutineExpr) bool {
	if definition == nil || source.Procedure == nil || prepared.Procedure == nil ||
		source.Procedure.RoutineDefinition() != definition || prepared.Procedure.RoutineDefinition() != definition {
		return false
	}
	switch mode {
	case defaultPredecessor:
		return definition.Kind == proc.SQLBody || definition.Kind == proc.HostBody
	case defaultCurrentPL:
		return currentPLDefault(source.Procedure) && currentPLDefault(prepared.Procedure)
	default:
		return false
	}
}

// AcceptedDefaultCall is an immutable view of one sealed lexical call path.
// Its private occurrence pointer cannot be fabricated from a matching SQL node.
type AcceptedDefaultCall struct{ node *acceptedDefaultCall }

// Original borrows the canonical source call and original mapper path for
// lineage validation only. The actual prepared invocation is separately sealed.
func (o AcceptedDefaultCall) Original() (plan.SQLRoutineExpr, []int, bool) {
	n := o.node
	if n == nil || n.self != n || !n.artifact.Valid() || !n.mode.valid(n.definition, n.source, n.original) {
		return plan.SQLRoutineExpr{}, nil, false
	}
	for _, member := range n.artifact.calls {
		if member == n {
			return n.source, n.path, true
		}
	}
	return plan.SQLRoutineExpr{}, nil, false
}

// prepared returns only the call produced by this occurrence's completed
// source-bound preparation. Original is independent source-lineage evidence;
// transformed arguments and their prepared descriptor must not be read from it.
func (o AcceptedDefaultCall) prepared() (plan.SQLRoutineExpr, bool) {
	if _, _, ok := o.Original(); !ok {
		return plan.SQLRoutineExpr{}, false
	}
	return o.node.original, true
}

// defaultActivation is operation-local. It is never reachable from the artifact.
type defaultActivation struct {
	artifact *AcceptedDefault
	host     Host
	context  context.Context
	active   bool
}

func (a *AcceptedDefault) activate(c *Context, root *acceptedDefaultRoot) (*defaultActivation, error) {
	if !a.Valid() || root != a.root || root.self != root || c.AcceptedDefault != a || c.DefaultOccurrence != a.occurrence || c.RoutineConsumer != a.relation || c.FoldOrigin.Owner != a.ref || c.FoldOrigin.Attribute != a.attribute || c.FoldOrigin.Definition != a.definition.OID {
		return nil, ids.ErrCatalogReplaced
	}
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	auth, _, err := RoutineAdmissionAuthority(c)
	if err != nil || !auth.Live() {
		if err != nil {
			return nil, err
		}
		return nil, ids.ErrCatalogReplaced
	}
	if err := preparation.AcceptedDefaultSource(auth.Catalog, a); err != nil {
		return nil, err
	}
	return &defaultActivation{artifact: a, host: c.sqlRoutineInvocationHost(), context: c.Context, active: true}, nil
}

func (a *AcceptedDefault) validate(auth RoutineAccess, occurrence AcceptedDefaultCall) (*catalog.Procedure, error) {
	source, _, sourceOK := occurrence.Original()
	call, preparedOK := occurrence.prepared()
	if !sourceOK || !preparedOK || occurrence.node.artifact != a || !auth.Live() {
		return nil, ids.ErrCatalogReplaced
	}
	current, err := preparation.AcceptedDefaultRoutine(auth.Catalog, a, occurrence)
	if err != nil {
		return nil, err
	}
	if current == nil || current.Address() != source.Procedure.Address() || current.Address() != call.Procedure.Address() || !call.Signature.ValidFor(current.RoutineDefinition()) {
		return nil, ids.ErrCatalogReplaced
	}
	if err := auth.Roles.ValidatePrincipal(source.Procedure.Owner); err != nil {
		return nil, err
	}
	// Reuse all current language/host/transform/provider validation. Equality is
	// intentionally checked against current itself, never weakened globally.
	if _, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, current); err != nil {
		return nil, err
	}
	if occurrence.node.mode == defaultCurrentPL && !currentPLDefault(current) {
		return nil, ids.ErrCatalogReplaced
	}
	if err := auth.Check(access.Requirement{Object: call.Ref, Privileges: authorization.ExecutePrivilege}); err != nil {
		return nil, err
	}
	return current, nil
}

// AcceptedDefaultInvocation is a short-lived, exact-call capability. Both its
// identity seal and its still-active root selection are checked at dispatch.
type AcceptedDefaultInvocation struct {
	self       *AcceptedDefaultInvocation
	activation *defaultActivation
	occurrence AcceptedDefaultCall
}

// Validate independently returns live environment authority and the executable
// selected by this exact sealed occurrence. It never changes prepared inputs.
func (i *AcceptedDefaultInvocation) Validate(auth RoutineAccess, host Host, captured *catalog.Procedure) (environment, executable *catalog.Procedure, err error) {
	if i == nil || i.self != i || i.activation == nil || !i.activation.active || i.activation.host != host || i.activation.context == nil {
		return nil, nil, ids.ErrCatalogReplaced
	}
	if err := i.activation.context.Err(); err != nil {
		return nil, nil, err
	}
	call, ok := i.occurrence.prepared()
	if !ok || call.Procedure != captured || i.occurrence.node.artifact != i.activation.artifact {
		return nil, nil, ids.ErrCatalogReplaced
	}
	current, err := i.activation.artifact.validate(auth, i.occurrence)
	if err != nil {
		return nil, nil, err
	}
	switch i.occurrence.node.mode {
	case defaultPredecessor:
		return current, captured, nil
	case defaultCurrentPL:
		return current, current, nil
	default:
		return nil, nil, ids.ErrCatalogReplaced
	}
}

func (c *Context) acceptedDefaultInvocation(node *acceptedDefaultCall) (*AcceptedDefaultInvocation, error) {
	activation := c.defaultActivation
	if node == nil || node.self != node || activation == nil || !activation.active || activation.host != c.sqlRoutineInvocationHost() || activation.artifact != node.artifact || c.AcceptedDefault != node.artifact || c.defaultCall != node || c.DefaultOccurrence != node.artifact.occurrence || c.RoutineConsumer != node.artifact.relation {
		return nil, ids.ErrCatalogReplaced
	}
	auth, _, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return nil, err
	}
	occurrence := AcceptedDefaultCall{node: node}
	if _, err := node.artifact.validate(auth, occurrence); err != nil {
		return nil, err
	}
	i := &AcceptedDefaultInvocation{activation: activation, occurrence: occurrence}
	i.self = i
	return i, nil
}

type acceptedDefaultRoot struct {
	expr.Info
	self     *acceptedDefaultRoot
	artifact *AcceptedDefault
	children [1]expr.Expr
}

func (n *acceptedDefaultRoot) Eval(context expr.Context) (sqltype.Value, error) {
	c, ok := context.(*Context)
	if !ok {
		return sqltype.Value{}, ids.ErrCatalogReplaced
	}
	local := *c
	activation, err := n.artifact.activate(&local, n)
	if err != nil {
		return sqltype.Value{}, err
	}
	defer func() { activation.active = false }()
	local.defaultActivation = activation
	return n.children[0].Eval(&local)
}
func (n *acceptedDefaultRoot) Children() []expr.Expr { return n.children[:] }
func (*acceptedDefaultRoot) PlanningFold() expr.Fold { return expr.FoldNever }
func (n *acceptedDefaultRoot) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	return nil, ids.ErrCatalogReplaced
}

type acceptedDefaultCall struct {
	expr.Info
	self       *acceptedDefaultCall
	artifact   *AcceptedDefault
	original   plan.SQLRoutineExpr
	source     plan.SQLRoutineExpr // Exact original input occurrence, before preparation.
	path       []int
	mode       defaultInvocationMode
	definition *proc.Definition
}

// matchesPrepared authenticates the value copy delegated by this exact wrapper.
// Equal argument values or call assembly metadata are not owned backing proof.
func (n *acceptedDefaultCall) matchesPrepared(call plan.SQLRoutineExpr) bool {
	original, ok := (AcceptedDefaultCall{node: n}).prepared()
	return ok && call.Info == original.Info && call.Procedure == original.Procedure &&
		call.Ref == original.Ref && call.InputCollation == original.InputCollation &&
		call.WrittenCount == original.WrittenCount && call.Variadic == original.Variadic &&
		call.Signature.Result == original.Signature.Result &&
		sameDefaultBacking(call.Args, original.Args) &&
		sameDefaultBacking(call.ArgNames, original.ArgNames) &&
		sameDefaultBacking(call.InputMap, original.InputMap) &&
		sameDefaultBacking(call.ChildSlots, original.ChildSlots) &&
		sameDefaultBacking(call.Signature.Inputs, original.Signature.Inputs)
}

func sameDefaultBacking[T any](a, b []T) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

func (n *acceptedDefaultCall) Children() []expr.Expr { return n.original.Args }
func (*acceptedDefaultCall) PlanningFold() expr.Fold { return expr.FoldNever }
func (n *acceptedDefaultCall) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	return nil, ids.ErrCatalogReplaced
}
func (n *acceptedDefaultCall) Eval(context expr.Context) (sqltype.Value, error) {
	c, ok := context.(*Context)
	if !ok || n.self != n {
		return sqltype.Value{}, ids.ErrCatalogReplaced
	}
	local := *c
	local.defaultCall = n
	if _, err := local.acceptedDefaultInvocation(n); err != nil {
		return sqltype.Value{}, err
	}
	return n.original.Eval(&local)
}

func preflightAcceptedDefault(c *Context, root *acceptedDefaultRoot, decisive bool) (expr.Expr, bool, error) {
	local := *c
	activation, err := root.artifact.activate(&local, root)
	if err != nil {
		return nil, false, err
	}
	defer func() { activation.active = false }()
	local.defaultActivation = activation
	if _, _, err := PreflightCoercionsWithPolicy(&local, root.children[0], decisive); err != nil {
		return nil, false, err
	}
	c.PreparedDomainChecks = local.PreparedDomainChecks
	return root, false, nil
}

// AdmitAcceptedDefault keeps the exact root/call selection around the existing
// owner's full child traversal, including SQL hidden-argument recipe boundaries.
func AdmitAcceptedDefault(c *Context, node expr.Expr, visit func(*Context, expr.Expr) error) (bool, error) {
	if c.AcceptedDefault != nil && c.defaultActivation == nil && node != c.AcceptedDefault.Expression() {
		return true, ids.ErrCatalogReplaced
	}
	switch n := node.(type) {
	case *acceptedDefaultRoot:
		local := *c
		activation, err := n.artifact.activate(&local, n)
		if err != nil {
			return true, err
		}
		defer func() { activation.active = false }()
		local.defaultActivation = activation
		return true, visit(&local, n.children[0])
	case *acceptedDefaultCall:
		local := *c
		local.defaultCall = n
		if _, err := local.acceptedDefaultInvocation(n); err != nil {
			return true, err
		}
		return true, visit(&local, n.original)
	}
	return false, nil
}

func (n *acceptedDefaultCall) IntrinsicVolatility() expr.Volatility {
	return n.original.IntrinsicVolatility()
}
func (n *acceptedDefaultCall) GroupProperties(children []expr.Expr) expr.Properties {
	return n.original.GroupProperties(children)
}
func (n *acceptedDefaultCall) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return n.original.Capture(kind)
}
