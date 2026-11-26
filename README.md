# okvault

okvault turns an Obsidian vault into a knowledge base your team and your AI assistants share. You write notes in Obsidian as usual. Assistants such as Claude Code look things up in the same notes: they search them, get stored answers to common questions, and record what they learn. The plugin lets you see what they see and keep the notes in good shape.

Notes stay plain Markdown files in your vault, in the [Open Knowledge Format](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md). Nothing is sent to an online service: okvault runs on your computer.

## What the plugin adds

Each view has an icon in the left ribbon and a command in the command palette (search for "okvault").

- **Ask the FAQ.** Type a question and get the answer your assistant would get: a stored FAQ answer when one matches, otherwise the notes that come closest. With an empty box it lists every FAQ.
- **Bundle map.** Your whole vault on one circle. Each folder is an arc, each note is a dot coloured by its type, and links between notes are drawn as curves. Rings mark notes that need attention: broken links, out of date, written by an assistant and not checked yet, drafts, or no owner. Filter by type, folder or problem, and double-click a folder to zoom into it.
- **Note inspector.** For the open note: who wrote it, who checked it and when, who owns it, its links and backlinks, similar notes, and format problems with one-click fixes.
- **Knowledge gaps.** Questions your assistants asked that no note answers yet, most asked first. One click turns a question into a draft FAQ.
- **Versioning and review.** If your team keeps the vault in git, this shows the sync state and has a *Sync now* button. It also lists the notes waiting for a person to check them, with *Mark as checked*.
- **Ticket workspace.** For teams using Jira: a ticket next to the notes about it, and a button to write down what you learned.

The [plugin guide](obsidian-plugin/README.md) describes every view and setting in detail.

## Get started

The plugin works on desktop only (Windows, macOS and Linux). It needs a small companion program, `okvault`, which the plugin starts and stops for you.

### 1. Install the plugin

In Obsidian, open *Settings → Community plugins → Browse*, search for **okvault**, then select *Install* and *Enable*.

### 2. Install the okvault program

okvault is installed with Go, a free tool from Google. You only do this once.

1. Download and run the Go installer for your system from [go.dev/dl](https://go.dev/dl/).
2. Open a terminal: *Terminal* on macOS or Linux, *PowerShell* on Windows. Run:

   ```
   go install github.com/baschen-dev/okvault/cmd/okvault@latest
   ```

3. Tell the plugin where the program is. In Obsidian, open *Settings → okvault* and set **okvault binary** to its full path:

   | System | Path (replace `you` with your user name) |
   |---|---|
   | Windows | `C:\Users\you\go\bin\okvault.exe` |
   | macOS | `/Users/you/go/bin/okvault` |
   | Linux | `/home/you/go/bin/okvault` |

   `go env GOPATH` in the terminal prints the folder before `\bin` or `/bin` if yours is different.

To update okvault later, run the same `go install` command again and restart Obsidian. Keep the program and the plugin on the same version.

### 3. Check it works

The status bar at the bottom of Obsidian shows **okvault ●** when everything is connected. **okvault ○** means the plugin cannot reach the program: click it to see why, and check the path from step 2.

The bundle map works even without the program, and the FAQ box then falls back to matching FAQ titles. Search, review status and the other views need it.

### 4. Connect your AI assistant (optional)

To let Claude Code use the same notes, run this once in a terminal, with the full path of your vault folder:

```
claude mcp add --scope user okvault -- okvault serve --vault "/path/to/your/vault" --author claude-code
```

If the terminal does not find `okvault`, use its full path from step 2 instead. Other assistants that support MCP work too; see [Using okvault without Obsidian](docs/server.md#quick-start).

## Your notes

Any existing vault works. To get the most out of okvault, give each note a `type` in its properties, for example `FAQ`, `Guide` or `Decision`. Notes without one still show up and are searchable, and the note inspector offers to add it.

An FAQ note has the question as its title, other ways of asking in its aliases, and the answer as its text. That is what *Ask the FAQ* and your assistants answer from.

## Working as a team

A team usually keeps the vault in a shared git repository, for example on GitHub. okvault can then commit and share each change for you, either directly or as a pull request your teammates review. It can also link notes to Jira tickets, assign owners to folders, and remind people to review notes that get old.

All of this is set up once for the whole team under *Settings → okvault → Team settings and connections*. The form checks your changes before saving them. *Check connections* on the same page shows whether this computer can reach Jira and GitHub.

If you also use the Obsidian Git plugin, turn off its automatic commit, pull and push, so only one tool writes to git. See [Obsidian Git](obsidian-plugin/README.md#obsidian-git).

## Troubleshooting

- **okvault ○ in the status bar.** The plugin could not start the program. Check **okvault binary** in *Settings → okvault*, then run the command *okvault: Reconnect to the server*.
- **"serves a different vault".** Your vault may contain the okvault notes in a subfolder. Set **okvault root** in *Settings → okvault* to that folder, for example `vault`.
- **A change does not show up.** Run *okvault: Rescan the vault*, or press the refresh button on the bundle map.
- **A button shows an error.** The message comes from okvault. For sync problems, the versioning panel shows the full text.

## What the plugin can access

The plugin reads your vault's notes, runs only the okvault program you set in its settings, and talks only to that program on your own computer. okvault itself contacts Jira or GitHub only if your team set them up. The [plugin guide](obsidian-plugin/README.md#what-the-plugin-can-access) has the details.

## Using okvault without Obsidian

okvault also works on its own, as a command-line tool and an MCP server for agents. [docs/server.md](docs/server.md) covers installing and registering it, the MCP tools, knowledge gaps, team and Jira settings, git sync, the vault layout and every command.

## Contributing

Bug reports, fixes and docs are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). To build and test, see [Development](docs/server.md#development).
