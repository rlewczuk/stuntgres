//go:build !noservertests

package reference_test

import (
	"fmt"
	"testing"
)

func insertDefaultRawSupplied(t *testing.T, dsn string) {
	t.Helper()
	for _, family := range insertDefaultFamilies {
		for _, named := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				t.Run(fmt.Sprintf("supplied/%s/named_%t/transaction_%t", family.name, named, explicit), func(t *testing.T) {
					w, peer := serverAuthenticated(t, dsn), serverAuthenticated(t, dsn)
					table := fmt.Sprintf("raw_supplied_%s_%t_%t", family.name, named, explicit)
					for _, query := range family.setup(table) {
						serverWantCode(t, w.query(t, query), "")
					}
					ready := byte('I')
					if explicit {
						insertDefaultCommand(t, w, "BEGIN", "BEGIN", 'T')
						ready = 'T'
					}
					name, column := "", "id"
					var supplied any = 100
					if named {
						name = "supplied"
					}
					if family.name == "now" {
						column = "stamp"
						supplied = "2024-01-02 03:04:05+00"
					}
					insertDefaultPrepare(t, w, name, "INSERT INTO "+table+"("+column+",c) SELECT $1,$2", []uint32{family.oid, 23}, false, ready)
					insertDefaultExecute(t, w, name, insertDefaultArgs(supplied, 1), 1, ready)
					count := 1
					// SERIAL is NOT NULL; nullable declarations independently prove
					// that a supplied NULL is not replaced by their default recipe.
					if family.name != "serial" {
						insertDefaultExecute(t, w, name, insertDefaultArgs(nil, 2), 1, ready)
						count++
					}
					if named {
						name = "omitted"
					}
					insertDefaultPrepare(t, w, name, "INSERT INTO "+table+"(c) SELECT $1", []uint32{23}, false, ready)
					insertDefaultExecute(t, w, name, insertDefaultArgs(3), 1, ready)
					insertDefaultHealthy(t, w, table, count+1, ready)
					if family.name == "now" {
						insertDefaultRead(t, w, "SELECT stamp = TIMESTAMPTZ '2024-01-02 03:04:05+00' AS supplied FROM "+table+" WHERE c = 1", []string{"supplied"}, []uint32{16}, [][]string{{"t"}}, ready)
						insertDefaultRead(t, w, "SELECT stamp IS NULL AS supplied FROM "+table+" WHERE c = 2", []string{"supplied"}, []uint32{16}, [][]string{{"t"}}, ready)
						query := "SELECT stamp IS NOT NULL AS produced FROM " + table + " WHERE c = 3"
						if explicit {
							query = "SELECT stamp = transaction_timestamp() AS produced FROM " + table + " WHERE c = 3"
						}
						insertDefaultRead(t, w, query, []string{"produced"}, []uint32{16}, [][]string{{"t"}}, ready)
					}
					if explicit {
						insertDefaultCommand(t, w, "COMMIT", "COMMIT", 'I')
					}
					if family.name != "now" {
						want := [][]string{{"100", "1"}}
						if family.name != "serial" {
							want = append(want, []string{"<NULL>", "2"})
						}
						want = append(want, []string{fmt.Sprint(family.first), "3"})
						insertDefaultRead(t, peer, "SELECT id,c FROM "+table+" ORDER BY c", []string{"id", "c"}, []uint32{family.oid, 23}, want, 'I')
					}
				})
			}
		}
	}
}
