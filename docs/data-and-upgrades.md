# Data and upgrades

Everything Jamie's Library keeps is in one **data directory**. Choose it with `--data DIR` or `JL_DATA_DIR`; the default is `./data` in the folder the server is started from. It is created on first start.

## What is in it

```
data/
  lock                        held while the server runs
  library.db                  the database
  library.db-wal, -shm        the database's write-ahead log, present while it runs
  tls/                        the local certificate authority and the server's certificates (not made when the app is served over plain HTTP for a proxy)
  local-ca-made               empty marker: says a local certificate authority was made, so a lost tls/ is noticed
  images/                     cover images and the library's logo
  backups/                    one copy of the database per upgrade, see below
  tmp/                        short-lived files; any over an hour old are removed at start and hourly
```

The directory is readable by its owner only (mode 0700) and so is every file in it (0600). Everything the server creates there is made owner-only, and at each start it re-tightens the directory, the database (with its log files), the lock file and `tmp/`. `backups/` and the files in it are made private when an upgrade writes them and are not re-checked later.

## Backing up

**A stopped copy of the data directory is a complete backup.** Stop the server, copy the whole directory, start the server again. To restore, put the copy where `--data` points and start the server.

Checked on the built program:

```
$ jamieslibrary --data ./data      # run, then stop with Ctrl-C or SIGTERM (exit 0)
$ cp -a data copy
$ jamieslibrary --data ./copy      # starts on the copy; GET /api/health answers {"status":"ok",...}
```

Copy only while the server is stopped: a copy taken while it runs can miss what is still in the write-ahead log. A second server cannot be started on a directory that is in use; it exits with

```
another Jamie's Library is already using the data directory "...": stop it first, or give this one its own directory
```

Backups hold private data: accounts and password hashes, every borrower's history, and the key of the local certificate authority in `tls/`. Restoring `tls/` is what lets phones keep trusting a restored server. Keep them as private as the data directory: store them where only the library's administrator can read them.

## Upgrading

Stop the server, replace the program with the new version, and start it. If the new version changes how the data is stored, it upgrades the database itself at start:

1. A database that already holds data is first copied to `backups/pre-<from>-to-<to>-<UTC time>.db` (for example `backups/pre-3-to-5-20261115T221500Z.db`, where the numbers are schema versions). A brand-new database has nothing to copy, and neither does a start with nothing to upgrade.
2. All the changes then run as one step: either every one applies or none does.

A `pre-...` file is an ordinary SQLite database file. It is never removed by the program; delete old ones when you no longer need them.

## When an upgrade refuses to start

The server prints one message and exits with a non-zero status. Your data is as it was before.

| Message begins | Meaning | What to do |
|---|---|---|
| `the upgrade failed at migration NNNN (name): ... Nothing was changed; a copy of the data from before the upgrade is at "..."` | A change in the new version could not be applied. | Nothing was altered, so the previous version of the program still runs on this directory: put it back and keep going. Report the migration number and message to the people who maintain the program. The named copy is there if you need it. |
| `cannot back up the data before upgrading, so nothing was changed: ...` | The copy could not be made (often a full disk, or `backups/` not writable). | Fix what the message names, then start again. |
| `this program is older than the data: the database is at schema version N and this program only knows up to M. Run a newer Jamie's Library on this data directory` | You started an older program on data a newer one has already upgraded. | Start the newer version. To go back for real, restore a copy of the directory taken before the upgrade (or a `backups/pre-...` file as `library.db`, with the server stopped and `library.db-wal` and `library.db-shm` removed). |

To use a `backups/pre-...` file as the data: stop the server, move `library.db`, `library.db-wal` and `library.db-shm` aside, copy the backup file to `library.db`, and start the old version.

## Other start-up errors

| Message | What to do |
|---|---|
| `cannot use the data directory "PATH": ...` | The directory can't be created, or its path runs through something that isn't a directory. Check the path and the permissions on its parent. |
| `the data directory "PATH" is not writable: ...` | Give the account that runs the server write access to it. |
| `cannot restrict "PATH" to its owner (0700 / 0600): ...` | The server doesn't own the file or directory. Make that account its owner. |
| `another Jamie's Library is already using the data directory ...` | Stop the other one, or use a different directory. A crashed server doesn't leave this behind: the lock is released when the process ends. |

Setting errors (`--port must be a port number from 1 to 65535, got "0"`, `JL_LOG_LEVEL must be one of debug, info, warn, error, got "loud"`) name the flag or variable at fault.
