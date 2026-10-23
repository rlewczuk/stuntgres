package visibility

import (
	"sync/atomic"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// Generation is the storage incarnation captured for one relation. Roots
// obtained after active-snapshot acquisition (including utility lock waits and
// committed TRUNCATE incarnations) can contain newer births. NeedsBirthFilter
// selects them at the existing frontier; it is not another frontier.
type Generation struct {
	Storage          *storage.Relation
	NeedsBirthFilter bool
}

// Data is a statement's captured, immutable data view: the frontier, the
// catalog read, retained roots and the transaction's own writes and deletes.
type Data struct {
	Tx                     ids.TxRef
	Command                ids.CommandID
	Frontier               ids.CommitSeq
	RewrittenFrontiers     map[ids.ObjectAddress]ids.CommitSeq
	Catalog                catalog.Read
	Committed              map[ids.ObjectAddress][]storage.RowVersion
	Generations            map[ids.ObjectAddress]Generation
	SingleGeneration       *storage.Relation
	SingleNeedsBirthFilter bool
	Own                    map[ids.ObjectAddress][]storage.RowVersion
	Ends                   map[ids.ObjectAddress]*storage.DeletionRoot
	Deleted                map[ids.ObjectAddress]*PrivateDeletes
	// DynamicRoots retains immutable prefixes for runtime-named pgrowlocks
	// targets. Only statements with that installed dependency allocate it.
	DynamicRoots map[ids.ObjectAddress]Root
}

// GenerationAt is the storage incarnation captured for address, or nil.
func (d *Data) GenerationAt(address ids.ObjectAddress) *storage.Relation {
	if d.Generations != nil {
		return d.Generations[address].Storage
	}
	if _, ok := d.Committed[address]; ok {
		return d.SingleGeneration
	}
	return nil
}

// NeedsBirthFilterAt reports whether address's capture needs a birth filter.
func (d *Data) NeedsBirthFilterAt(address ids.ObjectAddress) bool {
	if d.Generations != nil {
		return d.Generations[address].NeedsBirthFilter
	}
	_, captured := d.Committed[address]
	return captured && d.SingleNeedsBirthFilter
}

// FrontierAt is address's commit frontier, which a private rewrite can move.
func (d *Data) FrontierAt(address ids.ObjectAddress) ids.CommitSeq {
	if frontier, ok := d.RewrittenFrontiers[address]; ok {
		return frontier
	}
	return d.Frontier
}

// View retains exact immutable relation-local roots, not copied row values.
// Each handle has independent release authority. The immutable data is
// naturally reclaimed after the last view/result owner; no registry is needed.
type View struct {
	self *View
	data atomic.Pointer[Data]
}

// NewView returns a new original handle on data.
func NewView(data *Data) *View {
	v := &View{}
	v.self = v
	v.data.Store(data)
	return v
}

// Retain returns an independent handle on the same data, or nil when v is
// released or not an original handle.
func (v *View) Retain() *View {
	if v == nil || v.self != v {
		return nil
	}
	data := v.data.Load()
	if data == nil {
		return nil
	}
	return NewView(data)
}

// Release ends this handle only; copies and other handles are unaffected.
func (v *View) Release() {
	if v != nil && v.self == v {
		v.data.Store(nil)
	}
}

// Load returns the captured data, or nil once this handle is released.
// Unlike Data it does not panic and does not check the handle's identity.
func (v *View) Load() *Data { return v.data.Load() }

// Data returns the captured data. It panics on a released or copied handle.
func (v *View) Data() *Data {
	if v == nil || v.self != v {
		panic("stuntgres: visibility view is released")
	}
	data := v.data.Load()
	if data == nil {
		panic("stuntgres: visibility view is released")
	}
	return data
}

// Tx, Command, Frontier and Catalog read the captured data.
func (v *View) Tx() ids.TxRef { return v.Data().Tx }

func (v *View) Command() ids.CommandID { return v.Data().Command }

func (v *View) Frontier() ids.CommitSeq { return v.Data().Frontier }

func (v *View) Catalog() catalog.Read { return v.Data().Catalog }
