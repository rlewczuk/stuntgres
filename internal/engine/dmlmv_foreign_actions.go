package engine

import (
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type foreignKeyJob struct {
	child, parent, index   *catalog.Relation
	definition             catalog.ForeignKeyConstraint
	comparisons            []table.ForeignKeyComparison
	childKey               []sqltype.Value // Reused across final rows and waited probes.
	deletePlan, updatePlan *foreignActionPlan
}

func sameForeignKeyDefinition(a, b catalog.ForeignKeyConstraint) bool {
	return a.OID == b.OID && a.Target == b.Target && a.Index == b.Index && a.Full == b.Full && a.UpdateAction == b.UpdateAction && a.DeleteAction == b.DeleteAction && slices.Equal(a.Attributes, b.Attributes) && slices.Equal(a.Referenced, b.Referenced) && slices.Equal(a.DeleteAttributes, b.DeleteAttributes)
}

func refreshForeignActionJob(view catalog.Read, job *foreignKeyJob) error {
	child, cok := view.Relation(job.child.Address())
	parent, pok := view.Relation(job.parent.Address())
	idx, iok := view.Relation(job.index.Address())
	if !cok || !pok || !iok || idx.SQLIndex == nil || !index.SameRelationRowShape(child, job.child) || !index.SameRelationRowShape(parent, job.parent) || !index.SameIndexKeySemantics(idx, job.index) {
		return ids.ErrCatalogReplaced
	}
	found := false
	for _, current := range child.Constraints.ForeignKeys {
		if current.OID == job.definition.OID {
			if !sameForeignKeyDefinition(current, job.definition) {
				return ids.ErrCatalogReplaced
			}
			job.definition = current
			found = true
			break
		}
	}
	if !found {
		return ids.ErrCatalogReplaced
	}
	job.child, job.parent, job.index = child, parent, idx
	return nil
}

// This authority is private to one statement and one captured physical child.
// It never substitutes for the initiating target's admission or candidate view.
type foreignActionAdmission struct {
	ref               ids.ObjectRef
	storage           *storage.Relation
	relation          *catalog.Relation
	tx                ids.TxRef
	jobs              []foreignKeyJob
	writable, mutated bool
}

type foreignActionTransition struct {
	relation  ids.ObjectAddress
	old, next storage.RowVersion
	owner     authorization.PrincipalRef // zero means the initiating statement's actor
}

func foreignActionSameKey(comparisons []table.ForeignKeyComparison, old, next []sqltype.Value) bool {
	for _, comparison := range comparisons {
		if !sqltype.SameDatum(old[comparison.Parent], next[comparison.Parent]) {
			return false
		}
	}
	return true
}

// foreignActionScope is the statement's foreign-key action slot: admitted
// referencing relations, row transitions, settling state, prepared check jobs
// and the foreign-key targets of CREATE/ALTER TABLE.
type foreignActionScope struct {
	admissions     map[ids.ObjectAddress]*foreignActionAdmission
	transitions    []foreignActionTransition
	settling       bool
	exclusionAdded bool // a new event admitted while settling actions
	jobs           []foreignKeyJob
	keyTargets     []ids.ObjectRef
	alterKeys      []table.AlterForeignKeyAdmission
}

type foreignActionPlan struct {
	targets, parentSlots []int
	defaults             []expr.Expr
	defaultOrigins       []analyze.DefaultOrigin
	conversions          []sqltype.CoercionPlan
	convert              []bool
	input                sqltype.InputEnvironment
	domainChecks         bool
}

func foreignActionQuery(job *foreignKeyJob, event foreignActionTransition, action parser.ForeignKeyAction, view catalog.Read) string {
	var q strings.Builder
	deletion := action == parser.ForeignKeyCascade && event.next.Row == 0
	if deletion {
		q.WriteString("DELETE FROM ONLY ")
	} else {
		q.WriteString("UPDATE ONLY ")
	}
	q.WriteString(catalog.QuoteIdentifier(view.Get(job.child.Namespace()).Name()))
	q.WriteByte('.')
	q.WriteString(catalog.QuoteIdentifier(job.child.Name()))
	offset := 0
	if !deletion {
		q.WriteString(" SET ")
		attributes := job.definition.Attributes
		if event.next.Row == 0 && len(job.definition.DeleteAttributes) != 0 {
			attributes = job.definition.DeleteAttributes
		}
		for i, a := range attributes {
			if i != 0 {
				q.WriteString(", ")
			}
			q.WriteString(catalog.QuoteIdentifier(job.child.Columns[slices.Index(job.child.Attributes, a)].Name))
			q.WriteString(" = ")
			switch action {
			case parser.ForeignKeyCascade:
				q.WriteByte('$')
				q.WriteString(strconv.Itoa(i + 1))
				offset++
			case parser.ForeignKeySetNull:
				q.WriteString("NULL")
			case parser.ForeignKeySetDefault:
				q.WriteString("DEFAULT")
			}
		}
	}
	q.WriteString(" WHERE ")
	for i, a := range job.definition.Attributes {
		if i != 0 {
			q.WriteString(" AND ")
		}
		childSlot := slices.Index(job.child.Attributes, a)
		parentSlot := slices.Index(job.parent.Attributes, job.definition.Referenced[i])
		var comparison sqltype.IndexForeignKeyComparison
		for _, c := range job.comparisons {
			if c.Child == childSlot {
				comparison = c.Comparison
				break
			}
		}
		parentInput, childInput := comparison.InputTypes()
		q.WriteByte('$')
		q.WriteString(strconv.Itoa(offset + i + 1))
		if job.parent.Columns[parentSlot].Usage.Type != parentInput {
			q.WriteString("::")
			q.WriteString(catalog.Identifier(parentInput.NamespaceName()))
			q.WriteByte('.')
			q.WriteString(catalog.Identifier(parentInput.Name()))
		}
		q.WriteString(" OPERATOR(pg_catalog.=) ")
		q.WriteString(catalog.QuoteIdentifier(job.child.Columns[childSlot].Name))
		if job.child.Columns[childSlot].Usage.Type != childInput {
			q.WriteString("::")
			q.WriteString(catalog.Identifier(childInput.NamespaceName()))
			q.WriteByte('.')
			q.WriteString(catalog.Identifier(childInput.Name()))
		}
	}
	return q.String()
}
