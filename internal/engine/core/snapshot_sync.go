package core

import (
	"fmt"
	"sort"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/sqlstate"
)

// exportedSnapshot contains committed immutable authority only. It never owns
// an exporter's journal, writes, locks, or serializable conflict graph.
type exportedSnapshot struct {
	source *Engine
	ref ids.TxRef
	isolation session.Isolation
	readOnly bool
	roots map[ids.ObjectAddress]visibility.Root
	catalog *catalog.State
	frontier ids.CommitSeq
	xmin uint64
	snapshot *sqltype.Snapshot
	aborted bool
	pid ids.SessionID
}

func (h *statementHost) ExportSnapshot() (string, error) {
	s := h.scope()
	t, e := s.Owner, s.Engine()
	t.Mu.Lock()
	defer t.Mu.Unlock()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	s.Op.Session.Mu.Lock()
	defer s.Op.Session.Mu.Unlock()
	if err := PublicationOperationLocked(e, s.Op); err != nil { return "", err }
	if t.Ref != s.TxRef || s.TxRef.Epoch() != e.Catalog.State.Epoch() { return "", ids.ErrCatalogReplaced }
	if len(t.Savepoints) != 0 { return "", SQLError("25001", "cannot export a snapshot from a subtransaction") }
	if s.exportCapture == nil && t.Isolation.Stable() && t.CurrentSnapshot != nil {
		s.exportCapture = &exportedSnapshot{roots: t.Roots, catalog: t.SequenceCatalog, frontier: t.Frontier, snapshot: t.CurrentSnapshot, xmin: e.Visibility.Entries[t.Ref].Xmin}
	}
	if s.exportCapture == nil { return "", fmt.Errorf("stuntgres: snapshot export lacks admitted committed roots") }
	c := e.Cluster
	if c.snapshotSerial == ^uint64(0) { return "", fmt.Errorf("stuntgres: snapshot identifier exhausted") }
	record := *s.exportCapture
	record.source, record.ref, record.isolation, record.readOnly = e, t.Ref, t.Isolation, t.ReadOnly
	record.pid = s.Op.Session.Token().ID()
	// The ordinary inquiry excludes self. A shared cut must keep the exporting
	// transaction invisible even if it subsequently publishes its private work.
	own := e.Visibility.FullXIDLocked(t.Ref)
	if own != 0 && own < record.snapshot.Xmax() {
		members := make([]uint64, 0, record.snapshot.Len()+1)
		found := false
		for i := range record.snapshot.Len() { id := record.snapshot.Member(i); members = append(members, id); found = found || id == own }
		if !found { members = append(members, own) }
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		xmin := record.snapshot.Xmin()
		if own < xmin { xmin = own }
		var err error
		record.snapshot, err = sqltype.NewSnapshot(xmin, record.snapshot.Xmax(), members)
		if err != nil { return "", err }
	}
	c.snapshotSerial++
	token := fmt.Sprintf("%X-%X", uint64(t.Ref.Epoch()), c.snapshotSerial)
	if c.snapshots == nil { c.snapshots = make(map[string]*exportedSnapshot) }
	c.snapshots[token] = &record
	extension := ownerExtensionOf(t)
	extension.snapshotRef = t.Ref
	extension.snapshotTokens = append(extension.snapshotTokens, token)
	e.Visibility.RetainExportSnapshotAtLocked(t.Ref, record.xmin, record.snapshot.Xmin())
	t.ExportedSnapshots = true
	return token, nil
}

