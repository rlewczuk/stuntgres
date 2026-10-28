package catalog

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// CapturedIndexOrdering is an immutable installed ordering selected from a
// genuine catalog. It retains declarations, not a catalog, reader or authority
// callback. Sort keys share this record across immutable graph copies.
type CapturedIndexOrdering struct {
	usage sqltype.Usage
	kernel sqltype.IndexComparison
	refs [4]ids.ObjectRef // class, family, support member, routine
	originalClass *OperatorClass
	originalFamily *OperatorFamily
	originalMember *OperatorFamilySupport
	originalRoutine *Procedure
	classStamp OperatorClass
	familyStamp OperatorFamily
	memberStamp OperatorFamilySupport
	routineStamp orderingRoutineStamp
}

type orderingRoutineStamp struct {
	revision ids.DefinitionRevision
	namespace ids.ObjectAddress
	signature ProcedureSignature
	result ids.OID
	kind RoutineKind
	strict bool
	volatility expr.Volatility
	display RoutineDisplay
	installed InstalledRoutineDeclaration
	parallel byte
	cost,rows float32
	configuration *RoutineConfiguration
	implementation ExtensionImplementation
}

func orderingRoutineOf(p *Procedure) orderingRoutineStamp {
	return orderingRoutineStamp{
		revision: p.Revision(), namespace: p.Namespace(), signature: p.Signature,
		result: p.Result, kind: p.RoutineKind, strict: p.Strict, volatility: p.Volatility,
		display: p.Display, installed: p.Installed, implementation: p.Implementation,
		parallel: p.Parallel, cost: p.Cost, rows: p.Rows, configuration: p.Configuration,
	}
}

func nativeOrderingRoutine(p *Procedure) bool {
	return p != nil && p.Builtin == nil && p.Implementation != nil &&
		p.SQL == nil && p.SQLBody == nil && p.SQLDefaults == nil && p.Foreign == nil &&
		p.AccessMethod == NoAccessMethod && p.BrinSupport == 0 && p.Conversion.Name() == "" &&
		p.Dictionary == (ids.ObjectAddress{}) && p.RangeConstructor == nil &&
		p.DeclarationBody == nil && len(p.DeclarationDependencies) == 0 && len(p.Body) == 0 &&
		p.Aggregate == nil && p.Language == (ids.ObjectAddress{}) && p.PLHandler == NoPLHandler &&
		p.LiteralDefaults == nil && p.InputNames == nil && !p.SecurityDefiner &&
		p.Strict && p.Volatility == expr.Immutable
}

// CaptureIndexOrdering seals the exact registered source declarations and
// effective revisions after the typed support binder selected its operation.
func CaptureIndexOrdering(view Read, usage sqltype.Usage, class sqltype.IndexClass, kernel sqltype.IndexComparison) (*CapturedIndexOrdering, error) {
	invalid := func() (*CapturedIndexOrdering, error) {
		return nil, fmt.Errorf("stuntgres: invalid source ordering authority")
	}
	if view.Owner() == nil || usage.Type == nil || !kernel.DatumComparison().OrderingFor(usage) ||
		kernel.DatumComparison().DefaultBtreeOpclass() != class.OID() || class.MethodOID() != 403 {
		return invalid()
	}
	typ, ok := view.LookupType(usage.Type.ID())
	if !ok || typ != usage.Type {
		return invalid()
	}
	fact, ok := view.OperatorClass(ids.OID(class.OID()))
	if !ok || fact.Extension == (ids.ObjectAddress{}) {
		return invalid()
	}
	current, ok := view.IndexClass(fact.Address().ObjectID)
	if !ok || !current.SameIdentity(class) {
		return invalid()
	}
	family, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: fact.Family})
	if !ok || family.Extension != fact.Extension {
		return invalid()
	}
	member, ok := view.FamilySupport(fact.Family, fact.InputType, fact.InputType, 1)
	if !ok || member.InternalOwner != fact.Address() {
		return invalid()
	}
	procedure, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: member.Function})
	if !ok || !nativeOrderingRoutine(procedure) || procedure.Extension != fact.Extension {
		return invalid()
	}
	installation, ok := view.Extension(fact.Extension)
	if !ok {
		return invalid()
	}
	provider := view.Owner().Extensions[installation.Name()]
	if provider == nil || provider != procedure.Implementation {
		return invalid()
	}
	if err := provider.ValidateExtension(view, installation); err != nil {
		return nil, err
	}
	support := kernel.Support()
	if support != class.Support(1) || support.Provider == sqltype.BuiltinIndexSupport ||
		support.Family != uint32(family.Address().ObjectID) ||
		support.Member != uint32(member.Address().ObjectID) ||
		support.Routine != uint32(procedure.Address().ObjectID) ||
		support.Left != uint32(fact.InputType) || support.Right != support.Left ||
		support.ClassRevision != uint64(fact.Revision()) ||
		support.Revision != uint64(member.Revision()) ||
		support.FamilyRevision != uint64(family.Revision()) ||
		support.RoutineRevision != uint64(procedure.Revision()) ||
		fact.Revision() == 0 || family.Revision() == 0 || member.Revision() == 0 || procedure.Revision() == 0 {
		return invalid()
	}
	capture := &CapturedIndexOrdering{
		usage: usage, kernel: kernel,
		originalClass: fact, originalFamily: family, originalMember: member, originalRoutine: procedure,
		classStamp: *fact, familyStamp: *family, memberStamp: *member, routineStamp: orderingRoutineOf(procedure),
	}
	for i, address := range [4]ids.ObjectAddress{fact.Address(), family.Address(), member.Address(), procedure.Address()} {
		capture.refs[i] = view.Owner().Domain.Ref(view.Epoch(), address)
	}
	return capture, nil
}

// Validate checks original immutable declarations before any destination lookup.
// The comparison projection must still be paired with this exact typed kernel.
func (c *CapturedIndexOrdering) Validate(usage sqltype.Usage, comparison sqltype.Comparison) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid captured source ordering") }
	if c == nil || usage != c.usage || !comparison.OrderingFor(usage) ||
		!comparison.Equal(c.kernel.DatumComparison()) || c.originalClass == nil ||
		c.originalFamily == nil || c.originalMember == nil || !nativeOrderingRoutine(c.originalRoutine) {
		return invalid()
	}
	if *c.originalClass != c.classStamp || *c.originalFamily != c.familyStamp ||
		*c.originalMember != c.memberStamp || orderingRoutineOf(c.originalRoutine) != c.routineStamp {
		return invalid()
	}
	if c.originalRoutine.Address() != c.refs[3].Address || c.originalRoutine.Extension != c.classStamp.Extension {
		return invalid()
	}
	for i, address := range [4]ids.ObjectAddress{c.classStamp.Address(), c.familyStamp.Address(), c.memberStamp.Address(), c.originalRoutine.Address()} {
		ref := c.refs[i]
		if ref.Domain() == nil || ref.Domain() != c.refs[0].Domain() ||
			ref.Epoch != c.refs[0].Epoch || ref.Address != address {
			return invalid()
		}
	}
	return nil
}

// References exposes only the four source identities needed by the authorized
// transport mapper; it does not refresh declarations from a destination graph.
func (c *CapturedIndexOrdering) References() [4]ids.ObjectRef {return c.refs}
