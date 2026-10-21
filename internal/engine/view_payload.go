package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// viewOf returns d's concrete view definition, or nil when d stores none.
func viewOf(d *catalog.Relation) *viewDefinition {
	v, _ := d.View.(*viewDefinition)
	return v
}

// viewPayloadOf stores v as a payload; a nil definition stays a nil payload,
// so d.view == nil keeps meaning "no stored definition".
func viewPayloadOf(v *viewDefinition) catalog.ViewPayload {
	if v == nil {
		return nil
	}
	return v
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

func (v *viewDefinition) DependsOn(target ids.ObjectAddress) bool {
	for _, leaf := range v.sources {
		if target.SubID == 0 && leaf.ref.Address == target {
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
