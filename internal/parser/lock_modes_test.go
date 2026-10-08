package parser

import "testing"

func TestLockModesGrammar(t *testing.T) {
	modes := []string{"ACCESS SHARE", "ROW SHARE", "ROW EXCLUSIVE", "SHARE UPDATE EXCLUSIVE", "SHARE", "SHARE ROW EXCLUSIVE", "EXCLUSIVE", "ACCESS EXCLUSIVE"}
	for _, target := range []string{"t", "ONLY t", "ONLY(t)", "t *", `ONLY public."Mixed", b *, ONLY(c)`} {
		for _, prefix := range []string{"LOCK ", "LOCK TABLE "} {
			for _, mode := range append([]string{""}, modes...) {
				q := prefix + target
				if mode != "" {
					q += " IN " + mode + " MODE"
				}
				for _, suffix := range []string{"", " NOWAIT"} {
					t.Run(q+suffix, func(t *testing.T) {
						statements, err := Parse(q + suffix)
						if err != nil || len(statements) != 1 {
							t.Fatal(statements, err)
						}
						if _, ok := statements[0].(*LockTable); !ok {
							t.Fatalf("wrong statement: %T", statements[0])
						}
					})
				}
			}
		}
	}
	for _, q := range []string{"LOCK", "LOCK TABLE", "LOCK t,", "LOCK ,t", "LOCK ONLY t *", "LOCK t IN SHARE UPDATE MODE", "LOCK t IN ROW MODE", "LOCK t IN EXCLUSIVE", "LOCK t NOWAIT junk", "LOCK t IN ACCESS EXCLUSIVE MODE, b"} {
		controlParseError(t, q, "42601", "", "")
	}
}

func TestLockModesAST(t *testing.T) {
	modes := []struct {
		text string
		mode TableLockMode
	}{
		{"ACCESS SHARE", LockAccessShare}, {"ROW SHARE", LockRowShare},
		{"ROW EXCLUSIVE", LockRowExclusive}, {"SHARE UPDATE EXCLUSIVE", LockShareUpdateExclusive},
		{"SHARE", LockShare}, {"SHARE ROW EXCLUSIVE", LockShareRowExclusive},
		{"EXCLUSIVE", LockExclusive}, {"ACCESS EXCLUSIVE", LockAccessExclusive}, {"", LockAccessExclusive},
	}
	for _, mode := range modes {
		query := `LOCK ONLY public."Mixed", b *, ONLY(c)`
		if mode.text != "" {
			query += " IN " + mode.text + " MODE"
		}
		statements, err := Parse(query + " NOWAIT")
		if err != nil {
			t.Fatal(err)
		}
		lock := statements[0].(*LockTable)
		if lock.Mode != mode.mode || !lock.Nowait || len(lock.Targets) != 3 ||
			!lock.Targets[0].Only || lock.Targets[1].Only || !lock.Targets[2].Only ||
			len(lock.Targets[0].Name.Parts) != 2 || lock.Targets[0].Name.Parts[1].Text != "Mixed" ||
			lock.Targets[1].Name.Parts[0].Text != "b" || lock.Targets[2].Name.Parts[0].Text != "c" {
			t.Fatalf("%s: %#v", query, lock)
		}
	}
}
