# PostgreSQL

[中文](postgresql.zh-CN.md)

JEC keeps its data in SQLite in the data directory by default, which needs
no other service. It can keep it in PostgreSQL instead, for those who run
their services on a PostgreSQL server and back it up with the rest.
PostgreSQL 14 or later works; JEC is tested against PostgreSQL 18.

## Configuration

Set `JEC_DATABASE_URL`, or `database_url` in `config.yaml`, to a
connection URL:

```
postgres://jec:password@db.example.com:5432/jec?sslmode=require
```

- The URL is read from the environment or the configuration file only,
  never from a flag, which other processes can read.
- JEC creates its tables in the first schema of the search path, `public`
  by default, and keeps them current at each start. The database should
  belong to JEC alone.
- The data directory is still needed: for the master key, the
  configuration file, plugins and scripts.

An excerpt of a Compose file with a PostgreSQL service:

```yaml
services:
  jec:
    image: ghcr.io/xmzo/justenoughcerts:beta
    environment:
      JEC_DATABASE_URL: postgres://jec:change-me@db:5432/jec?sslmode=disable
    depends_on: [db]
  db:
    image: postgres:18
    environment:
      POSTGRES_USER: jec
      POSTGRES_PASSWORD: change-me
      POSTGRES_DB: jec
    volumes:
      - ./postgres:/var/lib/postgresql
```

## Moving from SQLite

1. Stop JEC.
2. Run `jec import-sqlite` with `JEC_DATABASE_URL` set, in the container
   with `docker compose run --rm jec import-sqlite`. It copies `jec.db` of
   the data directory, or the file `-from` names, into PostgreSQL, after
   bringing both up to the current version. A PostgreSQL database that
   holds an account already is refused unless `-replace` is given.
3. Start JEC with `JEC_DATABASE_URL` set. Keep the master key: the
   copied secrets are encrypted with it.

`jec.db` stays as it was; delete it once JEC runs as expected.

## Backups

- **Download a backup** in the settings works as with SQLite: it makes a
  consistent copy of the data as a SQLite file, which either kind of
  database can restore.
- **Restore** in the settings takes such a file: at the next start JEC
  replaces the rows of the PostgreSQL database with the backup's, after
  saving what it held as `jec.db.before-restore-<time>` in the data
  directory.
- PostgreSQL's own tools, such as `pg_dump`, back up the database as any
  other; restore one into an empty database before starting JEC.
