# Contributing to okvault

Thanks for helping. okvault is two things in one repository: the `okvault` Go binary (an MCP server and CLI for an OKF vault) and the Obsidian plugin in [`obsidian-plugin/`](obsidian-plugin/README.md). Bug reports, fixes, docs and new FAQ notes for the example vault are all welcome.

## Reporting a problem

Open an [issue](https://github.com/baschen-dev/okvault/issues) and include:

- what you ran or clicked, what you expected, and what happened instead;
- `okvault --version` (or the plugin version from *Settings → Community plugins*) and your OS;
- for the plugin, the Obsidian version, and any error from the developer console (*View → Toggle developer tools*).

Never paste tokens. okvault and the plugin read Jira and GitHub credentials from the environment, and their output names where a credential came from, never its value.

For a security problem, don't open a public issue: use [*Report a vulnerability*](https://github.com/baschen-dev/okvault/security/advisories/new) on the repository's Security tab, which reaches the maintainers privately.

## Setting up

You need Go (the version in [`go.mod`](go.mod)), Node.js 22.6 or later for the plugin, and [`just`](https://github.com/casey/just). On Windows the recipes are under `just win::<task>` and run in PowerShell 7.

```sh
just test          # go vet, unit tests, in-memory MCP and a real stdio smoke test
just lint index    # check the example vault, refresh its generated index.md files
just plugin-test   # plugin: typecheck, then its client against a real `okvault serve`
just plugin        # build the plugin into vault/.obsidian/plugins/okvault/
```

To see the plugin's views without Obsidian, run the preview from `obsidian-plugin/`: `node --experimental-strip-types test/preview/serve.ts`, then open `http://127.0.0.1:8790/` (`/settings-tab` shows the settings tab). It uses a small stub of the Obsidian API, so check real behaviour in Obsidian too.

## Making a change

1. Branch from `main`, keep the change to one topic, and add or update tests with it.
2. Run `just test`, `just lint` and, if you touched the plugin, `just plugin-test`. CI runs the same checks on Linux and Windows, and fails if the vault fails lint or a generated `index.md` is out of date.
3. Open a pull request whose title is a [Conventional Commit](https://www.conventionalcommits.org/): `feat(obsidian): …`, `fix(serve): …`, `docs: …`, `ci: …`. The title labels the pull request and becomes its line in the release notes, so write it for someone reading those notes. Add `!` (`feat!: …`) for a breaking change.
4. Pull requests are squash-merged, so the title is the commit message on `main`.

Some things to keep in mind:

- **The vault stays plain files.** No database or index files in the vault; anything okvault generates (`index.md`) must be reproducible with `okvault index`.
- **Credentials never land in files.** `.okvault.yaml` rejects unknown keys on purpose, so a misplaced `token:` fails loudly.
- **The plugin follows Obsidian's guidelines.** Use Obsidian's helpers (`createEl`, `createSvg`, `createFragment`) rather than `document.create*`, no `any`, sentence case in the UI, and no command names that repeat the plugin name. The [community directory](https://community.obsidian.md/plugins/okvault) scans every release and lists what it finds on the plugin's *Scorecard*.
- **Pin GitHub Actions to a full commit SHA**, with the version as a trailing comment.

## Writing notes for the example vault

Notes in [`vault/`](vault/) follow its own [conventions](vault/conventions/index.md): one concept per file, kebab-case paths, a `type` in the frontmatter, and relative Markdown links rather than wikilinks. Run `just lint index` before committing.

## Releasing

Maintainers release by bumping the plugin version with `just plugin-version X.Y.Z`, merging that, then pushing a `vX.Y.Z` tag. The `release` workflow does the rest; see [Releasing](obsidian-plugin/README.md#releasing).
