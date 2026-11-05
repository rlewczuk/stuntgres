package ddl

// DatabaseCollationVersion separates metadata validity from genuine provider
// inquiry. A changed version is copied into owned immutable descriptor data.
func DatabaseCollationVersion(recorded, actual *string) (*string, bool, error) {
	if recorded == nil && actual == nil {
		return nil, false, nil
	}
	if recorded == nil || actual == nil {
		return nil, false, sqlError("XX000", "invalid collation version change")
	}
	if *recorded == *actual {
		return recorded, false, nil
	}
	version := *actual
	return &version, true, nil
}
