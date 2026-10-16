package engine

// PostgreSQL's member-set/ID cache is transaction-local, capped at 256 entries.
// The caller owns the slice (manager-held ticket or private batch preparation).
func rememberMultiXact(cache []uint32, id uint32) []uint32 {
	for i, cached := range cache {
		if cached == id {
			if i == 0 {
				return cache
			}
			copy(cache[1:i+1], cache[:i])
			cache[0] = id
			return cache
		}
	}
	if len(cache) < 256 {
		cache = append(cache, 0)
	}
	copy(cache[1:], cache[:len(cache)-1])
	cache[0] = id
	return cache
}
