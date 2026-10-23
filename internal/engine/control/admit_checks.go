package control

import (
	"strings"

	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/largeobject"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
)

// Restore admission checks that read only the statement. Each returns the
// rejection for a shape Restore does not replay; AdmitStatement turns one into
// an admit entry.

// stagesTemporary reports a TEMPORARY or pg_temp-qualified relation, which a
// private restore stage cannot retract.
func stagesTemporary(persistence string, name parser.ObjectName) bool {
	return persistence == "temporary" || len(name.Parts) == 2 && (name.Parts[0].Text == "pg_temp" || strings.HasPrefix(name.Parts[0].Text, "pg_temp_"))
}

// CheckCreateTable rejects a temporary table, then checks its stored
// expressions. The caller has already captured and stripped its foreign keys.
func CheckCreateTable(s *parser.CreateTable) error {
	if stagesTemporary(s.Persistence, s.Name) {
		return sqlError("0A000", "restore cannot stage non-retractable effects")
	}
	for _, element := range s.Elements {
		if element.Column != nil {
			for _, constraint := range element.Column.Constraints {
				if err := dump.RestoreScalar(constraint.Default, true); err != nil {
					return err
				}
				if constraint.Generation != nil {
					if err := dump.RestoreScalar(constraint.Generation.Expression, true); err != nil {
						return err
					}
				}
				if constraint.Check != nil {
					if err := dump.RestoreScalar(constraint.Check.Expression, true); err != nil {
						return err
					}
				}
			}
		}
		if element.Check != nil {
			if err := dump.RestoreScalar(element.Check.Expression, true); err != nil {
				return err
			}
		}
		if element.Exclusion != nil {
			for _, key := range element.Exclusion.Index.Keys {
				if err := dump.RestoreScalar(key.Expression, true); err != nil {
					return err
				}
			}
			if err := dump.RestoreScalar(element.Exclusion.Index.Predicate, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// CheckAlterDomain admits a qualified named domain CHECK addition. A
// historical cast recipe may require a valid CHECK after CREATE DOMAIN.
// Admission preserves its validity flag; ordinary ALTER owns binding and
// validation against existing rows before the private stage can be published.
func CheckAlterDomain(raw *parser.AlterDomain) error {
	if raw == nil || raw.Action != parser.DomainAddCheck || raw.Check == nil || raw.Check.NoInherit || raw.Check.Name.Text == "" || raw.Constraint != raw.Check.Name || raw.IfExists || raw.Mode != 0 || len(raw.Name.Parts) != 2 || raw.Name.Parts[0].Text == "pg_temp" || strings.HasPrefix(raw.Name.Parts[0].Text, "pg_temp_") {
		return sqlError("0A000", "restore accepts only qualified named domain CHECK additions")
	}
	return dump.RestoreScalar(raw.Check.Expression, true)
}

// CheckAlterView admits only a materialized view extension dependency.
func CheckAlterView(s *parser.AlterView) error {
	if !s.Materialized || (s.Action != "depends" && s.Action != "no_depends") || s.IfExists {
		return sqlError("0A000", "restore accepts only materialized extension dependency ALTER")
	}
	return nil
}

// CheckCreateView rejects a temporary view.
func CheckCreateView(s *parser.CreateView) error {
	if stagesTemporary(s.Persistence, s.Name) {
		return sqlError("0A000", "restore cannot stage non-retractable effects")
	}
	return nil
}

// CheckCreateQueryTable admits only an unpopulated materialized view.
func CheckCreateQueryTable(s *parser.CreateQueryTable) error {
	if s.Form != parser.CreateMaterializedView || !s.NoData {
		return sqlError("0A000", "restore accepts only unpopulated materialized view definitions")
	}
	return nil
}

// CheckAlterIndex admits an index rename or partition attachment.
func CheckAlterIndex(s *parser.AlterIndex) error {
	if s.Action != "rename" && s.Action != "attach" {
		return sqlError("0A000", "restore accepts only index rename and partition attachment")
	}
	return nil
}

// CheckAlterTableOf admits only a typed-table attachment.
func CheckAlterTableOf(s *parser.AlterTableOf) error {
	if s.Type == nil {
		return sqlError("0A000", "restore accepts only typed-table attachments")
	}
	return nil
}

// CheckAttachPartition admits an unconditional attachment whose bound
// expressions are Restore scalars.
func CheckAttachPartition(s *parser.AlterTableAttachPartition) error {
	if s.IfExists {
		return sqlError("0A000", "restore accepts only unconditional partition attachments")
	}
	for _, tuple := range [3][]parser.PartitionDatum{s.Bound.Lower, s.Bound.Upper, s.Bound.Values} {
		for _, datum := range tuple {
			if datum.Kind == 0 {
				if err := dump.RestoreScalar(datum.Expression, true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// CheckCreateSequence rejects a temporary sequence.
func CheckCreateSequence(s *parser.CreateSequence) error {
	if s.Persistence == "temporary" {
		return sqlError("0A000", "restore cannot stage non-retractable effects")
	}
	return nil
}

// CheckAlterTableDefault admits a deferred SET DEFAULT or an inherited ONLY
// DROP DEFAULT.
func CheckAlterTableDefault(s *parser.AlterTableDefault) error {
	if !(s.Action == parser.SetDefault && s.Expression != nil) && !(s.Action == parser.DropDefault && s.Only && len(s.Name.Parts) == 2) {
		return sqlError("0A000", "restore accepts only deferred SET DEFAULT or inherited ONLY DEFAULT alterations")
	}
	return nil
}

// CheckSelect admits only generated sequence or large object state.
func CheckSelect(s *parser.Select) error {
	if !sequence.RestoresState(s) && !largeobject.RestoresBytes(s) {
		return sqlError("0A000", "restore accepts only generated sequence or large object state")
	}
	return nil
}

// CheckComment rejects a role comment, which is not database-local.
func CheckComment(s *parser.Comment) error {
	if s.Kind == parser.CommentRole {
		return sqlError("0A000", "restore accepts only database-local COMMENT targets")
	}
	return nil
}

// CheckInsert admits a plain VALUES INSERT whose subscripts and values are
// Restore scalars.
func CheckInsert(s *parser.Insert, eligible ...func(parser.FunctionExpr) bool) error {
	if len(s.With) != 0 {
		return sqlError("0A000", "restore INSERT WITH clauses are unsupported")
	}
	if s.Returning != nil {
		return sqlError("0A000", "restore INSERT RETURNING is unsupported")
	}
	if s.Conflict != nil {
		return sqlError("0A000", "restore INSERT conflict clauses are unsupported")
	}
	if s.Source != nil {
		return sqlError("0A000", "restore INSERT query sources are unsupported")
	}
	for _, target := range s.Targets {
		for _, step := range target.Steps {
			if step.Subscript == nil {
				continue
			}
			bound := step.Subscript
			if bound.Lower != nil {
				if err := dump.RestoreScalar(bound.Lower, false, eligible...); err != nil {
					return err
				}
			}
			if bound.Upper != nil {
				if err := dump.RestoreScalar(bound.Upper, false, eligible...); err != nil {
					return err
				}
			}
		}
	}
	for _, row := range s.Rows {
		for _, v := range row {
			if err := dump.RestoreScalar(v, false, eligible...); err != nil {
				return err
			}
		}
	}
	return nil
}
