package catalog

import (
	"iter"
	"stuntgres/internal/engine/ids"
)

// DynamicCallable distinguishes catalog-defined SQL bodies and real owned
// transition aggregates from immutable builtin/extension providers.
func (d *Procedure) DynamicCallable() bool {
	return d != nil && (d.SQL != nil || d.Aggregate != nil && d.Builtin == nil && d.Implementation == nil)
}

// Membership only; callable descriptors and grants come from the captured root.
type ProcedureNameKey struct {
	Namespace ids.ObjectAddress
	Name      string
}
type ProcedureNameIndex struct {
	Buckets      map[ProcedureNameKey][]ids.ObjectAddress
	DynamicNames map[string]int
}
type ProcedureIndexEdit struct {
	Index   *ProcedureNameIndex
	touched map[ProcedureNameKey]bool
}

func (edit *ProcedureIndexEdit) detach() {
	if edit.touched != nil {
		return
	}
	index := &ProcedureNameIndex{Buckets: make(map[ProcedureNameKey][]ids.ObjectAddress), DynamicNames: make(map[string]int)}
	if edit.Index != nil {
		index.Buckets, index.DynamicNames = cloneMap(edit.Index.Buckets), cloneMap(edit.Index.DynamicNames)
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
	if same && before.DynamicCallable() == after.DynamicCallable() {
		return
	}
	if !was && !is {
		return
	}
	edit.detach()
	if was && before.DynamicCallable() {
		edit.Index.DynamicNames[before.Name()]--
		if edit.Index.DynamicNames[before.Name()] == 0 {
			delete(edit.Index.DynamicNames, before.Name())
		}
	}
	if is && after.DynamicCallable() {
		edit.Index.DynamicNames[after.Name()]++
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
	index := &ProcedureNameIndex{Buckets: make(map[ProcedureNameKey][]ids.ObjectAddress), DynamicNames: make(map[string]int)}
	for address, descriptor := range descriptors {
		if d, ok := descriptor.(*Procedure); ok {
			key := ProcedureNameKey{d.Namespace(), d.Name()}
			index.Buckets[key] = append(index.Buckets[key], address)
			if d.DynamicCallable() {
				index.DynamicNames[d.Name()]++
			}
		}
	}
	return index
}
func (s *State) ProcedureNames() *ProcedureNameIndex         { return s.procedureNames }
func (s *State) SetProcedureNames(index *ProcedureNameIndex) { s.procedureNames = index }
func (s *State) HasDynamicProcedureName(name string) bool {
	return s.procedureNames != nil && s.procedureNames.DynamicNames[name] != 0
}
func (s *State) ProceduresNamed(namespace ids.ObjectAddress, name string) []ids.ObjectAddress {
	if s.procedureNames == nil {
		return nil
	}
	return s.procedureNames.Buckets[ProcedureNameKey{namespace, name}]
}
