package catalog

import (
	"iter"
	"maps"
	"stuntgres/internal/engine/ids"
)

// Membership only; callable descriptors and grants come from the captured root.
type ProcedureNameKey struct {
	Namespace ids.ObjectAddress
	Name      string
}
type ProcedureNameIndex struct {
	Buckets  map[ProcedureNameKey][]ids.ObjectAddress
	SQLNames map[string]int
}
type ProcedureIndexEdit struct {
	Index   *ProcedureNameIndex
	touched map[ProcedureNameKey]bool
}

func (edit *ProcedureIndexEdit) detach() {
	if edit.touched != nil {
		return
	}
	index := &ProcedureNameIndex{Buckets: make(map[ProcedureNameKey][]ids.ObjectAddress), SQLNames: make(map[string]int)}
	if edit.Index != nil {
		maps.Copy(index.Buckets, edit.Index.Buckets)
		maps.Copy(index.SQLNames, edit.Index.SQLNames)
	}
	edit.Index = index
	edit.touched = make(map[ProcedureNameKey]bool)
}
func (edit *ProcedureIndexEdit) bucket(key ProcedureNameKey, extra int) []ids.ObjectAddress {
	edit.detach()
	addresses := edit.Index.Buckets[key]
	if !edit.touched[key] {
		copyOf := make([]ids.ObjectAddress, len(addresses), len(addresses)+extra)
		copy(copyOf, addresses)
		addresses = copyOf
		edit.touched[key] = true
	}
	return addresses
}
func (edit *ProcedureIndexEdit) Replace(old, next Descriptor) {
	before, was := old.(*Procedure)
	after, is := next.(*Procedure)
	same := was && is && before.Address() == after.Address() && before.Namespace() == after.Namespace() && before.Name() == after.Name()
	if same && (before.SQL != nil) == (after.SQL != nil) {
		return
	}
	if !was && !is {
		return
	}
	edit.detach()
	if was && before.SQL != nil {
		edit.Index.SQLNames[before.Name()]--
		if edit.Index.SQLNames[before.Name()] == 0 {
			delete(edit.Index.SQLNames, before.Name())
		}
	}
	if is && after.SQL != nil {
		edit.Index.SQLNames[after.Name()]++
	}
	if same {
		return
	}
	if was {
		key := ProcedureNameKey{before.Namespace(), before.Name()}
		addresses := edit.bucket(key, 0)
		for i, address := range addresses {
			if address == before.Address() {
				copy(addresses[i:], addresses[i+1:])
				addresses = addresses[:len(addresses)-1]
				break
			}
		}
		if len(addresses) == 0 {
			delete(edit.Index.Buckets, key)
		} else {
			edit.Index.Buckets[key] = addresses
		}
	}
	if is {
		key := ProcedureNameKey{after.Namespace(), after.Name()}
		addresses := edit.bucket(key, 1)
		edit.Index.Buckets[key] = append(addresses, after.Address())
	}
}
func RebuildProcedureNames(descriptors iter.Seq2[ids.ObjectAddress, Descriptor]) *ProcedureNameIndex {
	index := &ProcedureNameIndex{Buckets: make(map[ProcedureNameKey][]ids.ObjectAddress), SQLNames: make(map[string]int)}
	for address, descriptor := range descriptors {
		if d, ok := descriptor.(*Procedure); ok {
			key := ProcedureNameKey{d.Namespace(), d.Name()}
			index.Buckets[key] = append(index.Buckets[key], address)
			if d.SQL != nil {
				index.SQLNames[d.Name()]++
			}
		}
	}
	return index
}
func (s *State) ProcedureNames() *ProcedureNameIndex         { return s.procedureNames }
func (s *State) SetProcedureNames(index *ProcedureNameIndex) { s.procedureNames = index }
func (s *State) HasSQLProcedureName(name string) bool {
	return s.procedureNames != nil && s.procedureNames.SQLNames[name] != 0
}
func (s *State) ProceduresNamed(namespace ids.ObjectAddress, name string) []ids.ObjectAddress {
	if s.procedureNames == nil {
		return nil
	}
	return s.procedureNames.Buckets[ProcedureNameKey{namespace, name}]
}
