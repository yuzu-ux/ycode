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

Model text is stripped of terminal control characters before display so a
response cannot activate ANSI/OSC control sequences.

## Session data

Sessions contain prompts, model responses, tool calls, and clipped tool output.
They are stored with mode `0600` under the operating system cache directory.
Anyone with access to the user's account may still be able to read them.

## Network

The configured provider receives the bounded prompt, repository map, selected
file contents returned by tools, and conversation history. YCode does not send
telemetry. `doctor` makes no network request unless `--network` is passed.
