Status: current

# IMAP and SMTP

This backend is for a mail server that already has the mailboxes. It does not create accounts. Stalwart remains the backend that provisions them.

`inbaid serve` uses this backend when `INBAI_MAIL=imap`. The account file is JSON. The object key is the account id the API uses, and it is the value passed as `inbox` to MCP.

```json
{
  "ada": {"user": "ada@example.com", "password": "the-mailbox-password"}
}
```

Keep that file mode `0600`.

```sh
# At least 32 characters
export INBAI_OPERATOR_TOKEN="$(openssl rand -hex 32)"
export INBAI_MAIL=imap
export INBAI_DOMAIN=example.com
export INBAI_IMAP_ADDRESS=mail.example.com:993
export INBAI_SMTP_ADDRESS=mail.example.com:587
export INBAI_IMAP_SECURITY=tls
export INBAI_SMTP_SECURITY=starttls
export INBAI_IMAP_ACCOUNTS=/path/to/accounts.json
export INBAI_AUTHSERV_ID=mail.example.com
inbaid serve
```

Port 993 is IMAP over TLS. Port 587 is SMTP submission, which starts in plaintext and upgrades with STARTTLS. Both modes are the defaults for those ports, so the two `SECURITY` lines can be left out. At startup `inbaid` prints the mode it uses for each server.

## Transport security

| Setting | Values | Default |
| --- | --- | --- |
| `INBAI_IMAP_SECURITY` | `tls`, `starttls`, `none` | `tls` for port 993, `starttls` for port 143 |
| `INBAI_SMTP_SECURITY` | `tls`, `starttls`, `none` | `tls` for port 465, `starttls` for ports 587 and 25 |
| `INBAI_ALLOW_PLAINTEXT_AUTH` | `1` | unset |
| `INBAI_IMAP_TLS_INSECURE` | `1` | unset |

- `tls` starts TLS before the first protocol byte.
- `starttls` connects in plaintext and runs `STARTTLS` before it logs in. If the server does not offer `STARTTLS`, the request fails. The password is not sent, and the connection does not fall back to plaintext.
- `none` sends the mailbox password in plaintext. It is allowed only for `localhost`, `127.0.0.0/8`, and `::1`. For any other host, startup stops unless `INBAI_ALLOW_PLAINTEXT_AUTH=1` is set.
- A port without a default, such as 3143, needs an explicit setting, or startup stops.

`inbaid` verifies the server certificate against the system roots and the host name in the address. `INBAI_IMAP_TLS_INSECURE=1` turns that check off for both servers, and `inbaid` prints a warning when it is set. `INBAI_TLS_INSECURE` is for the Stalwart certificate only. It is ignored in IMAP mode, and `inbaid` says so at startup when it is set.

`INBAI_IMAP_TLS=1` is deprecated. It means `INBAI_IMAP_SECURITY=tls` and `INBAI_SMTP_SECURITY=tls` for each one that is not set, and `inbaid` prints a deprecation notice at startup. Implicit TLS does not work on port 587, so a deployment that uses port 587 with `INBAI_IMAP_TLS=1` needs `INBAI_SMTP_SECURITY=starttls`.

When the SMTP server refuses the sender, a recipient, or the message, the API error shows the reply code and the server's text, for example `mail server refused the request: 550: 5.1.1 no such user`. Connection and login failures are logged on the server and not shown to the caller.

`INBAI_AUTHSERV_ID` is the authserv-id your mail server writes in the `Authentication-Results` headers it adds, usually its host name, for example `mail.example.com`. A message read reports SPF, DKIM, and DMARC only from the top-most header with that authserv-id. When it is unset, a message read reports none of them. See [untrusted mail](../../docs/agents/untrusted-mail.md).

The event cursor is `uidvalidity:uidnext`. A new UID is a created event. A message that is deleted is not reported, because a UIDNEXT cursor cannot see it. If the server changes UIDVALIDITY, the sync stops and the stored cursor stays where it was.

Message ids are IMAP UIDs. List and search return the subject. Get returns the text body and the `From` and `Reply-To` headers. An attachment on send is a MIME part.

Mail is sent as the account's `user`, so `user` must be an email address. This backend has no alias list. A `from` other than `user` is refused with `403`.

## Retention

Retention is off in IMAP mode. `inbaid` does not delete mail unless you turn retention on. `GET /v1/retention` reports `0` days.

To turn it on, set `INBAI_RETENTION_DAYS` to a number of days before `inbaid serve`, or run `inbai retention --days <n>`. A period set with `inbai retention` overrides `INBAI_RETENTION_DAYS`, and without `INBAI_DATABASE_URL` it lasts only until `inbaid` restarts. `INBAI_RETENTION_DAYS=0` keeps mail. A negative or non-numeric value stops startup.

Warning: once retention is on, `inbaid` permanently deletes every INBOX message older than the period in every account in the account file. The first sweep runs when `inbaid` starts, and later sweeps run about once an hour. The messages are flagged `\Deleted` and expunged on the mail server. They cannot be recovered from inbAI.
