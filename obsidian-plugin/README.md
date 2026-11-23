# okvault for Obsidian

An Obsidian plugin that shows the vault the way okvault's agents see it.

- **Ask the FAQ** (right sidebar). Type a question and get the same answer an agent gets from `kb_ask`: the stored FAQ answer with its match confidence, or, when no FAQ matches confidently, the notes an agent would be pointed to. `kb_search` results come below. With an empty box it lists every FAQ. The command *Ask the FAQ about the selection* asks about the selected text.
- **Bundle map** (a tab). This is the vault as an OKF bundle:
  - Each section (top-level folder) is an arc around a circle, and each concept is a dot coloured by its `type`.
  - Links between notes curve through their sections' anchors, so links between the same two sections run together.
  - A ring marks a note that needs a person: broken links, stale, agent edit not yet verified, draft, or no owner. These are `kb_review`'s reasons.
  - Hover a note to see its links. Click a note to open it, or click a section to focus it. Type chips hide or show types.
  - The table under the map lists the same data per section.

- **Versioning and review** (right sidebar, or click `okvault ●` in the status bar). This panel shows:
  - the team's git policy from `.okvault.yaml`, plus what okvault last reported: branch, pull request, unpushed commits and last sync;
  - any sync problem, word for word (a rebase conflict, a failed push). okvault never guesses at conflicts; it stops and tells you.
  - a **Sync now** button, which runs `okvault sync`: it commits your edits, rebases, pushes and updates the pull request;
  - the review queue (`kb_review`), with **Mark as checked** on agent edits and stale notes. That runs `okvault verify` under your git name, so you are the person vouching for the note.

  The command *Mark this note as checked* does the same for the open note.

- **Team settings** (*Settings → okvault*, below the plugin's own settings). This is a form for the vault's `.okvault.yaml`, which the whole team shares:
  - it covers git mode, when your Obsidian edits are committed, background sync, the pull request branch, the review interval, folder owners and the ticket tracker;
  - fields appear only when they apply to the chosen mode, and each field shows problems as you type;
  - **Save** has okvault check the file (`okvault lint` on a copy) before writing it, so a typo never reaches your teammates;
  - comments, key order and any keys the form does not show are kept;
  - you can preview the resulting file before saving;
  - if the file changed on disk since you opened the page, for example because a sync pulled a teammate's change, Save refuses and asks you to reload instead of overwriting it.

Search and FAQ answers come from okvault itself, over MCP (`okvault serve --port`), so the plugin uses exactly the ranking and FAQ thresholds that agents use. The map is built from Obsidian's metadata cache, so it stays live as you edit and works without the server. Without the server, its review rings show only drafts and broken links.

## Install

From the repository root:

```sh
just plugin          # or: just win::plugin
```

This builds the plugin into `vault/.obsidian/plugins/okvault/`; the folder is gitignored. Then in Obsidian go to *Settings → Community plugins* and enable **okvault**. The plugin is not enabled for the team in git, so each person turns it on themselves.

For another vault, copy `main.js`, `manifest.json` and `styles.css` from `obsidian-plugin/` (after `npm ci && npm run build`) into `<vault>/.obsidian/plugins/okvault/`.

## The okvault server

The plugin talks to `http://127.0.0.1:8766/mcp` by default. That is deliberately not okvault's own `--http` port (8765), so a server the plugin starts never takes the port you registered with Claude Code.

- **Automatic (default).** If nothing answers there, the plugin runs `okvault serve --vault <this vault> --port 8766` and stops it when Obsidian closes. `okvault` must be on your `PATH` (`go install` puts it in `$GOPATH/bin`), or set its full path in the plugin settings. It needs a build with `--port` (okvault after the HTTP transport landed).
- **Versioning** (setting, default *Follow .okvault.yaml*). The server follows the team's `git` settings: it keeps `index.md` files current as you edit, commits your edits after `commit_edits` of quiet, and rebases, pushes and opens the pull request in push and pr mode. With git off in `.okvault.yaml` it commits nothing. *Off* starts it `--read-only --git off` instead.
- **Your own server.** Point *Server URL* at a server you run, for example the one Claude Code uses (`http://127.0.0.1:8765/mcp`). The plugin connects to it and does not start another. Writes and git sync then follow that server's flags.

After connecting, the plugin checks that the server serves this vault; if none of its notes exist here it reports "serves a different vault" and stays offline. If you opened the repository root as the Obsidian vault rather than `vault/`, set **okvault folder** to `vault`.

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

`npm test` builds okvault from `../cmd/okvault` unless `OKVAULT_BIN` points at a binary; the preview needs `OKVAULT_BIN` or runs `go run`. The preview loads the real view code with a small stub of the Obsidian API. It is for checking layout and does not replace trying the plugin in Obsidian.
