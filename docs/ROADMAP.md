# Roadmap

YCode aims to be the smallest serious harness for developers who care about
latency, token cost, and auditable execution.

## v0.1 — foundation

- [x] single dependency-free Go binary
- [x] OpenAI-compatible streaming and tool calls
- [x] query-ranked repository maps
- [x] hard input and output budgets
- [x] deterministic old-turn compaction
- [x] content-addressed tool result deduplication
- [x] safe workspace editing and shell policies
- [x] resumable private local sessions
- [x] local context-reduction benchmark
- [x] checksum-verified macOS, Linux, and Windows installers

## v0.2 — compatibility and precision

- [ ] Responses API transport
- [ ] native Anthropic and Gemini transports
- [ ] provider capability discovery
- [ ] optional exact tokenizers with estimate calibration
- [ ] unified-diff preview before writes
- [ ] configurable approval rules per command family
- [ ] session export/import with secret scanning
- [ ] signed multi-platform release binaries

## v0.3 — extensibility without core bloat

- [ ] MCP stdio client behind lazy schema loading
- [ ] project-local skill files with explicit activation
- [ ] on-demand browser tool adapter
- [ ] local symbol index and change-aware map cache
- [ ] structured JSON event output for automation

## Later experiments

- terminal UI built on the same agent core
- isolated parallel workers with Git worktrees
- local-only semantic memory
- daemon/client sessions
- provider prompt-cache telemetry

## Acceptance rule

A feature should not enter the default path unless it can answer:

1. What recurring developer problem does it solve?
2. What does it add to cold startup, resident memory, and request tokens?
3. Can it be lazy or optional?
4. How is its authority constrained and audited?
