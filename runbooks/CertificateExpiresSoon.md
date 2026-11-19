# CertificateExpiresSoon

**Severity:** warning (phone, normal priority)

## What fired

A TLS certificate the blackbox exporter checks hourly expires in less than
14 days and hasn't been renewed. `instance` is the host:port, `job` which
group of probes:

| job | What | Renewed by |
|---|---|---|
| `tls-public-sites` | the public sites (`deployments/12-cert-expiry.yaml`) | Cloudflare's edge certificates, automatically |
| `tls-manage-vps` | mail and the Home Assistant hosts on the VPS (private repo) | certbot on the VPS (`certbot-renew.timer`) |
| `tls-k3s` | the k3s API server and the kubelets | k3s itself, when it starts |

## Impact

None yet. In 14 days: browsers and mail clients refuse the connection, or
for k3s, nodes stop talking to the API server.

## Check

What is served right now:

```
openssl s_client -connect <host>:<port> -servername <host> </dev/null 2>/dev/null | openssl x509 -noout -dates -issuer
# mail submission/relay ports:
openssl s_client -connect <host>:587 -starttls smtp </dev/null 2>/dev/null | openssl x509 -noout -dates
```

## Fix

- **Public sites (Cloudflare)**: Cloudflare renews its edge certificates
  well before expiry; if one is this close, check the SSL/TLS page of the
  zone in the Cloudflare dashboard (a paused zone, a DNS record not
  proxied, a CAA record blocking the CA).
- **VPS (certbot)**:
  ```
  systemctl status certbot-renew.timer   # enabled AND active?
  certbot renew --dry-run
  certbot renew
  ```
  Postfix and Dovecot only pick up a renewed certificate after a reload;
  the deploy hook `reload-mail.sh` does that (infra repo,
  `servers/manage-certbot/`). Check the probe again afterwards.
- **k3s**: k3s renews its certificates on start when they are close to
  expiry. Restart k3s one node at a time (or let kured's next reboot do
  it, if it's soon enough):
  ```
  k3s certificate check --output table   # on the node
  systemctl restart k3s        # master
  systemctl restart k3s-agent  # workers
  ```

## History

- 2026-11-13: the VPS renewal timer had been enabled but never started,
  so mail's certificate would have expired in December; there was also no
  reload hook. Found while setting up these probes.
- The cluster admin client certificate on the laptop expired unnoticed
  earlier in 2026. It isn't something a probe can reach; kubectl stopping
  is the signal.
