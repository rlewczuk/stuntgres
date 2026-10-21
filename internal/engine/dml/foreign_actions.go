package dml

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

type ForeignKeyJob struct {
	Child, Parent, Index   *catalog.Relation
	Definition             catalog.ForeignKeyConstraint
	Comparisons            []table.ForeignKeyComparison
	ChildKey               []sqltype.Value // Reused across final rows and waited probes.
	DeletePlan, UpdatePlan *ForeignActionPlan
}

func SameForeignKeyDefinition(a, b catalog.ForeignKeyConstraint) bool {
	return a.OID == b.OID && a.Target == b.Target && a.Index == b.Index && a.Full == b.Full && a.UpdateAction == b.UpdateAction && a.DeleteAction == b.DeleteAction && slices.Equal(a.Attributes, b.Attributes) && slices.Equal(a.Referenced, b.Referenced) && slices.Equal(a.DeleteAttributes, b.DeleteAttributes)
}

func RefreshForeignActionJob(view catalog.Read, job *ForeignKeyJob) error {
	child, cok := view.Relation(job.Child.Address())
	parent, pok := view.Relation(job.Parent.Address())
	idx, iok := view.Relation(job.Index.Address())
	if !cok || !pok || !iok || idx.SQLIndex == nil || !index.SameRelationRowShape(child, job.Child) || !index.SameRelationRowShape(parent, job.Parent) || !index.SameIndexKeySemantics(idx, job.Index) {
		return ids.ErrCatalogReplaced
	}
	found := false
	for _, current := range child.Constraints.ForeignKeys {
		if current.OID == job.Definition.OID {
			if !SameForeignKeyDefinition(current, job.Definition) {
				return ids.ErrCatalogReplaced
			}
			job.Definition = current
			found = true
			break
		}
	}
	if !found {
		return ids.ErrCatalogReplaced
	}
	job.Child, job.Parent, job.Index = child, parent, idx
	return nil
}

// This authority is private to one statement and one captured physical child.
// It never substitutes for the initiating target's admission or candidate view.
type ForeignActionAdmission struct {
	Ref               ids.ObjectRef
	Storage           *storage.Relation
	Relation          *catalog.Relation
	Tx                ids.TxRef
	Jobs              []ForeignKeyJob
	Writable, Mutated bool
}

type ForeignActionTransition struct {
	Relation  ids.ObjectAddress
	Old, Next storage.RowVersion
	Owner     authorization.PrincipalRef // zero means the initiating statement's actor
}

func ForeignActionSameKey(comparisons []table.ForeignKeyComparison, old, next []sqltype.Value) bool {
	for _, comparison := range comparisons {
		if !sqltype.SameDatum(old[comparison.Parent], next[comparison.Parent]) {
			return false
		}
	}
	return true
}

// ForeignActionScope is the statement's foreign-key action slot: admitted
// referencing relations, row transitions, settling state, prepared check jobs
// and the foreign-key targets of CREATE/ALTER TABLE.
type ForeignActionScope struct {
	Admissions     map[ids.ObjectAddress]*ForeignActionAdmission
	Transitions    []ForeignActionTransition
	Settling       bool
	ExclusionAdded bool // a new event admitted while settling actions
	Jobs           []ForeignKeyJob
	KeyTargets     []ids.ObjectRef
	AlterKeys      []table.AlterForeignKeyAdmission
}

type ForeignActionPlan struct {
	Targets, ParentSlots []int
	Defaults             []expr.Expr
	DefaultOrigins       []analyze.DefaultOrigin
	Conversions          []sqltype.CoercionPlan
	Convert              []bool
	Input                sqltype.InputEnvironment
	DomainChecks         bool
}

func ForeignActionQuery(job *ForeignKeyJob, event ForeignActionTransition, action parser.ForeignKeyAction, view catalog.Read) string {
	var q strings.Builder
	deletion := action == parser.ForeignKeyCascade && event.Next.Row == 0
	if deletion {
		q.WriteString("DELETE FROM ONLY ")
	} else {
		q.WriteString("UPDATE ONLY ")
	}
	q.WriteString(catalog.QuoteIdentifier(view.Get(job.Child.Namespace()).Name()))
	q.WriteByte('.')
	q.WriteString(catalog.QuoteIdentifier(job.Child.Name()))
	offset := 0
	if !deletion {
		q.WriteString(" SET ")
		attributes := job.Definition.Attributes
		if event.Next.Row == 0 && len(job.Definition.DeleteAttributes) != 0 {
			attributes = job.Definition.DeleteAttributes
		}
		for i, a := range attributes {
			if i != 0 {
				q.WriteString(", ")
			}
			q.WriteString(catalog.QuoteIdentifier(job.Child.Columns[slices.Index(job.Child.Attributes, a)].Name))
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
	for i, a := range job.Definition.Attributes {
		if i != 0 {
			q.WriteString(" AND ")
		}
		childSlot := slices.Index(job.Child.Attributes, a)
		parentSlot := slices.Index(job.Parent.Attributes, job.Definition.Referenced[i])
		var comparison sqltype.IndexForeignKeyComparison
		for _, c := range job.Comparisons {
			if c.Child == childSlot {
				comparison = c.Comparison
				break
			}
		}
		parentInput, childInput := comparison.InputTypes()
		q.WriteByte('$')
		q.WriteString(strconv.Itoa(offset + i + 1))
		if job.Parent.Columns[parentSlot].Usage.Type != parentInput {
			q.WriteString("::")
			q.WriteString(catalog.Identifier(parentInput.NamespaceName()))
			q.WriteByte('.')
			q.WriteString(catalog.Identifier(parentInput.Name()))
		}
		q.WriteString(" OPERATOR(pg_catalog.=) ")
		q.WriteString(catalog.QuoteIdentifier(job.Child.Columns[childSlot].Name))
		if job.Child.Columns[childSlot].Usage.Type != childInput {
			q.WriteString("::")
			q.WriteString(catalog.Identifier(childInput.NamespaceName()))
			q.WriteByte('.')
			q.WriteString(catalog.Identifier(childInput.Name()))
		}
	}
	return q.String()
}
