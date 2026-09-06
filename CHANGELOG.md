# Changelog

Notable changes to this library, newest first, per release. Written for whoever hosts the library or
receives its deliveries.

## v0.1.0

Initial code.

The receiver-facing contract in one place: one signed `POST` per event with a `t=…,v1=…` HMAC-SHA256
signature header (two `v1` values during a secret rotation), an event-type header and a delivery-id
header; `2xx` is delivered, `429` and `5xx` and a transport failure are retried on the schedule
1 min · 5 min · 30 min · 2 h · 8 h · 24 h (jittered) and then dead-lettered, any other `4xx` is dropped;
at-least-once, de-duplicate on the event id. For hosts: a `Store` interface with an in-memory
implementation, an in-process `Dispatcher`, a polling `Worker`, and the reference table shape under
`sql/`. For receivers: `Verify`, with a tolerance window and constant-time comparison.
