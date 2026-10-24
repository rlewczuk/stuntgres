package table

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

func OrderedChecks(d *catalog.Relation) []catalog.CheckConstraint {
	checks := slices.Clone(d.Constraints.Check)
	slices.SortFunc(checks, func(a, b catalog.CheckConstraint) int { return cmp.Compare(a.Ordinal, b.Ordinal) })
	return checks
}

func CheckDepends(d *catalog.Relation, target ids.ObjectAddress) bool {
	for _, c := range d.Constraints.Check {
		for _, dep := range c.Dependencies {
			a := dep.Ref.Address
			if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
				return true
			}
		}
	}
	return false
}

// The merge fence checks external identities against the candidate current root,
// not the stage's older validation snapshot. The publisher retries if that root
// changes before its single final install. No rendering or row evaluation here.
func ValidateCheckProviders(view catalog.Read, d *catalog.Relation) error {
	for _, c := range d.Constraints.Check {
		for _, dep := range c.Dependencies {
			address := dep.Ref.Address
			address.SubID = 0
			if address == d.Address() {
				continue
			}
			provider, ok := view.Descriptor(address)
			if !ok || !stored.ReferenceEpochValid(view, d, dep.Ref) || (provider.Kind() != ids.RelationObject && provider.Kind() != ids.TypeObject && provider.Kind() != ids.CollationObject && provider.Kind() != ids.ProcedureObject && provider.Kind() != ids.OperatorObject && provider.Kind() != ids.NamespaceObject && provider.Kind() != ids.TSDictionaryObject) || provider.Kind() == ids.NamespaceObject && dep.Ref.Address.SubID != 0 {
				return fmt.Errorf("stuntgres: invalid check provider dependency")
			}
			if provider.Kind() == ids.CollationObject {
				if err := stored.ValidateCollationDependency(view, dep.Ref, c.Expression); err != nil {
					return err
				}
			}
			if relation, ok := provider.(*catalog.Relation); ok && dep.Ref.Address.SubID != 0 && !relation.HasAttribute(dep.Ref.Address.SubID) {
				return fmt.Errorf("stuntgres: missing check provider attribute")
			}
		}
	}
	return nil
}

