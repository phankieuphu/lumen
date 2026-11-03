# Install and use lumen

lumen is a desktop SQL client for PostgreSQL, MySQL, and SQLite. Every change
you make through it lands in a local, hash-chained audit log.

> **Preview builds.** Versions below 1.0 are previews. The query editor,
> history, relations diagram, connections, and audit log work. The slow-query
> advisor, monitor, and AI settings screens are visible but say "not available
> yet" until their backend ships (see [plan.md](plan.md)). The apps are not
> code-signed yet, so your system will warn the first time you open one.

## Install

Download the file for your system from the
[Releases page](https://github.com/phankieuphu/lumen/releases). `SHA256SUMS`
lists a checksum for each file.

| System | File | Needs |
| --- | --- | --- |
| macOS 11 or later (Apple silicon and Intel) | `lumen-<version>-macos-universal.zip` | nothing else |
| Windows 10 or 11, 64-bit | `lumen-<version>-windows-amd64.zip` | Microsoft Edge WebView2 (already part of Windows 11 and updated Windows 10) |
| Linux, 64-bit | `lumen-<version>-linux-amd64.tar.gz` | GTK 4 and WebKitGTK 6.0 (below) |

### macOS

1. Unzip the download and move `lumen.app` to **Applications**.
2. Open it. The first time, macOS says *Apple could not verify "lumen" is
   free of malware…* because the app is not notarized yet. Click **Done**
   (not "Move to Trash").
3. Open **System Settings → Privacy & Security**, scroll to **Security**, and
   next to *"lumen" was blocked…* click **Open Anyway**. Enter your password
   and click **Open**. After that it opens normally, until the next update.

On macOS 14 and earlier you can instead right-click `lumen.app`, choose
**Open**, then **Open** again. macOS 15 and later no longer offer that.

Or clear the download flag from Terminal, which also fixes "lumen is
damaged and can't be opened":

```bash
xattr -dr com.apple.quarantine /Applications/lumen.app
```

### Windows

1. Unzip the download, for example to `C:\Program Files\lumen`.
2. Run `lumen.exe`. If SmartScreen shows "Windows protected your PC", choose
   **More info**, then **Run anyway**. This happens because the app is not
   signed yet.
3. If the window stays blank, install the
   [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/).

### Linux

Install the runtime libraries, then unpack and run:

```bash
# Ubuntu 24.04+ / Debian 13+
sudo apt install libgtk-4-1 libwebkitgtk-6.0-4
```

```bash
# Fedora
sudo dnf install gtk4 webkitgtk6.0
```

```bash
# Arch
sudo pacman -S gtk4 webkitgtk-6.0
```

```bash
tar -xzf lumen-*-linux-amd64.tar.gz
./lumen-*-linux-amd64/lumen
```

Saved passwords go to the Secret Service keychain (GNOME Keyring or KWallet),
which desktop sessions normally run. Without one, connections that need a
password cannot be saved; SQLite files still work.

### Build from source

You need Go 1.26+, Node.js 22+, and `make`. On Linux, also the GTK 4 and
WebKitGTK 6.0 development packages (`libgtk-4-dev libwebkitgtk-6.0-dev` on
Ubuntu).

```bash
git clone https://github.com/phankieuphu/lumen.git
```

```bash
cd lumen && make app
```

```bash
./bin/lumen
```

`make app` builds the UI and embeds it in `bin/lumen`. To try the UI in a
browser without Go, run `make ui-dev`: it uses made-up sample data and never
connects to a database.

## Use

### Add a connection

1. Open **Connections** (the plug icon in the left rail) and choose **New**.
2. Pick the engine. For PostgreSQL and MySQL fill in host, port, user,
   password, and database; for SQLite give the path to the database file.
3. Choose the **environment**:
   - **Dev**: no extra checks.
   - **Staging**: marked amber everywhere the connection appears.
   - **Production**: marked red, and every statement that writes data or
     changes the schema asks you to confirm before it runs.
4. Turn on **Read-only** to block every statement that writes. lumen checks
   this before sending the statement and again in the database session.
5. **Test connection**, then **Save**. The password is stored in your
   system keychain, never in lumen's own files.

### Run queries

1. Open the **Query editor** (the `</>` icon) and pick a connection in the
   sidebar.
2. Type SQL and press **Ctrl+Enter** (**⌘ Enter** on macOS) or **Run**. With
   text selected, only the selection runs.
3. Rows stream into the grid in pages, so large results appear quickly. A
   result stops at 100,000 rows; refine the query to see more.
4. **Cancel** stops a running query on the server.

### History, relations, and the audit log

- **History** lists the statements run on the selected connection, newest
  first. Click one to put it back in the editor.
- **Relations** draws the tables and foreign keys of the selected database.
  Click a table to highlight its links, double-click it to query it, and use
  **Copy Mermaid** to paste the diagram into docs.
- **Audit log** records every write and schema change from every connection.
  Entries cannot be edited or deleted, and each one is chained to the one
  before by a hash. **Verify chain** checks that nothing was altered;
  **Export CSV** and **Export JSON** save the filtered entries.

### Where lumen keeps data

lumen's own data (connection profiles, history, and the audit log) is one
SQLite file, `lumen.db`, in:

| System | Folder |
| --- | --- |
| macOS | `~/Library/Application Support/lumen` |
| Windows | `%AppData%\lumen` |
| Linux | `~/.config/lumen` |

Passwords are not in that file; they are in the system keychain.

Command-line options:

| Option | Effect |
| --- | --- |
| `-data-dir <path>` | Keep lumen's data somewhere else |
| `-debug` | Verbose logging to the terminal (never includes passwords) |
| `-version` | Print the version and exit |

### Uninstall

Delete the app (or `lumen.exe` / the unpacked folder). To remove your data
too, delete the folder in the table above. Saved passwords appear in your
keychain under the service name `lumen`.

## Report a problem

Open an issue at <https://github.com/phankieuphu/lumen/issues> with your
system, the lumen version (`lumen -version`), and what happened. Report
security problems as described in [SECURITY.md](../SECURITY.md) instead.
