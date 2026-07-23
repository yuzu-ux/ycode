# Contributing

YCode favors small, measurable changes.

## Development

```bash
make check
make build
./bin/ycode benchmark "your change"
```

`make check` runs formatting verification, `go vet`, the race-enabled test
suite, and a production build.

## Design constraints

- Keep the default binary free of third-party runtime dependencies.
- Do not add a tool schema when an existing action can express the operation.
- Preserve workspace path confinement and shell approval behavior.
- Add tests for provider wire changes and every authority-changing tool change.
- State the startup, memory, or token cost of a new always-on feature.
- Never persist provider credentials.

## Pull requests

Explain:

- the user problem;
- the smallest behavior change that solves it;
- validation performed;
- token, startup, memory, and security impact.

Keep unrelated refactors separate.
