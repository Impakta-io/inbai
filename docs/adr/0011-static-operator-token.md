Status: current

# ADR 0011: Static operator token

- Status: accepted
- Date: 2026-11-24

## Context

[ADR 0006](0006-oauth-client-credentials.md) said operators and agents both call the API with OAuth 2.1 client credentials. The code does not do that for operators. An operator presents one static bearer token. `inbai init` generates it and writes it to `config.json` as `operatorToken`, and `INBAI_OPERATOR_TOKEN` overrides it. `inbaid` compares it with the bearer token on each request and never exchanges it. Agents do use OAuth client credentials, as 0006 describes.

A static token fits the deployment the project targets: one operator, self-hosted. The CLI and `inbaid` already share `config.json`, so both have the token with no extra step. The operator needs no token refresh. A token exchange would also need a first credential to ask for the first token, and that credential would have to come from somewhere, which is the token we already have.

## Decision

An operator authenticates with the static operator token. It must be at least 32 characters. `inbaid` compares it in constant time and never exchanges it. It does not expire. An operator rotates it by editing `operatorToken` in `config.json` (or changing `INBAI_OPERATOR_TOKEN`) and restarting `inbaid`.

Agents authenticate with OAuth 2.1 client credentials, as decided in 0006. `ProvisionAgent` always creates a non-operator credential.

A token only names the caller. The service layer in `internal/service` decides what that caller may do.

This record replaces the part of 0006 that gives operators client credentials. The rest of 0006 stands.

## Consequences

Rotating the operator token is manual and restarts `inbaid`. Anyone who can read `config.json` is the operator. The file is written with mode 0600 in a directory created with mode 0700. There is one operator identity, so the audit log records `operator` and cannot tell two people apart.

`internal/auth` can still create a client credential with the operator flag set, and a token signed for it is accepted as an operator. Nothing issues such a credential. Whether to remove that path is an open question and is not decided here.
