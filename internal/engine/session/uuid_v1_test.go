package session

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

func uuidTimeTuple(b [16]byte) (uint64, uint16, [6]byte) {
	ticks := uint64(binary.BigEndian.Uint32(b[:4])) | uint64(binary.BigEndian.Uint16(b[4:6]))<<32 | uint64(binary.BigEndian.Uint16(b[6:8])&4095)<<48
	var node [6]byte
	copy(node[:], b[10:])
	return ticks, uint16(b[8]&63)<<8 | uint16(b[9]), node
}
func uuidTimeSources() (func() time.Time, func([]byte) error) {
	return func() time.Time { return time.Unix(1700000000, 123456000) }, func(p []byte) error {
		for i := range p {
			p[i] = byte(i + 4)
		}
		return nil
	}
}
func TestUUIDOsspTimePacking(t *testing.T) {
	b := uuidV1Pack(0x0123456789abcdef, 0x1234, [6]byte{1, 0x23, 0x45, 0x67, 0x89, 0xab})
	if hex.EncodeToString(b[:]) != "89abcdef4567112392340123456789ab" {
		t.Fatal(b)
	}
	for _, ticks := range []uint64{0, 1<<32 - 1, 1 << 32, 1<<48 - 1, 1 << 48, 1<<60 - 1} {
		b := uuidV1Pack(ticks, 0xffff, [6]byte{})
		got, seq, _ := uuidTimeTuple(b)
		if got != ticks || seq != 16383 || b[6]>>4 != 1 || b[8]&0xc0 != 0x80 {
			t.Fatal(ticks, b)
		}
	}
	for _, tc := range []struct {
		at   time.Time
		want uint64
	}{{time.Unix(0, 0), 122192928000000000}, {time.Unix(1700000000, 123456000), 139192928001234560}} {
		got, err := uuidV1Bucket(tc.at)
		if err != nil || got != tc.want {
			t.Fatal(got, tc.want, err)
		}
	}
}
func TestUUIDOsspTimeClock(t *testing.T) {
	var a uuidV1Allocator
	now, entropy := uuidTimeSources()
	at := now()
	now = func() time.Time { return at }
	ctx := context.Background()
	var first [6]byte
	for i := range 10 {
		b, err := a.generate(ctx, false, now, entropy, nil)
		ticks, _, node := uuidTimeTuple(b)
		if err != nil || ticks != 139192928001234560+uint64(i) {
			t.Fatal(i, b, err)
		}
		if i == 0 {
			first = node
		}
		if node != first {
			t.Fatal(node, first)
		}
	}
	waited := false
	b, err := a.generate(ctx, false, now, entropy, func(context.Context) error {
		waited = true
		if !a.mu.TryLock() {
			t.Fatal("wait holds allocator lock")
		}
		a.mu.Unlock()
		if a.state.slot != 9 {
			t.Fatal("waiting committed allocation", a.state)
		}
		at = time.Unix(1700000000, 123457000)
		peer, err := a.generate(ctx, false, now, entropy, nil)
		ticks, _, _ := uuidTimeTuple(peer)
		if err != nil || ticks != 139192928001234570 {
			t.Fatal(peer, err)
		}
		return nil
	})
	ticks, _, _ := uuidTimeTuple(b)
	if err != nil || !waited || ticks != 139192928001234571 {
		t.Fatal(ticks, waited, err)
	}
	old := a.state
	_, err = a.generate(ctx, false, func() time.Time { return time.Unix(-12219292801, 0) }, entropy, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22008", Message: "timestamp out of range for UUID version 1"}) || a.state != old {
		t.Fatal(err, a.state)
	}
}
func TestUUIDOsspTimeSequence(t *testing.T) {
	var a uuidV1Allocator
	now, _ := uuidTimeSources()
	ctx := context.Background()
	draw := 0
	entropy := func(p []byte) error {
		draw++
		for i := range p {
			p[i] = byte(draw*16 + i)
		}
		if len(p) == 8 {
			p[6] = 0x3f
			p[7] = 0xfe
		}
		return nil
	}
	b, err := a.generate(ctx, false, now, entropy, nil)
	_, seq, node := uuidTimeTuple(b)
	if err != nil || seq != 0x3ffe || node != [6]byte{19, 17, 18, 19, 20, 21} {
		t.Fatal(b, err)
	}
	backwards := func() time.Time { return now().Add(-time.Microsecond) }
	b, err = a.generate(ctx, false, backwards, entropy, nil)
	_, seq, _ = uuidTimeTuple(b)
	if err != nil || seq != 0x3fff {
		t.Fatal(b, err)
	}
	old := a.state
	fail := errors.New("entropy")
	earlier := func() time.Time { return now().Add(-2 * time.Microsecond) }
	if _, err = a.generate(ctx, false, earlier, func([]byte) error { return fail }, nil); err == nil || a.state != old {
		t.Fatal(err, a.state)
	}
	b, err = a.generate(ctx, false, earlier, entropy, nil)
	_, seq, next := uuidTimeTuple(b)
	if err != nil || seq != 0 || next == node || next[0]&3 != 3 {
		t.Fatal(b, err)
	}
	b, err = a.generate(ctx, false, earlier, entropy, nil)
	_, _, stable := uuidTimeTuple(b)
	if err != nil || stable != next {
		t.Fatal(b, err)
	}
}
func TestUUIDOsspTimeEntropy(t *testing.T) {
	var a uuidV1Allocator
	now, entropy := uuidTimeSources()
	ctx := context.Background()
	fail := errors.New("partial")
	if _, err := a.generate(ctx, false, now, func(p []byte) error { p[0] = 99; return fail }, nil); err == nil || a.state.initialized {
		t.Fatal(err, a.state)
	}
	b, err := a.generate(ctx, false, now, entropy, nil)
	_, seq, node := uuidTimeTuple(b)
	if err != nil || seq != 0x0a0b || node != [6]byte{7, 5, 6, 7, 8, 9} {
		t.Fatal(b, err)
	}
	old := a.state
	_, err = a.generate(ctx, true, now, func([]byte) error { return fail }, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: "could not generate random values"}) || a.state != old {
		t.Fatal(err, a.state)
	}
	b, err = a.generate(ctx, true, now, func(p []byte) error {
		if len(p) != 6 {
			t.Fatal(len(p))
		}
		for i := range p {
			p[i] = 0xff
		}
		return nil
	}, nil)
	ticks, _, mc := uuidTimeTuple(b)
	if err != nil || ticks != old.bucket+1 || mc != [6]byte{255, 255, 255, 255, 255, 255} || a.state.node != node {
		t.Fatal(b, err, a.state)
	}
	if _, err = a.generate(ctx, false, now, func([]byte) error { t.Fatal("initialized v1 demanded entropy"); return nil }, nil); err != nil {
		t.Fatal(err)
	}
	var fresh uuidV1Allocator
	draws := 0
	_, err = fresh.generate(ctx, true, now, func(p []byte) error {
		draws++
		if draws == 2 {
			return fail
		}
		return entropy(p)
	}, nil)
	if err == nil || fresh.state.initialized || draws != 2 {
		t.Fatal("initial mc failure committed seed", err, fresh.state, draws)
	}
	if _, err = fresh.generate(ctx, false, now, entropy, nil); err != nil || !fresh.state.initialized {
		t.Fatal(err, fresh.state)
	}
}
func TestUUIDOsspTimeCancellation(t *testing.T) {
	var a uuidV1Allocator
	now, entropy := uuidTimeSources()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.generate(ctx, false, func() time.Time { t.Fatal("precancel clock"); return time.Time{} }, func([]byte) error { t.Fatal("precancel entropy"); return nil }, nil); !errors.Is(err, context.Canceled) || a.state.initialized {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	if _, err := a.generate(ctx, false, now, func(p []byte) error { cancel(); return entropy(p) }, nil); !errors.Is(err, context.Canceled) || a.state.initialized {
		t.Fatal(err, a.state)
	}
	for range 10 {
		if _, err := a.generate(context.Background(), false, now, entropy, nil); err != nil {
			t.Fatal(err)
		}
	}
	old := a.state
	ctx, cancel = context.WithCancel(context.Background())
	_, err := a.generate(ctx, false, now, entropy, func(ctx context.Context) error {
		if !a.mu.TryLock() {
			t.Fatal("locked wait")
		}
		a.mu.Unlock()
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || a.state != old {
		t.Fatal(err, a.state)
	}
	// A frozen waiter remains parked until cancellation, while a peer can make
	// forward progress on the same allocator. Join it even on assertion failure.
	waitCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	entered := make(chan struct{})
	done := make(chan struct{})
	var waitErr error
	go func() {
		_, waitErr = a.generate(waitCtx, false, now, entropy, func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() })
		close(done)
	}()
	t.Cleanup(func() { stop(); <-done })
	select {
	case <-entered:
	case <-waitCtx.Done():
		t.Fatal(waitCtx.Err())
	}
	forward := func() time.Time { return now().Add(time.Microsecond) }
	if _, err := a.generate(context.Background(), false, forward, entropy, nil); err != nil {
		t.Fatal("peer blocked by waiter", err)
	}
	peerState := a.state
	stop()
	<-done
	if !errors.Is(waitErr, context.Canceled) || a.state != peerState {
		t.Fatal(waitErr, a.state)
	}
	ctx, cancel = context.WithCancel(context.Background())
	_, err = a.generate(ctx, true, forward, func(p []byte) error { cancel(); return entropy(p) }, nil)
	if !errors.Is(err, context.Canceled) || a.state != peerState {
		t.Fatal("precommit cancellation", err, a.state)
	}
}
func TestUUIDOsspTimeConcurrent(t *testing.T) {
	var a uuidV1Allocator
	_, entropy := uuidTimeSources()
	var clock atomic.Int64
	now := func() time.Time { return time.Unix(1700000000, clock.Load()) }
	wait := func(ctx context.Context) error { clock.Add(1000); return ctx.Err() }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const workers = 8
	const draws = 64
	start := make(chan struct{})
	values := make(chan [16]byte, workers*draws)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := range draws {
				b, err := a.generate(ctx, i%2 == 0, now, entropy, wait)
				if err != nil {
					errs <- err
					return
				}
				ticks, _, _ := uuidTimeTuple(b)
				if ticks > 139192928000000000+uint64(clock.Load()/1000)*10+9 {
					errs <- errors.New("future clock drift")
					return
				}
				values <- b
			}
		}()
	}
	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		cancel()
		<-done
		t.Fatal(ctx.Err())
	}
	close(values)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[[16]byte]bool{}
	for b := range values {
		if seen[b] {
			t.Fatal("duplicate", b)
		}
		seen[b] = true
	}
	if len(seen) != workers*draws {
		t.Fatal(len(seen))
	}
}
