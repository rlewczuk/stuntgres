package enginetests

import (
	"context"
	"encoding/binary"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

// Later tuples/segments are genuinely undecoded until their selected row. This
// guards against eager materialization followed by empty body-entry receipts.
func TestPageinspectLazyRows(t *testing.T) {
	t.Run("btree", func(t *testing.T) {
		raw := btUnitPage()
		binary.LittleEndian.PutUint16(raw[12:], 32)
		binary.LittleEndian.PutUint32(raw[28:], 32767|1<<15)
		saved := string(raw)
		page := btUnitRead(t, raw)
		count, err := page.ItemCount()
		if err != nil || count != 2 {
			t.Fatal(count, err)
		}
		item, err := page.Item(context.Background(), 0)
		if err != nil || item.Offset != 1 || item.Length != 16 {
			t.Fatal("first real tuple", item, err)
		}
		if _, err = page.Item(context.Background(), 1); err == nil {
			t.Fatal("later corrupt tuple was not decoded")
		}
		if string(raw) != saved {
			t.Fatal("row reader changed caller bytes")
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err = page.Item(canceled, 0); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	t.Run("hash", func(t *testing.T) {
		raw := hashUnitItems()
		binary.LittleEndian.PutUint32(raw[28:], 32767|1<<15)
		page := hashUnitRead(t, string(raw), sqltype.HashBucketPage)
		count, err := page.ItemCount()
		if err != nil || count != 2 {
			t.Fatal(count, err)
		}
		item, err := page.Item(context.Background(), 0)
		if err != nil || item.Offset != 1 || item.Data != 4294967295 {
			t.Fatal(item, err)
		}
		if _, err = page.Item(context.Background(), 1); err == nil {
			t.Fatal("later corrupt hash tuple not decoded")
		}
	})
	t.Run("gin", func(t *testing.T) {
		raw := ginUnitLeaf()
		binary.LittleEndian.PutUint16(raw[12:], 48)
		binary.LittleEndian.PutUint16(raw[46:], 9)
		page := ginUnitRead(t, string(raw), sqltype.GinLeafPage)
		first, next, present, err := page.LeafItem(context.Background(), 32)
		if err != nil || !present || next != 40 || first.First.Block != 8 || first.First.Offset != 41 || len(first.TIDs) != 1 {
			t.Fatal(first, next, present, err)
		}
		if _, _, _, err = page.LeafItem(context.Background(), next); err == nil {
			t.Fatal("later corrupt compressed segment not decoded")
		}
	})
}
