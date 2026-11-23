Status: current — an agent without a grant cannot read another inbox. The test is `TestAgentTokenCannotReadAnotherInbox` in [internal/api/isolation_test.go](../../internal/api/isolation_test.go). It covers REST and an MCP `tools/call`.

# Isolation tests

The service layer is the only place that checks a grant. The API test creates an agent, grants it `read` on one inbox, and then:

- `GET` the granted inbox returns the message.
- `GET` the other inbox is `403`, and the mail backend is not called.
- An MCP `tools/call` for `list_messages` on the other inbox is a tool error, and the mail backend is not called.
- The agent cannot `POST /v1/grants`.

A backend does not decide access. A transport does not either.
