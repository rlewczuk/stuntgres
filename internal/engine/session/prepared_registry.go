package session

import (
	"errors"
	"iter"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
)

// PreparedTransaction is a transaction detached by PREPARE TRANSACTION. The
// registry owns these roots and the original lock/SSI identity after detach;
// no session attachment is consulted to authorize completion.
type PreparedTransaction struct {
	GID      string
	Owner    authorization.PrincipalRef
	Ref      ids.TxRef
	Prepared time.Time
	Database string
	Ticket   *lock.Ticket
	Writes   map[ids.ObjectAddress][]storage.RowVersion
	Deletes  map[ids.ObjectAddress]*visibility.PrivateDeletes
	Stats    statistics.Ledger
	Catalog  *Journal
	Claimed  bool
	Roots    map[ids.ObjectAddress]visibility.Root
	Frontier ids.CommitSeq
	Options  TxOptions
}

// PreparedPublication is the 2PC phase of one publication: PREPARE installs
// Entry for Origin (Preparing), COMMIT PREPARED finishes a claimed Entry.
type PreparedPublication struct {
	Entry     *PreparedTransaction
	Preparing bool
	Origin    *TransactionOwner
}

// PreparedRegistry maps global transaction identifiers to prepared
// transactions. Its zero value is empty. Every method runs under the
// cluster-shared Engine.mu and takes no lock.
type PreparedRegistry struct {
	entries map[string]*PreparedTransaction
}

// Lookup returns the entry for gid, or nil.
func (r *PreparedRegistry) Lookup(gid string) *PreparedTransaction { return r.entries[gid] }

func (r *PreparedRegistry) Len() int { return len(r.entries) }

// All yields every entry in map order.
func (r *PreparedRegistry) All() iter.Seq2[string, *PreparedTransaction] {
	return func(yield func(string, *PreparedTransaction) bool) {
		for gid, entry := range r.entries {
			if !yield(gid, entry) {
				return
			}
		}
	}
}

// ReserveLocked allocates the map before PREPARE reaches its callback-free
// final installation.
func (r *PreparedRegistry) ReserveLocked() {
	if r.entries == nil {
		r.entries = make(map[string]*PreparedTransaction)
	}
}

// InstallLocked registers entry; ReserveLocked ran first.
func (r *PreparedRegistry) InstallLocked(entry *PreparedTransaction) { r.entries[entry.GID] = entry }

func (r *PreparedRegistry) RemoveLocked(gid string) { delete(r.entries, gid) }

// ClaimLocked reserves gid for COMMIT or ROLLBACK PREPARED by current. The
// errors come in PostgreSQL's order: missing, permission, busy.
func (r *PreparedRegistry) ClaimLocked(gid string, roles authorization.Read, current authorization.PrincipalRef) (*PreparedTransaction, error) {
	entry := r.entries[gid]
	if entry == nil {
		return nil, sqlError("42704", `prepared transaction with identifier "`+gid+`" does not exist`)
	}
	if err := entry.checkFinisher(roles, current); err != nil {
		return nil, err
	}
	if entry.Claimed {
		return nil, sqlError("55000", `prepared transaction with identifier "`+gid+`" is busy`)
	}
	entry.Claimed = true
	return entry, nil
}

// ReleaseLocked returns a claimed entry after a failed completion.
func (r *PreparedRegistry) ReleaseLocked(entry *PreparedTransaction) { entry.Claimed = false }

// PublicationPreflightLocked checks that p still matches the registry: a
// preparing origin still owns its block and its GID is free; a finishing
// entry is still registered and claimed. The finishing actor check follows
// separately (CheckActor), because it needs the published roles.
func (r *PreparedRegistry) PublicationPreflightLocked(p *PreparedPublication) error {
	if p.Preparing {
		if p.Origin.Ref != p.Entry.Ref || !p.Origin.InBlock() {
			return errors.New("stuntgres: stale preparing transaction")
		}
		if r.entries[p.Entry.GID] != nil {
			return sqlError("42710", `transaction identifier "`+p.Entry.GID+`" is already in use`)
		}
		return nil
	}
	if r.entries[p.Entry.GID] != p.Entry || !p.Entry.Claimed {
		return errors.New("stuntgres: stale prepared publication")
	}
	return nil
}

// CheckActor validates current in roles and checks that it may finish p.
func (p *PreparedTransaction) CheckActor(roles authorization.Read, current authorization.PrincipalRef) error {
	if err := roles.ValidatePrincipal(current); err != nil {
		return err
	}
	return p.checkFinisher(roles, current)
}

// checkFinisher admits the preparing role and superusers.
func (p *PreparedTransaction) checkFinisher(roles authorization.Read, current authorization.PrincipalRef) error {
	actor, exists := roles.Find(current)
	if current != p.Owner && (!exists || !actor.Superuser) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to finish prepared transaction", Hint: "Must be superuser or the user that prepared the transaction."}
	}
	return nil
}