func ValidateChecks(view catalog.Read, d *catalog.Relation, names map[string]bool) error {
	previous := ""
	for i, c := range d.Constraints.Check {
		if c.Ordinal < 0 {
			return fmt.Errorf("stuntgres: invalid check declaration ordinal")
		}
		for _, previous := range d.Constraints.Check[:i] {
			if previous.Ordinal == c.Ordinal {
				return fmt.Errorf("stuntgres: duplicate check declaration ordinal")
			}
		}
		if c.OID == 0 || c.Name == "" || len(c.Name) > 63 || !utf8.ValidString(c.Name) || strings.IndexByte(c.Name, 0) >= 0 || names[c.Name] || previous != "" && previous >= c.Name || c.Expression == nil || c.Expression.ResultUsage().Type == nil || c.Expression.ResultUsage().Type.ID() != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid check constraint identity")
		}
		names[c.Name], previous = true, c.Name
		var actual []ids.ObjectRef
		var commentSnapshot, visibilitySnapshot bool
		if _, err := (stored.ScalarRenderer{Validation: true, Owner: d, CapturedChecks: c.Dependencies, Dependencies: &actual, CommentSnapshot: &commentSnapshot, VisibilitySnapshot: &visibilitySnapshot}).SQL(context.Background(), view, c.Expression); err != nil {
			return err
		}
		if commentSnapshot != c.CommentSnapshot {
			return fmt.Errorf("stuntgres: inconsistent check comment snapshot manifest")
		}
		if visibilitySnapshot != c.VisibilitySnapshot {
			return fmt.Errorf("stuntgres: inconsistent check visibility snapshot manifest")
		}
		if len(actual) != len(c.Dependencies) {
			return fmt.Errorf("stuntgres: inconsistent check dependency manifest")
		}
		for _, ref := range actual {
			found := false
			for _, dep := range c.Dependencies {
				if dep.Ref == ref {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("stuntgres: missing check dependency")
			}
		}
		for _, dep := range c.Dependencies {
			address := dep.Ref.Address
			address.SubID = 0
			if address == d.Address() {
				if !stored.ReferenceEpochValid(view, d, dep.Ref) || !d.HasAttribute(dep.Ref.Address.SubID) {
					return fmt.Errorf("stuntgres: invalid check column dependency")
				}
			} else {
				target, ok := view.Descriptor(address)
				if !ok || !stored.ReferenceEpochValid(view, d, dep.Ref) || (target.Kind() != ids.RelationObject && target.Kind() != ids.TypeObject && target.Kind() != ids.CollationObject && target.Kind() != ids.ProcedureObject && target.Kind() != ids.OperatorObject && target.Kind() != ids.NamespaceObject && target.Kind() != ids.TSDictionaryObject) || target.Kind() == ids.NamespaceObject && dep.Ref.Address.SubID != 0 {
					return fmt.Errorf("stuntgres: invalid check provider dependency")
				}
				if target.Kind() == ids.CollationObject {
					if err := stored.ValidateCollationDependency(view, dep.Ref, c.Expression); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// validateCheckRow is the shared DML-independent completed-row validator. A
// future ADD/VALIDATE consumer supplies its own traversal and publication stage.
// UNKNOWN passes, unlike WHERE. The no-CHECK path does not allocate or bind.
func ValidateCheckRow(d *catalog.Relation, row []sqltype.Value, eval *exec.Context) (*catalog.CheckConstraint, error) {
	if len(d.Constraints.Check) == 0 {
		return nil, nil
	}
	oldRow, oldScope, oldFrame := eval.Row, eval.Scope, eval.Frame
	eval.Row, eval.Scope, eval.Frame = row, 1, 1
	defer func() { eval.Row, eval.Scope, eval.Frame = oldRow, oldScope, oldFrame }()
	program, admission, err := prepareCheckProgram(d, eval)
	if err != nil {
		return nil, err
	}
	// Lazy SQL-body demand happens during truth, after preflight's stack
	// context has ended. Keep this actual CHECK's original recipe for Eval;
	// the shared statement manifest cannot stand in for a stored constraint.
	oldRecipe, oldGraft := eval.RoutineRecipe, eval.RoutineGraft
	oldChecks, oldReferences, oldConsumer := eval.RoutineChecks, eval.RoutineReferences, eval.RoutineConsumer
	oldProgram := eval.RoutineProgram
	eval.RoutineProgram = admission
	defer func() {
		eval.RoutineRecipe, eval.RoutineGraft = oldRecipe, oldGraft
		eval.RoutineChecks, eval.RoutineReferences, eval.RoutineConsumer = oldChecks, oldReferences, oldConsumer
		eval.RoutineProgram = oldProgram
		if _, hosted := eval.LiveHost().(CheckRecipeHost); !hosted && oldProgram != nil {
			oldProgram.Include(admission)
		}
	}()
	for i := range d.Constraints.Check {
		c := &d.Constraints.Check[i]
		eval.RoutineRecipe, eval.RoutineGraft = true, false
		eval.RoutineChecks, eval.RoutineReferences, eval.RoutineConsumer = c.Dependencies, nil, d
		expression := c.Expression
		if program.Expressions != nil {
			expression = program.Expressions[i]
		}
		value, err := expression.Eval(eval)
		if err != nil {
			return nil, err
		}
		truth, err := plan.TruthOf(value)
		if err != nil {
			return nil, err
		}
		if truth == plan.False {
			return c, nil
		}
	}
	return nil, nil
}

func CheckDetailError(schema, table, name, detail string) error {
	d := sqlError("23514", `new row for relation "`+table+`" violates check constraint "`+name+`"`).(*sqlstate.Error)
	d.Schema, d.Table, d.Constraint, d.Detail = schema, table, name, detail
	return d
}

type CheckDependencyEdge struct {
	Relation *catalog.Relation
	Check    catalog.CheckConstraint
	Target   ids.ObjectAddress
}

func CheckDependents(provider DropDependencyReader, targets []ids.ObjectAddress, mode parser.DropMode) ([]CheckDependencyEdge, error) {
	var relations []*catalog.Relation
	for address, descriptor := range provider.InputDescriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(d.Constraints.Check) == 0 {
			continue
		}
		if staged, ok := provider.Replacement(address).(*catalog.Relation); ok {
			d = staged
		}
		if provider.Removed(address) {
			continue
		}
		dropped := false
		for _, target := range targets {
			if target == address {
				dropped = true
				break
			}
		}
		if !dropped {
			relations = append(relations, d)
		}
	}
	slices.SortFunc(relations, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	var edges []CheckDependencyEdge
	for _, d := range relations {
		var kept []catalog.CheckConstraint
		changed := false
		for _, c := range d.Constraints.Check {
			var dependency ids.ObjectAddress
			for _, dep := range c.Dependencies {
				a := dep.Ref.Address
				if a.ClassID == d.Address().ClassID && a.ObjectID == d.Address().ObjectID {
					continue
				}
				for _, target := range targets {
					if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
						dependency = target
						break
					}
				}
			}
			if dependency == (ids.ObjectAddress{}) {
				kept = append(kept, c)
				continue
			}
			edges = append(edges, CheckDependencyEdge{d, c, dependency})
			if mode == parser.DropRestrict {
				continue
			}
			changed = true
		}
		if changed {
			if err := provider.InheritanceAlterGuard(d); err != nil {
				return nil, err
			}
			copy := *d
			revision, err := provider.Revision(copy.Revision())
			if err != nil {
				return nil, err
			}
			copy.ObjectRevision = revision
			copy.Constraints.Check = kept
			if err := provider.Replace(&copy); err != nil {
				return nil, err
			}
		}
	}
	slices.SortStableFunc(edges, func(a, b CheckDependencyEdge) int {
		if a.Relation.Address().ObjectID < b.Relation.Address().ObjectID {
			return -1
		}
		if a.Relation.Address().ObjectID > b.Relation.Address().ObjectID {
			return 1
		}
		return cmp.Compare(a.Check.Ordinal, b.Check.Ordinal)
	})
	return edges, nil
}
