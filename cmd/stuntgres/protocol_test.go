package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestServerProtocolFraming(t *testing.T) {
	t.Run("unknown tag before length", func(t *testing.T) {
		conn := framingInput([]byte{'?', 1, 2, 3, 4})
		w := newWire(conn)
		_, err := w.readMessage(nil)
		assertFramingState(t, err, "08P01")
		// bufio may read ahead, but no body/length parsing or allocation occurs.
		if diagnostic(err).Message != "invalid frontend message type 63" {
			t.Fatal(err)
		}
	})
	t.Run("large announcement tiny fulfilled prefix", func(t *testing.T) {
		w := newWire(framingInput(framingPacket('Q', maxMessageLength, []byte("tiny"))))
		_, err := w.readMessage(nil)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(err)
		}
		// Direct reader exercise exposes capacity, independent of frame retirement.
		w = newWire(framingInput([]byte("tiny")))
		body, err := w.readBody(nil, 1<<29)
		if !errors.Is(err, io.ErrUnexpectedEOF) || cap(body) > 8192 || string(body) != "tiny" {
			t.Fatalf("body %q cap %d err %v", body, cap(body), err)
		}
	})
	t.Run("per tag limits", func(t *testing.T) {
		for _, tag := range []byte{'C', 'D', 'E', 'H', 'S', 'c', 'f', 'X'} {
			w := newWire(framingInput(framingPacket(tag, maxSmallMessageLength+1, nil)))
			if _, err := w.readMessage(nil); !errors.Is(err, errInvalidFrame) {
				t.Fatalf("%c: %v", tag, err)
			}
		}
		for _, tag := range []byte{'P', 'B', 'Q', 'F', 'd'} {
			w := newWire(framingInput(framingPacket(tag, maxSmallMessageLength+1, nil)))
			if _, err := w.readMessage(nil); !errors.Is(err, io.EOF) {
				t.Fatalf("%c: %v", tag, err)
			}
		}
	})
	t.Run("bounded primitives and full consumption", func(t *testing.T) {
		p := protocolInput{body: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 'x'}}
		n, err := p.Uint16()
		if err != nil || n != 65535 {
			t.Fatal(n, err)
		}
		size, err := p.Int32()
		if err != nil || size != -1 {
			t.Fatal(size, err)
		}
		if err := p.End(); diagnostic(err).Message != "invalid message format" {
			t.Fatal(err)
		}
		if _, err := p.Bytes(2); diagnostic(err).Message != "insufficient data left in message" {
			t.Fatal(err)
		}
		if _, err := p.Bytes(-1); err == nil {
			t.Fatal("negative byte count accepted")
		}
		if _, err := p.Byte(); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Byte(); diagnostic(err).Message != "no data left in message" {
			t.Fatal(err)
		}
	})
	t.Run("encoded fields preserve null empty and buffer ownership", func(t *testing.T) {
		conn := framingInput(nil)
		c := &client{wire: newWire(conn)}
		fields := []engine.ProtocolField{{Null: true}, {Bytes: []byte{}}, {Bytes: []byte{0, 255}}}
		buffer := make([]byte, 0, 64)
		original := buffer[:cap(buffer)]
		logical, transport := c.writeFields(fields, &buffer)
		if logical != nil || transport != nil {
			t.Fatal(logical, transport)
		}
		if &buffer[0] != &original[0] {
			t.Fatal("encoded row discarded sufficient buffer capacity")
		}
		// Borrowed engine fields can change after the synchronous frame write.
		fields[2].Bytes[0] = 42
		if err := c.wire.flush(); err != nil {
			t.Fatal(err)
		}
		wantBody := []byte{0, 3, 255, 255, 255, 255, 0, 0, 0, 0, 0, 0, 0, 2, 0, 255}
		if want := framingPacket('D', 20, wantBody); !bytes.Equal(conn.output.Bytes(), want) {
			t.Fatalf("encoded row %x want %x", conn.output.Bytes(), want)
		}
		failure := errors.New("socket failed")
		conn = framingInput(nil)
		conn.writeErr = failure
		c.wire = newWire(conn)
		logical, transport = c.writeFields([]engine.ProtocolField{{Bytes: make([]byte, 5000)}}, &buffer)
		if logical != nil || !errors.Is(transport, failure) {
			t.Fatalf("socket failure misclassified: %v / %v", logical, transport)
		}
	})
	t.Run("connection COPY hooks reuse actual stream state", func(t *testing.T) {
		logs := &serverLogs{ops: logHandler(io.Discard, false), sql: logHandler(io.Discard, false)}
		s, err := newServer(context.Background(), nil, nil, logs)
		if err != nil {
			t.Fatal(err)
		}
		defer s.cancel()
		conn := framingInput(nil)
		c, err := s.newClient(conn)
		if err != nil {
			t.Fatal(err)
		}
		defer c.finish(engine.CleanClose)
		p := &c.copyStream
		defer p.closeCommand()
		p.ctx = context.Background()
		if err := c.copyHooks.Start(engine.CopyProtocolMetadata{}); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Write([]byte("out")); err != nil {
			t.Fatal(err)
		}
		if err := p.complete(); err != nil {
			t.Fatal(err)
		}
		p.closeCommand()
		if err := c.wire.flush(); err != nil {
			t.Fatal(err)
		}
		want := framingPacket('H', 7, []byte{0, 0, 0})
		want = append(want, framingPacket('d', 7, []byte("out"))...)
		want = append(want, framingPacket('c', 4, nil)...)
		if !bytes.Equal(conn.output.Bytes(), want) {
			t.Fatalf("COPY output %x want %x", conn.output.Bytes(), want)
		}
		// Ordinary commands share the capability but must not repeat CopyDone.
		p.ctx = context.Background()
		if err := p.complete(); err != nil {
			t.Fatal(err)
		}
		p.closeCommand()
		if err := c.wire.flush(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(conn.output.Bytes(), want) {
			t.Fatal("inactive stream emitted another CopyDone")
		}
		for range 2 {
			p.ctx = context.Background()
			messages := make(chan frontendMessage, 2)
			messages <- frontendMessage{tag: 'd', body: []byte("next")}
			messages <- frontendMessage{tag: 'c'}
			c.messages = messages
			c.freeBuffers = make(chan []byte, 2)
			if err := c.copyHooks.Start(engine.CopyProtocolMetadata{From: true, Format: 1, ColumnFormats: []int16{1}}); err != nil {
				t.Fatal(err)
			}
			var data [8]byte
			n, err := p.Read(data[:])
			if err != nil || string(data[:n]) != "next" {
				t.Fatalf("reused input %q %v", data[:n], err)
			}
			if err := c.copyHooks.FinishInput(); err != nil {
				t.Fatal(err)
			}
			if err := p.complete(); err != nil {
				t.Fatal(err)
			}
			p.closeCommand()
			if p.ctx != nil || p.borrowed || p.current.body != nil {
				t.Fatal("completed stream retained operation/frame")
			}
			want = append(want, framingPacket('G', 9, []byte{1, 0, 1, 0, 1})...)
			if err := c.wire.flush(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(conn.output.Bytes(), want) {
				t.Fatalf("input completion %x want %x", conn.output.Bytes(), want)
			}
		}
		// Error cleanup must relinquish an only partially consumed frame.
		p.ctx = context.Background()
		messages := make(chan frontendMessage, 1)
		messages <- frontendMessage{tag: 'd', body: []byte("tail")}
		c.messages = messages
		c.freeBuffers = make(chan []byte, 2)
		if err := c.copyHooks.Start(engine.CopyProtocolMetadata{From: true}); err != nil {
			t.Fatal(err)
		}
		var data [1]byte
		if n, err := p.Read(data[:]); n != 1 || err != nil {
			t.Fatal(n, err)
		}
		p.closeCommand()
		p.closeCommand()
		if p.ctx != nil || p.borrowed || p.current.body != nil {
			t.Fatal("error cleanup pinned a borrowed frame")
		}
		select {
		case body := <-c.freeBuffers:
			if string(body) != "tail" {
				t.Fatalf("returned frame %q", body)
			}
		default:
			t.Fatal("borrowed frame not returned")
		}
		select {
		case <-c.freeBuffers:
			t.Fatal("borrowed frame returned twice")
		default:
		}
	})
}
func TestServerProtocolCancellation(t *testing.T) {
	s := &server{backends: make(map[uint32]*client)}
	c := &client{server: s, id: 2, keyLength: 32}
	for i := range c.key {
		c.key[i] = byte(i + 1)
	}
	s.backends[c.id] = c
	first, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	c.commandCancel = cancel
	for _, key := range [][]byte{nil, c.key[:4], append(append([]byte{}, c.key[:]...), 0), bytes.Repeat([]byte{1}, 32)} {
		s.cancelCommand(c.id, key)
		if first.Err() != nil {
			t.Fatal("wrong key canceled owner")
		}
	}
	s.cancelCommand(3, c.key[:])
	if first.Err() != nil {
		t.Fatal("wrong PID canceled owner")
	}
	s.cancelCommand(c.id, c.key[:])
	if context.Cause(first) != errUserCancel {
		t.Fatal(context.Cause(first))
	}
	c.commandCancel = nil
	s.cancelCommand(c.id, c.key[:])
	second, cancelSecond := context.WithCancelCause(context.Background())
	defer cancelSecond(nil)
	if second.Err() != nil {
		t.Fatal("idle cancel leaked")
	}
	c.commandCancel = cancelSecond
	c.key[31]++
	stale := append([]byte{}, c.key[:]...)
	stale[31]--
	s.cancelCommand(c.id, stale)
	if second.Err() != nil {
		t.Fatal("stale full key canceled")
	}
	s.cancelCommand(c.id, c.key[:])
	if context.Cause(second) != errUserCancel {
		t.Fatal("current command not canceled")
	}
	c.keyLength = 4
	third, cancelThird := context.WithCancelCause(context.Background())
	defer cancelThird(nil)
	c.commandCancel = cancelThird
	s.cancelCommand(c.id, c.key[:])
	if third.Err() != nil {
		t.Fatal("long key matched 3.0")
	}
	s.cancelCommand(c.id, c.key[:4])
	if context.Cause(third) != errUserCancel {
		t.Fatal("3.0 key not matched")
	}
	t.Run("typed cancellation diagnostics preserve owned context", func(t *testing.T) {
		original := *errUserCancel
		enriched := original
		enriched.Context = "COPY server_failed_identity, line 1: \"1\""
		conversion := &sqlstate.Error{Code: "22P02", Message: "conversion remains primary", Context: "owned conversion context"}
		terminal := &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "terminal session", Context: "owned fatal context"}
		foreign := original
		foreign.Context = "not the registered cause"
		for _, tc := range []struct {
			name                 string
			cause, input         error
			code, message, where string
			fatal                bool
		}{
			{"registered cause", errUserCancel, context.Canceled, "57014", original.Message, "", false},
			{"enriched primary with cleanup cancellation", errUserCancel, errors.Join(context.Canceled, &enriched), "57014", original.Message, enriched.Context, false},
			{"conversion before cancellation", errUserCancel, errors.Join(conversion, context.Canceled), "22P02", conversion.Message, conversion.Context, false},
			{"unknown plain cause", errors.New(original.Message), context.Canceled, "57014", context.Canceled.Error(), "", false},
			{"unknown typed cause identity", &foreign, context.Canceled, "57014", context.Canceled.Error(), "", false},
			{"terminal cause", terminal, context.Canceled, "57P01", terminal.Message, terminal.Context, true},
			{"later terminal primary after user cause", errUserCancel, errors.Join(context.Canceled, terminal), "57P01", terminal.Message, terminal.Context, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancelCause(context.Background())
				cancel(tc.cause)
				var log bytes.Buffer
				logs := &serverLogs{ops: logHandler(io.Discard, false), sql: logHandler(&log, false)}
				conn := framingInput(nil)
				c := &client{server: &server{logs: logs}, wire: newWire(conn)}
				err := c.writeContextError(ctx, engine.ProtocolContext{Source: "exact source"}, tc.input)
				if tc.fatal {
					var d *sqlstate.Error
					if !errors.As(err, &d) || d.Code != tc.code || !c.fatalSent {
						t.Fatalf("terminal disposition: %v / %v", err, c.fatalSent)
					}
				} else if err != nil || c.fatalSent {
					t.Fatalf("ordinary disposition: %v / %v", err, c.fatalSent)
				}
				if err := c.wire.flush(); err != nil {
					t.Fatal(err)
				}
				packet := conn.output.Bytes()
				if len(packet) < 5 || packet[0] != 'E' {
					t.Fatalf("error frame: %x", packet)
				}
				fields := framingFields(t, packet[5:])
				if fields['C'] != tc.code || fields['M'] != tc.message || fields['W'] != tc.where {
					t.Fatalf("diagnostic: %#v", fields)
				}
				severity := "ERROR"
				if tc.fatal {
					severity = "FATAL"
				}
				if fields['S'] != severity || fields['V'] != severity {
					t.Fatalf("severity: %#v", fields)
				}
				var record map[string]any
				if err := json.Unmarshal(log.Bytes(), &record); err != nil {
					t.Fatal(err)
				}
				if record["error"] != tc.code || record["errmsg"] != tc.message || record["query"] != "exact source" {
					t.Fatal(record)
				}
				if *errUserCancel != original {
					t.Fatal("shared cancellation cause was mutated")
				}
			})
		}
	})
	t.Run("copy frame bytes defer only registered user cancel", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			cause   error
			packets [][]byte
			want    string
		}{
			{"split frame bytes", errUserCancel, [][]byte{[]byte("1"), []byte("\n")}, "1\n"},
			{"conversion belongs to engine", errUserCancel, [][]byte{[]byte("bad\n")}, "bad\n"},
			{"arbitrary bytes remain opaque", errUserCancel, [][]byte{{255, 0}}, "\xff\x00"},
			{"unknown cause", errors.New(errUserCancel.Message), [][]byte{[]byte("1\n")}, ""},
			{"fatal operation", &sqlstate.Error{Severity: "FATAL", Code: "57P01", Message: "terminal"}, [][]byte{[]byte("1\n")}, ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancelCause(context.Background())
				cancel(tc.cause)
				messages := make(chan frontendMessage, 2)
				for _, body := range tc.packets {
					messages <- frontendMessage{tag: 'd', body: body}
				}
				close(messages)
				c := &client{ctx: context.Background(), messages: messages, freeBuffers: make(chan []byte, 2)}
				p := protocolCopy{client: c, ctx: ctx}
				defer p.release()
				var got []byte
				var scratch [2]byte
				if tc.want == "" {
					n, err := p.Read(scratch[:])
					if n != 0 || !errors.Is(err, context.Canceled) || len(messages) != len(tc.packets) {
						t.Fatalf("non-user cancel consumed frame: %d %v queued %d", n, err, len(messages))
					}
					return
				}
				for len(got) < len(tc.want) {
					n, err := p.Read(scratch[:])
					got = append(got, scratch[:n]...)
					if err != nil {
						t.Fatal(err)
					}
				}
				if string(got) != tc.want || context.Cause(ctx) != tc.cause {
					t.Fatalf("bytes %q cause %v", got, context.Cause(ctx))
				}
			})
		}
	})
	t.Run("physical close interrupts borrowed copy data", func(t *testing.T) {
		physical, closePhysical := context.WithCancel(context.Background())
		defer closePhysical()
		ctx, cancel := context.WithCancelCause(physical)
		cancel(errUserCancel)
		messages := make(chan frontendMessage, 1)
		messages <- frontendMessage{tag: 'd', body: []byte("1\n")}
		c := &client{ctx: physical, messages: messages, freeBuffers: make(chan []byte, 2)}
		p := protocolCopy{client: c, ctx: ctx}
		defer p.release()
		var scratch [1]byte
		if n, err := p.Read(scratch[:]); n != 1 || err != nil {
			t.Fatal(n, err)
		}
		closePhysical()
		if n, err := p.Read(scratch[:]); n != 0 || !errors.Is(err, context.Canceled) {
			t.Fatalf("physical close ignored: %d %v", n, err)
		}
	})
	t.Run("copy logical cancel wakes then awaits physical frame", func(t *testing.T) {
		physical, closePhysical := context.WithCancel(context.Background())
		ctx, cancel := context.WithCancelCause(physical)
		messages := make(chan frontendMessage)
		c := &client{ctx: physical, messages: messages, freeBuffers: make(chan []byte, 2)}
		p := protocolCopy{client: c, ctx: ctx}
		type outcome struct {
			data string
			err  error
		}
		result := make(chan outcome, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer p.release()
			var data [2]byte
			n, err := p.Read(data[:])
			result <- outcome{string(data[:n]), err}
		}()
		t.Cleanup(func() {
			closePhysical()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("COPY frame reader did not join")
			}
		})
		// Receiving and releasing Flush establishes actual frame-read progress.
		select {
		case messages <- frontendMessage{tag: 'H'}:
		case <-time.After(2 * time.Second):
			t.Fatal("COPY reader did not receive Flush")
		}
		select {
		case <-c.freeBuffers:
		case <-time.After(2 * time.Second):
			t.Fatal("COPY reader did not release Flush")
		}
		cancel(errUserCancel)
		select {
		case messages <- frontendMessage{tag: 'd', body: []byte("1\n")}:
		case out := <-result:
			t.Fatalf("logical cancel ended physical frame read: %+v", out)
		case <-time.After(2 * time.Second):
			t.Fatal("COPY reader did not await Data")
		}
		select {
		case out := <-result:
			if out.data != "1\n" || out.err != nil {
				t.Fatalf("fulfilled frame: %+v", out)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("COPY reader did not return fulfilled bytes")
		}
	})
	t.Run("later fatal interrupts copy wait after user cancel", func(t *testing.T) {
		physical, closePhysical := context.WithCancel(context.Background())
		ctx, cancel := context.WithCancelCause(physical)
		cancel(errUserCancel)
		messages := make(chan frontendMessage)
		terminal := make(chan error, 1)
		fatal := &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "canonical terminal"}
		c := &client{ctx: physical, messages: messages, sessionEnded: terminal, freeBuffers: make(chan []byte, 2)}
		p := protocolCopy{client: c, ctx: ctx}
		result := make(chan error, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer p.release()
			var data [2]byte
			_, err := p.Read(data[:])
			result <- err
		}()
		t.Cleanup(func() {
			closePhysical()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("terminal COPY reader did not join")
			}
		})
		select {
		case messages <- frontendMessage{tag: 'H'}:
		case <-time.After(2 * time.Second):
			t.Fatal("COPY reader did not receive Flush")
		}
		select {
		case <-c.freeBuffers:
		case <-time.After(2 * time.Second):
			t.Fatal("COPY reader did not release Flush")
		}
		terminal <- fatal
		select {
		case err := <-result:
			if err != fatal || context.Cause(ctx) != errUserCancel {
				t.Fatalf("terminal priority lost: %v / %v", err, context.Cause(ctx))
			}
		case <-time.After(2 * time.Second):
			t.Fatal("later fatal waited for CopyData")
		}
	})
	t.Run("physical watcher requests end before held lease drains", func(t *testing.T) {
		e := engine.NewWithSettings(nil, "watcher mechanics", false)
		principal, err := e.ResolveLogin("postgres")
		if err != nil {
			t.Fatal(err)
		}
		session, err := e.StartSession(context.Background(), engine.SessionStart{ID: 2, Authenticated: principal})
		if err != nil {
			t.Fatal(err)
		}
		operationCtx, cancelOperation := context.WithCancelCause(context.Background())
		op, err := session.Acquire(operationCtx)
		if err != nil {
			cancelOperation(nil)
			session.RequestEnd(engine.InitFailure)
			joined, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			cleanupErr := session.WaitEnd(joined)
			cancel()
			t.Fatal(errors.Join(err, cleanupErr))
		}
		physical, closePhysical := context.WithCancel(context.Background())
		c := &client{server: &server{}, ctx: physical, session: session}
		c.watchSessionEnd()
		t.Cleanup(func() {
			closePhysical()
			cancelOperation(nil)
			session.RequestEnd(engine.InvalidConnection)
			op.Done()
			joined, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := session.WaitEnd(joined); err != nil {
				t.Error(err)
			}
			select {
			case <-c.sessionDone:
			case <-joined.Done():
				t.Error("session watcher did not join")
			}
		})
		cancelOperation(errUserCancel)
		closePhysical()
		// The operation still owns its lease and still has its first cause.
		// Physical retirement must therefore use independent session authority.
		select {
		case <-session.EndRequested():
		case <-time.After(2 * time.Second):
			t.Fatal("physical close waited for operation lease")
		}
		if context.Cause(op.Context()) != errUserCancel {
			t.Fatal("first operation cause replaced")
		}
		select {
		case <-c.sessionDone:
		case <-time.After(2 * time.Second):
			t.Fatal("physical watcher waited for cleanup")
		}
	})
}
func TestServerPreparedLogValues(t *testing.T) {
	parameters := []engine.ProtocolLogParameter{
		{OID: 23, Type: "integer", Format: 1, Decoded: true, Bytes: []byte("42")},
		{OID: 25, Type: "text", Null: true},
		{OID: 25, Type: "text", Decoded: true, Bytes: []byte{}},
		{OID: 23, Type: "integer", Format: 1, Bytes: []byte{0xff, 0}},
		{OID: 25, Type: "text", Bytes: []byte{0xff}},
	}
	got := logParameters(parameters)
	before := append([]byte{}, parameters[3].Bytes...)
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var values []map[string]any
	if err = json.Unmarshal(encoded, &values); err != nil {
		t.Fatal(err)
	}
	if values[0]["value"] != "42" || values[0]["format"] != "binary" || values[0]["encoding"] != "text" {
		t.Fatal(values[0])
	}
	if values[1]["null"] != true || values[1]["value"] != nil || values[2]["value"] != "" {
		t.Fatal(values)
	}
	if values[3]["value"] != "/wA=" || values[3]["encoding"] != "base64" || values[4]["value"] != "/w==" {
		t.Fatal(values)
	}
	if !reflect.DeepEqual(before, parameters[3].Bytes) {
		t.Fatal("logging mutated input")
	}
	parameters[0].Bytes[0] = '0'
	if *got[0].Value != "42" {
		t.Fatal("log record borrowed mutable bytes")
	}
	empty, err := json.Marshal(logParameters(nil))
	if err != nil || string(empty) != "[]" {
		t.Fatal(string(empty), err)
	}
}
