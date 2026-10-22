package util

import (
	"maps"
	"testing"
)

// FuzzPersistentMap interprets data as operations on a persistent map, a
// Transient and a builtin model. The first byte picks the hash: the real one
// or one of the forced hashes that create collisions and deep prefixes. Each
// later pair of bytes is an operation and a key. The seed corpus runs as part
// of the ordinary tests.
func FuzzPersistentMap(f *testing.F) {
	f.Add([]byte{0, 0, 1, 0, 2, 1, 1, 2, 3})
	f.Add([]byte{1, 0, 1, 0, 2, 0, 3, 1, 2, 2, 1, 4, 9})
	f.Add([]byte{2, 0, 7, 0, 12, 0, 17, 1, 12, 3, 0, 1, 7})
	f.Add([]byte{3, 0, 31, 0, 63, 0, 95, 2, 63, 4, 0, 1, 95})
	f.Add([]byte{4, 0, 1, 0, 33, 0, 65, 1, 33, 1, 65, 1, 1})
	hashes := []hashFunc[int]{realHash[int], forcedHashes["constant"], forcedHashes["top-bits"], forcedHashes["buckets"], forcedHashes["low-bits"]}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		h := hashes[int(data[0])%len(hashes)]
		var m PersistentMap[int, int]
		model := map[int]int{}
		var kept []PersistentMap[int, int]
		var keptModels []map[int]int
		var tr *PersistentMapTransient[int, int]
		set := func(k, v int) {
			if tr != nil {
				tr.install(tr.current().set(tr.edit, pmapEntry[int, int]{hash: h(k), key: k, value: v}))
			} else {
				m = forcedSet(m, h, k, v)
			}
			model[k] = v
		}
		del := func(k int) {
			if tr != nil {
				tr.install(tr.current().delete(tr.edit, h(k), k))
			} else {
				m = forcedDelete(m, h, k)
			}
			delete(model, k)
		}
		for i := 1; i+1 < len(data); i += 2 {
			k, v := int(data[i+1]), i
			switch data[i] % 5 {
			case 0:
				set(k, v)
			case 1:
				del(k)
			case 2:
				if tr == nil {
					kept, keptModels = append(kept, m), append(keptModels, maps.Clone(model))
				}
			case 3:
				if tr == nil {
					tr = m.Transient()
				}
				set(k, v)
				del(k + 1)
			case 4:
				if tr != nil {
					m, tr = tr.Persistent(), nil
				}
			}
			if tr == nil {
				checkModel(t, m, h, model, k, k+1)
			}
		}
		if tr != nil {
			m = tr.Persistent()
		}
		checkModel(t, m, h, model)
		for i := range kept {
			checkModel(t, kept[i], h, keptModels[i])
		}
	})
}
