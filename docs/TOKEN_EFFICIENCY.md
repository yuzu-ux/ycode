# Token efficiency

YCode treats model input as a budget, not a transcript dump.

## 1. Repository map instead of repository upload

The mapper obtains Git-visible files when possible and falls back to a bounded
filesystem walk. It:

- skips dependency/build/cache directories;
- excludes secret-like filenames;
- ranks paths using terms in the current request;
- favors project instructions and manifests;
- extracts a few language-agnostic symbol signatures;
- stops at `repo_map_tokens`.

The map is navigation, not source truth. The system prompt tells the model to
read a file before editing it.

## 2. Two schemas

Tool schemas are included in each compatible chat request. YCode combines file
operations behind `workspace` and command execution behind `shell`, avoiding a
long list of near-duplicate function descriptions.

## 3. Stable then dynamic

The static behavior/safety prompt is the first message. Query-specific map
content follows it. Providers that cache common prefixes can therefore reuse
the stable portion across turns.

## 4. Whole-turn compaction

When estimated input crosses the hard budget, YCode removes the oldest complete
user turn. A turn includes its assistant tool calls and tool results, preventing
invalid orphan messages.

Before removal, a deterministic capsule records bounded lists of:

- prior requests;
- tool operations;
- observed outcomes.

This compaction performs no additional model call. It trades the nuance of an
LLM summary for predictable latency, cost, and behavior.

## 5. Tool result ledger

Each tool result receives a short SHA-256-derived reference.

- First-seen oversized output keeps approximately 60% from the beginning and
  40% from the end.
- Repeated identical output becomes one short reference.
- The original and avoided token estimates are accumulated in session stats.

## 6. Accounting

YCode uses a conservative four-bytes-per-token estimate because the exact
tokenizer differs by model and provider. Provider-reported usage takes
precedence when available, but budget enforcement remains deterministic and
provider-independent.

`ycode benchmark` measures the repository-map reduction without making an API
request:

```bash
ycode benchmark "the task being measured"
```

`ycode run --stats` reports cumulative values for the actual agent run.

## Known tradeoffs

- Filename and symbol ranking is lexical, not semantic.
- Four bytes per token is not an exact tokenizer.
- A deterministic capsule can omit a subtle decision from a very old turn.
- A compact two-tool schema gives the model fewer strongly typed affordances.

The roadmap includes optional exact tokenizers and smarter local ranking, but
the core must remain useful without a background model or embedding runtime.
