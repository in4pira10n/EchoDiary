# EchoDiary

A lightweight, local-first diary and notes app in the Echo family. It uses Go, embedded web assets, vanilla JavaScript, EchoKit, per-entry JSON files, and a rebuildable SQLite index.

## Run

Requires Go 1.25+ and a local EchoKit checkout. When EchoKit is a sibling folder, it is discovered automatically.

```sh
cd EchoDiary
go run .
```

Open `http://127.0.0.1:3001`. Other devices on the same LAN can use `http://<server-ip>:3001`. The default listener is `0.0.0.0`; use `-host 127.0.0.1` for local access only. Stop with Ctrl+C.

```sh
go run . -port 3001 -kit /path/to/EchoKit
# ECHOKIT_DIR also sets the EchoKit directory.
# Choose a folder to own this diary library:
go run . -library "/path/to/My Diary"
# ECHODIARY_LIBRARY can also provide the library root. Without either,
# the process working directory is used as the library root.
```

## Build

Build all supported platforms, or select one platform and architecture. Add
`--package` to create a ZIP for Windows or a tar.gz for macOS/Linux. `--clean`
clears the staging directory before building.

```sh
./build.sh
./build.sh windows amd64 --package
./build.sh macos arm64 v1.2.0 --package
```

Binaries are written under `.build/<platform>-<arch>/`. With `--package`,
archives follow EchoGallery's output rules: Windows amd64 uses
`/Volumes/Users/Public/Downloads` and Linux amd64 uses `/Volumes/share` when
the destination exists and is writable; otherwise packages go to `~/Downloads`.
Other targets also use `~/Downloads`. The script prints each archive's full path.

For a direct local build without the multi-platform script:

```sh
go build -trimpath -o build/EchoDiary .
./build/EchoDiary
```

The executable embeds the app's HTML, CSS, JavaScript, and preview images. EchoKit remains an external runtime dependency. Launching from another working directory is supported; pass `-kit` if EchoKit is not alongside the project or executable.

## Import from Kehua (可话)

Notes default to creation time, newest first. Settings → General → Note sorting
also offers oldest first and title order; the preference is saved per browser.
Consecutive images use a square grid (three columns on desktop, two on phones).
In editing mode, drag an image within its grid to reorder it; on touchscreens,
hold for a moment before dragging. The order is saved in each entry’s JSON file.

In **Settings → General → Import Kehua**, enter the absolute path of an extracted
Kehua export folder on the computer running EchoDiary. The folder must contain
`我的动态/`, or be that directory itself. ZIP upload is not part of this MVP.

Each timestamped entry becomes one note in the current library, with an empty
title and the full body. Paragraphs and image positions are preserved; images
are copied into each note’s `entries/<created-ms>_<uuid>/images/` directory. Original export
dates are interpreted in the server's local timezone. The source is never changed.

Import fingerprints are saved in `.echodiary/storage.json` and indexed in SQLite, so importing the
same entries again skips them without overwriting subsequent edits. Fingerprints
remain valid when either folder moves. Identical timestamp/body pairs count as
duplicates. Missing or unsupported images and videos keep their original textual
references and appear in the import report. This MVP does not play imported videos.
Notes and fingerprints use a recoverable file commit; failures before the commit
leave existing entries intact, and interrupted commits are completed on reopening.
The import flow removes newly staged images when an attempt fails. Imports never replace existing notes or images.

## Application Icons

The canonical EchoDiary icon is `assets/icons/EchoDiary.png`. Derived web assets are generated into `web/static/assets/`: the favicon, Apple Touch Icon, and PWA icons. `assets/icons/EchoDiary.icns` is ready for a macOS app bundle's `Contents/Resources`, while `assets/icons/EchoDiary.ico` is the Windows package icon. On Windows amd64, the optional root-level `rsrc_windows_amd64.syso` embeds the same icon into the executable.

Regenerate the derived files on macOS with:

```sh
python3 -m pip install -r scripts/requirements-icons.txt
sh scripts/generate-icons.sh
```

The source framing is preserved; generated files are only downsampled to the standard platform sizes. The script does not redraw or add a background to the supplied icon.

## Data

Pass `-library "/path/to/My Diary"` or set `ECHODIARY_LIBRARY` to choose the library root. This mirrors EchoGallery's portable-library model: the host selects the root, while the library's private data travels with that folder.

