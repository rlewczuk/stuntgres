package storage

import (
	"context"
	"fmt"
	"sort"

	"stuntgres/internal/sqltype"
)

// ExtendedMCV is the immutable retained construction fact. Dictionaries contain
// only non-NULL prepared values referenced by Items, in first retained order.
// Its slices are owned by the enclosing ExtendedStatistics publication.
type ExtendedMCV struct {
	Rows         uint64
	Dictionaries [8][]sqltype.Value
	Items        []ExtendedMCVItem
}

type ExtendedMCVItem struct {
	Nulls          [8]bool
	Indexes        [8]uint16
	Count          uint64
	MarginalCounts [8]uint64
}

type mcvMarginal struct {
	value sqltype.Value
	count uint64
	next  int
}

type mcvJoint struct {
	groups [8]int
	count  uint64
	next   int
}

// MCVCollector shares the prepared row with the other extended collectors.
// Integer marginal IDs avoid copying full tuples into joint scratch storage.
type MCVCollector struct {
	input           ExtendedInput
	grouping        [8]sqltype.Grouping
	marginalBuckets [8]map[uint64]int
	marginals       [8][]mcvMarginal
	buckets         map[uint64]int
	joints          []mcvJoint
	rows            uint64
}

func newMCVCollector(input ExtendedInput, grouping [8]sqltype.Grouping) (*MCVCollector, error) {
	if input.MCVType == nil || input.MCVTarget < 1 || input.MCVTarget > 10000 {
		return nil, fmt.Errorf("invalid MCV collection layout")
	}
	c := &MCVCollector{input: input, grouping: grouping, buckets: make(map[uint64]int)}
	for i := range input.Columns {
		c.marginalBuckets[i] = make(map[uint64]int)
	}
	return c, nil
}

func (c *MCVCollector) addPrepared(ctx context.Context, keys *[8]sqltype.Value, hashes *[8]uint64) error {
	var groups [8]int
	hash := uint64(14695981039346656037)
	for i := range c.input.Columns {
		if err := ctx.Err(); err != nil {
			return err
		}
		found := -1
		for link := c.marginalBuckets[i][hashes[i]]; link != 0; link = c.marginals[i][link-1].next {
			if err := ctx.Err(); err != nil {
				return err
			}
			same, err := c.grouping[i].Equal(ctx, c.marginals[i][link-1].value, keys[i])
			if err != nil {
				return err
			}
			if same {
				found = link - 1
				break
			}
		}
		if found < 0 {
			found = len(c.marginals[i])
			c.marginals[i] = append(c.marginals[i], mcvMarginal{value: keys[i], next: c.marginalBuckets[i][hashes[i]]})
			c.marginalBuckets[i][hashes[i]] = found + 1
		}
		c.marginals[i][found].count++
		groups[i] = found
		hash = (hash ^ hashes[i]) * 1099511628211
	}
	for link := c.buckets[hash]; link != 0; link = c.joints[link-1].next {
		if err := ctx.Err(); err != nil {
			return err
		}
		joint := &c.joints[link-1]
		equal := true
		for i := range c.input.Columns {
			same, err := c.grouping[i].Equal(ctx, c.marginals[i][joint.groups[i]].value, keys[i])
			if err != nil {
				return err
			}
			if !same {
				equal = false
				break
			}
		}
		if equal {
			joint.count++
			c.rows++
			return ctx.Err()
		}
	}
	c.joints = append(c.joints, mcvJoint{groups: groups, count: 1, next: c.buckets[hash]})
	c.buckets[hash] = len(c.joints)
	c.rows++
	return ctx.Err()
}

func (c *MCVCollector) finish(ctx context.Context) (*ExtendedMCV, sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, sqltype.Value{}, err
	}
	if c.rows == 0 {
		return nil, sqltype.Value{}, nil
	}
	order := make([]int, len(c.joints))
	for i := range order {
		if err := ctx.Err(); err != nil {
			return nil, sqltype.Value{}, err
		}
		order[i] = i
	}
	var canceled error
	sort.Slice(order, func(i, j int) bool {
		if canceled == nil {
			canceled = ctx.Err()
		}
		a, b := order[i], order[j]
		if c.joints[a].count != c.joints[b].count {
			return c.joints[a].count > c.joints[b].count
		}
		return a < b
	})
	if canceled != nil {
		return nil, sqltype.Value{}, canceled
	}
	if len(order) > c.input.MCVTarget {
		order = order[:c.input.MCVTarget]
	}
	data := &ExtendedMCV{Rows: c.rows, Items: make([]ExtendedMCVItem, len(order))}
	var indexes [8]map[int]uint16
	for i := range c.input.Columns {
		indexes[i] = make(map[int]uint16)
	}
	for n, ordinal := range order {
		if err := ctx.Err(); err != nil {
			return nil, sqltype.Value{}, err
		}
		joint := c.joints[ordinal]
		item := &data.Items[n]
		item.Count = joint.count
		for i := range c.input.Columns {
			if err := ctx.Err(); err != nil {
				return nil, sqltype.Value{}, err
			}
			id := joint.groups[i]
			marginal := c.marginals[i][id]
			item.MarginalCounts[i] = marginal.count
			item.Nulls[i] = marginal.value.IsNull()
			if item.Nulls[i] {
				continue
			}
			index, exists := indexes[i][id]
			if !exists {
				index = uint16(len(data.Dictionaries[i]))
				indexes[i][id] = index
				data.Dictionaries[i] = append(data.Dictionaries[i], marginal.value)
			}
			item.Indexes[i] = index
		}
	}
	usages := make([]sqltype.Usage, len(c.input.Columns))
	for i, column := range c.input.Columns {
		usages[i] = column.Usage
	}
	value, err := BuildExtendedMCV(ctx, c.input.MCVType, usages, data)
	if err != nil {
		return nil, sqltype.Value{}, err
	}
	return data, value, nil
}

// BuildExtendedMCV derives native frequencies from retained integer counts.
// Snapshot reconstruction uses this same adapter under destination descriptors.
func BuildExtendedMCV(ctx context.Context, descriptor *sqltype.Descriptor, usages []sqltype.Usage, data *ExtendedMCV) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if data == nil || data.Rows == 0 || len(usages) < 1 || len(usages) > 8 || len(data.Items) < 1 || len(data.Items) > 10000 {
		return sqltype.Value{}, fmt.Errorf("invalid MCV construction fact")
	}
	items := make([]sqltype.StatisticsMCVItem, len(data.Items))
	for i, item := range data.Items {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		if item.Count == 0 || item.Count > data.Rows {
			return sqltype.Value{}, fmt.Errorf("invalid MCV count")
		}
		baseline := 1.0
		for j := range usages {
			if item.MarginalCounts[j] < item.Count || item.MarginalCounts[j] > data.Rows {
				return sqltype.Value{}, fmt.Errorf("invalid MCV marginal count")
			}
			baseline *= float64(item.MarginalCounts[j]) / float64(data.Rows)
		}
		items[i] = sqltype.StatisticsMCVItem{Nulls: item.Nulls, Indexes: item.Indexes, Frequency: float64(item.Count) / float64(data.Rows), BaseFrequency: baseline}
	}
	return sqltype.StatisticsMCVBuild(ctx, descriptor, usages, data.Dictionaries, items)
}
