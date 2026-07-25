# Security

## Supported versions

YCode is currently pre-1.0. Security fixes are applied to the latest commit on
the default branch until tagged releases begin.

## Reporting a vulnerability

Please use GitHub's private vulnerability reporting for this repository. Do not
open a public issue containing an exploit, credential, or private source code.

## Trust model

YCode coordinates three untrusted inputs:

1. user and repository text;
2. model-generated tool calls;
3. process output.

The model is not a security boundary. Every tool call is validated locally.

## File boundary

The `workspace` tool:

- rejects absolute paths and parent-directory escapes;
- resolves existing symlinks and rejects targets beyond the workspace;
- refuses to edit `.git`;
- limits editable files to 4 MiB;
- writes through a same-directory temporary file;
- has no delete operation;
- can be disabled with `--read-only`.

## Shell boundary

`shell_policy` has three values:

- `safe` — only focused inspection, build, and test command families;
- `ask` — safe commands run, all other non-blocked commands need interactive
  one-time approval;
- `allow` — non-blocked commands run without prompting.

Destructive or privileged patterns such as `sudo`, broad recursive root/home
removal, `git reset --hard`, `git clean -f`, filesystem formatting, shutdown,
and fork bombs stay blocked in every mode.

Shell subprocesses do not inherit environment variables whose names look like
API keys, tokens, passwords, credentials, or secrets. Secret-like file paths
also leave the automatic safe-command path. Build and test commands still
execute repository code, which may itself be hostile; use operating-system
isolation for repositories you do not trust.

Pattern checks are defense in depth, not an operating-system sandbox. In
`allow` mode a sufficiently creative command can modify anything available to
the current user. Run YCode in a container or disposable account when working
with an untrusted model, repository, or prompt.

## Secrets

- API keys are read from environment variables.
- API keys are never sent over non-loopback plain HTTP.
- Config templates store only the environment variable name.
- Doctor output reveals only whether a key exists.
- Repository maps skip common environment, credential, secret, and private-key
  filenames.
- Sessions do not intentionally store API keys.

Tool output or model responses can still contain a secret that was present in a
source file or command. Review the session cache and terminal output according
to your project's data policy.

Model text from YCode's own provider loop is stripped of terminal control
characters before display so a response cannot activate ANSI/OSC control
sequences. External CLI connections render that CLI's output directly and rely
on its terminal-safety behavior.

## Session data

Sessions contain prompts, model responses, tool calls, and clipped tool output.
They are stored with mode `0600` under the operating system cache directory.
Anyone with access to the user's account may still be able to read them.

## Network

The configured provider receives the bounded prompt, repository map, selected
file contents returned by tools, and conversation history. YCode does not send
telemetry. `doctor` makes no network request unless `--network` is passed.
Provider and discovery requests do not follow HTTP redirects.

`local` connection mode accepts only loopback endpoints and never reads or sends
an API key. Local discovery is opt-in through `ycode connect local` and performs
only `GET /v1/models` requests against known loopback ports. The selected local
runtime still receives model context, but that traffic does not leave the
machine through YCode.

`cli` connection mode stores only an allowlisted executable identifier:
`codex`, `claude`, or `opencode`. YCode resolves it on PATH and launches it
directly with an argv array; prompts are never interpolated into a shell
command. The external CLI owns its login data, and YCode does not copy that
credential material into configuration or sessions. Credential-like environment
variables are removed from the child process, so these adapters expect the CLI
to have its own saved login.

An external coding CLI has its own tools and permission model. A normal YCode
run selects that CLI's non-interactive editing mode; `--read-only` selects its
restricted mode. Review the external CLI's configuration before using it in an
untrusted repository. OpenCode auto mode still honors explicit deny rules but
automatically approves actions that would otherwise ask.
