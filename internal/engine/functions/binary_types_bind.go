package functions

import "stuntgres/internal/sqltype"

// SupplyBinaryTypes fills the binding catalog's usages a binary resolution
// demands, keeping usages already supplied.
func SupplyBinaryTypes(b Binder, demand BinaryDemand, types *BinaryTypes) {
	if demand == 0 {
		return
	}
	if demand.Needs(sqltype.Bytea) && types.Bytea.Type == nil {
		types.Bytea = b.Type(sqltype.Bytea).Usage()
	}
	if demand.Needs(sqltype.Bit) && types.Bit.Type == nil {
		types.Bit = b.Type(sqltype.Bit).Usage()
	}
	if demand.Needs(sqltype.VarBit) && types.VarBit.Type == nil {
		types.VarBit = b.Type(sqltype.VarBit).Usage()
	}
	if demand.Needs(sqltype.Int4) && types.Integer.Type == nil {
		types.Integer = b.Type(sqltype.Int4).Usage()
	}
	if demand.Needs(sqltype.Int8) && types.Bigint.Type == nil {
		types.Bigint = b.Type(sqltype.Int8).Usage()
	}
	if demand.Needs(sqltype.Name) && types.Name.Type == nil {
		types.Name = b.Type(sqltype.Name).Usage()
	}
	if demand.Needs(sqltype.Text) && types.Text.Type == nil {
		types.Text = b.Type(sqltype.Text).Usage()
	}
}
