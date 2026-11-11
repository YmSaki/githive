---
name: githive-usage
description: How to use githive's MCP tools to read and write a project's issues, tasks, chat, notifications, and users registry, stored directly in git refs. Use this whenever the user asks about issues, tasks, notifications, chat threads, or when you've just cloned/opened a repository that has a refs/projects/ namespace and want project context.
---

# Using githive

githive stores project memory (issues, tasks, chat, notifications, a users
registry) as event-sourced commit chains under `refs/projects/*` in the same
git repository as the code. The data is fetched from the Git remote; a plain
clone normally fetches source branches only, not project refs.

## First thing in a new session

Right after opening a repository that uses githive:

1. For a newly cloned repository, run `githive init` once in that repository
   using the CLI, if shell access is available. It fetches and adopts project
   refs before creating any missing shared configuration. If shell access is
   unavailable, explain that initialization requires the CLI; MCP has no init
   tool. Never infer that project memory is empty from `.git/refs/projects`:
   Git may store refs in packed-refs, and project refs may not be fetched yet.
2. Call the MCP `sync` tool to obtain current remote project memory. If sync
   fails, report the failure and distinguish cached local data from current
   remote state; an empty local list does not prove the project has no work.
3. Call `whoami` to confirm the identity used for writes. If identity is
   missing, report the required local Git user.name/user.email configuration
   before writing; do not invent an actor identity.
4. Call `status` to get unpushed refs, unread notifications and your tasks.
5. Skim `issue_list` / `task_list` (paginated - see below) for open work
   before assuming you know the project's state from files alone. githive's
   issues/tasks are the source of truth for planning discussions the user
   may expect you to already know about.

## Reading is unpaginated where it's small, paginated where it grows

`issue_list`, `task_list`, `chat_list`, and `notify_list` accept `cursor`
and `limit` (default 50, max 200) because these grow without bound over a
project's lifetime. Don't fetch more than you need: filter first (`status`,
`label`, `assignee`, `owner`, `mine`, `unread`), and only page through
results if you actually need the full set. `users_list` has no pagination -
a users/groups registry is bounded by team size.

You can also read a single issue/task/chat thread as a resource, e.g.
`githive://issue/<id>`, instead of calling the corresponding `*_show` tool -
useful if your host surfaces resources distinctly from tool calls.

## Writing does not auto-sync

Unlike the CLI (which pushes after every write by default), MCP write tools
never sync automatically (docs/adr/0011-mcp-no-auto-sync.md) - a long-running
session calling many tools would otherwise pay a network round trip per
call. Your writes land locally right away and are visible to every read
tool immediately, but other clones (and other people) won't see them until
you call `sync`. Call `sync` explicitly:

- before ending a work session where you made changes the user should be
  able to see elsewhere,
- before telling the user "done" on a task that involved writes,
- and periodically during a long session doing many writes, so you don't
  accumulate a large unpushed batch that's more likely to hit a merge
  conflict resolved by retry.

`status`'s `unpushed_refs` tells you what hasn't been synced yet.

## Referring to people

Tools that take a person (`issue_new`'s `assignees`, `task_new`/
`task_reassign`'s `owner`, `notify_post`'s `to` for `user:` targets) accept
either a registered username or a bare email - you don't need to look up
someone's email if you already know their githive username. If you don't
know either, `users_list` shows every registered user.

## Verifying trust

If the user asks whether project history can be trusted (shared/public
repo, onboarding a new collaborator, suspicious activity), call `verify`.
It walks every ref's commit chain checking SSH signatures and registry
admin permissions, and returns `ok: false` with a `reports` breakdown if
anything fails - including simply having no signing configured yet, which
is normal for a project that hasn't adopted signing (docs/11-security.md).
