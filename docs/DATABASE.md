# Choosing a database: SQLite, PostgreSQL or MySQL

GWatch keeps everything — nodes, checks, history, events, accounts, settings — in one
database. Out of the box that is an **embedded SQLite file**, `gwatch.db`, in the data
directory. It needs no setup, no server and no account, it is what every release is
tested against first, and it is the right choice for almost every install. If you have
never thought about which database GWatch uses, you do not need to start now.

This page is for the other case: you already run a **PostgreSQL** or **MySQL/MariaDB**
server (a NAS with a database app, a home-lab box, a hosted instance) and you would rather
GWatch kept its history there — for its backups, its disk, or simply so that everything
you run stores its data in one place. Since this feature landed (#34) GWatch can be
pointed at either, with the SQLite file still the default and still working exactly as it
did.

What does **not** change with the backend: the web interface, the API, backups and
restores (an encrypted `.gwbackup` archive is the same whichever database it came from),
the agent, and the way secrets are protected (they are encrypted with the data directory's
`gwatch.key` before they are written, on every backend).

## Which one?

| | SQLite (default) | PostgreSQL | MySQL / MariaDB |
| --- | --- | --- | --- |
| Setup | None | A database and a user on your server | A database and a user on your server |
| Versions | The library inside GWatch | 12 or newer (tested on 16) | MySQL 8.0.13 or newer, MariaDB 10.5 or newer (tested on MariaDB 10.11) |
| Where the data is | `gwatch.db` in the data directory | On the server | On the server |
| Backups | GWatch's own archives, plus a copy of the file | GWatch's own archives, plus your `pg_dump` | GWatch's own archives, plus your `mysqldump` |
| Pre-upgrade safety copy | Made automatically before a schema migration | **Not made** — take a server-side backup before upgrading GWatch | **Not made** — same |
| Concurrency | Single writer | Single writer (see [Performance](#performance)) | Single writer |

The driver for each is compiled into every GWatch binary; nothing extra to install.

## Where the choice is made

The database has to be known *before* it is opened, so it cannot live in the settings
table like everything else under **Settings**. It is decided from four places, and each
one overrides the one above it:

1. **The default**: the SQLite file `gwatch.db` in the data directory.
2. **`database.json` in the data directory.** Written by **Settings › Database**, by
   `gwatch install --db-…` and by `gwatch migrate-db`. This is how the background service
   finds a server: the service is started with `--data-dir` alone, and reads the file.
3. **Environment variables** `GWATCH_DB_DRIVER`, `GWATCH_DB_HOST`, `GWATCH_DB_PORT`,
   `GWATCH_DB_USER`, `GWATCH_DB_PASSWORD`, `GWATCH_DB_NAME`, `GWATCH_DB_SCHEMA`,
   `GWATCH_DB_SSLMODE`, `GWATCH_DB_DSN`. The natural way to configure a container
   ([`DOCKER.md`](DOCKER.md)).
4. **Command-line flags** `--db-driver`, `--db-host`, `--db-port`, `--db-user`,
   `--db-password`, `--db-name`, `--db-schema`, `--db-sslmode`, `--db-dsn`.

Overrides apply field by field: `GWATCH_DB_PASSWORD` alone, on top of a `database.json`
that names the server, replaces just the password. The service log's first lines say
which database was opened and where the answer came from.

**Passwords.** Prefer `GWATCH_DB_PASSWORD` or the settings page over `--db-password`: a
flag is visible to every user of the machine in the process list, and GWatch prints a
warning when it sees one. In `database.json` the password is stored **encrypted** with
the data directory's `gwatch.key`, the same key that protects the SMTP password in the
settings table — a copy of the file on its own does not give the password away, and a
`database.json` copied to another machine without its `gwatch.key` cannot be read.

### `database.json`

```json
{
  "driver": "postgres",
  "host": "db.lan",
  "port": 5432,
  "user": "gwatch",
  "password": "enc:v1:…",
  "database": "gwatch",
  "schema": "gwatch",
  "sslMode": "verify-full"
}
```

| Field | Meaning |
| --- | --- |
| `driver` | `sqlite` (default), `postgres` or `mysql` (`mariadb` is accepted as a spelling of `mysql`) |
| `host`, `port` | The server. Ports default to 5432 and 3306 |
| `user`, `password` | The account GWatch signs in as. Save through the settings page or `gwatch install`/`migrate-db` and the password is encrypted for you; a plaintext password in a hand-written file is accepted and encrypted the next time the file is saved |
| `database` | The database name on the server |
| `schema` | PostgreSQL only: the schema to keep the tables in. Empty means the user's default search path, normally `public` |
| `sslMode` | TLS to the server: `disable`, `prefer` (default), `require`, `verify-ca`, `verify-full` — see below |
| `dsn` | Optional: a complete connection string in the driver's own syntax, in place of `host`…`sslMode`. For a Unix socket, a PgBouncer, or a driver option not covered above |

Delete the file (or choose *SQLite* on the settings page) to go back to the default at
the next start.

### TLS

`sslMode` uses PostgreSQL's names for both drivers:

| `sslMode` | PostgreSQL | MySQL / MariaDB |
| --- | --- | --- |
| `disable` | No TLS | No TLS |
| `prefer` (default) | TLS if the server offers it | TLS if the server offers it |
| `require` | TLS, certificate not verified | TLS, certificate not verified |
| `verify-ca`, `verify-full` | TLS, certificate verified (full also checks the host name) | TLS, certificate verified by the system trust store |

A server with a self-signed certificate that you want verified needs the CA added to the
machine's trust store, or `require` if you accept an unverified connection on a network
you already trust.

## Setting up the server

GWatch creates its own tables the first time it connects, and adds columns and indexes on
upgrades. The user therefore needs to **own its database (or schema)** — the everyday
permissions plus `CREATE` — and nothing on the rest of the server.

### PostgreSQL

```sql
CREATE USER gwatch WITH PASSWORD 'choose-a-long-one';
CREATE DATABASE gwatch OWNER gwatch;
```

That is enough: as the database's owner, `gwatch` may create tables in `public` and
nothing else on the server. To keep GWatch's tables apart from another application's in a
shared database, give it a schema of its own and name it in `schema`:

```sql
CREATE USER gwatch WITH PASSWORD 'choose-a-long-one';
CREATE SCHEMA gwatch AUTHORIZATION gwatch;      -- in the shared database
```

GWatch sets `search_path` to that schema on every connection, so nothing else has to know.
The user needs `CONNECT` on the database (granted to everyone by default) and `USAGE` and
`CREATE` on the schema, which owning it provides. Remember `pg_hba.conf`: the server must
accept password connections from the machine GWatch runs on.

### MySQL / MariaDB

```sql
CREATE DATABASE gwatch CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'gwatch'@'%' IDENTIFIED BY 'choose-a-long-one';
GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, REFERENCES ON gwatch.* TO 'gwatch'@'%';
```

`CREATE`, `ALTER`, `INDEX` and `REFERENCES` are what the schema set-up and later upgrades
need; the first four are the everyday ones. Replace `'%'` with the address GWatch connects
from if you prefer. GWatch's tables use InnoDB and `utf8mb4` whatever the server's
defaults, and set `sql_mode` to include `ANSI_QUOTES` on its own connections only.

### Test it

**Settings › Database › Test connection** opens a connection with the details in the
form, runs `SELECT version()` and reports the answer. Nothing is written — not to the
server, not to `database.json` — so it is safe to try against a database GWatch is not
using yet. The same check runs before anything is saved.

## Moving an existing install: `gwatch migrate-db`

Saving a server under **Settings › Database** changes where GWatch *looks*, not what is
there: a fresh server database starts empty, and after the restart GWatch would show
first-run setup. To take your nodes, history, accounts and everything else along, copy
the SQLite file into the server once:

1. **Set up the server** as above, and (optionally) save the connection under
   **Settings › Database**, using *Test connection* to be sure it works.
2. **Stop GWatch** — `gwatch stop` for the Windows service, or Ctrl+C on a console. The
   copy needs the SQLite file to itself.
3. **Run the copy:**

   ```
   gwatch migrate-db
   ```

   uses the connection saved in `database.json`. Or name it on the command line, with the
   password in the environment:

   ```
   set GWATCH_DB_PASSWORD=…                      (PowerShell: $env:GWATCH_DB_PASSWORD = '…')
   gwatch migrate-db --db-driver postgres --db-host db.lan --db-user gwatch --db-name gwatch --db-sslmode verify-full
   ```

   Add `--data-dir` if GWatch's data is not in the default place. The command prints its
   progress by stage and a final count:

   ```
   Copying C:\ProgramData\GWatch\gwatch.db into postgres://gwatch@db.lan:5432/gwatch
   Connected: PostgreSQL 16.4 …
     configuration      12
     results            48210
     rollups            9130
     events             1456
     hardware readings  20115
   Done: 12 nodes, 31 checks, 48210 results, 9130 rollups, 1456 events, 20115 hardware readings, 2 wallboards, 3 users, 1 API keys, 2 agents.
   Saved the connection to C:\ProgramData\GWatch\database.json.
   ```

   The target must be **empty** — a database GWatch has never started against. If it
   already holds GWatch data (say, from the restart in step 1), run again with
   `--replace` to empty it first. Nothing is ever changed in the SQLite file.
4. **Start GWatch again** (`gwatch start`). The log's first lines name the new database,
   and **Settings › Database** shows it as the one in use.

What comes across is exactly what a full backup restored onto the server would bring:
settings, nodes and checks (with their ids, so history lines up), dependencies,
dashboards, saved charts, maintenance windows, triggers and endpoints, wallboards (with
their projection addresses), user accounts (with their password hashes — everyone can
sign in as before), API keys, hardware agents (with their ids, so their readings still
belong to them) and, as history, raw results, rollups, events and hardware readings.
Browser sessions are not copied: everyone signs in once more. Pairing codes are not
copied: they are short-lived invitations.

The SQLite file is left in place as a fallback. Once you are happy on the server, it can
be archived or deleted; to go back to it, delete `database.json` and start GWatch.

## Backups

**Settings › Backups** works identically on every backend: an archive is a logical
export (configuration as JSON, history as line-delimited JSON), encrypted with the
password you choose, and a restore reads it back through the same code whether the
target is a file or a server. An archive made on SQLite restores onto PostgreSQL and the
other way round, which is also how you would move *from* a server back to the file, or
between servers: back up, point GWatch at the new database, restart, restore.

Archives made by this version (format 2) additionally carry wallboards, user accounts,
API keys, hardware agents and hardware readings. Restoring one **merges** the accounts,
keys and agents — one already present is left alone, so restoring never overwrites the
password of the administrator doing it and never resurrects a key that was deliberately
revoked — and replaces the wallboards. An older archive (format 1) restores as it always
did and leaves all of those untouched. [`RESTORE.md`](RESTORE.md) has the full list.

Two things a server backend cannot do:

- **The pre-upgrade safety copy.** When a new GWatch version changes the schema, on
  SQLite it first copies `gwatch.db` to `gwatch.db.before-vN` so the upgrade can be
  undone. A server database is not a file GWatch can copy, so on PostgreSQL and MySQL
  this step is skipped — the log says so — and **you should take a server-side backup
  (`pg_dump`, `mysqldump`) before upgrading GWatch**, or at least a GWatch backup with
  history. Downgrading GWatch without one is not supported on any backend.
- **Vacuum.** Reclaiming space after large deletions is the server's own job
  (autovacuum, `OPTIMIZE TABLE`); GWatch does not ask for it.

## Performance

GWatch serialises its writes: one connection, one write at a time, on every backend.
That is how it was built for SQLite (where it is the only sane choice) and it is kept
for the servers deliberately — correctness first, and a home or small-office monitor is
nowhere near the limit. Reads use a small pool of their own. A server may not be faster
than the local file for a single GWatch; what it buys you is your own backup tooling,
your own disk, and one place for everything you run.

## Troubleshooting

- **"connect to postgres://… : … password authentication failed"** — the user or
  password is wrong, or `pg_hba.conf` does not allow a password connection from this
  machine.
- **"… permission denied for schema public"** (PostgreSQL 15+) — the user does not own
  the database or schema. Use `CREATE DATABASE gwatch OWNER gwatch` or give it a schema
  of its own, as above.
- **"… Error 1142 … CREATE command denied"** (MySQL) — the grant is missing `CREATE`,
  `ALTER`, `INDEX` or `REFERENCES`.
- **"the database password cannot be decrypted"** — `database.json` was written with a
  different `gwatch.key` than the one in the data directory now. Enter the password
  again under **Settings › Database**, or set `GWATCH_DB_PASSWORD`.
- **The settings page says a restart is needed but nothing changes after one** — a flag
  or environment variable is overriding the file; the log's `database:` line says which.
- **"this database (schema version N) was written by a newer GWatch"** — the same
  rollback guard SQLite has: an older GWatch refuses a database a newer one has upgraded,
  without touching it. Upgrade GWatch again, or restore your server-side backup.

## For developers

Everything the store asks of the database that the three disagree on lives in
`internal/store/dialect.go` and the three files beside it; every query is written once,
in SQLite's form, and translated on its way out. The schema is written once too and
translated per dialect rather than kept in three copies. The whole store test suite, and
the engine, API, backup and hostmon suites, run against all three backends in CI; to do
the same locally:

```bash
GWATCH_TEST_DB=postgres GWATCH_TEST_PG_DSN='postgres://gwatch:gwatch@localhost:5432/gwatch?sslmode=disable' go test ./internal/store/... ./internal/backup/...
GWATCH_TEST_DB=mysql    GWATCH_TEST_MYSQL_DSN='gwatch:gwatch@tcp(127.0.0.1:3306)/gwatch' go test ./internal/store/... ./internal/backup/...
```

Each test creates a schema (PostgreSQL) or database (MySQL) of its own and drops it
afterwards, so the connecting user needs `CREATE` on the server; a throwaway local
server is the intended target.
