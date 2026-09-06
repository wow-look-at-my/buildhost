# Why CLAUDE.md reached 3.02x budget with nothing going red

CLAUDE.md was 120,754 characters against a 40,000-character budget. Every one of those characters was re-sent on every request of every agent session that opened this repo. It got there through a dozen edits, across several sessions, and nothing anywhere failed.

This document is the account of why. The interesting part is not the file size. It is that four separate mechanisms existed to prevent exactly this, and not one of them was able to.

## What the budget is

Claude Code loads every CLAUDE.md verbatim into the prompt. There is no truncation worth the name. The only hard cap skips a file over 4 MiB entirely. The "limit" the CLI talks about is a warning threshold, and it changes nothing about what gets sent. An oversized instruction file is therefore not cut off. It is billed, in full, forever. It crowds out the context the actual task needs, and its rules compete with each other for attention.

The 40,000 figure is the CLI's own floor for the same measurement. Characters, not bytes.

## The four things built to catch it

**1. The CLI's own warning renders only in the terminal UI.** It appears in the Ink startup banner and the `/status` dialog. It never appears on the web surface, and it is never shown to the model. An agent editing a CLAUDE.md has no way to learn from the CLI that the file is already over budget.

**2. The web config's `claude-md-budget` hook was installed in a stale version.** `PazerOP/claude-code-web-config` registers that hook on three events -- SessionStart (advise), PostToolUse (re-measure after every edit, including edits made through Bash that name no file path), and Stop (block the end of a turn that left a file over budget). The environment this repo's sessions ran in was provisioned from an older build: a 10,113-byte copy of the script registered on **SessionStart only**. No post-edit re-measure. No Stop gate. The escalation machinery existed upstream and was simply not present.

**3. The one message that did fire told the agent to stand down.** The stale SessionStart advisory ended with: "Do NOT go reorganize these files right now unless that IS the task. This is standing context for when you next touch one of them." A session that read the warning, correctly, did nothing about it. (The current upstream wording is the opposite -- "Editing one of these files this session? Then fix it as you go" -- which is another way of saying the fix had already been made upstream and had not reached here.)

**4. The one guard that reached this container was the wrong one.** Delivery was the whole failure, not authorship. Config installs once, when an environment is built. A snapshot that predates a guard therefore never receives it, and it has no way to fetch it. The self-updater ships inside the artifact it updates.

## The rule that follows

Enforcement belongs in the channel that refreshes. The guard is the `claude-md-budget` marketplace plugin, reinstalled every session. It is the ONLY copy. A per-repo CI script that re-derived the same rule was tried here and deleted. Three implementations of one rule is the disease, not the cure.

It fails on two things:

- **over budget** -- more than 40,000 characters.
- **at the wall** -- at or above 97.5% of budget. One character under the limit is not a fix. The next ordinary edit breaks it, and whoever makes that edit inherits the extraction that was skipped.

A third check, **unwrapped**, covers any line over 150 columns that has room to wrap. It exists, and it is off unless `CC_CLAUDE_MD_WIDTH` is set. Wrapping is still worth doing. An unwrapped file makes every edit a one-line diff no reviewer can read. A paragraph that runs for thousands of columns is the visible SHAPE of an item that belongs in a pointer to `docs/`. It is just not what this guard is for.

## How the file was brought back

By extraction, never summarization. The prose moved into `docs/` VERBATIM. CLAUDE.md kept a one-line pointer to each file. Paragraph breaks go at the existing topic boundaries. No wording changed. 120,754 characters became 9,299, which is 23% of budget. Every backticked identifier in the old file was checked, and each one still exists somewhere in the tree.

Two things surfaced during the extraction that are worth recording, because both are symptoms of the same disease:

- The "Browser sign-in with GitHub" bullet appeared **twice**, in two versions that had drifted apart. That is 12,519 characters of near-duplicate. The older copy predated the dead-token re-auth path. Nobody noticed, because nobody can read the file end to end.
- One bullet (`internal/brew/`) was 12,669 characters on its own. It is a technical manual in a bullet's clothes, larger by itself than a third of the whole budget.

CLAUDE.md is an index: what exists, the invariants one line each, and where the depth lives. The depth goes in `docs/`, where it can grow without being billed on every request.
