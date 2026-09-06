# Table shape for a relational Store

`V1__webhook_tables.sql` is the reference DDL behind the library's `Store` interface: three tables
(`subscription`, `event`, `delivery`) whose columns match the Go types one for one. It is not applied
by the library — copy it into your own migration set, put it in your own schema, and implement `Store`
over it in whatever way your service talks to its database.

Two things the shape decides on purpose:

- **Secrets are references, not values.** The subscription row holds `secret_ref_current` and
  `secret_ref_previous`; your `Store` resolves them to bytes when the worker asks. Two references allow
  a rotation with an overlap, and `previous_expires_at` says when the old one stops signing.
- **The event body is kept.** A delivery is re-sent byte-identical, and a host can show a receiver what
  it missed. How long events are kept is your retention policy, not the library's.

A worker polls `delivery` for non-terminal rows whose `next_attempt_at` has passed; the partial index
serves exactly that. If several workers run against one database, claim rows when you read them
(`SELECT … FOR UPDATE SKIP LOCKED`) so two workers never send the same delivery.
