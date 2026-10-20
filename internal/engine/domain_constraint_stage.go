package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// domainConstraintEdit is the outcome of staging an ALTER DOMAIN constraint
// action.
type domainConstraintEdit struct {
	Next       catalog.Type // the domain after the action
	Selected   int          // the added, dropped or validated check in Next
	Validating bool         // existing values must satisfy the selected check
	Skipped    bool         // DROP IF EXISTS met no such check; nothing was staged
}

// stageAlterDomainConstraintLocked stages the constraint action of raw on
// old. The caller validates rows and publishes.
func stageAlterDomainConstraintLocked(h typedef.StageHost, auth typedef.Authority, old *catalog.Type, raw *parser.AlterDomain, input sqltype.InputEnvironment) (domainConstraintEdit, error) {
	next := *old
	next.DomainChecks = slices.Clone(old.DomainChecks)
	selected := -1
	for i, check := range next.DomainChecks {
		if check.Name == raw.Constraint.Text {
			selected = i
			break
		}
	}
	validating := raw.Action == parser.DomainValidateCheck || raw.Action == parser.DomainAddCheck && !raw.Check.NotValid
	var err error
	switch raw.Action {
	case parser.DomainAddCheck:
		check := *raw.Check
		// REL_18_0 ALTER accepts but ignores NO INHERIT. CREATE keeps its
		// own cooking rejection; never relax the shared CREATE binder.
		check.NoInherit = false
		err = h.BindDomainChecks(auth, &next, []parser.ColumnConstraint{{Check: &check, Pos: check.Pos}}, input)
		if err == nil {
			for i, c := range next.DomainChecks {
				if !slices.ContainsFunc(old.DomainChecks, func(prior catalog.DomainCheckConstraint) bool { return prior.OID == c.OID }) {
					selected = i
					break
				}
			}
			next.SQLType, err = sqltype.Domain(old.SQLType.ID(), old.Name(), old.SQLType.SQLName(), old.SQLType.DomainBase(), true, old.SQLType.OwnDomainNotNull())
		}
	case parser.DomainDropCheck, parser.DomainValidateCheck:
		if selected < 0 {
			message := fmt.Sprintf("constraint %q of domain %q does not exist", raw.Constraint.Text, catalog.ObjectNameText(raw.Name))
			if raw.Action == parser.DomainDropCheck && raw.IfExists {
				typedef.Notice(h, "00000", message+", skipping")
				return domainConstraintEdit{Skipped: true}, nil
			}
			err = sqlError("42704", message)
		} else if raw.Action == parser.DomainDropCheck {
			err = h.RemoveDomainCheck(old, next.DomainChecks[selected].OID)
		} else {
			next.DomainChecks[selected].NotValid = false
		}
	default:
		err = fmt.Errorf("stuntgres: invalid domain constraint action")
	}
	if err == nil && raw.Action != parser.DomainDropCheck {
		next.ObjectRevision, err = h.Revision()
		if err == nil {
			err = h.ReplaceUnsynchronized(&next)
		}
	}
	if err != nil {
		return domainConstraintEdit{}, err
	}
	return domainConstraintEdit{Next: next, Selected: selected, Validating: validating}, nil
}
