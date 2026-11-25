# okvault for Obsidian

An Obsidian plugin that shows the vault the way okvault's agents see it.

- **Ask the FAQ** (right sidebar). Type a question and get the same answer an agent gets from `kb_ask`: the stored FAQ answer with its match confidence, or, when no FAQ matches confidently, the notes an agent would be pointed to. `kb_search` results come below. With an empty box it lists every FAQ. The command *Ask the FAQ about the selection* asks about the selected text.
- **Bundle map** (a tab). This is the vault as an OKF bundle:
  - Each section (top-level folder) is an arc around a circle, and each concept is a dot coloured by its `type`.
  - Links between notes curve through their sections' anchors, so links between the same two sections run together.
  - A ring marks a note that needs a person: broken links, stale, agent edit not yet verified, draft, or no owner. These are `kb_review`'s reasons.
  - Hover a note to see its links. Click a note to open it, or click a section to focus it.
  - Type and section chips filter the map. A filtered-out note leaves the map completely: its dot, its links, its place on the circle, and an emptied section's arc and table row. The remaining notes spread over the whole circle, and colours stay the same. *Show all* clears the filters. Filters are saved with the plugin's settings.
  - The table under the map lists the same data per section.

- **Versioning and review** (right sidebar, or click `okvault ●` in the status bar). This panel shows:
  - the team's git policy from `.okvault.yaml`, plus what okvault last reported: branch, pull request, unpushed commits and last sync;
  - any sync problem, word for word (a rebase conflict, a failed push). okvault never guesses at conflicts; it stops and tells you.
  - a **Sync now** button, which runs `okvault sync`: it commits your edits, rebases, pushes and updates the pull request;
  - the review queue (`kb_review`), with **Mark as checked** on agent edits and stale notes. That runs `okvault verify` under your git name, so you are the person vouching for the note.

  The command *Mark this note as checked* does the same for the open note.

- **Note inspector** (right sidebar; follows the open note). It answers "can I trust this note, and who looks after it?":
  - **Trust:** written by an agent or a person, who checked it and when, whether an agent changed it since, and the days until its review date (`stale_after`). It has a **Mark as checked** button.
  - **Owners and tickets:** owners, including folder defaults from `.okvault.yaml`. Ticket chips open the ticket workspace or the tracker.
  - **Format:** this note's `okvault lint` findings, with quick fixes. You can set a missing `type` or description, add a title, or rename the file to kebab-case; Obsidian updates the links to it.
  - **Links:** backlinks, outgoing links and broken links, as okvault resolves them.
  - **Similar notes:** ranked by the same search agents use, so near-duplicates get merged instead of multiplied.
- **Ticket workspace** (right sidebar). Type a key such as `PROJ-123`, run *Open the ticket under the cursor*, or click a ticket key in reading view. Ticket keys become links only for the projects listed in `tracker.projects`.
  - It shows the ticket from Jira (summary, status, assignee, description, recent comments) next to the notes linked to it, mentioning it, or likely relevant (`kb_ticket`).
  - **Record what we learned** creates a note from a `_templates/` template, with `tickets: [PROJ-123]` filled in, in the folder where that type of note usually lives.
  - With no ticket open, it lists every ticket the vault documents.
  - Without Jira credentials it still shows the vault's own notes.
- **Knowledge gaps** (right sidebar). Lists the questions agents asked that no FAQ answers confidently, most asked first, from `kb_gaps`. Each gap shows its other phrasings, who asked and when, and the notes agents read instead. Three actions:
  - **Answer this** creates an FAQ with the question as its title and the other phrasings as aliases. The note starts with `status: draft` and links to the related notes. A draft never answers `kb_ask`, so agents don't get a half-written answer. Remove `status: draft` once the answer is written.
  - **Add as aliases** fixes a near miss: an FAQ that came close (50% or more) and probably answers the question already.
  - **Dismiss** hides a question the vault should not answer.

  Gaps an FAQ now answers drop out on their own. The questions are recorded per machine by okvault, outside the vault (see the main README). Your own searches in the FAQ panel are not counted. Agents' okvault must be recent enough to record gaps.
