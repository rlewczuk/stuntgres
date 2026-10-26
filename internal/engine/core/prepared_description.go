package core

import (
 "context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
)

type Description struct {
	Parameters analyze.ParameterInfo
	Results    []catalog.Column
	// Logical user identities belong to an owner/epoch, not merely a reused OID.
	CatalogOwner *catalog.Owner
	CatalogEpoch ids.CatalogEpoch
	// Public signatures stay immutable. Invalidation refreshes temporal-now and
	// specialized groups atomically, preserving native logical literal identity.
	Temporals        []analyze.TemporalConstant
	Types            map[analyze.PreparedTypeKey]analyze.TypeOccurrence
	Sequences        *sequence.Analysis
	Regtypes         *regobj.Analysis
	Analysis         *Analysis
	Folds            *PreparedFolds
	FoldPlanning     *FoldPublication // Candidate-only; never published.
	PlanOwner        ids.SessionToken
	PlanGeneration   uint64
	RefreshTemporals bool // Candidate-only request; never published in a description.
}


// RetireAnalysisTypeCasts retires the description's refreshable type occurrences:
// casts and constructor shapes. It makes invalidation sticky, so a
// failed reanalysis cannot revive them when the path is restored.
func (d *Description) RetireAnalysisTypeCasts() {
	Types, retired := analyze.RetireRefreshableTypes(d.Types)
	if !retired {
		return
	}
	d.Types = Types
	if d.Analysis != nil {
		d.Analysis.NeedsReanalysis = true
	}
}

// RetireAnalysisTypeCastsForRebind keeps eligible nominal installed-scalar occurrences
// only in this attempt's candidate. Failure cannot revive the retired original.
func (d *Description) RetireAnalysisTypeCastsForRebind(installedScalarAuthorityRebind bool) map[analyze.PreparedTypeKey]analyze.TypeOccurrence {
	var captures map[analyze.PreparedTypeKey]analyze.TypeOccurrence
	if installedScalarAuthorityRebind {
		for key, captured := range d.Types {
			if key.Kind == analyze.IdentityCast && captured.Kind == analyze.IdentityCast && captured.Base.Type != nil && (captured.Base.Type.IsHstore() || captured.Base.Type.IsLquery() || (captured.Base.Type.IsLtree() || captured.Base.Type.IsLtxtquery()) || captured.Base.Type.IsEan13() || captured.Base.Type.IsIsbn() || captured.Base.Type.IsIsbn13() || captured.Base.Type.IsIsmn() || captured.Base.Type.IsIsmn13() || captured.Base.Type.IsIssn() || captured.Base.Type.IsIssn13() || captured.Base.Type.IsUpc()) {
				if captures == nil {
					captures = make(map[analyze.PreparedTypeKey]analyze.TypeOccurrence)
				}
				captures[key] = captured
			}
		}
	}
	d.RetireAnalysisTypeCasts()
	return analyze.PublishRefreshableTypes(d.Types, captures)
}
// AcceptAnalysis publishes a complete successful capture refresh before frame
// import or execution. The caller has validated the canonical signature and
// admitted native binding; failures before this operation leave the old group.
func (d *Description) AcceptAnalysis(ctx context.Context, sequences sequence.Analysis, regtypes *regobj.Analysis, refreshed *Description) error {
 if err := ctx.Err(); err != nil { return err }
 if d.Sequences != nil { *d.Sequences = sequences } else if len(sequences.Constants) != 0 { d.Sequences = &sequences }
 if d.Regtypes != nil && regtypes != nil { *d.Regtypes = *regtypes } else { d.Regtypes = regtypes }
 if refreshed != nil {
  d.Temporals = refreshed.Temporals
  d.Types = analyze.PublishRefreshableTypes(d.Types,refreshed.Types)
  d.Analysis = refreshed.Analysis
  d.PlanGeneration = refreshed.PlanGeneration
 }
 return nil
}