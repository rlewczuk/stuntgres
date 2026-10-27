package stored

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

type TriggerEvent uint8

const (
	TriggerInsert TriggerEvent = 1 << iota
	TriggerUpdate
	TriggerDelete
	TriggerTruncate
)

type TriggerTiming uint8

const (
	TriggerBefore TriggerTiming = iota + 1
	TriggerAfter
)

type TriggerLevel uint8

const (
	TriggerRow TriggerLevel = iota + 1
	TriggerStatement
)

type TriggerParts struct {
	OID            ids.OID
	Name           string
	Relation       ids.ObjectRef
	Procedure      ids.ObjectRef
	Parent         ids.ObjectAddress // Parent trigger identity for a partition clone.
	ParentRevision ids.DefinitionRevision
	Events         TriggerEvent
	Timing         TriggerTiming
	Level          TriggerLevel
	UpdateColumns  []int32
	Arguments      []string
	When           expr.Expr
	Presentation   *View
	Binding        *analyze.Binding
	References     []ids.ObjectAddress
	AutoExtensions []ids.ObjectAddress
}

type Trigger struct{ parts TriggerParts }

func validateTriggerParts(p TriggerParts) error {
	if p.OID == 0 || p.Name == "" || p.Relation.Address.ClassID != ids.RelationClassID || p.Relation.Address.ObjectID == 0 || p.Procedure.Address.ClassID != ids.ProcedureClassID || p.Procedure.Address.ObjectID == 0 || p.Events == 0 || p.Events&^(TriggerInsert|TriggerUpdate|TriggerDelete|TriggerTruncate) != 0 || (p.When == nil) != (p.Presentation == nil) || (p.When == nil) != (p.Binding == nil) {
		return fmt.Errorf("stuntgres: invalid trigger definition")
	}
	if (p.Timing != TriggerBefore && p.Timing != TriggerAfter) || (p.Level != TriggerRow && p.Level != TriggerStatement) || p.Timing == TriggerBefore && p.Level != TriggerRow || p.Events&TriggerTruncate != 0 && p.Level != TriggerStatement || p.Parent.ObjectID != 0 && p.Level != TriggerRow {
		return fmt.Errorf("stuntgres: invalid trigger timing or level")
	}
	if p.When != nil && (p.When.ResultUsage().Type == nil || p.When.ResultUsage().Type.ID() != sqltype.Bool) {
		return fmt.Errorf("stuntgres: nonboolean trigger condition")
	}
	if !slices.Contains(p.References, p.Procedure.Address) {
		return fmt.Errorf("stuntgres: missing trigger function dependency")
	}
	for i, a := range p.AutoExtensions {
		if a.ClassID != ids.ExtensionClassID || a.ObjectID == 0 || a.SubID != 0 || i > 0 && p.AutoExtensions[i-1].ObjectID >= a.ObjectID {
			return fmt.Errorf("stuntgres: invalid trigger extension dependency")
		}
	}
	return nil
}
func NewTrigger(p TriggerParts) (Trigger, error) {
	p.AutoExtensions = slices.Clone(p.AutoExtensions)
	slices.SortFunc(p.AutoExtensions, func(a, b ids.ObjectAddress) int { return cmp.Compare(a.ObjectID, b.ObjectID) })
	p.AutoExtensions = slices.Compact(p.AutoExtensions)
	if err := validateTriggerParts(p); err != nil {
		return Trigger{}, err
	}
	p.UpdateColumns = slices.Clone(p.UpdateColumns)
	p.Arguments = slices.Clone(p.Arguments)
	p.References = slices.Clone(p.References)
	return Trigger{parts: p}, nil
}
func (t Trigger) Parts() TriggerParts { return t.parts }
func (t Trigger) Name() string        { return t.parts.Name }
func (t Trigger) OID() ids.OID        { return t.parts.OID }
func (t Trigger) Fires(event TriggerEvent, targets []int32) bool {
	if t.parts.Events&event == 0 {
		return false
	}
	if event != TriggerUpdate || len(t.parts.UpdateColumns) == 0 {
		return true
	}
	for _, column := range t.parts.UpdateColumns {
		if slices.Contains(targets, column) {
			return true
		}
	}
	return false
}

type TriggerList []Trigger

func (t TriggerList) Len() int { return len(t) }
func (t TriggerList) OID(i int) ids.OID { return t[i].parts.OID }

func (t TriggerList) Visit(visit func(catalog.TriggerFact) error) error {
	for _, definition := range t {
		p := definition.parts
		if err := visit(catalog.TriggerFact{OID: p.OID, Name: p.Name, Procedure: p.Procedure.Address, Parent: p.Parent, References: p.References, AutoExtensions: p.AutoExtensions}); err != nil {
			return err
		}
	}
	return nil
}
func (t TriggerList) Validate(read catalog.Read, owner *catalog.Relation) error {
	if len(t) != 0 && owner.RelationKind != catalog.TableRelation && owner.RelationKind != catalog.PartitionedRelation && owner.RelationKind != catalog.ForeignRelation {
		return fmt.Errorf("stuntgres: trigger payload on invalid relation kind")
	}
	previous := ""
	for i, definition := range t {
		p := definition.parts
		if err := validateTriggerParts(p); err != nil {
			return err
		}
		if i > 0 && strings.Compare(previous, p.Name) >= 0 {
			return fmt.Errorf("stuntgres: unordered trigger payload")
		}
		previous = p.Name
		if p.Relation.Address != owner.Address() || !ReferenceEpochValid(read, owner, p.Relation) || !ReferenceEpochValid(read, owner, p.Procedure) {
			return fmt.Errorf("stuntgres: invalid trigger provenance")
		}
		d, ok := read.Get(p.Procedure.Address).(*catalog.Procedure)
		if !ok || d.SQL == nil || d.SQL.Invocation != proc.DMLTriggerInvocation || d.SQL.PL == nil || len(d.SQL.Inputs) != 0 {
			return fmt.Errorf("stuntgres: invalid trigger function")
		}
		if err := (ProcedureProvider{}).Validate(read, d); err != nil {
			return err
		}
		if err := validateTriggerWhen(read, owner, p); err != nil {
			return err
		}
		for _, address := range p.AutoExtensions {
			ext, ok := read.Extension(address)
			if !ok {
				return fmt.Errorf("stuntgres: missing trigger extension dependency")
			}
			if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(read, ext); err != nil {
				return err
			}
		}
		for _, attribute := range p.UpdateColumns {
			if !owner.HasAttribute(attribute) {
				return fmt.Errorf("stuntgres: missing trigger UPDATE attribute")
			}
		}
		for _, address := range p.References {
			whole := address
			whole.SubID = 0
			if read.Get(whole) == nil {
				return fmt.Errorf("stuntgres: missing trigger dependency")
			}
			if address.SubID > 0 {
				relation, ok := read.Get(whole).(*catalog.Relation)
				if !ok || !relation.HasAttribute(address.SubID) {
					return fmt.Errorf("stuntgres: missing trigger dependency attribute")
				}
			}
		}
	}
	return nil
}
func TriggersOf(d *catalog.Relation) []Trigger { list, _ := d.Triggers.(TriggerList); return list }
func TriggersPayload(list []Trigger) catalog.TriggersPayload {
	if len(list) == 0 {
		return nil
	}
	return TriggerList(list)
}
