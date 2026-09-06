# Security policy

This library sends signed HTTP notifications from a host system to endpoints that other systems
register, and gives those receivers the code to verify them. Its risk sits on both sides: a host that
signs wrongly, or a receiver that verifies loosely, turns a webhook into a way to inject events into
a system that trusts them.

Please report security problems privately. Do not open a public issue, pull request or discussion for
anything that could be exploited before a fix exists.

## How to report

Use **[private vulnerability reporting](https://github.com/gmb-lib/go-webhook/security/advisories/new)**
on this repository. The report stays visible only to you and the maintainers until an advisory is
published, and it gives us one place to discuss and co-ordinate a fix with you.

Please include, as far as you can establish it:

- what the problem is, and what an attacker gains from it;
- the smallest set of steps that reproduces it, and against which version or commit;
- the header and body bytes that trigger it, with anything sensitive removed;
- whether you have told anyone else, and whether a disclosure date already binds you.

## What happens next

- We acknowledge a report within **five working days**.
- We tell you whether we can reproduce it, and what we think its severity is, as soon as we know.
- We keep you updated while a fix is prepared, and we agree a disclosure date with you. Our default is
  to publish an advisory once a fix is available, and in any case within **90 days** of the report —
  earlier if the problem is already public or being exploited.
- We credit you in the advisory unless you would rather stay anonymous.

There is no bug-bounty programme. We are grateful anyway, and we say so publicly.

## What we consider most serious

- `Verify` accepting a delivery whose signature does not match a secret the receiver holds, or one
  whose timestamp is outside the tolerance window — a forged or replayed event.
- A comparison that leaks timing, so a signature can be guessed byte by byte.
- The worker sending a delivery **unsigned**, or signed with a secret that has expired.
- A delivery sent to an endpoint the subscription did not register, or an event of one client reaching
  another client's endpoint.
- A header or body that makes `ParseSignature` or `Verify` panic, or cost unbounded time or memory.

Denial of service against the host (for example a receiver that never answers) and findings that need
an already-compromised host are in scope but lower priority. This module depends on the standard
library only, so there is no third-party dependency surface to report.

## What this library does not decide

It does not choose secrets, store them, or rotate them on a schedule; it does not enforce TLS on the
endpoint URL; it does not authenticate the host to the receiver beyond the signature. Those are the
host's responsibilities, and a weakness there is reported to the host system, not here.

## Supported versions

Security fixes land on the most recent release. Older tags are not patched; if you are pinned to one,
the fix is to move forward.
