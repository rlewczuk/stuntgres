//go:build !noservertests

package reference_test

import (
	"io"
	"testing"
)

func serverStartupBoundaries(t *testing.T, dsn string) {
	t.Helper()
	for _, n := range []uint32{7, 10005} {
		w := serverDial(t, dsn)
		w.send(t, serverPut32(nil, n))
		if fr, err := w.readFrame(); err != io.EOF {
			t.Fatalf("startup length %d reply %c %x: %v", n, fr.tag, fr.body, err)
		}
	}
	for _, partial := range [][]byte{{0, 0}, {0, 0, 0, 12, 0, 3}} {
		w := serverDial(t, dsn)
		w.send(t, partial)
		w.Close()
		serverScalar(t, serverAuthenticated(t, dsn), "SELECT 42", "42")
	}
	_, user, _, database := serverEndpoint(t, dsn)
	missingTerminator := serverPut32(nil, 196608)
	missingTerminator = serverCString(serverCString(missingTerminator, "user"), user)
	missingTerminator = serverCString(serverCString(missingTerminator, "database"), database)
	missingTerminator = append(serverPut32(nil, uint32(len(missingTerminator)+4)), missingTerminator...)
	for _, c := range []struct {
		name          string
		packet        []byte
		code, message string
	}{
		{"missing_user", serverStartup(196608, "database", "stuntgres_reference"), "28000", "no PostgreSQL user name specified in startup packet"},
		{"missing_terminator", missingTerminator, "08P01", "invalid startup packet layout: expected terminator as last byte"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := serverDial(t, dsn)
			w.send(t, c.packet)
			fr := w.frame(t)
			fields := serverFields(fr.body)
			if fr.tag != 'E' || fields['S'] != "FATAL" || fields['V'] != "FATAL" || fields['C'] != c.code || fields['M'] != c.message {
				t.Fatalf("startup diagnostic %v", fields)
			}
			if _, err := w.readFrame(); err != io.EOF {
				t.Fatalf("startup fatal EOF %v", err)
			}
		})
	}
}
