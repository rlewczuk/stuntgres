package sqltype

// QueryNullUsage admits only canonical builtin pseudo identities that carry
// typed SQL NULL in expressions. Self-ownership rejects copied descriptors;
// this capability grants no non-NULL codec, declaration family or stored type.
func (u Usage) QueryNullUsage() bool {
	d := u.Type
	return d != nil && d.queryNullOwner == d && d.lookupOnly &&
		d.namespace == "pg_catalog" && u.Typmod == -1 &&
		u.Collation == (CollationRef{}) && d.element == nil && d.base.Type == nil
}
