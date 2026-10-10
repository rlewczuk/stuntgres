package engine

// A private version maintains only indexes ready when its statement accepted it.
// Nil is used by already committed/imported logical roots, not public write admission.
func rowMaintainsIndex(row rowVersion, index *relationDescriptor) bool {
	if row.indexAdmission == nil {
		return true
	}
	admitted, ok := row.indexAdmission.descriptors[index.address].(*relationDescriptor)
	return ok && admitted.sqlIndex != nil && admitted.sqlIndex.ready && admitted.sqlIndex.live
}
