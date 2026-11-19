# CertificateExpiresVerySoon

**Severity:** critical (phone, high priority)

## What fired

A certificate expires in less than 3 days, or (negative days) already
has. [CertificateExpiresSoon](CertificateExpiresSoon.md) has been firing
for over 10 days before this, so the automatic renewal has failed. The
date comes from the unverified `tls-expiry-*` probes, which still read it
when the certificate is past its date.

## Impact

Within days: the site, mail, or (for `tls-k3s`) the cluster's internal
connections stop working.

## Fix

The steps are the same as in [CertificateExpiresSoon](CertificateExpiresSoon.md),
but don't wait for the automatic path:

- VPS: `certbot renew --force-renewal --cert-name <name>`, then make sure
  the mail services reloaded.
- k3s: restart k3s on the affected node now. If the certificate is already
  expired, stop k3s, run `k3s certificate rotate`, start it again.
- Cloudflare: if the edge certificate can't be renewed in time, an
  advanced or custom certificate in the dashboard. Don't turn the proxy
  off for the record: the origins use Cloudflare Origin CA certificates,
  which browsers don't trust.

Then find out why the renewal failed, so it doesn't come back in 60 days.
