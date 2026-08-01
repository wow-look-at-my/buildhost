# Why CLAUDE.md reached 3.02x budget with nothing going red

CLAUDE.md was 120,754 characters against a 40,000-character budget. Every one of
those characters was re-sent on every request of every agent session that opened
this repo. It got there through a dozen edits, across several sessions, and
nothing anywhere failed.

This document is the account of why, because the interesting part is not the file
size -- it is that four separate mechanisms existed to prevent exactly this and
none of them could.

## What the budget is

Claude Code loads every CLAUDE.md verbatim into the prompt. There is no
truncation worth the name: the only hard cap skips a file over 4 MiB entirely,
and the "limit" the CLI talks about is a warning threshold that changes nothing
about what gets sent. So an oversized instruction file is not cut off -- it is
billed, in full, forever, crowding out the context the actual task needs, and its
rules compete with each other for attention.

The 40,000 figure is the CLI's own floor for the same measurement. Characters,
not bytes.

## The four things that should have caught it

**1. The CLI's own warning renders only in the terminal UI.** It appears in the
Ink startup banner and the `/status` dialog. It never appears on the web surface,
and it is never shown to the model. An agent editing a CLAUDE.md has no way to
learn from the CLI that the file is already over budget.

**2. The web config's `claude-md-budget` hook was installed in a stale version.**
`PazerOP/claude-code-web-config` registers that hook on three events --
SessionStart (advise), PostToolUse (re-measure after every edit, including edits
made through Bash that name no file path), and Stop (block the end of a turn that
left a file over budget). The environment this repo's sessions ran in was
provisioned from an older build: a 10,113-byte copy of the script registered on
**SessionStart only**. No post-edit re-measure. No Stop gate. The escalation
machinery existed upstream and was simply not present.

**3. The one message that did fire told the agent to stand down.** The stale
SessionStart advisory ended with: "Do NOT go reorganize these files right now
unless that IS the task. This is standing context for when you next touch one of
them." A session that read the warning, correctly, did nothing about it. (The
current upstream wording is the opposite -- "Editing one of these files this
session? Then fix it as you go" -- which is another way of saying the fix had
already been made upstream and had not reached here.)

**4. The repo-local CI check was deleted in favour of the hook.** Commit
`76110b4` removed a CLAUDE.md wrap check from `ci.yml` with the reasoning: "The
rule belongs in the web config, where it applies to every repo a session touches,
not bolted onto one repo's CI. It now lives in
`claude_snippets/claude-md-is-an-index.md` and is enforced by the
claude-md-budget hook at edit time and at end of turn."

That reasoning is right about where the rule belongs and wrong about what
enforcement survives the move. A hook fires only if the session that happens to
be running has a current config installed. CI runs for everyone, every push,
regardless of whose machine or which config version. Trading the second for the
first traded a check that always runs for one that runs conditionally -- and the
condition turned out to be false here.

## The rule that follows

A hook and a CI check are layers, not duplicates. The hook tells the model, in
the session, while it can still act. CI is the thing that cannot be skipped. A
requirement that is only enforced by advice is enforced by nothing on the day the
advice does not arrive.

`.github/scripts/claude-md-budget.ts` (CI job `claude-md-budget`) is the layer
that fails the build. It measures every CLAUDE.md in the repo and fails on three
things:

- **over budget** -- more than 40,000 characters;
- **at the wall** -- at or above 97.5% of budget, because landing one character
  under the limit is not a fix: the next ordinary edit breaks it, and whoever
  makes that edit inherits the extraction that was skipped;
- **unwrapped** -- any line over 150 columns that could have been wrapped, since
  an unwrapped file makes every edit a one-line diff no reviewer can read, and a
  paragraph running for thousands of columns is the visible SHAPE of an item that
  should have been a pointer to `docs/`.

Each class was verified to go red before the check was wired in.

## How the file was brought back

By extraction, never summarization: the prose moved into `docs/` VERBATIM and
CLAUDE.md kept a one-line pointer to each. Paragraph breaks were added at
existing topic boundaries; no wording was changed. 120,754 characters became
9,299 -- 23% of budget -- and every backticked identifier in the old file was
verified to still exist somewhere in the tree.

Two things surfaced during the extraction that are worth recording, because both
are symptoms of the same disease:

- The "Browser sign-in with GitHub" bullet appeared **twice**, in two versions
  that had drifted apart -- 12,519 characters of near-duplicate. The older copy
  predated the dead-token re-auth path. Nobody noticed because nobody could read
  the file end to end.
- One bullet (`internal/brew/`) was 12,669 characters on its own -- a technical
  manual wearing a bullet's clothes, larger by itself than a third of the whole
  budget.

CLAUDE.md is an index: what exists, the invariants one line each, and where the
depth lives. The depth goes in `docs/`, where it can grow without being billed on
every request.
