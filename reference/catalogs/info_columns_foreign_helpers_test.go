package reference_test

import (
	"context"
	"errors"

	"stuntgres"
)

// Metadata must inspect real registered method sets without invoking planning,
// scans or mutation. Every unexpected I/O callback returns a visible error.
var icForeignIO = errors.New("columns metadata attempted foreign I/O")

type icForeignRead struct{}

func (icForeignRead) GetForeignRelSize(context.Context, stuntgres.ForeignRequest) (stuntgres.ForeignRelSize, error) {
	return stuntgres.ForeignRelSize{}, icForeignIO
}
func (icForeignRead) GetForeignPaths(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignRelSize) ([]stuntgres.ForeignPath, error) {
	return nil, icForeignIO
}
func (icForeignRead) GetForeignPlan(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignPath) (stuntgres.ForeignPlan, error) {
	return stuntgres.ForeignPlan{}, icForeignIO
}
func (icForeignRead) BeginForeignScan(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	return nil, icForeignIO
}

type icForeignUpdate struct{ icForeignRead }

func (icForeignUpdate) ExecForeignUpdate(context.Context, stuntgres.ForeignModifyRequest, any, stuntgres.ForeignRowIdentity, stuntgres.ForeignRow) (stuntgres.ForeignRow, bool, error) {
	return nil, false, icForeignIO
}

type icForeignDelete struct{ icForeignRead }

func (icForeignDelete) ExecForeignDelete(context.Context, stuntgres.ForeignModifyRequest, any, stuntgres.ForeignRowIdentity, stuntgres.ForeignRow) (stuntgres.ForeignRow, bool, error) {
	return nil, false, icForeignIO
}

type icForeignBoth struct{ icForeignUpdate }

func (icForeignBoth) ExecForeignDelete(context.Context, stuntgres.ForeignModifyRequest, any, stuntgres.ForeignRowIdentity, stuntgres.ForeignRow) (stuntgres.ForeignRow, bool, error) {
	return nil, false, icForeignIO
}

func icConfigureForeign(d *stuntgres.Driver) error {
	for _, entry := range []struct {
		name    string
		wrapper stuntgres.ForeignDataWrapper
	}{
		{"neither", icForeignRead{}}, {"update_only", icForeignUpdate{}}, {"delete_only", icForeignDelete{}}, {"both", icForeignBoth{}},
	} {
		if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "ic_" + entry.name}, Wrapper: entry.wrapper}); err != nil {
			return err
		}
	}
	return nil
}
