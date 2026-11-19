# TLSProbeFailing

**Severity:** warning (phone, normal priority)

## What fired

For about 2 hours (every probe in that time) the blackbox exporter
couldn't complete a verified TLS handshake with `instance` (`job` says which group, see
[CertificateExpiresSoon](CertificateExpiresSoon.md)).

## Impact

Either the certificate is invalid (wrong name, incomplete chain) or
already expired, or the host is down or unreachable from the cluster. An
expired one also fires CertificateExpiresVerySoon with negative days,
from the unverified `tls-expiry-*` twin probe (since 2026-11-19); if that
one is quiet, look at the name and the chain first.

## Check

From the laptop and from the cluster:

```
openssl s_client -connect <host>:<port> -servername <host> </dev/null 2>&1 | grep -E 'Verify return code|notAfter'
curl -sv https://<host>/ -o /dev/null 2>&1 | grep -E 'expire|SSL|Connected'
```

- `certificate has expired` / `unable to get local issuer`: a certificate
  problem; fix it as in [CertificateExpiresVerySoon](CertificateExpiresVerySoon.md).
- Connection refused or timeout: the host or the service is down; is the
  same host red on https://status.smega.eu?
- Fine from outside, failing from the cluster: DNS or routing from inside
  (the probe runs in the `monitoring` namespace):
  ```
  kubectl -n monitoring logs deploy/blackbox-prometheus-blackbox-exporter | tail
  ```

## Fix

Fix the certificate or bring the host back. If the target is gone for
good, remove it from the Probe (`deployments/12-cert-expiry.yaml`, or the
private repo's `probes/`), with a comment saying why.

## History

- 2026-11-17: nevnapok.eu had a domain problem; taken out of the probe
  until fixed instead of firing every 3 hours.
