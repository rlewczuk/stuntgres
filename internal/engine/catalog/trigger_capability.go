package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
)

// BuiltinDMLTrigger identifies only a builtin declaration, never a user routine
// with the same name. Callers still validate current-root/provider provenance.
func BuiltinDMLTrigger(d *Procedure) ids.OID {
	if d == nil || d.Builtin == nil || d.SQL != nil || d.RoutineKind != FunctionRoutine || d.Result != 2279 || d.Signature != "" {
		return 0
	}
	identity := d.Builtin.Identity
	if d.Address() != (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID}) || identity.Namespace != ids.PGCatalogNamespaceOID || identity.Signature != "" || d.Builtin.Kind != FunctionRoutine || d.Builtin.Result != 2279 || d.Builtin.Providers == 0 {
		return 0
	}
	switch identity.ID {
	case 1291:
		if identity.Name == "suppress_redundant_updates_trigger" {
			return identity.ID
		}
	case 3752:
		if identity.Name == "tsvector_update_trigger" {
			return identity.ID
		}
	case 3753:
		if identity.Name == "tsvector_update_trigger_column" {
			return identity.ID
		}
	}
	return 0
}

// SupportsDMLTrigger is shared by installation, stored validation and firing.
// It describes body capability, not permission or current catalog authority.
func SupportsDMLTrigger(d *Procedure) bool {
	return BuiltinDMLTrigger(d) != 0 || d != nil && d.RoutineKind == FunctionRoutine && d.Result == 2279 && d.Signature == "" && d.SQL != nil && d.SQL.Invocation == proc.DMLTriggerInvocation && d.SQL.PL != nil && len(d.SQL.Inputs) == 0
}
