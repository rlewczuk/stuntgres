package fdw

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// A zero-column tuple is still a real returned row. This isolates present-row
// completion from value conversion without inventing an alternative row codec.
type progressProvider struct {
	catalog.ForeignDataWrapper
	size, units, ordinal int
	batches              []int
	failure              error
}

func (p *progressProvider) GetForeignModifyBatchSize(context.Context, catalog.ForeignModifyRequest, any) (int, error) {
	return p.size, nil
}
func (p *progressProvider) unit() error {
	p.units++
	if p.units == 2 {
		return p.failure
	}
	return nil
}
func (p *progressProvider) ExecForeignInsert(context.Context, catalog.ForeignModifyRequest, any, catalog.ForeignRow) (catalog.ForeignRow, bool, error) {
	if err := p.unit(); err != nil {
		return nil, false, err
	}
	p.ordinal++
	return catalog.ForeignRow{}, p.ordinal != 2, nil
}
func (p *progressProvider) ExecForeignBatchInsert(_ context.Context, _ catalog.ForeignModifyRequest, _ any, rows []catalog.ForeignRow) ([]catalog.ForeignRow, error) {
	p.batches = append(p.batches, len(rows))
	if err := p.unit(); err != nil {
		return nil, err
	}
	var out []catalog.ForeignRow
	for range rows {
		p.ordinal++
		if p.ordinal != 2 {
			out = append(out, catalog.ForeignRow{})
		}
	}
	return out, nil
}

type progressRuntime struct{ WriteRuntime }

func (progressRuntime) Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment) {
	return sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}
}

func TestCopyProgressForeignAcceptedRows(t *testing.T) {
	for _, size := range []int{1, 2} {
		for _, fail := range []bool{false, true} {
			failure := errors.New("provider insertion failed")
			provider := &progressProvider{size: size}
			if fail {
				provider.failure = failure
			}
			modification := &Modification{Source: &exec.ForeignQuerySource{Wrapper: provider}}
			accepted := 0
			count, err := modification.InsertRows(context.Background(), progressRuntime{}, make([][]sqltype.Value, 4), false, func(row []sqltype.Value) error {
				if len(row) != 0 {
					t.Fatal("unexpected tuple width")
				}
				accepted++
				return nil
			})
			if fail {
				if !errors.Is(err, failure) || count != 0 || accepted != 1 {
					t.Fatalf("size%d failure count=%d accepted=%d err=%v", size, count, accepted, err)
				}
			} else if err != nil || count != 3 || accepted != 3 {
				t.Fatalf("size%d count=%d accepted=%d err=%v", size, count, accepted, err)
			}
			if size == 2 && !reflect.DeepEqual(provider.batches, []int{2, 2}) {
				t.Fatal("accept callback disabled batching", provider.batches)
			}
			if size == 1 && len(provider.batches) != 0 {
				t.Fatal("single-row path batched", provider.batches)
			}
		}
	}
}
