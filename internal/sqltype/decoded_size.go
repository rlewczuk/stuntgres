package sqltype

// LogicalSize is a deterministic decoded-data budget: a sixteen-byte value slot
// plus canonical scalar bytes and structural entries. JSONB charges its decoded
// tree, not the redundant rendered-text cache. It neither formats a value nor
// invokes type I/O, and is not Go heap or physical PostgreSQL tuple size.
// Immutable payloads make it safe during callback-free capture under Engine.Mu.
func (v Value) LogicalSize() int64 {
	if v.null {
		return 16
	}
	if v.jsonb != nil {
		return 16 + logicalJSONSize(v.jsonb.root)
	}
	size := int64(16 + len(v.text))
	if v.array != nil {
		size += int64(8 * len(v.array.dimensions))
		for _, element := range v.array.elements {
			size += element.LogicalSize()
		}
	}
	if v.composite != nil {
		for _, field := range v.composite.fields {
			size += field.LogicalSize()
		}
	}
	if v.xml != nil {
		size += int64(len(v.xml.result.Original()))
	}
	if v.multirange != nil {
		for _, member := range v.multirange.members {
			size += logicalRangeSize(member)
		}
	}
	if v.typ != nil && v.typ.BaseType().IsRange() {
		size += logicalRangeSize(v.rangeDatum)
	}
	if v.queryInt != nil {
		size += int64(16 * len(v.queryInt.nodes))
	}
	if v.lquery != nil {
		size += int64(len(v.lquery.text))
	}
	if v.ltree != nil {
		size += int64(len(v.ltree.text))
	}
	if v.ltxtquery != nil {
		size += int64(len(v.ltxtquery.text))
	}
	if v.hstore != nil {
		for _, pair := range v.hstore.pairs {
			size += int64(16 + len(pair.key) + len(pair.value))
		}
	}
	if v.ftsVector != nil {
		size += int64(len(v.ftsVector.String()))
	}
	if v.ftsQuery != nil {
		size += int64(len(v.ftsQuery.String()))
	}
	if v.cube != nil {
		size += int64(8 * len(v.cube.coordinates))
	}
	if v.geometry != nil {
		size += 32
	}
	if v.path != nil {
		size += int64(16 * len(v.path.points))
	}
	if v.polygon != nil {
		size += int64(16 * len(v.polygon.points))
	}
	if v.snapshot != nil {
		size += 16 + int64(8*len(v.snapshot.members))
	}
	if v.statistics != nil {
		size += int64(len(v.statistics.binary) + len(v.statistics.display))
	}
	return size
}

func logicalRangeSize(r rangeDatum) int64 {
	size := int64(16)
	for _, bound := range [2]rangeBound{r.lower, r.upper} {
		size += int64(16 + len(bound.coefficient))
		if bound.value != nil {
			size += bound.value.LogicalSize()
		}
	}
	return size
}

func logicalJSONSize(node jsonbNode) int64 {
	size := int64(16 + len(node.text) + len(node.number.digits))
	for _, item := range node.items {
		size += logicalJSONSize(item)
	}
	for _, field := range node.fields {
		size += int64(len(field.key)) + logicalJSONSize(field.value)
	}
	return size
}
