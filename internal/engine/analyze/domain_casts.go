package analyze

// Environment and definition invalidation retires explicit user-type and
// selected geometric type-name casts and anonymous constructor shapes. Frozen composite expansion identity keeps
// its existing contract. Neither function mutates a map it is given.

func refreshableTypeKey(key PreparedTypeKey) bool {
	return key.Kind == DomainCast || key.Kind == IdentityCast || key.Kind == TypeNameCast || key.Kind == RecordConstructor
}

// RetireRefreshableTypes returns types without refreshable casts/constructor shapes and
// reports whether it had any. Without any it returns types itself.
func RetireRefreshableTypes(types map[PreparedTypeKey]TypeOccurrence) (map[PreparedTypeKey]TypeOccurrence, bool) {
	for key := range types {
		if !refreshableTypeKey(key) {
			continue
		}
		next := make(map[PreparedTypeKey]TypeOccurrence, len(types)-1)
		for k, v := range types {
			if !refreshableTypeKey(k) {
				next[k] = v
			}
		}
		return next, true
	}
	return types, false
}

// PublishRefreshableTypes replaces refreshable casts/constructor shapes from a successful
// analysis, preserving unrelated frozen composite occurrences.
func PublishRefreshableTypes(types, captures map[PreparedTypeKey]TypeOccurrence) map[PreparedTypeKey]TypeOccurrence {
	for key := range captures {
		if !refreshableTypeKey(key) {
			continue
		}
		next := make(map[PreparedTypeKey]TypeOccurrence, len(types)+len(captures))
		for k, v := range types {
			if !refreshableTypeKey(k) {
				next[k] = v
			}
		}
		for k, v := range captures {
			if refreshableTypeKey(k) {
				next[k] = v
			}
		}
		return next
	}
	return types
}

// ReuseRecordConstructors keeps exact anonymous descriptor identity after a
// fresh Describe proved every member usage. fresh is the private successful
// binder's map; no published capture or datum is retagged or mutated.
func ReuseRecordConstructors(fresh, previous map[PreparedTypeKey]TypeOccurrence) {
	for key, current := range fresh {
		if key.Kind != RecordConstructor {
			continue
		}
		old, ok := previous[key]
		if !ok || old.Base.Type == nil || current.Base.Type == nil || old.Base.Type.CompositeLen() != current.Base.Type.CompositeLen() {
			continue
		}
		same := true
		for i := range current.Base.Type.CompositeLen() {
			if old.Base.Type.CompositeMember(i) != current.Base.Type.CompositeMember(i) {
				same = false
				break
			}
		}
		if same {
			fresh[key] = old
		}
	}
}