```text
My Diary/
├── entries/                  # Authoritative active notes, outside .echodiary
│   └── <created-ms>_<uuid>/
│       ├── note.json         # Text, rich HTML/Markdown, dates, flags, image order
│       └── images/           # This note’s attachments
└── .echodiary/
    ├── library.json          # Stable library identity (layout version 2)
    ├── storage.json          # Bootstrap state, ordering, durable import fingerprints
    ├── notes.sqlite          # Rebuildable query index
    ├── images/               # Upload/import staging; legacy unclaimed images
    ├── recycle-bin/<id>/     # Recoverable deleted note.json + images/
    └── trash/<id>/           # Existing permanent-delete archive behavior
```

An existing version-1 library is converted on first opening. All note fields,
rich text/Markdown, IDs, timestamps, image order, recycle-bin records, and Kehua
fingerprints are preserved. Referenced images are copied into their owning
entry directories; shared images receive independent copies. Existing `/media/`
URLs continue to work. The legacy flat image folder is retained as staging and
a fallback for older/unclaimed uploads. The manifest is upgraded to version 2,
so older binaries refuse to open the migrated library.

Active entry folders use `<creation-Unix-milliseconds>_<UUID>`. UUID note IDs are
reused; older custom/Kehua IDs receive a stable derived UUID while the JSON/API ID
and import fingerprints remain unchanged. Legacy dates without a zone are read as
UTC+8 on every platform; unknown historical dates use `0` instead of an invented
creation time. Existing ID-named folders are renamed automatically, preserving
all files. Editing content or its displayed creation date does not rename an
already assigned folder.

The JSON files are authoritative. On opening a library, EchoDiary recovers any
pending file commit and rebuilds SQLite from the JSON files and storage metadata.
With the app stopped, `notes.sqlite` (and any `-wal`/`-shm` companions) can be
removed and will be recreated. Do not remove `storage.json`: it preserves first-run
state and import history, including permanently deleted imported entries. Stop the
app before editing JSON files manually; invalid JSON stops opening rather than
being overwritten from the index. Only one new-version process can open a library
for writing at a time. Back up the entire library, including `entries/` and
`.echodiary/`; copying only `.echodiary/` no longer copies the notes.

For a controlled migration, stop older EchoDiary processes, back up the complete
library folder, then run:

```sh
go run ./cmd/migrate-library -library "/path/to/My Diary"
```

The command performs migration without starting HTTP, checks managed attachments,
and reports note/trash/image counts. Running it again is safe. To test an index
rebuild or deletion without changing the original library, use a complete copy.

The older app-local `.echodiary-data` import remains supported: when the selected
library has neither a database nor file-storage metadata, SQLite is snapshotted
and images are copied. The source is never removed. Conflicting images stop the
copy. A marker is written only after a successful copy; a missing SQLite index
in an already file-backed library never triggers this legacy import.

The first browser to open a fresh library can import its valid legacy local cache. A new library with no cache starts empty; there are no built-in sample notes. After initialization, an empty library stays empty. Notes and image files are stored on the server, so browsers on the same LAN see the same notes. Appearance preferences remain browser-local.

On first launch, initialize EchoDiary from a browser on the server host by creating its single administrator account and choosing the initial library folder. This one-time local-only setup prevents another LAN user from claiming the administrator account first. Later launches show the login page and can be accessed from other devices. Passwords are PBKDF2-SHA256 hashes in the host's user configuration directory. Login sessions use signed, HttpOnly, SameSite cookies and survive server restarts for up to 14 days; signing material is stored in the mode-`0600` settings file. The settings file defaults to the platform user-config location under `EchoDiary/settings.json`; `ECHODIARY_CONFIG` can override it.

The settings page can add an existing library or create a folder on the server host, star libraries, and switch the active library without restarting. Enter a host path such as `/Volumes/Diary` or `C:\\Users\\Name\\Diary`; the browser cannot browse the server's filesystem. If the folder already contains `.echodiary`, its existing manifest, entries, metadata, and images are reused without clearing them. Library paths and stats are only exposed after login. The app does not provide a library-folder delete action.

