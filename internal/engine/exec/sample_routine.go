package exec

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// sampleRoutine is the consumed native counterpart of the handler's returned
// method table. The pure method is not another pg_proc invocation and retains
// neither an evaluator nor the handler's temporary trusted subject.
type sampleRoutine struct {
	selectRows func(context.Context, *Stage, *Stage, sqltype.Value) error
}

func initializeSampleRoutine(evaluation *Context, source *plan.SourceNode) (sampleRoutine, error) {
	if source == nil || source.Sample == nil || source.Occurrence < 0 {
		return sampleRoutine{}, fmt.Errorf("stuntgres: missing actual sampling source")
	}
	sample := source.Sample
	// Stored plans retain the original selected declaration across lawful
	// rename/owner/ACL COW. Authenticate that carrier before using the current
	// descriptor; the provider still requires exact current catalog identity.
	if sample.Procedure == nil || evaluation.Catalog.Owner() == nil || sample.Handler.Domain() != evaluation.Catalog.Domain() || sample.Handler.Epoch != evaluation.Catalog.Epoch() || sample.Handler.Address != sample.Procedure.Address() {
		return sampleRoutine{}, ids.ErrCatalogReplaced
	}
	procedure, err := ValidateStoredRoutineDescriptor(evaluation.Catalog, sample.Procedure)
	if err != nil {
		return sampleRoutine{}, err
	}
	argumentType, err := extension.SamplingArgumentType(evaluation.Catalog, procedure)
	if err != nil {
		return sampleRoutine{}, err
	}
	if sample.Argument == nil || sample.Argument.ResultUsage().Type == nil || sample.Argument.ResultUsage().Type.ID() != argumentType {
		return sampleRoutine{}, fmt.Errorf("stuntgres: invalid sampling argument recipe")
	}
	site := DirectRoutineInitializationSite{Routine: procedure, Reference: sample.Handler, Purpose: DirectSample, Source: source.Ref, Occurrence: uint32(source.Occurrence)}
	if err := BeginDirectRoutineInitialization(evaluation, site); err != nil {
		return sampleRoutine{}, err
	}
	token, err := evaluation.EnterInitializedDirectRoutine(site)
	if err != nil {
		return sampleRoutine{}, err
	}
	defer token.Leave()
	// GetTsmRoutine passes an internal pointer datum with isnull=false, even
	// though the pointer is NULL. STRICT therefore still invokes the handler.
	// nodeSamplescan retains its returned table before evaluating SQL arguments;
	// raw scan methods and rescans are not further handler invocations.
	if argumentType == sqltype.Float8 {
		return sampleRoutine{selectRows: selectSampleDuration}, nil
	}
	return sampleRoutine{selectRows: selectSampleCount}, nil
}

func selectSampleCount(ctx context.Context, out, child *Stage, value sqltype.Value) error {
	count, err := value.Integer()
	if err != nil {
		return err
	}
	return selectSampleRows(ctx, out, child, count)
}

func selectSampleDuration(ctx context.Context, out, child *Stage, value sqltype.Value) error {
	millis, err := value.Float()
	if err != nil {
		return err
	}
	// Nonzero time sampling consumes the initialized child and its read lease.
	if millis != 0 {
		child.beginRead()
	}
	return selectSampleTime(ctx, out, child, millis)
}