- **Connections (this machine)** (*Settings → okvault → Team settings and connections*; before Obsidian 1.13, on *Settings → okvault* itself). **Check connections** runs `okvault connections`. The plugin stores no tokens: okvault finds them as it always does, and the check shows where they came from, never their values.
  - **Jira** (`JIRA_API_TOKEN`, plus `JIRA_EMAIL` on Jira Cloud): who you are signed in as, and the projects you can see as a checklist with a filter. **Use for this vault** puts the site and the selected projects into Team settings below, where you review them and press Save. Selected keys Jira no longer returns stay selected and are marked.
  - **GitHub and git:** the repository, the API token source (`GH_TOKEN`, `GITHUB_TOKEN` or `gh auth login`), whether you can push, and a `git push --dry-run` that tests git's credential manager or SSH key without sending anything.
  - Problems come with what to do, for example `gh auth login` and `gh auth setup-git`, or where to create a Jira API token. Obsidian reads environment variables when it starts, so restart it after setting one.
- **Team settings** (*Settings → okvault → Team settings and connections*, below the connections check; before Obsidian 1.13, below the plugin's own settings). This is a form for the vault's `.okvault.yaml`, which the whole team shares:
  - it covers git mode, when your Obsidian edits are committed, background sync, the pull request branch, the review interval, folder owners and the ticket tracker;
  - fields appear only when they apply to the chosen mode, and each field shows problems as you type;
  - **Save** has okvault check the file (`okvault lint` on a copy) before writing it, so a typo never reaches your teammates;
  - comments, key order and any keys the form does not show are kept;
  - you can preview the resulting file before saving;
  - if the file changed on disk since you opened the page, for example because a sync pulled a teammate's change, Save refuses and asks you to reload instead of overwriting it.

Search and FAQ answers come from okvault itself, over MCP (`okvault serve --port`), so the plugin uses exactly the ranking and FAQ thresholds that agents use. The map is built from Obsidian's metadata cache, so it stays live as you edit and works without the server. Without the server, its review rings show only drafts and broken links.

## Install

**From Obsidian.** In Obsidian go to *Settings → Community plugins → Browse*, search for **okvault**, then install and enable it. It needs the `okvault` binary (see [The okvault server](#the-okvault-server)). The plugin is also listed in the [community directory](https://community.obsidian.md/plugins/okvault).

**From a release.** Each [GitHub release](https://github.com/baschen-dev/okvault/releases) carries `main.js`, `manifest.json` and `styles.css`. Put the three files in `<vault>/.obsidian/plugins/okvault/`, or install with [BRAT](https://github.com/TfTHacker/obsidian42-brat) (*Add beta plugin*, `baschen-dev/okvault`), which also updates it.

**From source.** From the repository root:

```sh
just plugin          # or: just win::plugin
```

This builds the plugin into `vault/.obsidian/plugins/okvault/`; the folder is gitignored. Then in Obsidian go to *Settings → Community plugins* and enable **okvault**. The plugin is not enabled for the team in git, so each person turns it on themselves.

For another vault, copy `main.js`, `manifest.json` and `styles.css` from `obsidian-plugin/` (after `npm ci && npm run build`) into `<vault>/.obsidian/plugins/okvault/`.

### What the plugin can access

Obsidian's directory scan flags these, so here is why the plugin needs each one:

- **Running programs** (`child_process`): it starts `okvault serve` for this vault, and runs `okvault sync`, `verify`, `lint` and `connections` when you press the matching buttons. It runs only the okvault binary set in its settings. Team settings are checked by piping the draft `.okvault.yaml` to `okvault lint --config -`, so the plugin never touches files outside the vault API (okvault 0.4.4 or later; an older okvault says it is too old to check settings).
- **Every file in the vault**: the bundle map, note inspector and ticket workspace read all notes' metadata from Obsidian's cache.
- **Network**: it talks only to the okvault server at *Server URL*, by default `http://127.0.0.1:8766/mcp` on this machine. okvault itself reaches Jira and GitHub when the team has set them up.
- **base64**: this comes from the bundled `yaml` library, which supports YAML's `!!binary` type. The plugin does not use it.

Release assets carry GitHub build provenance attestations: `gh attestation verify main.js --repo baschen-dev/okvault`.

## The okvault server

The plugin talks to `http://127.0.0.1:8766/mcp` by default. That is deliberately not okvault's own `--http` port (8765), so a server the plugin starts never takes the port you registered with Claude Code.

- **Automatic (default).** If nothing answers there, the plugin runs `okvault serve --vault <this vault> --port 8766` and stops it when Obsidian closes. `okvault` must be on your `PATH` (`go install` puts it in `$GOPATH/bin`), or set its full path in the plugin settings. It needs a build with `--port` (okvault after the HTTP transport landed).
- **Versioning** (setting, default *Follow .okvault.yaml*). The server follows the team's `git` settings: it keeps `index.md` files current as you edit, commits your edits after `commit_edits` of quiet, and rebases, pushes and opens the pull request in push and pr mode. With git off in `.okvault.yaml` it commits nothing. *Off* starts it `--read-only --git off` instead.
- **Your own server.** Point *Server URL* at a server you run, for example the one Claude Code uses (`http://127.0.0.1:8765/mcp`). The plugin connects to it and does not start another. Writes and git sync then follow that server's flags.

After connecting, the plugin checks that the server serves this vault; if none of its notes exist here it reports "serves a different vault" and stays offline. If you opened the repository root as the Obsidian vault rather than `vault/`, set **okvault root** to `vault`.

**okvault root** (setting) is the okvault folder, always relative to the Obsidian vault. Empty, `.` and `/` all mean the vault itself, which is the default. `./docs/kb/` and `docs\kb` both mean `docs/kb`. A path that leaves the vault (`..`) is refused. Notes outside the root are left out of every view, and the root's `.okvault.yaml` and `.vaultignore` are the ones used.

**`.vaultignore`** at the okvault root lists files the server does not index (see [Vault layout](../README.md#vault-layout)). The plugin applies the same rules, so ignored notes are missing from the bundle map, the FAQ and the inspector as well. Obsidian sends no events for dotfiles, so the plugin reads the file again every 30 seconds, and also when you press refresh on the bundle map.

## Obsidian Git

Let one tool write to git. In an okvault vault that tool is okvault; keep Obsidian Git for history and diffs. We ran Obsidian Git's commit-and-sync (stage, commit, fetch, merge, push) in a loop next to `okvault serve`, with a teammate pushing to `main`. No edits were lost, but:

- **push mode:** Obsidian Git always commits first, so history becomes `vault backup:` commits and merge commits instead of okvault's `docs(kb):` commits rebased on `main`.
- **pr mode:** okvault works on `okvault/<you>`, which has no upstream, so Obsidian Git's push fails on every sync. If you let it set an upstream, it pushes the same branch okvault force-pushes.
- **Both modes:** neither tool honours the other's lock. okvault hit git's `index.lock` while Obsidian Git was committing; it recovered on its next pass, but Obsidian Git shows the same error to you.
- **Bypassing review:** in pr mode, anything Obsidian Git commits while you are on `main` (before okvault switches to your branch, or with okvault not running) is pushed straight to `main`. Protect `main` on GitHub if reviews must be mandatory.

When git is on and Obsidian Git is enabled with automatic commit, pull or push (or, in pr mode, with push enabled), the versioning panel lists the settings to change and opens Obsidian Git's settings. It does not change them for you.

The plugin is desktop only, because it starts a local process and talks to localhost.

## Development

```sh
npm ci
npm run dev        # rebuild main.js on change
npm run check      # tsc
npm test           # MCP client, bundle model, and sync/verify in a scratch repo with a bare remote
node --experimental-strip-types test/preview/serve.ts   # views in a browser, http://127.0.0.1:8790/
```

CI builds and tests the plugin on every pull request; the build is attached to the run as the `okvault-obsidian` artifact.

### Releasing

1. `just plugin-version 0.4.2` (or `just win::plugin-version 0.4.2`) sets the version in `manifest.json`, `package.json` and `package-lock.json`, copies `manifest.json` to the repository root, and adds the version to the root `versions.json` with the plugin's `minAppVersion`. Merge that change.
2. Tag `main` with the same version: `git tag v0.4.2 && git push origin v0.4.2`. Don't create the release yourself.

The `release` workflow checks that the tag matches both manifests, runs the tests, builds, and publishes the release with generated notes under the bare version tag (`0.4.2`) on the same commit. That is the one release per version: Obsidian's directory only installs from a release tagged exactly with the manifest version, while `v0.4.2` stays a plain git tag for Go (`go install …@v0.4.2`, `git describe`). Re-running the workflow replaces the release's assets.

### Community plugin directory

okvault is listed in Obsidian's [community directory](https://community.obsidian.md/plugins/okvault). The directory reads `manifest.json` and `versions.json` at the root of `main` (`just plugin-version` writes both), and installs from the GitHub release tagged exactly with that version (no `v`). So a merged version bump plus its `vX.Y.Z` tag is all a store update needs; the workflow publishes the release the directory expects. If the directory doesn't notice a new version, use *Check for new releases* on the plugin's page under your community.obsidian.md account. The directory scans each release; its results are on the plugin's *Scorecard* tab.

`npm test` builds okvault from `../cmd/okvault` unless `OKVAULT_BIN` points at a binary; the preview needs `OKVAULT_BIN` or runs `go run`. The preview loads the real view code with a small stub of the Obsidian API. It is for checking layout and does not replace trying the plugin in Obsidian.
