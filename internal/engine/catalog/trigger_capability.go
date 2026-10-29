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

// NativeDMLTrigger identifies native bodies after ordinary provider admission.
// Capability alone is not a substitute for current catalog authentication.
func NativeDMLTrigger(d *Procedure) bool {
	if BuiltinDMLTrigger(d) != 0 {
		return true
	}
	if d == nil {
		return false
	}
	p, ok := d.Implementation.(interface{ SupportsDMLTrigger(*Procedure) bool })
	return ok && p.SupportsDMLTrigger(d)
}

// SupportsDMLTrigger is shared by installation, stored validation and firing.
// It describes body capability, not permission or current catalog authority.
func SupportsDMLTrigger(d *Procedure) bool {
	return NativeDMLTrigger(d) || d != nil && d.RoutineKind == FunctionRoutine && d.Result == 2279 && d.Signature == "" && d.SQL != nil && d.SQL.Kind == proc.PLpgSQLBody && d.SQL.Invocation == proc.DMLTriggerInvocation && len(d.SQL.Inputs) == 0
}
