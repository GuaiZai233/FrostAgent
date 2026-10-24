# Skills

Skills are portable instruction bundles built around `SKILL.md`, YAML frontmatter, and optional `scripts/`, `references/`, and `assets/` resources. FrostAgent imports their file format and applies its own runtime and security controls.

## Import and management

Select an instance and open **Skills → 导入 ZIP**. Supported layouts include:

- `SKILL.md` at ZIP root, with a frontmatter name.
- `<skill>/SKILL.md`, including multiple skill directories.
- Repository wrappers such as `<repo>/.claude/skills/<skill>/SKILL.md`, `.agents/skills/`, `.codex/skills/`, `.gemini/skills/`, and plugin `skills/` directories.

The importer discovers every `SKILL.md` and retains files beneath its directory. Files outside skill directories are not imported. Nested skill roots and duplicate names are rejected. Each batch imports atomically; existing skills are never silently overwritten. Delete the old skill explicitly before importing a replacement.

Imports start disabled. The page provides instruction/resource listings, compatibility warnings, enable/disable, and deletion. Skills belong to one instance and persist in SQL, including binary resources and executable flags; ZIP contents are never extracted on the host. Schema v14 supports SQLite and PostgreSQL. The migration preserves the released Embedding v12/v13 history and existing Skills data from the development v12 layout. Full instance backups include optional `skills.json`; older backups without that member still restore. Configuration-only exports/copies do not include skills.

Limits: 16 MiB uploaded ZIP, 32 MiB expanded import and total resources per instance, 8 MiB per skill, 1024 ZIP members, 100 skills per instance, and 32 KiB UTF-8 `SKILL.md`. Absolute/traversal paths, drive/ADS paths, backslashes, symlinks, special files, case-insensitive duplicates, and file/directory conflicts are rejected.

## Discovery and activation

Adapter sessions isolate active instructions and arguments by sender execution sequence, while keeping the group sandbox shared. Runtime injection clones skill state for the successor attempt so late tools cannot mutate its activation state. Proactive replacements start with fresh skill state. See [runtime injection](runtime-injection.md).

Only enabled skill names, abbreviated descriptions and environment requirements enter the model's catalog. The model calls `load_skill` to activate a relevant skill. Up to four active skills retain their instructions separately from normal conversation history. Their content is reconstructed on each iteration and survives history trimming. Disabling/deleting a skill stops injection and subsequent resource/execution access; resetting the session clears activated skills. Prompt Inspector includes the actual skill context.

Users can invoke an enabled skill with `/skill-name arguments`. `disable-model-invocation: true` makes activation manual-only; `user-invocable: false` blocks manual invocation. `$ARGUMENTS` is replaced with manual invocation arguments. No script executes merely because a skill was imported or loaded.

`read_skill_resource` reads UTF-8 text resources up to 48 KiB from an activated skill. Binary/large resources can be used in the sandbox.

## Script execution and compatibility

`run_skill` prepares all skill files in a fresh directory within the current session's isolated sandbox and executes the supplied command there. The shell variables `SKILL_DIR` and `CLAUDE_SKILL_DIR` point to that directory. The sandbox must be enabled; its namespace includes the instance and session. There is no fallback to host execution. Resource transfer uses bounded shell requests, base64 ZIP data, and Python 3 inside the sandbox. The runtime needs `mktemp`, `printf`, Python 3, and the language/dependencies required by the chosen skill. Executable ZIP files keep only their executable flag; the sandbox restores them as `0755` so shebang scripts can run directly (for example, `./scripts/task.sh`). Other original permission bits are discarded. A shared timeout budget covers preparation and execution, with each remote command receiving the remaining time and the backend retaining its response/cleanup envelope. Preparation failure or timeout stops script execution; command exit codes, timeouts and bounded stdout/stderr are returned faithfully. Session release removes sandbox resources.

Standard frontmatter `name`, `description`, and `compatibility` are recognized. Claude-style missing names use the containing directory; missing descriptions use the first nonempty instruction line. Original files and other metadata are retained.

Claude Code-specific `allowed-tools`, `context`, `agent`, `model`, `hooks`, `mcp_servers`, `paths`, and named `arguments` are surfaced as compatibility warnings and are not interpreted. Shell context interpolation (`!` followed by a backtick) is retained literally and warned about. These bundles require adaptation when they depend on those mechanisms. Format import does not claim identical harness execution semantics.

Skill metadata cannot grant tool permissions. Management requests use existing control-plane authorization and HTTP Host/Origin protection. All skill tool calls pass through the existing security gate.
