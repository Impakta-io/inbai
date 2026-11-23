Status: current

# ADR 0006: OAuth 2.1 client credentials

- Status: accepted
- Date: 2026-11-23

## Context

Operators and headless agents both need to call the API. A protected HTTP MCP server is an OAuth resource server. Replicas must verify a caller without a shared session store.

## Decision

`inbaid` is the authorization server and the resource server. Callers use OAuth 2.1 client credentials. Access tokens are short-lived and signed, and they only identify the caller. Grants are loaded from PostgreSQL on each request. The server publishes authorization-server metadata and protected-resource metadata. The CLI and `inbai mcp serve` perform the token exchange. Human sign-in through an external OIDC provider is a later decision.

## Consequences

One mechanism covers the CLI, REST, and MCP. Revocation takes effect on the next request. The project does not embed a full identity provider in the first release.
