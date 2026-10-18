package engine

import "stuntgres/internal/engine/ids"

// viewPayload is the frozen stored-SQL presentation of a view or
// materialized view, as relationDescriptor.View holds it. It exposes only
// what catalog code needs: the rewrite rule identity, dependencies, temporary
// ownership and validation. The concrete *viewDefinition, which holds plan IR,
// stays in the engine behind viewOf.
type viewPayload interface {
	// RuleOID returns the pg_rewrite identity of the view's _RETURN rule.
	RuleOID() ids.OID
	// DependsOn reports whether the presentation reads target. A zero SubID
	// matches the whole object.
	DependsOn(target ids.ObjectAddress) bool
	// TemporaryConsumer returns the relation that owns the presentation's
	// temporary captures and the session that owns them; both are zero for a
	// presentation without temporary references.
	TemporaryConsumer() (ids.ObjectAddress, ids.SessionToken)
	// CapturedReference returns the descriptor the presentation captured for
	// a temporary reference.
	CapturedReference(ref ids.ObjectRef) (Descriptor, bool)
	// Validate checks the presentation against its owning relation.
	Validate(v CatalogRead, owner *relationDescriptor) error
}

// viewOf returns d's concrete view definition, or nil when d stores none.
func viewOf(d *relationDescriptor) *viewDefinition {
	v, _ := d.View.(*viewDefinition)
	return v
}

// viewPayloadOf stores v as a payload; a nil definition stays a nil payload,
// so d.view == nil keeps meaning "no stored definition".
func viewPayloadOf(v *viewDefinition) viewPayload {
	if v == nil {
		return nil
	}
	return v
}

func (v *viewDefinition) RuleOID() ids.OID { return v.rule }

func (v *viewDefinition) DependsOn(target ids.ObjectAddress) bool {
	for _, leaf := range v.sources {
		if target.SubID == 0 && leaf.ref.Address == target {
			return true
		}
	}
	for _, dependency := range v.dependencies {
		a := dependency.ref.Address
		if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
			return true
		}
	}
	return false
}

func (v *viewDefinition) TemporaryConsumer() (ids.ObjectAddress, ids.SessionToken) {
	return v.consumer, v.temporary
}

func (v *viewDefinition) CapturedReference(ref ids.ObjectRef) (Descriptor, bool) {
	d, ok := v.references[ref]
	return d, ok
}
