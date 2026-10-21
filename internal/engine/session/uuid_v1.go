package session

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"

	"stuntgres/internal/sqlstate"
)

const uuidV1Epoch int64 = 122192928000000000
const uuidV1Max uint64 = 1<<60 - 1

// The physical session owns both the fallback node and its allocation authority.
// This state is nontransactional and never serialized or replaced with catalogs.
// Only this private mutex protects it; clock-progress waits never hold the mutex.
// Separate random node epochs/sessions provide probabilistic, not durable, uniqueness.
type uuidV1Allocator struct {
	mu    sync.Mutex
	state uuidV1State
	// Reusable entropy staging, protected by mu; never part of committed state.
	scratch [8]byte
}
type uuidV1State struct {
	initialized bool
	node        [6]byte
	sequence    uint16
	bucket      uint64
	slot        uint8
}

// UUIDV1 allocates a v1 UUID using either the stable random fallback node or a
// fresh multicast node. Neither path discovers a host MAC or uses an OS UUID ABI.
func (s *Session) UUIDV1(ctx context.Context, multicast bool) ([16]byte, error) {
	return s.uuidTime.generate(ctx, multicast, time.Now, func(p []byte) error { _, err := rand.Read(p); return err }, uuidV1Wait)
}
func uuidV1Wait(ctx context.Context) error {
	timer := time.NewTimer(time.Microsecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
func uuidV1Bucket(at time.Time) (uint64, error) {
	// Unix seconds remain valid beyond UnixNano's limited year range. Check before
	// multiplication/addition, including the partially representable final second.
	seconds := at.Unix()
	if seconds < -12219292800 || seconds > int64(uuidV1Max/10000000)-12219292800 {
		return 0, uuidV1Error("22008", "timestamp out of range for UUID version 1")
	}
	ticks := uint64(seconds+12219292800)*10000000 + uint64(at.Nanosecond()/1000)*10
	if ticks > uuidV1Max {
		return 0, uuidV1Error("22008", "timestamp out of range for UUID version 1")
	}
	return ticks, nil
}
func uuidV1Pack(ticks uint64, sequence uint16, node [6]byte) [16]byte {
	var b [16]byte
	binary.BigEndian.PutUint32(b[:4], uint32(ticks))
	binary.BigEndian.PutUint16(b[4:6], uint16(ticks>>32))
	binary.BigEndian.PutUint16(b[6:8], uint16(ticks>>48)&0xfff|0x1000)
	binary.BigEndian.PutUint16(b[8:10], sequence&0x3fff|0x8000)
	copy(b[10:], node[:])
	return b
}
func uuidV1Entropy(ctx context.Context, entropy func([]byte) error, p []byte) error {
	err := entropy(p)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return uuidV1Error("XX000", "could not generate random values")
	}
	return nil
}
func uuidV1Error(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func (a *uuidV1Allocator) generate(ctx context.Context, multicast bool, now func() time.Time, entropy func([]byte) error, wait func(context.Context) error) ([16]byte, error) {
	var zero [16]byte
	for {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		a.mu.Lock()
		if err := ctx.Err(); err != nil {
			a.mu.Unlock()
			return zero, err
		}
		bucket, err := uuidV1Bucket(now())
		if err != nil {
			a.mu.Unlock()
			if canceled := ctx.Err(); canceled != nil {
				return zero, canceled
			}
			return zero, err
		}
		next := a.state
		if next.initialized && bucket == next.bucket && next.slot == 9 {
			a.mu.Unlock()
			if wait == nil {
				wait = uuidV1Wait
			}
			if err := wait(ctx); err != nil {
				return zero, err
			}
			continue // Recapture clock and state: peers may allocate while unlocked.
		}
		if !next.initialized {
			if err := uuidV1Entropy(ctx, entropy, a.scratch[:]); err != nil {
				a.mu.Unlock()
				return zero, err
			}
			copy(next.node[:], a.scratch[:6])
			next.node[0] |= 3
			next.sequence = binary.BigEndian.Uint16(a.scratch[6:]) & 0x3fff
			next.initialized = true
			next.slot = 0
		} else if bucket < next.bucket {
			next.sequence = (next.sequence + 1) & 0x3fff
			if next.sequence == 0 {
				if err := uuidV1Entropy(ctx, entropy, a.scratch[:6]); err != nil {
					a.mu.Unlock()
					return zero, err
				}
				copy(next.node[:], a.scratch[:6])
				next.node[0] |= 3
			}
			next.slot = 0
		} else if bucket == next.bucket {
			next.slot++
		} else {
			next.slot = 0
		}
		node := next.node
		if multicast {
			if err := uuidV1Entropy(ctx, entropy, a.scratch[:6]); err != nil {
				a.mu.Unlock()
				return zero, err
			}
			copy(node[:], a.scratch[:6])
			node[0] |= 3
		}
		if bucket+uint64(next.slot) > uuidV1Max {
			a.mu.Unlock()
			return zero, uuidV1Error("22008", "timestamp out of range for UUID version 1")
		}
		if err := ctx.Err(); err != nil {
			a.mu.Unlock()
			return zero, err
		}
		next.bucket = bucket
		value := uuidV1Pack(bucket+uint64(next.slot), next.sequence, node)
		a.state = next
		a.mu.Unlock()
		return value, nil
	}
}
