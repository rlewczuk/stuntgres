package analyze

// Environment and definition invalidation permanently retires domain and
// selected geometric type-name casts; frozen composite identity keeps its
// existing contract. Neither function mutates a map it is given: another
// description or an admitted binder may share it.

func domainCastKey(key PreparedTypeKey) bool {
	return key.Kind == DomainCast || key.Kind == TypeNameCast
}

// RetireDomainCasts returns types without its domain and type-name casts and
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

// PublishDomainCasts returns types with its domain and type-name casts
// replaced by those of a successful analysis' captures. Without a cast in
// captures it returns types itself.
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
