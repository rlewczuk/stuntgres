package storage

import "context"

// FreespaceLayout is an immutable compact live-population estimate. Index
// domains retain no entirely unused pages, so their LastAvail is zero too.
// Physical FSM trees, freshness/history and relation_size allocation are separate.
type FreespaceLayout struct {
	Blocks    int64
	LastAvail int64
}

func FreespacePayload(ctx context.Context, payload int64) (FreespaceLayout, error) {
	if err := ctx.Err(); err != nil {
		return FreespaceLayout{}, err
	}
	if _, err := AddSize(payload, 0); err != nil {
		return FreespaceLayout{}, err
	}
	if payload == 0 {
		return FreespaceLayout{}, nil
	}
	blocks := payload / 8168
	used := payload % 8168
	if used != 0 {
		blocks++
	} else {
		used = 8168
	}
	return FreespaceLayout{Blocks: blocks, LastAvail: (8168 - used) / 32 * 32}, nil
}
func (f FreespaceLayout) Probe(block int64) int64 {
	if block < 0 || block >= f.Blocks || block != f.Blocks-1 {
		return 0
	}
	return f.LastAvail
}