// importSnapshot is called with the importer's owner mutex held. The shared
// publication mutex linearizes source termination with installation; no peer
// owner mutex is taken.
func (s *StatementScope) importSnapshot(token string) error {
	e, t := s.Engine(), s.Owner
	e.Mu.Lock()
	defer e.Mu.Unlock()
	s.Op.Session.Mu.Lock()
	defer s.Op.Session.Mu.Unlock()
	if err := PublicationOperationLocked(e, s.Op); err != nil { return err }
	if t.Ref != s.TxRef || t.Ref.Epoch() != e.Catalog.State.Epoch() { return ids.ErrCatalogReplaced }
	if t.SnapshotSet || e.Visibility.FullXIDLocked(t.Ref) != 0 || len(t.Savepoints) != 0 { return SQLError("25001", "SET TRANSACTION SNAPSHOT must be called before any query") }
	if !t.Isolation.Stable() { return SQLError("0A000", "a snapshot-importing transaction must have isolation level SERIALIZABLE or REPEATABLE READ") }
	if strings.IndexFunc(token, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'A' && r <= 'F' || r == '-') }) >= 0 { return SQLError("22023", "invalid snapshot identifier: \""+token+"\"") }
	// PostgreSQL's legal empty identifier names its snapshot directory rather
	// than a missing export. Preserve the released diagnostic without filesystem
	// access or using native persistence as snapshot transport.
	if token==""{return SQLError("XX000",`could not read file "pg_snapshots/": Is a directory`)}
	record := e.Cluster.snapshots[token]
	if record == nil { return SQLError("42704", "snapshot \""+token+"\" does not exist") }
	if t.Isolation == session.Serializable {
		if record.isolation != session.Serializable { return SQLError("0A000", "a serializable transaction cannot import a snapshot from a non-serializable transaction") }
		if !t.ReadOnly && record.readOnly { return SQLError("0A000", "a non-read-only serializable transaction cannot import a snapshot from a read-only transaction") }
	}
	if record.source != e { return SQLError("0A000", "cannot import a snapshot from a different database") }
	if record.aborted {
		return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"55000",Message:"could not import the requested snapshot",Detail:fmt.Sprintf("The source process with PID %d is not running anymore.",record.pid)}
	}
	if t.Isolation == session.Serializable && t.ReadOnly && t.Deferrable { return SQLError("0A000", "a snapshot-importing transaction must not be READ ONLY DEFERRABLE") }
	if _, err := e.Visibility.SnapshotLocked(t.Ref, false); err != nil { return err }
	e.Visibility.AdoptSnapshotLocked(t.Ref, record.xmin, record.snapshot.Xmin())
	if t.Isolation == session.Serializable { e.Visibility.CaptureImportedSerializableLocked(t.Ref, t.ReadOnly, false, record.frontier) }
	t.CurrentSnapshot, t.Frontier = record.snapshot, record.frontier
	t.Roots, t.SequenceCatalog = record.roots, record.catalog
	ownerExtensionOf(t).largeObjectBase = record.catalog
	t.SnapshotSet = true
	return nil
}

// A failed explicit block has left the running-transaction array, but its
// exported identifiers remain until block cleanup. Retain only diagnostic
// identity, not committed roots or a live snapshot advertisement.
func (e *Engine) abortSnapshotsLocked(ref ids.TxRef) {
	owner:=session.TxOwnerOf(ref)
	if owner==nil{return}
	extension,_:=owner.Extension.(*ownerExtension)
	if extension==nil||extension.snapshotRef!=ref{return}
	for _,token:=range extension.snapshotTokens{
		if record:=e.Cluster.snapshots[token];record!=nil{
			record.aborted=true
			record.roots,record.catalog,record.snapshot=nil,nil,nil
		}
	}
}

func (e *Engine) expireSnapshotsLocked(ref ids.TxRef) {
	if e.Cluster == nil || ref.Domain() != e.Catalog.Domain { return }
	owner := session.TxOwnerOf(ref)
	if owner == nil { return }
	extension, _ := owner.Extension.(*ownerExtension)
	if extension == nil || extension.snapshotRef != ref { return }
	for _, token := range extension.snapshotTokens { delete(e.Cluster.snapshots, token) }
	extension.snapshotRef, extension.snapshotTokens = ids.TxRef{}, nil
}

func (e *Engine) fenceSnapshotsLocked() {
	if e.Cluster == nil { return }
	for _, record := range e.Cluster.snapshots {
		if record.source == e { e.expireSnapshotsLocked(record.ref) }
	}
}