All diary, image, and library-management API routes require an authenticated session. First-run setup is available only until the administrator is created. Keep the service on a trusted private network and do not expose it directly to the public internet; use a trusted network or HTTPS reverse proxy when accessing it remotely.

## API

- `GET /api/notes` returns notes and whether first-run bootstrap is pending.
- `POST /api/notes/initialize` imports first-run notes once.
- `PUT /api/notes/{id}` creates or updates a note.
- `DELETE /api/notes/{id}` deletes a note.
- `GET /api/auth/status`, `POST /api/auth/setup`, `POST /api/auth/login`, and `POST /api/auth/logout` manage the local administrator session.
- `GET /api/libraries` lists registered libraries; `POST /api/libraries` adds and activates one. Library `activate`, `favorite`, `rename`, and `relocate` actions are also authenticated. Relocation only accepts an existing `.echodiary` directory with the same manifest ID.

## UI

The responsive interface uses EchoKit tabs, glass actions, modals, and appearance controls. Appearance follows the system by default; explicit light/dark choices are remembered per browser.

## Structure

```text
main.go                 Entry point
internal/server/        HTTP server, EchoKit discovery, graceful shutdown
web/embed.go            Embedded app assets
assets/icons/           Canonical icon and desktop platform exports
web/static/             HTML, CSS, JS, PWA assets, and preview images
build/                  Local build output (ignored)
```

## Rich text and Markdown

The editor supports native bold, italic, and underline shortcuts (Command on macOS,
Ctrl on Windows), without a formatting toolbar. Rich
HTML pasted from another app is cleaned before insertion; scripts, event handlers,
and arbitrary source styling are removed. Preview and reopening retain formatting.

Rich-text editing also recognizes Markdown shortcuts as you type: `# ` through
`###### ` create headings (including changing an existing heading level); `- `, `* `, and `+ ` create bullet lists; `1. ` creates
an ordered list; `> ` creates a quote; and three backticks followed by a space
create a code block. Paired `**bold**`, `*italic*`, `~~strikethrough~~`, and
backticks for inline code format the typed span immediately. Native undo reverses
the conversion. Shortcuts are skipped inside code, links, and image controls,
and wait until IME composition finishes. Existing text is not converted on open.

The bottom **MD** tab opens editable Markdown source. It supports headings, emphasis,
strikethrough, lists and task lists, blockquotes, fenced code, links, images, and
GFM tables. Paste Markdown into this tab and use the eye tab to preview, or switch
to rich text to edit the rendered document. Plain-text paste into rich text remains
literal. Existing plain-text notes are not automatically interpreted as Markdown.

Markdown source is retained exactly until the rendered document is changed in rich
text mode. Switching back to MD after a rich-text edit generates Markdown from that
document; underline and tables may use safe embedded HTML to preserve formatting.
Uploaded images work in either mode and remain in the library's image directory.

SQLite uses an additive migration for rich HTML, Markdown source, and source format
on both notes and trash. The existing plain body remains available for summaries
and sharing. HTML is sanitized again by the server before saving. The frontend
vendors Marked, DOMPurify, and Turndown locally (licenses and versions under
`web/static/vendor`), so these features do not require a CDN at runtime.


## Organizing notes

The home view switches between all notes and custom folders. Folder view always includes a **Root**
card, even when there are no custom folders. Open Root to view all notes. Custom
folders appear alongside it; each card uses a member diary's first image as its
cover when available. Use the plus action in folder view to create folders; within a note list it creates notes. Rename or delete
folders from their context menu, and move notes using a note's context menu or the
selection toolbar. Deleting a folder preserves its notes. Folder membership is
stored atomically in `.echodiary/folders.json` and does not rename entry directories.
Moving a note assigns it to one folder; moving to All notes removes its assignment.
Trashing and restoring a note preserves its folder membership.

Sort notes by creation date, modification date (either direction), or title in
Settings. Escape returns from an open diary to its original list or folder, then
from a folder to the folder overview. It closes overlays or selection first and
does not navigate between main tabs. The third main tab, Handoff, is reserved and
currently contains no content.

## iOS Shortcuts handoff

The Handoff tab creates revocable, library-scoped bearer keys for listing note choices and appending clipboard text or Markdown without a login cookie. Keys may also be restricted to one note. See [the setup and API guide](docs/handoff.md).
