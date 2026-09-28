module sre-portfolio

// PROVISIONAL (D-003): toolchain not installed yet (disk-blocked).
// Confirm with `go version` after `brew install go`, then tighten
// to the exact version and commit go.sum.
go 1.27

require github.com/jackc/pgx/v5 v5.11.0

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)
