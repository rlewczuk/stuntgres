package partition

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

const DetachFinalizeHint = "Use ALTER TABLE ... DETACH PARTITION ... FINALIZE to complete the pending detach operation."

func PendingChild(view catalog.Read, parent *catalog.Relation) *catalog.Relation {
	if parent.Partition != nil {
		for _, a := range parent.Partition.Children {
			if child, ok := view.Relation(a); ok && child.Membership != nil && child.Membership.DetachPending {
				return child
			}
		}
	}
	return nil
}

func PendingDetachError(child *catalog.Relation, ordinary bool) error {
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Hint: DetachFinalizeHint}
	if ordinary {
		d.Message = fmt.Sprintf(`cannot detach partition "%s"`, child.Name())
		d.Detail = "The partition is being detached concurrently or has an unfinished detach."
	} else {
		d.Message = fmt.Sprintf(`cannot alter partition "%s" with an incomplete detach`, child.Name())
	}
	return d
}

func ConcurrentPendingError(view catalog.Read, parent, child *catalog.Relation) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf(`partition "%s" already pending detach in partitioned table "%s.%s"`, child.Name(), view.Get(parent.Namespace()).Name(), parent.Name()), Hint: DetachFinalizeHint}
}

func RequirePending(parent, child *catalog.Relation) error {
	if err := DetachMembership(parent, child); err != nil {
		return err
	}
	if !child.Membership.DetachPending {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf(`cannot complete detaching partition "%s"`, child.Name()), Detail: "There's no pending concurrent detach."}
	}
	return nil
}

func StagePending(stage ddl.Stage, parent, child, predecessor *catalog.Relation, revision ids.DefinitionRevision) error {
	if err := DetachMembership(parent, child); err != nil {
		return err
	}
	next, membership := *child, *child.Membership
	membership.DetachPending = true
	next.Membership = &membership
	next.ObjectRevision = revision
	next.History.DetachPredecessor = predecessor
	next.History.DetachPublication = 0
	next.History.DetachRoot = ids.ObjectAddress{}
	if predecessor != nil {
		next.History.DetachPublication = revision
		next.History.DetachRoot = child.Address()
	}
	return stage.Replace(&next)
}
