package catalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/sqltype"
)

// SepgsqlRoutine seals an actual admitted C declaration independently of its
// mutable SQL name, owner, ACL and routine attributes. It is not an extension.
type SepgsqlRoutine struct {
	owner   *Owner
	address ids.ObjectAddress
	policy  *sepgsql.Runtime
	symbol  string
	layout  *proc.Definition
}

func SepgsqlDeclaration(symbol string) (ProcedureSignature, ids.OID, bool, bool) {
	switch symbol {
	case "sepgsql_getcon":
		return "", 25, false, true
	case "sepgsql_setcon", "sepgsql_restorecon":
		return "25", 16, false, true
	case "sepgsql_mcstrans_in", "sepgsql_mcstrans_out":
		return "25", 25, true, true
	}
	return "", 0, false, false
}
func NewSepgsqlRoutine(view Read, policy *sepgsql.Runtime, symbol string, address ids.ObjectAddress, formals []proc.Formal) (*SepgsqlRoutine, error) {
	signature, result, _, known := SepgsqlDeclaration(symbol)
	owner := view.Owner()
	if !known || policy == nil || owner == nil || owner.Sepgsql != policy || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 {
		return nil, fmt.Errorf("stuntgres: invalid sepgsql declaration capability")
	}
	typ, ok := view.LookupType(sqltype.ID(result))
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing sepgsql result type")
	}
	layout := &proc.Definition{Kind: proc.NativeBody, Result: typ.Usage(), Source: symbol, Name: symbol}
	var native []proc.Formal
	if signature != "" {
		text, ok := view.LookupType(sqltype.Text)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing sepgsql input type")
		}
		formal := proc.Formal{Mode: "i", Usage: text.Usage()}
		if formals != nil {
			if len(formals) != 1 || formals[0].Mode != "i" || formals[0].HasDefault || formals[0].Usage.Type == nil || formals[0].Usage.Type.ID() != sqltype.Text {
				return nil, fmt.Errorf("stuntgres: invalid sepgsql input declaration")
			}
			formal.Name = formals[0].Name
		}
		native = []proc.Formal{formal}
	} else if len(formals) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid sepgsql zero-argument declaration")
	}
	if err := layout.SetFormals(native); err != nil {
		return nil, err
	}
	return &SepgsqlRoutine{owner: owner, address: address, policy: policy, symbol: symbol, layout: layout}, nil
}
func (r *SepgsqlRoutine) Symbol() string           { return r.symbol }
func (r *SepgsqlRoutine) Policy() *sepgsql.Runtime { return r.policy }
func ValidateSepgsqlRoutine(view Read, d *Procedure) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid sepgsql declaration authority") }
	r := d.Sepgsql
	if r == nil || r.owner != view.Owner() || r.address != d.Address() || r.policy == nil || view.Owner().Sepgsql != r.policy || view.Get(d.Address()) != d {
		return invalid()
	}
	signature, result, _, ok := SepgsqlDeclaration(r.symbol)
	if !ok || d.Signature != signature || d.Result != result || d.RoutineKind != FunctionRoutine || d.Display.Language != CRoutineLanguage || !d.Display.HasBinary || d.Display.Binary != "$libdir/sepgsql" || d.Display.Source != r.symbol || d.Language != (ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 13}) || d.SQL != nil || d.Host != nil || d.HostHandler != nil || d.HostConversion != nil || d.Builtin != nil || d.Implementation != nil || d.Origin != nil || d.Foreign != nil || d.Aggregate != nil || d.RangeConstructor != nil || d.PLHandler != NoPLHandler || d.SQLBody != nil || d.SQLDefaults != nil || d.ReturnsSet() || len(d.Body) != 0 || d.Conversion.Name() != "" || r.layout.Kind != proc.NativeBody {
		return invalid()
	}
	if typ, ok := view.LookupType(sqltype.ID(result)); !ok || r.layout.Result != typ.Usage() {
		return invalid()
	}
	return nil
}
