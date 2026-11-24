Status: partial — `TestOneInboxOnStalwart` exercises inbox creation, mail delivery, bulk generation of 10 addresses, OAuth isolation, MCP reply, event recovery across an `inbaid` stop/start, storage quotas, and `inbai login` when `INBAI_INTEGRATION=1`. `TestTwoReplicasShareStateAndJobs` exercises two `inbaid` replicas sharing PostgreSQL when `INBAI_TEST_DATABASE` is set. CI does not set either variable.

# Integration tests

`TestOneInboxOnStalwart` starts the Compose stack, including `inbaid`, runs `inbai init`, leaves recovery mode, creates `alice@inbai.test`, sends and reads mail, bulk-generates 10 addresses, verifies agent isolation and MCP reply with OAuth credentials, confirms that a message delivered while `inbaid` is stopped still appears on `GET /v1/events` after `inbaid` restarts, reads and updates disk storage quotas (`inbai inbox quota`), and verifies `inbai login`.

```sh
INBAI_INTEGRATION=1 go test ./test/integration -count=1 -timeout 10m
```

`TestTwoReplicasShareStateAndJobs` starts two `inbaid` HTTP servers backed by one PostgreSQL database. It verifies shared signing keys, cross-replica OAuth tokens and isolation on REST and MCP, immediate suspension, `FOR UPDATE SKIP LOCKED` bulk jobs claimed by the second replica, and event feed and signed webhook delivery after the first replica stops. The webhook receiver is a TLS test server on loopback, and both replicas set the operator policy that allows private targets, as `INBAI_WEBHOOK_ALLOW_PRIVATE=true` does for `inbaid`.

```sh
INBAI_TEST_DATABASE='postgres://inbai:inbai@127.0.0.1:5432/inbai?sslmode=disable' go test ./test/integration -count=1 -run TestTwoReplicasShareStateAndJobs
```

`TestOneInboxOnStalwart` needs Docker. It publishes the Compose ports, including 25, 443, and 8080, and removes the project `inbai-live` when it finishes. CI does not start PostgreSQL or Stalwart.
