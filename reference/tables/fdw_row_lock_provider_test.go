package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"stuntgres"
)

// This optional wrapper uses immutable source rid/generation and a source-owned
// revision. Neither visible keys nor local heap identities identify remote rows.
type fdwSQLRowLocker struct {
	*fdwSQLWriter
	sibling       stuntgres.ForeignRowMarkType
	beforeRefetch func(context.Context, stuntgres.ForeignRowIdentity) error
	recheck       func(context.Context, stuntgres.ForeignRow) (bool, error)
	afterRefetch  func(context.Context, stuntgres.ForeignRowMarkRequest, stuntgres.ForeignRow) (stuntgres.ForeignRow, error)
	planHook      func(stuntgres.ForeignRowMarkPlan) stuntgres.ForeignRowMarkPlan
}

func (w *fdwSQLRowLocker) GetForeignRowMarkType(ctx context.Context, r stuntgres.ForeignRowMarkRequest) (stuntgres.ForeignRowMarkPlan, error) {
	kind := w.sibling
	switch r.Strength {
	case stuntgres.ForeignLockKeyShare:
		kind = stuntgres.ForeignRowMarkKeyShare
	case stuntgres.ForeignLockShare:
		kind = stuntgres.ForeignRowMarkShare
	case stuntgres.ForeignLockNoKeyUpdate:
		kind = stuntgres.ForeignRowMarkNoKeyUpdate
	case stuntgres.ForeignLockUpdate:
		kind = stuntgres.ForeignRowMarkUpdate
	}
	plan := stuntgres.ForeignRowMarkPlan{Type: kind}
	if kind != stuntgres.ForeignRowMarkCopy {
		plan.Identity = stuntgres.ForeignUpdateTargets{Names: []string{"rid", "generation", "revision"}}
	}
	if w.planHook != nil {
		plan = w.planHook(plan)
	}
	return plan, ctx.Err()
}

func (w *fdwSQLRowLocker) RefetchForeignRow(ctx context.Context, r stuntgres.ForeignRowMarkRequest, identity stuntgres.ForeignRowIdentity) (stuntgres.ForeignRefetchResult, error) {
	if len(identity) != 3 {
		return stuntgres.ForeignRefetchResult{}, errors.New("SQL row locker: invalid identity")
	}
	if w.beforeRefetch != nil {
		if err := w.beforeRefetch(ctx, identity); err != nil {
			return stuntgres.ForeignRefetchResult{}, err
		}
	}
	tx, err := fdwWriterTx(r.ForeignRequest)
	if err != nil {
		return stuntgres.ForeignRefetchResult{}, err
	}
	columns := fdwWriterColumns(r.ForeignRequest)
	q := "SELECT " + strings.Join(columns, ",") + ",revision FROM " + fdwRemote(r.ForeignRequest) + " WHERE rid=$1 AND generation=$2"
	if r.RowMark == nil {
		return stuntgres.ForeignRefetchResult{}, errors.New("SQL row locker: missing selected mark")
	}
	switch r.RowMark.Type {
	case stuntgres.ForeignRowMarkReference:
	case stuntgres.ForeignRowMarkKeyShare:
		q += " FOR KEY SHARE"
	case stuntgres.ForeignRowMarkShare:
		q += " FOR SHARE"
	case stuntgres.ForeignRowMarkNoKeyUpdate:
		q += " FOR NO KEY UPDATE"
	case stuntgres.ForeignRowMarkUpdate:
		q += " FOR UPDATE"
	default:
		return stuntgres.ForeignRefetchResult{}, fmt.Errorf("SQL row locker: invalid selected mark %d", r.RowMark.Type)
	}
	if r.RowMark.Type != stuntgres.ForeignRowMarkReference {
		switch r.WaitPolicy {
		case stuntgres.ForeignLockNoWait:
			q += " NOWAIT"
		case stuntgres.ForeignLockSkipLocked:
			q += " SKIP LOCKED"
		}
	}
	values := make(stuntgres.ForeignRow, len(columns)+1)
	dest := make([]any, len(values))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := tx.QueryRowContext(ctx, q, identity[0], identity[1]).Scan(dest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return stuntgres.ForeignRefetchResult{}, nil
		}
		return stuntgres.ForeignRefetchResult{}, err
	}
	for i, value := range values {
		if bytes, ok := value.([]byte); ok {
			values[i] = append([]byte(nil), bytes...)
		}
	}
	row := values[:len(columns)]
	if w.afterRefetch != nil {
		row, err = w.afterRefetch(ctx, r, row)
		if err != nil {
			return stuntgres.ForeignRefetchResult{}, err
		}
	}
	// A test hook may perform real SQL after this read. Its version is then
	// uncertain, so conservatively demand the ordinary local/provider recheck.
	return stuntgres.ForeignRefetchResult{Row: row, Present: true, Updated: values[len(columns)] != identity[2] || w.afterRefetch != nil}, nil
}

