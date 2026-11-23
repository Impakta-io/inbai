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
export INBAI_OPERATOR_TOKEN='the-operator-token'
export INBAI_MAIL=imap
export INBAI_DOMAIN=example.com
export INBAI_IMAP_ADDRESS=mail.example.com:993
export INBAI_SMTP_ADDRESS=mail.example.com:587
export INBAI_IMAP_TLS=1
export INBAI_IMAP_ACCOUNTS=/path/to/accounts.json
inbaid serve
```

`INBAI_IMAP_TLS=1` wraps both connections in TLS. Leave it unset only for a server on localhost that does not offer TLS.

The event cursor is `uidvalidity:uidnext`. A new UID is a created event. A message that is deleted is not reported, because a UIDNEXT cursor cannot see it. If the server changes UIDVALIDITY, the sync stops and the stored cursor stays where it was.

Message ids are IMAP UIDs. List and search return the subject. Get returns the text body. An attachment on send is a MIME part.
