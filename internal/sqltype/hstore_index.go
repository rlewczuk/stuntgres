package sqltype

// The catalog boundary authenticates installed declarations and exact support
// roles before setting HstoreIndexSupport. These checks retain its concrete
// method/type/member authority; dynamic routine IDs are never builtin IDs.
func validHstoreIndexSupport(u Usage, c IndexClass, s IndexSupport, method uint32) bool {
	return u.Type != nil && u.Type.BaseType().IsHstore() && u.validCollation() &&
		c.input == u.Type.BaseType().ID() && c.method == method && c.oid != 0 && c.family != 0 &&
		c.hash == (method == 405) && !c.gist && !c.brin && !c.spgist &&
		s.Provider == HstoreIndexSupport && s.Family == c.family &&
		s.Left == uint32(c.input) && s.Right == s.Left && s.Member != 0 && s.Routine != 0 &&
		s.Revision != 0 && s.FamilyRevision != 0 && s.RoutineRevision != 0
}

func validHstoreHashSupport(u Usage, c IndexClass) bool {
	a, b := c.Support(1), c.Support(2)
	return validHstoreIndexSupport(u, c, a, 405) && validHstoreIndexSupport(u, c, b, 405) &&
		a.Member != b.Member && a.Routine != b.Routine && a.FamilyRevision == b.FamilyRevision
}
