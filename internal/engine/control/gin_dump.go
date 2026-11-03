package control

import (
	"context"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
)

func (c *dumpCollection) captureGin(ctx context.Context) error {
	for _, descriptor := range c.OriginalCatalog.Descriptors() {
		target, ok := descriptor.(*catalog.Relation)
		if !ok || target.SQLIndex == nil || target.SQLIndex.Method != catalog.GinMethod || temporaryDumpObject(c.source.State, target) {
			continue
		}
		record, err := index.CaptureGinSplit(ctx, c.OriginalCatalog, target, c.source.IndexRoots[target.Address()], c.source.Relations[target.SQLIndex.Parent], c.source.Frontier)
		if err != nil {
			return err
		}
		c.GinIndexes = append(c.GinIndexes, record)
		if table, ok := c.TableIndices[target.SQLIndex.Parent.ObjectID]; ok {
			c.Tables[table].GinReplay = true
		}
	}
	slices.SortFunc(c.GinIndexes, func(a, b index.GinDumpIndex) int {
		if order := strings.Compare(a.Schema, b.Schema); order != 0 {
			return order
		}
		return strings.Compare(a.Name, b.Name)
	})
	return ctx.Err()
}
func emitDumpGin(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	for _, record := range s.GinIndexes {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := index.EncodeGinIndex(record)
		if err != nil {
			return err
		}
		if err := w.emit(line); err != nil {
			return err
		}
	}
	return ctx.Err()
}
