package sqltype

// CastMetadata describes one implemented PostgreSQL catalog edge. Context is
// i (implicit), a (assignment), or e (explicit); Method is f (function),
// b (binary), or i (I/O). These are catalog facts, not native execution opcodes.
// OID is a stable local generated identity, not another server's row OID.
type CastMetadata struct {
	OID             uint32
	Source, Target  ID
	Function        uint32
	Context, Method byte
}

// Provider facts distinguish declared conversions from automatic string I/O.
type castCatalogFact struct {
	oid, function   uint32
	context, method byte
}

// Existing local identities remain stable when additional providers are released.
var specialCastMetadata = [...]CastMetadata{
	{20046, Text, BPChar, 0, 'i', 'b'},
	{20047, Text, VarChar, 0, 'i', 'b'},
	{20048, BPChar, Text, 401, 'i', 'f'},
	{20049, BPChar, VarChar, 401, 'i', 'f'},
	{20050, VarChar, Text, 0, 'i', 'b'},
	{20051, VarChar, BPChar, 0, 'i', 'b'},
	{20052, Int4, Bool, 2557, 'e', 'f'},
	{20053, Bool, Int4, 2558, 'e', 'f'},
	{20054, Bool, Text, 2971, 'a', 'f'},
	{20055, BPChar, BPChar, 668, 'i', 'f'},
	{20056, VarChar, VarChar, 669, 'i', 'f'},
	{20057, Time, Time, 1968, 'i', 'f'},
	{20058, Timestamp, Timestamp, 1961, 'i', 'f'},
	{20059, Timestamptz, Timestamptz, 1967, 'i', 'f'},
	{20060, Interval, Interval, 1200, 'i', 'f'},
	{20061, Timetz, Timetz, 1969, 'i', 'f'},
	{20062, Numeric, Numeric, 1703, 'i', 'f'},
	{20063, JSON, JSONB, 0, 'a', 'i'},
	{20064, JSONB, JSON, 0, 'a', 'i'},
}

// Construct the immutable catalog once; catalog reads allocate nothing.
var castMetadata = initializeCastMetadata()

func initializeCastMetadata() []CastMetadata {
	rows := make([]CastMetadata, 73, 220)
	install := func(row CastMetadata) {
		if row.OID < 20000 || row.OID >= 20000+uint32(len(rows)) || rows[row.OID-20000].OID != 0 {
			panic("sqltype: invalid fixed cast catalog identity")
		}
		rows[row.OID-20000] = row
	}
	for _, spec := range baselineCasts {
		if spec.catalog.oid != 0 {
			fact := spec.catalog
			install(CastMetadata{fact.oid, spec.source, spec.target, fact.function, fact.context, fact.method})
		}
	}
	for _, row := range specialCastMetadata {
		install(row)
	}
	for _, row := range rows {
		if row.OID == 0 {
			panic("sqltype: incomplete fixed cast catalog")
		}
	}
	for _, row := range releasedCastMetadata {
		found := false
		for _, old := range rows {
			if old.Source == row.Source && old.Target == row.Target {
				found = true
				break
			}
		}
		if !found {
			row.OID = 20000 + uint32(len(rows))
			rows = append(rows, row)
		}
	}
	return rows
}

// CastMetadataCount returns the number of explicitly declared catalog edges.
func CastMetadataCount() int { return len(castMetadata) }

// CastMetadataAt returns an immutable value copy, or false for an invalid index.
func CastMetadataAt(index int) (CastMetadata, bool) {
	if index < 0 || index >= len(castMetadata) {
		return CastMetadata{}, false
	}
	return castMetadata[index], true
}

// BinaryCoercible reports a directional, implicit representation-compatible
// edge using the same released cast facts as pg_cast. Domain checks are not
// binary coercions; a source domain can expose its base representation.
func BinaryCoercible(source, target *Descriptor) bool {
	if source == nil || target == nil {
		return false
	}
	if source == target {
		return true
	}
	if target.IsDomain() {
		return false
	}
	source = source.BaseType()
	if source == target {
		return true
	}
	if source.IsCitext() && (target.ID() == Text || target.ID() == VarChar) {
		return true
	}
	for _, fact := range castMetadata {
		if fact.Source == source.ID() && fact.Target == target.ID() && fact.Context == 'i' && fact.Method == 'b' {
			return true
		}
	}
	return false
}
