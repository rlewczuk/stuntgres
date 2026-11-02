package core

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/largeobject"
)

const largeObjectWrite = 131072
const largeObjectRead = 262144

// Admission serializes this backend-local table. Engine.Mu protects catalog
// resolution/publication; rollback and disposal already hold the owner lock.
// A slot owns only immutable bytes, never an authorization graph or query scope.
type largeObjectDescriptor struct {
	oid ids.OID
	ordinal uint64
	writable bool
	offset int64
	content largeobject.Value
}

type largeObjectDescriptors struct {
	slots []largeObjectDescriptor
	ordinal uint64
}

func (t *largeObjectDescriptors) open(d largeObjectDescriptor) (int32, error) {
	if t.ordinal == math.MaxUint64 {
		return 0, SQLError("54000", "too many large-object descriptors")
	}
	i := 0
	for i < len(t.slots) && t.slots[i].ordinal != 0 { i++ }
	if i > math.MaxInt32 {
		return 0, SQLError("54000", "too many large-object descriptors")
	}
	t.ordinal++
	d.ordinal = t.ordinal
	if i == len(t.slots) { t.slots = append(t.slots, d) } else { t.slots[i] = d }
	return int32(i), nil
}

func (t *largeObjectDescriptors) lookup(fd int32) (*largeObjectDescriptor, error) {
	if fd < 0 || int64(fd) >= int64(len(t.slots)) || t.slots[fd].ordinal == 0 {
		return nil, SQLError("42704", fmt.Sprintf("invalid large-object descriptor: %d", fd))
	}
	return &t.slots[fd], nil
}

func (t *largeObjectDescriptors) rollback(ordinal uint64) {
	for i := range t.slots {
		if t.slots[i].ordinal > ordinal { t.slots[i] = largeObjectDescriptor{} }
	}
}

func (t *largeObjectDescriptors) unlink(oid ids.OID) {
	for i := range t.slots {
		if t.slots[i].oid == oid { t.slots[i] = largeObjectDescriptor{} }
	}
}

// Capture at query admission, before any argument can modify the journal. Only
// this descriptor-opening view uses the stable LO base; convenience APIs retain
// their existing query view. The overlay shares immutable descriptors/pages.
func (s *StatementScope) captureLargeObjectOpeningLocked(e *Engine) {
	if s.largeObjectOpening.State() != nil { return }
	base := ownerExtensionOrZero(s.Owner).largeObjectBase
	if !s.Owner.Isolation.Stable() || base == nil {
		s.largeObjectOpening = s.largeObjectRead
		if s.largeObjectOpening.State() == nil { s.largeObjectOpening = e.PrivateCatalogLocked(s.PendingCatalog()) }
		return
	}
	state := base
	if journal := s.PendingCatalog(); journal != nil {
		for a, d := range journal.Descriptors {
			if a.ClassID != ids.LargeObjectClassID { continue }
			if state == base { state = catalog.CloneState(base) }
			if d == nil { state.RemoveDescriptor(a) } else { state.SetDescriptor(a, d) }
		}
	}
	s.largeObjectOpening = catalog.NewRead(&e.Catalog.Owner, state)
}

