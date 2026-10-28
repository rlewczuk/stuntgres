package sqltype

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/workadmission"
)

func TestPgcryptoSHAAdmissionByteQuantum(t *testing.T) {
	refused := errors.New("next hash credit refused")
	for _, repeat := range []bool{false, true} {
		name, quantum := "string", 4096
		if repeat {
			name, quantum = "repeat", 64
		}
		t.Run(name, func(t *testing.T) {
			granted := false
			ctx := workadmission.Activate(workadmission.With(context.Background(), func(_ context.Context, class workadmission.Class) error {
				if class != workadmission.WorkPasswordSHA {
					return nil
				}
				if granted {
					return refused
				}
				granted = true
				return nil
			}))
			h := sha256.New()
			var err error
			if repeat {
				var digest [64]byte
				for i := range digest {
					digest[i] = 'x'
				}
				err = pgcryptoSHARepeat(ctx, h, digest[:], 65)
			} else {
				var scratch [4096]byte
				err = writeHashString(ctx, h, strings.Repeat("x", 4097), &scratch, workadmission.WorkPasswordSHA)
			}
			// The actual hash state, not a callback count, witnesses the granted
			// amount and that refusal authorizes no byte from the next quantum.
			want := sha256.Sum256(bytes.Repeat([]byte{'x'}, quantum))
			if err != refused || !bytes.Equal(h.Sum(nil), want[:]) {
				t.Fatalf("bounded hash state: %x error %v", h.Sum(nil), err)
			}
		})
	}
}

func TestPgcryptoSHAAdmissionCanceledGrant(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := workadmission.Activate(workadmission.With(parent, func(context.Context, workadmission.Class) error {
		cancel()
		return nil
	}))
	h := sha256.New()
	var scratch [4096]byte
	err := writeHashString(ctx, h, "password", &scratch, workadmission.WorkPasswordSHA)
	want := sha256.Sum256(nil)
	if err != context.Canceled || !bytes.Equal(h.Sum(nil), want[:]) {
		t.Fatalf("canceled grant wrote password bytes: %x %v", h.Sum(nil), err)
	}
}
