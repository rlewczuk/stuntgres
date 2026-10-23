package analyze

// Environment and definition invalidation retires explicit user-type and
// selected geometric type-name casts. Frozen composite expansion identity keeps
// its existing contract. Neither function mutates a map it is given.

func domainCastKey(key PreparedTypeKey) bool {
	return key.Kind == DomainCast || key.Kind == IdentityCast || key.Kind == TypeNameCast
}

// RetireDomainCasts returns types without its identity/type-name cast group and
// reports whether it had any. Without any it returns types itself.
func RetireDomainCasts(types map[PreparedTypeKey]TypeOccurrence) (map[PreparedTypeKey]TypeOccurrence, bool) {
	for key := range types {
		if !domainCastKey(key) {
			continue
		}
		next := make(map[PreparedTypeKey]TypeOccurrence, len(types)-1)
		for k, v := range types {
			if !domainCastKey(k) {
				next[k] = v
			}
		}
		return next, true
	}
	return types, false
}

// PublishDomainCasts replaces the identity/type-name cast group from a successful
// analysis, preserving unrelated frozen composite occurrences.
func PublishDomainCasts(types, captures map[PreparedTypeKey]TypeOccurrence) map[PreparedTypeKey]TypeOccurrence {
	for key := range captures {
		if !domainCastKey(key) {
			continue
		}
		next := make(map[PreparedTypeKey]TypeOccurrence, len(types)+len(captures))
		for k, v := range types {
			if !domainCastKey(k) {
				next[k] = v
			}
		}
		for k, v := range captures {
			if domainCastKey(k) {
				next[k] = v
			}
		}
		return next
	}
	return types
}
