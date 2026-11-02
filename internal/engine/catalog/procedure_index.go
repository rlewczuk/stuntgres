package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// DynamicCallable distinguishes catalog-defined bodies, owned constructors and
// transition aggregates from immutable builtin/extension providers.
func (d *Procedure) DynamicCallable() bool {
	return d != nil && (d.RoutineDefinition() != nil || d.RangeConstructor != nil || d.Aggregate != nil && d.Builtin == nil && d.Implementation == nil)
}

// Membership only; callable descriptors and grants come from the captured root.
type ProcedureNameKey struct {
	Namespace ids.ObjectAddress
	Name      string
}
type ProcedureNameIndex struct {
	// Buckets is persistent: an edit shares it and copies only the paths it
	// changes, so a renamed or added routine never copies every builtin name.
	Buckets      util.PersistentMap[ProcedureNameKey, []ids.ObjectAddress]
	DynamicNames map[string]int
	// Relabelled provider spellings (including vacated names) must resolve
	// against the catalog, never resurrect a released registry fallback.
	ProviderOverrides map[string]bool
}

// Named returns the addresses of the procedures named key.
func (x *ProcedureNameIndex) Named(key ProcedureNameKey) []ids.ObjectAddress {
	if x == nil {
		return nil
	}
	addresses, _ := x.Buckets.Get(key)
	return addresses
}

// ProcedureIndexEdit changes a copy of Index: its first change gives the edit
// its own index header, each small map is copied when first written, and each
// touched bucket is copied at most once.
type ProcedureIndexEdit struct {
	Index              *ProcedureNameIndex
	touched            map[ProcedureNameKey]bool // nil until Index is the edit's own
	dynamic, overrides bool                      // the edit owns that map
}

func (edit *ProcedureIndexEdit) own() {
	if edit.touched != nil {
		return
	}
	index := &ProcedureNameIndex{}
	if edit.Index != nil {
		*index = *edit.Index
	}
	edit.Index = index
	edit.touched = make(map[ProcedureNameKey]bool)
}

func (edit *ProcedureIndexEdit) dynamicNames() map[string]int {
	edit.own()
	if !edit.dynamic {
		edit.Index.DynamicNames, edit.dynamic = cloneMap(edit.Index.DynamicNames), true
	}
	return edit.Index.DynamicNames
}

func (edit *ProcedureIndexEdit) providerOverrides() map[string]bool {
	edit.own()
	if !edit.overrides {
		edit.Index.ProviderOverrides, edit.overrides = cloneMap(edit.Index.ProviderOverrides), true
	}
	return edit.Index.ProviderOverrides
}

func (edit *ProcedureIndexEdit) bucket(key ProcedureNameKey, extra int) []ids.ObjectAddress {
	edit.own()
	addresses, _ := edit.Index.Buckets.Get(key)
	if !edit.touched[key] {
		copyOf := make([]ids.ObjectAddress, len(addresses), len(addresses)+extra)
		copy(copyOf, addresses)
		addresses = copyOf
		edit.touched[key] = true
	}
	return addresses
}
func providerProcedureRelabeled(before, after *Procedure) bool {
	return before != nil && after != nil && (before.Builtin != nil || before.Implementation != nil) &&
		(before.Address() != after.Address() || before.Namespace() != after.Namespace() || before.Name() != after.Name())
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
	if providerProcedureRelabeled(before, after) {
		overrides := edit.providerOverrides()
		overrides[before.Name()] = true
		overrides[after.Name()] = true
	}
	if was && before.DynamicCallable() {
		names := edit.dynamicNames()
		names[before.Name()]--
		if names[before.Name()] == 0 {
			delete(names, before.Name())
		}
	}
	if is && after.DynamicCallable() {
		edit.dynamicNames()[after.Name()]++
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
			edit.Index.Buckets = edit.Index.Buckets.Delete(key)
		} else {
			edit.Index.Buckets = edit.Index.Buckets.Set(key, addresses)
		}
	}
	if is {
		key := ProcedureNameKey{after.Namespace(), after.Name()}
		addresses := edit.bucket(key, 1)
		edit.Index.Buckets = edit.Index.Buckets.Set(key, append(addresses, after.Address()))
	}
}
func RebuildProcedureNames(descriptors iter.Seq2[ids.ObjectAddress, Descriptor]) *ProcedureNameIndex {
	index := &ProcedureNameIndex{DynamicNames: make(map[string]int)}
	buckets := index.Buckets.Transient()
	for address, descriptor := range descriptors {
		if d, ok := descriptor.(*Procedure); ok {
			key := ProcedureNameKey{d.Namespace(), d.Name()}
			addresses, _ := buckets.Get(key)
			buckets.Set(key, append(addresses, address))
			if d.DynamicCallable() {
				index.DynamicNames[d.Name()]++
			}
		}
	}
	index.Buckets = buckets.Persistent()
	return index
}
func (s *State) ProcedureNames() *ProcedureNameIndex         { return s.procedureNames }
func (s *State) SetProcedureNames(index *ProcedureNameIndex) { s.procedureNames = index }
func (s *State) HasDynamicProcedureName(name string) bool {
	return s.procedureNames != nil && s.procedureNames.DynamicNames[name] != 0
}

func (s *State) HasProviderProcedureOverride(name string) bool {
	return s.procedureNames != nil && s.procedureNames.ProviderOverrides[name]
}
func (s *State) ProceduresNamed(namespace ids.ObjectAddress, name string) []ids.ObjectAddress {
	return s.procedureNames.Named(ProcedureNameKey{namespace, name})
}
