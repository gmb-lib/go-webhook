# Table shape for a relational Store

`V1__webhook_tables.sql` is the reference DDL behind the library's `Store` interface: three tables
(`subscription`, `event`, `delivery`) whose columns match the Go types one for one.
`V2__webhook_event_correlation_id.sql` adds the event's `correlation_id` (the causing request's id,
sent as `X-Correlation-ID` on every attempt; nullable). Neither is applied by the library — copy them
into your own migration set in order, put them in your own schema, and implement `Store` over them in
whatever way your service talks to its database. A host that copied `V1` before `V2` existed adds `V2`
as its own next migration.

Two things the shape decides on purpose:

- **Secrets are references, not values.** The subscription row holds `secret_ref_current` and
  `secret_ref_previous`; your `Store` resolves them to bytes when the worker asks. Two references allow
  a rotation with an overlap, and `previous_expires_at` says when the old one stops signing.
- **The event body is kept.** A delivery is re-sent byte-identical, and a host can show a receiver what
  it missed. How long events are kept is your retention policy, not the library's.

A worker polls `delivery` for non-terminal rows whose `next_attempt_at` has passed; the partial index
serves exactly that. If several workers run against one database, claim rows when you read them
(`SELECT … FOR UPDATE SKIP LOCKED`) so two workers never send the same delivery.
