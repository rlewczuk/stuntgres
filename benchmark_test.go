package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres"
)

func BenchmarkOpenDatabase(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		connector, err := stuntgres.NewDriver().OpenConnector("benchmark")
		if err != nil {
			b.Fatal(err)
		}
		db := sql.OpenDB(connector)
		if err = db.PingContext(ctx); err != nil {
			db.Close()
			b.Fatal(err)
		}
		if err = db.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