func (w *fdwSQLRowLocker) RecheckForeignScan(ctx context.Context, r stuntgres.ForeignRowMarkRequest, row stuntgres.ForeignRow) (bool, error) {
	if w.recheck != nil {
		return w.recheck(ctx, row)
	}
	// No remote quals are pushed by this wrapper. Local SQL remains authoritative.
	return true, ctx.Err()
}

// These wrappers deliberately omit one optional method set. Their inherited
// scans/transactions still execute the same genuine source SQL.
type fdwRowMarkWithoutRefetch struct {
	*fdwSQLWriter
	locker *fdwSQLRowLocker
}

func (w fdwRowMarkWithoutRefetch) GetForeignRowMarkType(ctx context.Context, r stuntgres.ForeignRowMarkRequest) (stuntgres.ForeignRowMarkPlan, error) {
	return w.locker.GetForeignRowMarkType(ctx, r)
}

type fdwRowMarkWithoutTransaction struct {
	stuntgres.ForeignDataWrapper
	locker *fdwSQLRowLocker
}

func (w fdwRowMarkWithoutTransaction) GetForeignRowMarkType(ctx context.Context, r stuntgres.ForeignRowMarkRequest) (stuntgres.ForeignRowMarkPlan, error) {
	return w.locker.GetForeignRowMarkType(ctx, r)
}

func (w fdwRowMarkWithoutTransaction) RefetchForeignRow(ctx context.Context, r stuntgres.ForeignRowMarkRequest, identity stuntgres.ForeignRowIdentity) (stuntgres.ForeignRefetchResult, error) {
	return w.locker.RefetchForeignRow(ctx, r, identity)
}

type fdwRowMarkWithoutIdentity struct{ *fdwSQLRowLocker }
type fdwRowMarkScanOnly struct{ stuntgres.ForeignScan }

func (w fdwRowMarkWithoutIdentity) BeginForeignScan(ctx context.Context, r stuntgres.ForeignRequest, p stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	scan, err := w.fdwSQLWriter.BeginForeignScan(ctx, r, p)
	if err != nil {
		return nil, err
	}
	return fdwRowMarkScanOnly{scan}, nil
}

func (w *fdwSQLRowLocker) registerRowMarkVariants(d *stuntgres.Driver) error {
	for _, variant := range []struct {
		name    string
		wrapper stuntgres.ForeignDataWrapper
	}{
		{"fdw_mark_no_refetch", fdwRowMarkWithoutRefetch{w.fdwSQLWriter, w}},
		{"fdw_mark_no_transaction", fdwRowMarkWithoutTransaction{w.fdwSQLWriter, w}},
		{"fdw_mark_no_identity", fdwRowMarkWithoutIdentity{w}},
	} {
		if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: variant.name}, Wrapper: variant.wrapper}); err != nil {
			return err
		}
	}
	return nil
}
