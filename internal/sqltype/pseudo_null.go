package sqltype

// QueryNullUsage admits canonical builtin pseudo identities carrying typed SQL
// NULL in expressions. Self-ownership rejects copied descriptors. The separate
// OID32 DDLCommandValue constructor is the sole non-NULL query exception.
func (u Usage) QueryNullUsage() bool {
	d := u.Type
	return d != nil && d.queryNullOwner == d && d.lookupOnly &&
		d.namespace == "pg_catalog" && u.Typmod == -1 &&
		u.Collation == (CollationRef{}) && d.element == nil && d.base.Type == nil
}
