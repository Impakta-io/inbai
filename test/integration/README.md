Status: partial — `TestOneInboxOnStalwart` creates an address, delivers a message, and reads it back. It runs when `INBAI_INTEGRATION=1`. CI does not set that variable.

# Integration tests

`TestOneInboxOnStalwart` starts the Compose stack, runs `inbai init`, leaves recovery mode, and then uses `inbaid` to create `alice@inbai.test`, send a message to that address, and read the body back.

```sh
INBAI_INTEGRATION=1 go test ./test/integration -count=1 -timeout 10m
```

The test needs Docker. It publishes the Compose ports, including 25, 443, and 8080, and removes the project `inbai-live` when it finishes. CI does not start Stalwart.
