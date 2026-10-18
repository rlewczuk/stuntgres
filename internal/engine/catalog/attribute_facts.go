package catalog

// Released facts belong to initialized catalog descriptors, separately from
// their adapter's ability to return physical fields or infer query nullability.
type CatalogAttribute struct {
	NotNull    bool
	Dimensions int16
	Collation  uint32
}