// Caller holds Engine.Mu and the admitted transaction owner. ACL checks belong
// exclusively to open; reads/writes borrow that admitted mode, not live roles.
func (s *StatementScope) largeObjectDescriptorLocked(e *Engine, r functions.LargeObjectRequest) (functions.LargeObjectResult, error) {
	out := functions.LargeObjectResult{}
	if s.Owner.Ref != s.TxRef {
		return out, ids.ErrCatalogReplaced
	}
	table := &ownerExtensionOf(s.Owner).largeObjects
	if r.Name == "lo_open" {
		writable := r.Mode&largeObjectWrite != 0
		if writable && s.Op.EffectiveReadOnly() { return out, SQLError("25006", "cannot execute lo_open(INV_WRITE) in a read-only transaction") }
		if r.Mode&(largeObjectRead|largeObjectWrite) == 0 { return out, SQLError("22023", fmt.Sprintf("invalid flags for opening a large object: %d", r.Mode)) }
		auth, err := s.callableAuthorizationLocked(e)
		if err != nil { return out, err }
		s.captureLargeObjectOpeningLocked(e)
		view := s.largeObjectOpening
		if writable { view = e.PrivateCatalogLocked(s.PendingCatalog()) }
		a := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: r.OID}
		d, _ := view.Get(a).(*catalog.LargeObject)
		if d == nil { return out, LargeObjectMissingError(r.OID) }
		if !OperationReplayOrZero(s.Op).LargeObjectAuthority {
			auth.Recapture(view)
			privileges := authorization.SelectPrivilege
			if writable { privileges |= authorization.UpdatePrivilege }
			if err := auth.Check(access.Requirement{Object: auth.Ref(a), Privileges: privileges}); err != nil { return out, err }
		}
		entry := largeObjectDescriptor{oid: r.OID, writable: writable}
		if !writable { entry.content = d.Content }
		out.Integer, err = table.open(entry)
		return out, err
	}
	truncate := r.Name == "lo_truncate" || r.Name == "lo_truncate64"
	if (r.Name == "lowrite" || truncate) && s.Op.EffectiveReadOnly() { return out, SQLError("25006", "cannot execute "+r.Name+"() in a read-only transaction") }
	if r.Name == "loread" {
		r.Length = max(r.Length, 0)
		if r.Length > largeobject.MaximumRead { return out, SQLError("XX000", fmt.Sprintf("invalid memory alloc request size %d", r.Length+4)) }
	}
	d, err := table.lookup(r.Descriptor)
	if err != nil { return out, err }
	if r.Name == "lo_close" { *d = largeObjectDescriptor{}; return out, nil }
	// Cursor-only operations precede live-content resolution: a peer unlink
	// does not invalidate tell/SET/CUR. Only END needs the mode-selected EOF.
	switch r.Name {
	case "lo_lseek", "lo_lseek64", "lo_tell", "lo_tell64":
		position := d.offset
		if r.Name == "lo_lseek" || r.Name == "lo_lseek64" {
			switch r.Whence {
			case 0:
				position = r.Offset
			case 1:
				position += r.Offset
			case 2:
				content := d.content
				if d.writable {
					a := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: d.oid}
					object, _ := e.PrivateCatalogLocked(s.PendingCatalog()).Get(a).(*catalog.LargeObject)
					// The released current-view page scan sees no pages after
					// peer unlink. Frozen READ descriptors keep their backing.
					content = largeobject.Value{}
					if object != nil { content = object.Content }
				}
				position = content.Size() + r.Offset
			default:
				return out, SQLError("22023", fmt.Sprintf("invalid whence setting: %d", r.Whence))
			}
			if err := largeobject.Seek(position); err != nil { return out, err }
			d.offset = position
		}
		// The int32 wrapper checks its result after inv_seek moves the cursor.
		// Savepoint recovery therefore retains that movement on overflow.
		if (r.Name == "lo_lseek" || r.Name == "lo_tell") && position > math.MaxInt32 {
			return out, SQLError("22003", fmt.Sprintf("%s result out of range for large-object descriptor %d", r.Name, r.Descriptor))
		}
		out.Position = position
		return out, nil
	}
	if (r.Name == "lowrite" || truncate) && !d.writable { return out, SQLError("55000", fmt.Sprintf("large object descriptor %d was not opened for writing", r.Descriptor)) }
	content := d.content
	var object *catalog.LargeObject
	if d.writable {
		a := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: d.oid}
		object, _ = e.PrivateCatalogLocked(s.PendingCatalog()).Get(a).(*catalog.LargeObject)
		if object == nil { return out, LargeObjectMissingError(d.oid) }
		content = object.Content
	}
	if r.Name == "loread" {
		out.Data, err = content.Read(d.offset, r.Length)
		if err == nil { d.offset += int64(len(out.Data)) }
		return out, err
	}
	if truncate {
		content, err = content.Truncate(r.Length)
	} else {
		content, err = content.Write(d.offset, r.Data)
	}
	if err != nil { return out, err }
	if truncate || len(r.Data) != 0 {
		n := *object
		n.Content = content
		n.ObjectRevision, err = e.Catalog.RevisionLocked()
		if err != nil { return out, err }
		changes := s.Catalog
		if changes == nil { changes = s.PendingCatalog().Clone() }
		e.CatalogChangeLocked(changes, n.Address(), &n)
		s.Catalog = changes
	}
	if !truncate {
		d.offset += int64(len(r.Data))
		out.Integer = int32(len(r.Data))
	}
	return out, nil
}
