# CredentialExpiresSoon, CredentialExpiresVerySoon

**Severity:** warning 30 days before; critical 7 days before

## What fired

A credential with a known expiry date is close to it. The list of
credentials, where each one lives, when it expires and how to rotate it
is an inventory in the private config repo ( `credentials/inventory.yaml` );
a script turns the dated ones into this rule ( `credentials/render.py` ).
There are no values in it, only names, places and dates.

Certificates are not in it: they have their own expiry alerts
( CertificateExpiresSoon ), read from the certificates themselves.

## Impact

Whatever uses the credential stops working on that date: a node drops
off the tailnet, terraform can't change DNS, a backend can't reach its
cloud service.

## Fix

Follow the entry's `rotate` line. Then update its `expires` and
`changed` in the inventory, run `python3 credentials/render.py`, and
commit; the alert resolves at the next evaluation ( hourly ).

If the system has an option to turn expiry off and the credential is
safe without it, that's a valid fix too: set `expires: never`.

## Unknown expiries

Entries with `expires: unknown` have an expiry we haven't read yet, so
they can't alert. Finding those dates is part of keeping the inventory.

## History

- New on 2026-11-23 with 15 entries; one dated ( a Tailscale node key,
  2027-05-16 ), three unknown.
