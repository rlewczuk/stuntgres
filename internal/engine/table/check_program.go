package table

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// CheckRecipeHost borrows statement-owned CHECK preparation without extending
// the mandatory execution Host interface. Registration uses the statement's
// existing consumed-program collection and readmission fences.
type CheckRecipeHost interface {
	CommandIdentity() ids.CommandIdentity
	ReferentialActor() (authorization.PrincipalRef, bool)
	CheckPrograms() map[*catalog.Relation]exec.CheckProgram
	RecordCheckProgram(*catalog.Relation, exec.CheckProgram, exec.CheckPhase)
}

func prepareCheckProgram(d *catalog.Relation, evaluation *exec.Context) (exec.CheckProgram, *exec.RoutineProgram, error) {
	host, _ := evaluation.LiveHost().(CheckRecipeHost)
	command := evaluation.DomainPhase
	if host != nil {
		evaluation.PreparedChecks = host.CheckPrograms()
		command = host.CommandIdentity()
	}
	auth, _, err := exec.RoutineAdmissionAuthority(evaluation)
	if err != nil {
		return exec.CheckProgram{}, nil, err
	}
	phase := exec.CheckPhase{Command: command, Actor: auth.State.CurrentUser}
	if host != nil {
		actor, ri := host.ReferentialActor()
		phase.RI = ri && actor == phase.Actor
	}
	program, prepared := evaluation.PreparedChecks[d]
	for _, initialized := range program.Phases {
		if initialized.Command == phase.Command && initialized.Actor == phase.Actor && initialized.RI == phase.RI {
			return program, initialized.Admission, nil
		}
	}
	phase.Admission = new(exec.RoutineProgram)
	if prepared {
		if err := phase.Admission.InitializePhase(program.Phases[0].Admission, auth); err != nil {
			return exec.CheckProgram{}, nil, err
		}
	} else {
		// All constraints prepare successfully before any row truth. Each owns
		// its original manifest; a borrowed statement manifest is insufficient.
		for i, check := range d.Constraints.Check {
			if check.NotEnforced {
				continue
			}
			local := *evaluation
			local.RoutineRecipe, local.RoutineGraft = true, false
			local.RoutineChecks = check.Dependencies
			local.RoutineReferences, local.RoutineConsumer = nil, d
			local.RoutineProgram = phase.Admission
			node, changed, err := exec.PreflightCoercionsWithPolicy(&local, check.Expression, true)
			evaluation.PreparedDomainChecks = local.PreparedDomainChecks
			if err != nil {
				return exec.CheckProgram{}, nil, err
			}
			if err := exec.AdmitExpression(&local, node, local.Catalog); err != nil {
				return exec.CheckProgram{}, nil, err
			}
			if changed {
				if program.Expressions == nil {
					program.Expressions = make([]expr.Expr, len(d.Constraints.Check))
					for j, original := range d.Constraints.Check {
						program.Expressions[j] = original.Expression
					}
				}
				program.Expressions[i] = node
			}
		}
	}
	program.Phases = append(program.Phases, phase)
	if host != nil {
		host.RecordCheckProgram(d, program, phase)
		evaluation.PreparedChecks = host.CheckPrograms()
	} else {
		if evaluation.PreparedChecks == nil {
			evaluation.PreparedChecks = make(map[*catalog.Relation]exec.CheckProgram)
		}
		evaluation.PreparedChecks[d] = program
	}
	return program, phase.Admission, nil
}
