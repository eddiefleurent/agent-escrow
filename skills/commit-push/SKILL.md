---
name: commit-push
description: Create atomic conventional commits with emoji and push them to the current branch upstream. Use when a user asks to commit and push changes, run pre-commit verification, auto-stage unstaged files, split unrelated changes into multiple commits, or push with --no-verify and/or --force options.
---

# Commit Push

Execute a reliable commit-and-push workflow that prefers safe defaults, preserves atomic history, and uses emoji conventional commit messages.

## Parse Command Options

Support these forms:

- `$commit-push`
- `$commit-push --no-verify`
- `$commit-push --force`
- `$commit-push --no-verify --force`

Interpret options:

- `--no-verify`: skip verification checks before commit.
- `--force`: push with `git push --force`.

If options are malformed or unknown, stop and ask for clarification.

## Run Verification (Default)

Unless `--no-verify` is present:

1. Detect package manager by lockfile:
   - `pnpm-lock.yaml` -> `pnpm`
   - `yarn.lock` -> `yarn`
   - `bun.lockb` or `bun.lock` -> `bun`
   - `package-lock.json` -> `npm`
2. If `package.json` exists, run available scripts in this order when present:
   - `lint`
   - `format:check` (or fallback `format` if only formatting command exists)
   - `build`
   - `docs` or `docs:generate`
3. If no JS toolchain is detected, run project-native verification that matches repo conventions (for example, `make test-all`, language-native lint/test/build commands, or documented CI entrypoint).

If verification fails, stop, report the failure, and do not commit or push.

## Stage Changes

1. Check staged state (`git status --short`).
2. If zero files are staged, stage all tracked and untracked changes (`git add -A`).
3. If still no staged changes, stop and report that there is nothing to commit.

## Inspect and Split Logically

1. Review staged diff (`git diff --staged`).
2. Decide whether changes are one logical unit or multiple unrelated units.
3. If clearly mixed concerns, propose split commits and perform separate commits in a safe order.
4. Keep each commit atomic and focused.

## Compose Commit Message

Use emoji conventional commits:

- `✨ feat: ...`
- `🐛 fix: ...`
- `📝 docs: ...`
- `💄 style: ...`
- `♻️ refactor: ...`
- `⚡️ perf: ...`
- `✅ test: ...`
- `🔧 chore: ...`

Rules:

- Use imperative present tense.
- Keep first line concise (target <= 72 chars).
- Match type to dominant intent of the diff.

## Commit

1. Create commit(s) with the selected message(s).
2. If commit fails, stop and report the error.

## Push

1. Detect current branch and upstream.
2. If upstream is missing, set it with `git push --set-upstream origin <branch>`.
3. Push:
   - default: `git push`
   - with `--force`: `git push --force`
4. Report push result and any remote messages.

## Output Contract

After execution, report:

1. Checks run (or explicitly skipped).
2. Files committed and commit SHA(s).
3. Commit message(s).
4. Push target and whether upstream was set.
5. Any warnings (for example, force push used).
