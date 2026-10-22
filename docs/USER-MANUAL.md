# Backstory User Manual

Backstory gives your AI coding agents a memory. When you start a new session with Claude Code,
Codex, Hermes or Pi, the agent already knows what happened last time: what was decided, what
got finished, and what comes next. You don't have to explain yesterday again.

This manual covers everyday use: what Backstory keeps, how to ask your agent to remember
things, the panel, and every command. To install it, see the [README](../README.md).

**New to Linux?** Commands in this manual go in a terminal (on Omarchy, `SUPER + Enter` opens
one). Type or paste them and press Enter. `~` means your home folder, for example
`/home/you`. Lines starting with `#` are comments; you don't type them.

---

## Contents

1. [How it works in one minute](#1-how-it-works-in-one-minute)
2. [Words you'll see](#2-words-youll-see)
3. [Check that it's working](#3-check-that-its-working)
4. [Asking your agent to remember things](#4-asking-your-agent-to-remember-things)
5. [The panel](#5-the-panel)
6. [Command reference](#6-command-reference)
7. [Privacy: pausing, fixing and forgetting](#7-privacy-pausing-fixing-and-forgetting)
8. [Settings](#8-settings)
9. [Troubleshooting](#9-troubleshooting)

---

## 1. How it works in one minute

Backstory is a small program that runs in the background on your computer (a "daemon"). Your
agents talk to it; nothing goes over the network.

1. **It watches.** While an agent works, Backstory records what happened: which commands ran,
   which files changed, whether things failed. This is the **timeline**.
2. **Agents write notes.** When something matters, such as a decision, a finished task or
   where to pick up next, the agent saves a short **record**. Backstory's instructions teach
   agents to do this on their own, and you can ask them to.
3. **It hands back a summary.** When you start the next session in the same project, the
   agent gets a short summary first: the latest handoff, what changed since, and anything
   that needs attention.

You mostly don't have to do anything. Work as usual, and the next session starts warm.

**Your old work counts too.** Run `backstory backfill claude` (or `codex`, `hermes`, `pi`)
once after installing to import past sessions, so the first summary isn't empty.

---

## 2. Words you'll see

| Word | What it means |
|---|---|
| **Project** | A folder you work in. A git repository is one project. Backstory works this out itself; you never name projects. |
| **Session** | One run of an agent, from start to exit. |
| **Timeline** | What Backstory *saw* happen: commands, file edits, exit codes. This is fact. |
| **Record** | A note an agent (or you) saved on purpose. It comes in one of five **kinds** (see [section 4](#4-asking-your-agent-to-remember-things)). |
| **Handoff** | The record kind that says "here is where things stand, here is what's next". The newest handoff is what the next session sees first. |
| **Tier** | Who wrote a record. **You** (`human-declared`) means you wrote or corrected it. **Agent** (`agent-declared`) means an agent wrote it. **Inferred** is reserved for a future version. |
| **Possibly stale** | Backstory found evidence that a handoff may be out of date, for example a command failed after it was written. It's a flag, not a verdict. |
| **Superseded** | A record replaced by a newer one. It's kept in history, not shown by default. |
| **Project key** | The exact name Backstory uses for a project, usually the path of its `.git` folder. A few commands take it; `backstory projects` lists them. |

---

## 3. Check that it's working

```
backstory install --check
```

Every line should end in `present`, and the last line should say the daemon is `answering`:

```
claude: mcp-server: present (/home/you/.local/bin/backstory)
claude: session-start-hook: present (/home/you/.local/bin/backstory)
...
daemon: answering (/run/user/1000/backstory/sock)
```

Then, from inside one of your project folders:

```
cd ~/projects/my-app
backstory status
```

```
project: /home/you/projects/my-app/.git
capture: on
budget remaining: 60
```

`capture: on` means Backstory is recording. If something is missing, see
[Troubleshooting](#9-troubleshooting).

---

## 4. Asking your agent to remember things

Agents save records with a tool called `note`. Backstory's instructions already tell them
when, so most records appear without you asking. You can also ask in plain words. There is no
special syntax.

There are five kinds of record.

### `handoff`: where things stand, and what's next

The most important kind. The newest handoff is the first thing the next session reads. A good
one says what is true now, the next step, and anything that must *not* be done.

> **You:** "We're stopping here. Write a handoff."

> **Saved:** `handoff`: "Login form works and is committed. Password reset is not started.
> Don't change the session cookie name; the mobile app depends on it."
> **Next step:** "Build the password-reset email."

The **next step** is optional: one line, up to 200 characters. The panel shows it on the
project's row.

### `decision`: what was chosen and why

Use when you picked one option over another, so nobody undoes it later by accident.

> **You:** "Note a decision: we're using SQLite, not Postgres, because this is a desktop app."

> **Saved:** `decision`: "Storage is SQLite, not Postgres: single-user desktop app, no
> server to run."

Before an agent changes a file that a decision is about, Backstory's instructions tell it to
read that decision first.

### `outcome`: something got done

Use when work is finished. A good outcome points at proof, such as the timeline event of the
passing test run. Backstory treats an outcome with no proof as a claim, not a fact.

> **You:** "Tests pass now. Record that as an outcome."

> **Saved:** `outcome`: "Fixed the date-parsing bug; `npm test` passes (timeline event 4812)."

After a session's first `git commit`, Claude Code, Codex, Hermes and Pi get one short
reminder to record an outcome, if the session hasn't saved anything yet. It never blocks the
agent and appears at most once per session.

### `note`: a fact worth keeping

For anything useful that isn't a decision or a result: where something lives, a gotcha, a
setup detail.

> **You:** "Remember that the staging server needs the VPN on."

> **Saved:** `note`: "Staging (staging.example.com) is only reachable with the office VPN
> on."

### `claim`: "I'm working on this"

A temporary heads-up that one session is working on something, so another agent in the same
project knows. It has an expiry time and lapses on its own. It's a courtesy, not a lock:
nothing is blocked.

> **You:** "Claim the payments module for the next two hours."

> **Saved:** `claim` about `src/payments/`, expires in 2 hours.

### Extras an agent can attach

You rarely need these, but they're why records stay tidy:

- **about**: the files or folders the record is about. Lets an agent find "the decision about
  this file".
- **supersedes**: the id of an older record this one replaces. A new handoff normally
  supersedes the previous one, so old handoffs stop showing up.
- **evidence**: timeline event ids that prove an outcome.

### Things never to put in a note

Passwords, API keys and tokens. Backstory removes common secret shapes before saving
(see [Privacy](#7-privacy-pausing-fixing-and-forgetting)), but don't rely on it.

### Getting memory back out

An agent that starts with the summary already has the essentials, and won't fetch them
again. To dig deeper, just ask:

> "What did we decide about the database?"
> "What was I doing in this project last week?"
> "Show me the backstory on `src/auth/session.ts`."

The agent uses Backstory's `recall` and `timeline` tools to answer. You can read the same
memory yourself with `backstory recall` and `backstory records`
([section 6](#6-command-reference)) or in the panel's Memory view.

---

## 5. The panel

The panel is a pop-up window in the Omarchy desktop that shows your week at a glance and lets
you jump back into any project.

### Opening and closing

- Press **`CTRL + SHIFT + B`**, or
- click the Backstory icon in the top bar (if you added it; see `panel/README.md`).

Press the same key, click the icon again, or press **`Esc`** to close it.

### What's on it

From top to bottom:

- **Header**: "Backstory" and a one-line summary of the week.
- **Here card**: the project you're in right now (the folder of your focused terminal), with
  **Continue**, **Terminal** and **Memory** buttons.
- **NEEDS YOU**: only shown when something needs a look. It can show:
  - a handoff that's **possibly stale** (it has a **Dismiss** button: click it if the handoff
    is still right)
  - a session that ended with **uncommitted changes**
  - a **contradiction**: one record says another is wrong, with evidence
  - an **expired claim** with no outcome recorded
  - Codex hooks that still need approval ([Troubleshooting](#9-troubleshooting))
- **RECENT**: one row per project you worked on this week, newest first. Each row shows:
  - small **chips** for the agents used there, newest first
  - the handoff's **next step**, if there is one
  - a small **from <device>** chip when that handoff, or a record in the Memory view, was
    written on another of your paired devices (nothing shows for records from this machine)
  - a small bar of activity across the week

### What you can do on a row

| Action | Mouse | Keyboard |
|---|---|---|
| Move between rows | | `j` / `k` or `↓` / `↑` |
| **Continue**: open the newest agent in that folder, with the handoff | click **Continue** | `Enter` |
| Show one sub-row per agent | click the agent chips | `→` (fold with `←`) |
| Open that specific agent | click a sub-row | `Enter` on the sub-row |
| Open a terminal in the project's folder | **Terminal** | `t` |
| See what's saved for that project | **Memory** | `m` |
| Refresh | | `r` |
| Help (lists the agent tools too) | | `?` |
| Bigger / smaller / reset text | | `Ctrl` + `+` / `Ctrl` + `-` / `Ctrl` + `0` |
| Close | close button | `Esc` |

### The Memory view

Opens for one project and lists everything saved there, newest first. Each record shows its
tier (**You**, **Agent** or **Inferred**), how old it is, and its first line. A mark appears
if it's not current (for example possibly stale or superseded).

- **Click a record** to read all of it.
- **Edit** opens it in a terminal editor. Saving writes a *new* corrected record that replaces
  the old one, marked as yours. The old one stays in history.
- **Forget** (on one record) deletes it after you confirm.
- **History** toggle: also show superseded and deleted records.
- **Forget activity and saved records** at the bottom erases what Backstory recorded for this
  project. Choose **Last hour**, **Today** or **Everything here**. It first shows how many
  sessions, events and records would go, and only deletes after you confirm.
- **Back** returns to the main view.

### Groups

Some projects are really one piece of work, like a website and its API in two folders. A
**group** shows them as one row in RECENT, with the members underneath.

- Click the **gear** icon, pick a project, then pick an existing group or type a new name, and
  press **+**.
- Or use the command line: `backstory group set <group> --here` from inside the project.

Most projects don't need a group.

---

## 6. Command reference

Every command is `backstory <command>`. Commands that work on "a project" use the folder
you're in unless you pass `--project KEY`. `backstory projects` lists the keys.

**Read-only** commands only look and never change anything. **Human-only** commands are ones
no agent can run on your behalf through Backstory's tools.

### At a glance

| Command | What it does |
|---|---|
| [`status`](#status) | Is Backstory running, which project am I in, is capture on |
| [`this-week`](#this-week) | The panel's view, as text |
| [`projects`](#projects) | List every project Backstory knows, with its key |
| [`records`](#records) | List what's saved for a project |
| [`recall`](#recall) | Read a project's memory as a story |
| [`timeline`](#timeline) | List what Backstory saw happen |
| [`edit`](#edit) | Correct a record |
| [`delete`](#delete) | Delete one record |
| [`affirm`](#affirm) | Clear a "possibly stale" flag on a handoff |
| [`purge`](#purge) | Forget activity (and records) for a session or project |
| [`capture`](#capture) | Pause or resume recording |
| [`group`](#group) | Group related projects into one row |
| [`backfill`](#backfill) | Import past sessions from an agent's history |
| [`export`](#export) | Write a project's memory to a markdown file |
| [`pair`](#pair) | Pair with another computer over ssh |
| [`sync`](#sync) | Share memory with your paired computers |
| [`install`](#install) | Connect Backstory to your agents, or check or remove that |
| [`tools`](#tools) | List the tools agents get |
| [`version`](#version) | Print the installed version |

`daemon`, `mcp`, `hook`, `shell` and `sync-serve` are run by the system, your agents and your
other devices; you don't need to run them yourself. (`sync-serve` is the end a paired device
talks to when it syncs with this one.)

### status

Checks the background service and tells you what it sees for the current folder.

```
backstory status
```

If it says `daemon unreachable`, Backstory isn't running; see
[Troubleshooting](#9-troubleshooting).

### this-week

The same three parts as the panel, as text: things needing attention, where you left off in
each project (with the latest handoff), and per-day activity.

```
backstory this-week
```

```
Where you left off:
- my-app (/home/you/projects/my-app) — last activity 2026-10-19T00:50:16Z; handoff (id 255bf26b-…): Login form works and is committed. Next: password-reset email.

The week:
2026-10-17 · my-app: 3 session(s), 12 file(s) touched, 2 record(s) written
```

Add `--json` for machine-readable output.

### projects

Lists every project with its exact **key**, last activity, and session and record counts.
Read-only.

```
backstory projects
```

```
LABEL               KEY                                       LAST ACTIVITY     SESSIONS  RECORDS
projects/my-app     /home/you/projects/my-app/.git            2026-10-19 15:10  33        26
```

Copy the KEY when another command asks for `--project KEY`.

### records

Lists what's saved for a project, newest first: id, age, kind, tier, status and the text.
Read-only.

```
backstory records                     # the project you're in
backstory records --kind decision     # only decisions
backstory records --history           # include superseded and deleted
backstory records --project KEY       # another project
```

```
3947dcca  16d  decision  agent-declared  current         Storage is SQLite, not Postgres…
090af43a  16d  handoff   agent-declared  possibly-stale  Login form works and is committed…
```

The short id at the start (`3947dcca`) is what `edit`, `delete` and `affirm` take.

### recall

Reads a project's memory as an ordered story, with each record's status and how far the code
has moved since it was written. Read-only.

```
backstory recall                       # summary (default)
backstory recall --altitude headline   # shortest
backstory recall --altitude full       # everything
```

`--budget N` caps the length (in tokens, roughly words); `--project KEY` picks another project.

### timeline

Lists what Backstory observed: commands, tool results, session git state. Read-only.

```
backstory timeline --limit 20          # the last 20 events
backstory timeline --since 2h          # the last two hours
backstory timeline --kind command      # only shell commands
```

`--since` takes a duration (`30m`, `2h30m`) or a date and time like `2026-10-19T09:00:00Z`.

### edit

Corrects a record. Your editor opens with the current text; save and close to write the
corrected version. The new record is marked as yours (**You**) and replaces the old one, which
stays in history. Human-only.

```
backstory edit 3947dcca
backstory edit 3947dcca --file fixed.txt      # take the new text from a file
echo "New text" | backstory edit 3947dcca --stdin
```

The editor is whatever `$VISUAL` or `$EDITOR` is set to.

### delete

Deletes one record. Its text is wiped from the store. Human-only.

```
backstory delete 3947dcca          # asks you to confirm
backstory delete 3947dcca --yes    # no question
```

### affirm

Tells Backstory "this handoff is still right", which clears its **possibly stale** flag until
new evidence turns up. Same as the panel's **Dismiss** button. Human-only.

```
backstory affirm 090af43a
```

### purge

Forgets recorded activity: whole sessions, plus any records written during them. Purged
sessions are never re-imported by `backfill`. Human-only.

**Always try `--dry-run` first.** It shows what would go and changes nothing.

```
backstory purge --project KEY --since 1h --dry-run      # preview: the last hour
backstory purge --project KEY --since 1h                # do it (asks to confirm)
backstory purge --session SESSION_ID                    # one session; leaves records alone
```

`--since` and `--until` take a duration or a date and time. `--location DIR` limits it to the
one row the panel shows for that folder. `--yes` skips the confirmation.

### capture

Pauses or resumes recording for every agent at once.

```
backstory capture off       # stop recording
backstory capture on        # start again
backstory capture status    # which is it?
```

While capture is off, nothing new is recorded and agents can't save notes. Everything already
saved stays where it is.

### group

Groups related projects into one row in the panel and `this-week`. Human-only.

```
backstory group set website --here            # put the current project in group "website"
backstory group set website KEY               # or name a project by key
backstory group clear --here                  # remove it from its group
backstory group list                          # show groups, and projects with none
```

### link

Tells Backstory that a project here is the same project as one on another machine. Two
checkouts of one git repository already match by their normalized remote, so you only need
`link` for a project with no remote, or a workspace, or to override the remote. Human-only:
no agent can set or clear a link. A project with no remote and no link has no sync identity
and never matches anything.

```
backstory link set notes --here               # call the current project "notes" on every machine
backstory link set notes KEY                  # or name a project by key
backstory link clear --here                   # drop the link (the remote, if any, applies again)
backstory link list                           # every project with its sync identity
```

`backstory projects` shows the same identity in its SYNC column. A `sync:<id>` project (made when
another computer sends a project you have no copy of) shows its id with source `synced`.

### pair

Pairs this computer with another one you can reach over ssh, so the two can share memory.
You pair once, on one computer; that sets up **both** sides, and after that [`sync`](#sync)
works from either one. The other computer needs Backstory installed and `ssh <host>` must
work without a password prompt (use an ssh key).

```
backstory pair laptop                         # pair with the computer you reach as "laptop"
backstory pair --back desk laptop             # the laptop reaches this computer as "desk"
backstory pair --remove laptop                # stop syncing with it (by its name or ssh host)
```

It prints the other computer's name. The other computer records this one by name, and
reaches it back by your Tailscale name when you have one: it tries
`<your user>@<your MagicDNS name>`, then `<your user>@<your short host name>`, then
`<your user>@<the address it saw you connect from>`, and records the first that answers
(a name it already has in known_hosts is far likelier to work than a raw tailnet IP; host
keys stay strict and nothing is added to known_hosts). Without Tailscale it uses only the
address. If none answers it records the Tailscale name (or the address). Use
`--back <ssh-host>` (an ssh alias, a name that works there) when none of those will work;
it is the only host tried. The other computer checks that way right away. If it cannot reach this one, `pair` still
succeeds and exits 0, prints one line naming the failed path and the command to run on the
other computer, and that computer's `status` shows this one as not ok until a sync works.
A pairing started from the other side never replaces a host that computer already has for
this one (even when that check fails); only an explicit `--back <ssh-host>` replaces it.
A computer you removed with `--remove` is set up fresh when paired again, as if it were new.

`pair` refuses to pair a computer with itself, and a computer whose Backstory is too old to
sync. `--remove` only stops syncing on the computer you run it on: everything the other
computer already sent stays in your memory, and the other computer keeps syncing with you
until you also run `backstory pair --remove` there.

### sync

Shares memory with every paired computer: it pulls what the other one has that you don't,
then pushes what you have that it doesn't. Run it whenever you like; running it again when
nothing changed moves nothing.

Each computer gets one line: `laptop: pulled 12 (2 already here), sent 486 (42 already on laptop)`.
`--dry-run` counts the same changes, so its line (`would pull 12, would send 486`) matches
the real run's pulled and sent numbers; the "already" counts are the part of them the
receiving side held before.

```
backstory sync                                # every paired computer
backstory sync laptop                         # just one
backstory sync --dry-run                      # say how much would move; change nothing
backstory sync --quiet                        # print nothing unless a computer fails (what the timer runs)
```

Every attempt is recorded, and `backstory status` shows a `sync:` line per computer with
when it last worked and what the latest attempt did: `ok`, `unreachable` (the computer
could not be reached), `error` (it answered but something was wrong, such as no Backstory
there, a version too old to sync, or ssh refusing your key) or `paused` (capture is off
here, so nothing was imported). If any computer is not `ok`, `sync` prints one line saying
which and why, and exits non-zero. A sync cut short is safe to repeat: nothing is lost or
doubled.

When sync is on (at least one paired computer) and it is not working, Backstory says so
loudly instead of letting you resume with the other computer's work silently missing:
`backstory status` prints `SYNC FAILING with <computer>: <reason> (last ok <time>|never)` as
its first line and exits **3** (a daemon that cannot be reached still exits 1, so a script can
tell the two apart; healthy is 0), NEEDS YOU gets a `sync-failing` item, every new session's
memory block opens with a warning, and the timer sends one desktop notification when sync
starts failing and one when it recovers (never one per tick). See
[Sync is failing](#9-troubleshooting).

#### Sharing memory between two computers

1. Install Backstory on both, and make sure `ssh <other-computer>` works with a key.
2. On one of them, run `backstory pair <other-computer>`. Once is enough.
3. Run `backstory sync` on either one whenever you want them to catch up.

To sync automatically: `backstory install sync`. It needs a paired computer, and runs
`backstory sync --quiet` every five minutes from a systemd user timer (`backstory install sync
--check` reports it; `backstory install sync --remove` stops it). A plain `backstory install`
never turns it on. If a sync is already running (yours or the timer's), the second reports
`busy` and does nothing.

Sessions of shared projects sync (when, which agent, which computer), whether or not they wrote a note; nothing of unshared projects does.

Projects match across computers by their git remote; for a project with no remote, use
[`link`](#link) on both. By default ssh runs in batch mode and never asks for a password;
to use another ssh command, set `BACKSTORY_SYNC_SSH` (it is run as
`<command> <host> sh -c '<script>'`, where the script runs `backstory sync-serve` from the other
computer's PATH, or from `~/.local/bin/backstory` when its SSH server, such as Tailscale SSH, gives
commands a bare PATH). If it finds neither, the error names both places it looked.

### backfill

Imports past sessions from an agent's own history, so memory isn't empty on day one. Safe to
run again: it skips what it already imported.

```
backstory backfill claude
backstory backfill codex
backstory backfill hermes
backstory backfill pi
```

### export

Writes a project's memory as a markdown file that other tools can read, for example Claude's
auto-memory or Hermes' `MEMORY.md`.

```
backstory export                          # print it
backstory export --out ~/notes/my-app.md  # write it to a file
```

`--altitude headline` makes it shorter.

### install

Connects Backstory to your agents. `make install` already runs it once. Run it again after you
install a new agent.

```
backstory install                  # set up every agent found on this computer
backstory install claude codex     # only these
backstory install --check          # what's set up? (changes nothing)
backstory install codex --remove   # undo it for one agent
backstory install bash             # also record commands you type in bash
```

Agent names: `claude`, `codex`, `hermes`, `pi`, and `agents` (a generic `AGENTS.md` file, only
installed when named).

### tools

Lists the five tools agents get (`recall`, `note`, `timeline`, `confirm`, `status`) with their
descriptions. The panel's Help (`?`) shows the same list.

```
backstory tools --json
```

### version

```
backstory version
```

---

## 7. Privacy: pausing, fixing and forgetting

**Everything stays on your computer.** Backstory makes no network connections. The store is a
single file, `~/.local/share/backstory/backstory.db`, readable only by you. On a default
Omarchy install your disk is encrypted, so the store is too.

One caveat: when an agent *reads* your memory, that text becomes part of its conversation, and
goes to whichever AI company runs the agent's model, just like anything else you show it.

**Secrets are removed before saving**, for common shapes: API keys (`sk-…`), GitHub and Slack
tokens, bearer tokens, JWTs, AWS key ids, private key blocks, `password=…`, and any
`SOMETHING_KEY=…`, `_TOKEN=`, `_SECRET=` or `_PASSWORD=` value. Other secrets aren't detected,
so don't paste them into a session you want remembered.

### Your controls

| You want to… | Do this |
|---|---|
| Pause all recording | `backstory capture off` (and `capture on` to resume) |
| Keep **one** agent session out entirely | Start it with `BACKSTORY_OFF=1`, e.g. `BACKSTORY_OFF=1 codex` |
| Never record shell commands in a folder | Create an empty file there: `touch .backstory-ignore` |
| Fix a wrong record | `backstory edit <id>`, or **Edit** in the Memory view |
| Delete one record | `backstory delete <id>`, or **Forget** on the record |
| Forget an hour, a day, or a whole project | `backstory purge … --dry-run` first, or **Forget activity and saved records** |
| Say a flagged handoff is still right | `backstory affirm <id>`, or **Dismiss** in NEEDS YOU |

`BACKSTORY_OFF=1` affects only that one program and anything it starts. Other sessions keep
recording as normal.

**Deleted text and backups.** Deleting and purging wipe the text from the store. But Backstory
sometimes keeps a backup copy of the store beside it (files named `backstory.db.pre-…` in
`~/.local/share/backstory/`), and those still hold the old text until you delete them.

**Forgetting reaches your other machines.** If you have paired machines, what you delete or
purge here is forgotten on them at the next sync, and a sync can never bring it back.

**Agents can't erase your memory.** Only you can delete, edit, purge, pause or group. The
tools agents get can add records but never remove them.

---

## 8. Settings

Backstory works with no configuration. Two optional settings change which folders appear in
the panel and `this-week`. Both are environment variables holding a colon-separated list of
folders.

- **`BACKSTORY_PROJECT_ROOTS`**: only list projects inside these folders, for example
  `BACKSTORY_PROJECT_ROOTS=~/projects:~/work`. Keeps scratch folders and config tweaks out of
  the panel. Nothing stops being recorded; it only changes what's listed.
- **`BACKSTORY_WORKSPACE_DIRS`**: folders that hold many projects. The default is `~/projects`.
  A session started in such a folder itself, rather than inside one of its projects, is kept as
  one "workspace" row.

### Where to set them

On Omarchy, desktop-wide settings go in a small file that's read when you log in. Create it once:

```
mkdir -p ~/.config/uwsm/env.d
nano ~/.config/uwsm/env.d/50-backstory
```

Put this line in it, with your own folders, then save (`Ctrl + O`, `Enter`, `Ctrl + X`):

```
export BACKSTORY_PROJECT_ROOTS="$HOME/projects:$HOME/work"
```

**Then log out and back in.** The setting now reaches the panel and every terminal. To check it:

```
echo "$BACKSTORY_PROJECT_ROOTS"
```

**Can't log out right now?** This reaches the panel only, until your next login. Add this
line at the end of `~/.config/hypr/hyprland.lua`:

```
hl.env("BACKSTORY_PROJECT_ROOTS", os.getenv("HOME") .. "/projects")
```

Then run `hyprctl reload` and `omarchy restart shell`.

See [Project roots](../README.md#project-roots) in the README for exactly what counts as in
scope.

---

## 9. Troubleshooting

**`backstory: command not found`**
The program is in `~/.local/bin`, which may not be on your PATH yet. Open a new terminal. If
it still fails, rerun `make install` from the `backstory` folder and read its last lines.

**`daemon unreachable`**
The background service isn't running. Start it and check it:

```
systemctl --user restart backstory
systemctl --user status backstory
```

Its log is `journalctl --user -u backstory -n 50`.

**The panel doesn't open with `CTRL + SHIFT + B`**
`make install` prints a line to add to `~/.config/hypr/hyprland.lua` if it isn't there yet:
`require("hypr.backstory")`. Add it, save, and run `hyprctl reload`. If the panel still doesn't
appear, run `omarchy restart shell`.

**`make install` ran while my screen was locked**
It finishes normally. It never restarts the shell over a lock screen; instead it schedules the
restart for when you unlock (waiting up to 24 hours), so the panel loads then. A restart that
fails is reported, never fatal: run `omarchy restart shell` yourself.

**Codex starts with no memory**
Codex has to trust Backstory once. Open `codex` in a project, choose **"Trust and Continue"**,
then **"Trust all and continue"** for the hooks. Until then, NEEDS YOU shows a reminder.

**Hermes doesn't use Backstory**
Restart Hermes after `backstory install hermes`; a running Hermes doesn't pick up new plugins.

**The panel lists folders I don't care about**
Set `BACKSTORY_PROJECT_ROOTS` ([section 8](#8-settings)), or remove them with
`backstory purge`.

**A handoff says "possibly stale"**
Something happened after it was written, such as a failed command or a later edit to a file it
names. Read it. If it's still right, **Dismiss** it (or `backstory affirm <id>`). If not, ask
the agent for a fresh handoff.

**Sync is failing**
`backstory status` starts with `SYNC FAILING with <computer>: <reason>`. The reason is one of:

- *the latest attempt failed (the text says why)*: the other computer answered but something
  was wrong. If it names a **protocol** or **version** mismatch, update Backstory on **both**
  computers (`make install`) and sync again. If ssh refused your key, fix `ssh <computer>`
  (key loaded, `ssh <computer>` works without a prompt). If Backstory is missing there,
  install it.
- *unreachable for N hours across M attempts*: the computer has been off or off the network
  for longer than 24 hours of recorded attempts. Turn it on, or, if you no longer use it,
  `backstory pair --remove <computer>`. The limit is the `sync_unreachable_hours` setting.
- *the sync timer is installed but not active*: the timer stopped. Run
  `systemctl --user enable --now backstory-sync.timer` (or `backstory install sync`).

Run `backstory sync` by hand to retry; one good sync clears the warning. `sync paused (capture
off)` is not a failure: turn capture on with `backstory capture on`. A computer you paired but
never synced shows `never synced`; run `backstory sync`.

**Undo an update**
`make install` keeps the previous version at `~/.local/bin/backstory.prev`:

```
systemctl --user stop backstory
cp ~/.local/bin/backstory.prev ~/.local/bin/backstory
systemctl --user start backstory
```
