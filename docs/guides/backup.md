Status: partial — `inbai backup` and `inbai restore` copy the inbAI database. Message bodies stay in Stalwart and are a separate copy.

# Backup

inbAI state and mail live in different places. A backup of one does not include the other.

The inbAI database holds inbox cursors, the event feed, webhook subscriptions and their secrets, webhook deliveries, the job queue, the suppression list, grants, client credentials, send quotas, idempotency keys, bulk jobs, the audit log, retention periods, sender allowlists, and the signing key. `inbai backup` writes those tables to one file. The file does not contain message bodies. A file written before grants were stored leaves the grant, credential, quota, idempotency, and job tables as they are. A file written before the audit log leaves that table as it is. A file written before retention periods were stored leaves that table as it is. A file written before sender allowlists were stored leaves that table as it is. A file written before the signing key was stored leaves that row as it is. A file written before the job queue was stored leaves that table as it is. A file written before idempotency keys were bound to a request restores each key as completed, and a repeat of any request with that key returns its first result.

Stalwart holds messages and attachments in its own database and volume. Copy those with the tools that administer Stalwart. inbAI does not read Stalwart's tables.

## Take a backup

Stop `inbaid` first so a sweep does not write during the copy. Then take both copies before starting it again.

```sh
export INBAI_DATABASE_URL='postgres://stalwart:password@postgres:5432/inbai?sslmode=disable'
inbai backup --file inbai.json
```

On the Compose network that URL reaches the `inbai` database. The Compose file does not publish port 5432, so run the command where that host name resolves.

The file mode is `0600`. It contains webhook secrets, hashed client secrets, and the signing key. Keep it with the same care as the database password.

For the Compose stack in [deploy/compose](../../deploy/compose/README.md), the Stalwart database is the `stalwart` database in the same Postgres service, and the message volume is the `stalwart` volume. Dump that database with `pg_dump` and archive the volume. Do this in the same window as `inbai backup`.

A message can arrive between the two copies. That is safe in one direction: after restore, the next sweep asks Stalwart for changes since the restored cursor, so mail that arrived after the cursor is still found. A message removed from Stalwart after the inbAI copy can leave an event whose message no longer exists. Read that event, and treat a missing message as already gone.

## Restore

Restore the Stalwart database and volume first, and start Stalwart. Then, with `inbaid` still stopped, replace the inbAI database:

```sh
export INBAI_DATABASE_URL='postgres://stalwart:password@postgres:5432/inbai?sslmode=disable'
inbai restore --file inbai.json
```

Restore replaces the inbAI tables. It does not merge them with rows already there. Start `inbaid` after it finishes.

## Test a restore

Restore into a second, empty `inbai` database. Point `INBAI_DATABASE_URL` at that database, run `inbai restore`, then `inbai backup` again and compare the two files. The event ids, cursors, and suppressed addresses should match. Start `inbaid` against the second database only after that comparison, so it does not write into the copy you are checking.
