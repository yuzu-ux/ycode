# YCode project instructions

- Keep the runtime dependency-free unless a dependency has a measured,
  documented benefit that cannot reasonably be implemented in the standard
  library.
- Run `gofmt`, `go vet ./...`, `go test -race ./...`, and a production build
  before publishing.
- Treat provider payloads, workspace paths, shell authority, and session data as
  security boundaries.
- Any always-on feature must document its startup, memory, and token cost.
- Prefer small packages and narrow interfaces over a framework layer.
