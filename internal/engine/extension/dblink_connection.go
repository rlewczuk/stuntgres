package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Slots append to, rather than renumber, the original six installed identities.
const dblinkConnectionFirstSlot = 6

var dblinkConnections = [...]struct {
	name, symbol    string
	signature       catalog.ProcedureSignature
	result          uint32
	strict, definer bool
}{
	{"dblink_connect", "dblink_connect", "25", 25, true, false},
	{"dblink_connect", "dblink_connect", "25,25", 25, true, false},
	{"dblink_connect_u", "dblink_connect", "25", 25, true, true},
	{"dblink_connect_u", "dblink_connect", "25,25", 25, true, true},
	{"dblink_disconnect", "dblink_disconnect", "", 25, true, false},
	{"dblink_disconnect", "dblink_disconnect", "25", 25, true, false},
	{"dblink_get_connections", "dblink_get_connections", "", 1009, false, false},
}

// SupportsConnectionRoutine classifies bodies that enter the shared routine
// environment. It does not replace full installed declaration validation.
func (p *DblinkProvider) SupportsConnectionRoutine(d *catalog.Procedure) bool {
	return p == StockDblink && d != nil && d.Implementation == p &&
		d.Origin.Valid(d.Address()) && d.Origin.Installation.Implementation == p &&
		d.Origin.Declaration >= dblinkConnectionFirstSlot && d.Origin.Declaration < dblinkMemberCount
}

func ValidateDblinkConnectionCall(v catalog.Read, p *catalog.Procedure, call functions.DblinkConnection) error {
	if p == nil || DblinkProviderOf(p) != StockDblink || call.RoutineReference != p {
		return fmt.Errorf("stuntgres: invalid dblink connection identity")
	}
	name, err := ProcedureInstallationName(v, p)
	if err != nil {
		return err
	}
	if name != call.Operation.Name() || string(p.Signature) != call.Signature() || p.Result != ids.OID(call.ResultOID()) {
		return fmt.Errorf("stuntgres: mismatched dblink connection declaration")
	}
	if err := call.ValidateArguments(call.Children()); err != nil {
		return err
	}
	typ, ok := v.LookupType(sqltype.ID(p.Result))
	if !ok || call.ResultUsage() != typ.Usage() {
		return fmt.Errorf("stuntgres: invalid dblink connection result")
	}
	return nil
}
