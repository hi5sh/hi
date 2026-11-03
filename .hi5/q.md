# Notes for hi q in the hi CLI repository

- Go, one package in `internal/cli`, started by `cmd/hi`; embedded files are declared in the root's `embed.go`. Check a change with `gofmt -l . && go vet ./... && go test ./...`.
- Build a local binary into the scratch or /tmp folder, never into the repository root: `go build -o /tmp/hi ./cmd/hi`
- Releases happen by pushing a tag `vX.Y.Z` on main; GitHub Actions builds the binaries. Don't tag without being asked.
- The website is `site/` (Jekyll); `.github/workflows/pages.yml` deploys it on every push to main that changes it.
- Never print or commit `.env`; it holds server passwords and provider keys.
