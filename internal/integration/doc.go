// Package integration holds the repo layer's execution suite. Every file
// that talks to Postgres carries `//go:build integration`, so the untagged
// `go test ./...` suite stays DB-free and sees this package as empty.
//
// Run it with a disposable database only:
//
//	TEST_DATABASE_URL=postgres://spa:spa@localhost:5432/spa_test \
//	go test -tags=integration -race -count=1 -p 1 ./...
package integration
