package typedef

import (
	"errors"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// DomainValidation selects historical validation without conflating NULLness
// with CHECK expression execution.
type DomainValidation uint8

const (
	DomainValidateNone DomainValidation = iota
	DomainValidateValues
	DomainValidateNulls
)

type DomainEdit struct {
	Next       catalog.Type
	Selected   int
	Validation DomainValidation
	Skipped    bool
}

// StageAlterDomainLocked stages privately; the caller owns row validation and
// atomic publication. No lock-manager waits occur here.
func StageAlterDomainLocked(h StageHost, auth ddl.Authority, old *catalog.Type, raw *parser.AlterDomain, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) (DomainEdit, error) {
	next := *old
	if raw.Action == parser.DomainAddCheck || raw.Action == parser.DomainValidateCheck {
		next.DomainChecks = slices.Clone(old.DomainChecks)
	}
	selected := -1
	if raw.Action == parser.DomainDropCheck || raw.Action == parser.DomainValidateCheck {
		for i, check := range next.DomainChecks {
			if check.Name == raw.Constraint.Text {
				selected = i
				break
			}
		}
	}
	validation := DomainValidateNone
	if raw.Action == parser.DomainValidateCheck || raw.Action == parser.DomainAddCheck && !raw.Check.NotValid {
		validation = DomainValidateValues
	}
	var err error
	switch raw.Action {
	case parser.DomainAddCheck:
		check := *raw.Check
		// REL_18_0 ALTER accepts but ignores NO INHERIT. CREATE keeps its
		// own cooking rejection; never relax the shared CREATE binder.
		check.NoInherit = false
		err = h.BindDomainConstraints(auth, &next, []parser.ColumnConstraint{{Check: &check, Pos: check.Pos}}, input)
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
				ddl.Notice(h, "00000", message+", skipping")
				return DomainEdit{Skipped: true}, nil
			}
			err = sqlError("42704", message)
		} else if raw.Action == parser.DomainDropCheck {
			err = h.RemoveDomainCheck(old, next.DomainChecks[selected].OID)
		} else {
			next.DomainChecks[selected].NotValid = false
		}
	case parser.DomainSetDefault, parser.DomainDropDefault:
		next.DomainDefault, next.DomainDefaultOwn, next.DomainDefaultText = nil, false, ""
		if raw.Action == parser.DomainSetDefault {
			base := old.SQLType.DomainBase().Type
			ref := ddl.ObjectRef(h, auth, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.ID())})
			err = h.AccessView(auth).CanUse(auth, ref)
			if err == nil {
				err = h.BindDomainDefault(auth, &next, raw.Default, input, output)
				var diagnostic *sqlstate.Error
				if errors.As(err, &diagnostic) {
					copy := *diagnostic
					copy.Position = ""
					err = &copy
				}
			}
		}
	case parser.DomainSetNotNull, parser.DomainDropNotNull:
		own := raw.Action == parser.DomainSetNotNull
		if old.SQLType.OwnDomainNotNull() == own {
			return DomainEdit{Skipped: true}, nil
		}
		if own {
			err = BindDomainNotNull(h, auth, &next, "")
			validation = DomainValidateNulls
		} else {
			next.DomainNotNull = nil
		}
		if err == nil {
			next.SQLType, err = sqltype.Domain(old.SQLType.ID(), old.Name(), old.SQLType.SQLName(), old.SQLType.DomainBase(), len(next.DomainChecks) != 0, own)
		}
	default:
		err = fmt.Errorf("stuntgres: invalid domain action")
	}
	if err == nil && raw.Action != parser.DomainDropCheck {
		next.ObjectRevision, err = h.Revision()
		if err == nil {
			err = h.ReplaceUnsynchronized(&next)
		}
	}
	if err != nil {
		return DomainEdit{}, err
	}
	return DomainEdit{Next: next, Selected: selected, Validation: validation}, nil
}
