// Package largeobject owns immutable, sparse large-object byte values.
package largeobject

import (
	"fmt"
	"maps"
	"strings"
	"stuntgres/internal/sqlstate"
)

const PageSize int64 = 2048
const MaximumSize int64 = 2147483647 * PageSize
const MaximumRead int64 = 1073741819

// Value's pages are private: published values and their pages never change.
// A short page or absent page below size denotes zero bytes, not EOF.
type Value struct {
	size  int64
	pages map[int64][]byte
}

func failure(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func Seek(offset int64) error {
	if offset < 0 || offset > MaximumSize {
		return failure("22023", fmt.Sprintf("invalid large object seek target: %d", offset))
	}
	return nil
}

func (v Value) Validate() error {
	if v.size < 0 || v.size > MaximumSize {
		return fmt.Errorf("stuntgres: invalid large object size")
	}
	var end int64
	for page, data := range v.pages {
		if page < 0 || page >= MaximumSize/PageSize || len(data) == 0 || int64(len(data)) > PageSize || page*PageSize+int64(len(data)) > v.size {
			return fmt.Errorf("stuntgres: invalid large object page")
		}
		end = max(end, page*PageSize+int64(len(data)))
	}
	if end != v.size {
		return fmt.Errorf("stuntgres: large object EOF has no stored tail")
	}
	return nil
}

func (v Value) Write(offset int64, data string) (Value, error) {
	if err := Seek(offset); err != nil {
		return Value{}, err
	}
	if int64(len(data)) > MaximumSize-offset {
		return Value{}, failure("22023", fmt.Sprintf("invalid large object write request size: %d", len(data)))
	}
	if len(data) == 0 {
		return v, nil
	}
	n := Value{size: max(v.size, offset+int64(len(data))), pages: maps.Clone(v.pages)}
	if n.pages == nil {
		n.pages = make(map[int64][]byte)
	}
	for len(data) > 0 {
		page, within := offset/PageSize, offset%PageSize
		count := min(int64(len(data)), PageSize-within)
		old := n.pages[page]
		chunk := make([]byte, max(int64(len(old)), within+count))
		copy(chunk[:within], old[:min(int64(len(old)), within)])
		if end := within + count; end < int64(len(old)) {
			copy(chunk[end:], old[end:])
		}
		copy(chunk[within:], data[:count])
		n.pages[page] = chunk
		data = data[count:]
		offset += count
	}
	return n, nil
}

// Read follows the released allocation-before-seek validation order. A negative
// length means whole-object read; the fragment wrapper rejects it separately.
func (v Value) Read(offset int64, length int64) (string, error) {
	var count int64
	if v.size > offset {
		// Negative seeks are rejected after sizing, without overflowing the
		// subtraction for MinInt64 or allocating an oversized bytea.
		if offset < 0 && v.size > MaximumRead+offset {
			if length < 0 || length > MaximumRead {
				return "", failure("54000", "large object read request is too large")
			}
			count = length
		} else {
			count = v.size - offset
			if length >= 0 {
				count = min(count, length)
			}
		}
	}
	if count > MaximumRead {
		return "", failure("54000", "large object read request is too large")
	}
	if err := Seek(offset); err != nil {
		return "", err
	}
	var out strings.Builder
	out.Grow(int(count))
	var zero [PageSize]byte
	for position := int64(0); position < count; {
		page, within := (offset+position)/PageSize, (offset+position)%PageSize
		n := min(count-position, PageSize-within)
		written := int64(0)
		if data := v.pages[page]; within < int64(len(data)) {
			tail := data[within:min(int64(len(data)), within+n)]
			out.Write(tail)
			written = int64(len(tail))
		}
		out.Write(zero[:n-written])
		position += n
	}
	return out.String(), nil
}

// Chunks yields immutable stored pages, never holes. Consumers must not modify
// the borrowed slices. No EOF-sized buffer or duplicate payload is built.
func (v Value) Chunks(yield func(offset int64, data []byte) bool) {
	for page, data := range v.pages {
		if !yield(page*PageSize, data) {
			return
		}
	}
}
