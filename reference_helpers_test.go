//go:build reference

package stuntgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"stuntgres"
)

const referenceImage = "postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854"

var referenceContainer *postgres.PostgresContainer
var referenceAdmin *sql.DB
var referenceDSN, referenceContainerID string
var referenceMemoryDriver = stuntgres.NewDriver()

func TestMain(m *testing.M) {
	code := runReferenceMain(m)
	os.Exit(code)
}

func runReferenceMain(m *testing.M) (code int) {
	code = 1
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintf(os.Stderr, "reference startup/runtime panic: %v\n", p)
			code = 1
		}
		if referenceAdmin != nil {
			if err := referenceAdmin.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "reference close: %v\n", err)
				code = 1
			}
		}
		if referenceContainer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if err := referenceContainer.Terminate(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "reference terminate: %v\n", err)
				code = 1
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var err error
	referenceContainer, err = postgres.Run(ctx, referenceImage,
		postgres.WithDatabase("stuntgres_reference"), postgres.WithUsername("stuntgres"),
		postgres.WithPassword("stuntgres"), postgres.BasicWaitStrategies(),
		testcontainers.WithEnv(map[string]string{"POSTGRES_INITDB_ARGS": "--locale=C --encoding=UTF8"}))
	if err != nil {
		fmt.Fprintf(os.Stderr, "reference startup: %v\n", err)
		return
	}
	referenceContainerID = referenceContainer.GetContainerID()
	fmt.Fprintf(os.Stderr, "reference container: %s\n", referenceContainerID)
	referenceDSN, err = referenceContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reference connection string: %v\n", err)
		return
	}
	referenceAdmin, err = sql.Open("postgres", referenceDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reference open: %v\n", err)
		return
	}
	if err = referenceAdmin.PingContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "reference ping: %v\n", err)
		return
	}
	var version, encoding, locale string
	if err = referenceAdmin.QueryRowContext(ctx, "SELECT current_setting('server_version_num'), current_setting('server_encoding'), datcollate FROM pg_database WHERE datname = current_database()").Scan(&version, &encoding, &locale); err != nil {
		fmt.Fprintf(os.Stderr, "reference configuration: %v\n", err)
		return
	}
	if version != "180000" || encoding != "UTF8" || locale != "C" {
		fmt.Fprintf(os.Stderr, "reference configuration mismatch: version=%q encoding=%q locale=%q\n", version, encoding, locale)
		return
	}
	sql.Register("stuntgres_reference_memory", sqlmw.Driver(referenceMemoryDriver, referenceInterceptor{}))
	sql.Register("stuntgres_reference_postgres", sqlmw.Driver(&pq.Driver{}, referenceInterceptor{}))
	code = m.Run()
	return
}

type referencePair struct {
	memory, postgres, native     *sql.DB
	memoryRecord, postgresRecord *referenceRecorder
	ctx                          context.Context
}

func newReferencePair(t *testing.T) *referencePair {
	t.Helper()
	if referenceContainer.GetContainerID() != referenceContainerID {
		t.Fatal("reference container identity changed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	reset := func() {
		resetCtx, resetCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer resetCancel()
		if _, err := referenceAdmin.ExecContext(resetCtx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
			t.Errorf("reference schema reset: %v", err)
		}
	}
	reset()
	if t.Failed() {
		t.FailNow()
	}
	p := &referencePair{ctx: ctx, memoryRecord: &referenceRecorder{}, postgresRecord: &referenceRecorder{}}
	var err error
	connector, err := referenceMemoryDriver.OpenConnector("reference/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	p.native = sql.OpenDB(connector)
	p.memory, err = sql.Open("stuntgres_reference_memory", "reference/"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	p.postgres, err = sql.Open("stuntgres_reference_postgres", referenceDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, db := range []*sql.DB{p.memory, p.postgres, p.native} {
			if err := db.Close(); err != nil {
				t.Errorf("case close: %v", err)
			}
		}
		reset()
	})
	for _, db := range []*sql.DB{p.memory, p.postgres, p.native} {
		if err := db.PingContext(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return p
}
