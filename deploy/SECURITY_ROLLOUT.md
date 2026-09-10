# Security hardening rollout

## Changes delivered

- Session creation and deletion both use Secure cookies for any configured
  non-loopback or HTTPS dashboard origin. `COOKIE_SECURE=false` cannot override
  that protection. Plain HTTP LAN dashboards must move to HTTPS. Localhost HTTP
  development remains supported.
- The main agent repairs cached credential-file permissions before using the
  file, rejects symlinks, and saves replacements through a private temporary file
  and rename. Persistence errors are reported. Existing local secret files were
  tightened to `0600`; upgraded agents apply the cache repair at registration.
  Windows still relies on installer ACLs; Unix modes are not Windows ACLs.
- Docker Compose publishes both application ports on loopback by default.
  Same-origin `/api` requests proxy from the dashboard container to `api:8000`.
- Obsolete public token/secret settings were removed from the dashboard's local
  environment and example. No signing secrets belong in `NEXT_PUBLIC_*` values.
- `nginx-security.conf` provides HSTS and basic security headers for the HTTPS
  virtual host. It does not cover subdomains or request preload.
- `require-s3-https.py` preserves bucket grants and adds a non-TLS deny for client
  principals, with the AWS service-principal exception recommended by
  [AWS](https://docs.aws.amazon.com/AmazonS3/latest/userguide/amazon-s3-policy-keys.html).

The shared agent queue directory has not been made private: the unprivileged
desktop collector must be able to enqueue data for the main service. Redesigning
that shared queue/IPC boundary and using OS-backed credential storage are separate
hardening work; owner-only registration credentials do not secure the whole queue.

## Production host steps

The public URL alone is not SSH or deployment access. These steps must run on
the host, or be executed once that access is provided.

1. Pull the committed changes. In the Compose environment, set:

   ```dotenv
   NEXT_PUBLIC_API_URL=/api
   DASHBOARD_ORIGIN=https://devicepulse.waybeyond.tech
   PUBLIC_API_URL=https://devicepulse.waybeyond.tech/api
   API_BIND_ADDRESS=127.0.0.1
   DASHBOARD_BIND_ADDRESS=127.0.0.1
   ```

   Set `COOKIE_SECURE=true` in the production API environment too. Do not replace
   the existing secrets or database settings. Remove obsolete public token/secret
   variables from the production dashboard build configuration.

2. For nginx running directly on the same host, rebuild/recreate both services:

   ```bash
   docker compose config --quiet
   docker compose build api dashboard
   docker compose up -d --force-recreate api dashboard
   ```

   If nginx runs in a container, use a shared Docker network and service-name
   upstreams instead of host loopback. Do not apply host-upstream assumptions
   blindly. Direct non-Docker installations must restrict their listening ports
   separately; these Compose bindings only affect Docker deployments.

3. A complete cleaned-up virtual host is provided in
   `deploy/devicepulse.nginx.conf`. It preserves both `/api/*` and legacy root
   API routes, including `/commands`, `/reports`, `/users`, and `/health`, which
   were absent from the previous nginx configuration. Install it as the site's
   enabled nginx configuration after backing up the existing file.

   Alternatively, install `deploy/nginx-security.conf` as
   `/etc/nginx/snippets/devicepulse-security.conf` and include it inside the
   existing TLS `server {}` for the production domain:

   ```nginx
   include /etc/nginx/snippets/devicepulse-security.conf;
   ```

   Keep the HTTP-to-HTTPS redirect and existing certificates/upstreams. nginx
   locations with their own `add_header` directives may need this include too,
   because header inheritance can be overridden. Validate and reload:

   ```bash
   sudo nginx -t
   sudo systemctl reload nginx
   ```

4. Confirm `Strict-Transport-Security: max-age=86400` on HTTPS homepage and API
   responses. After a stable rollout, increase max-age to `31536000`. Log in
   again and check the actual login cookie for `Secure`, `HttpOnly`, and
   `SameSite=Lax`. Verify that the host firewall/cloud security group restricts
   ports 8000/3000 appropriately; a timeout alone does not establish that.

5. Publish upgraded agents to apply persistent credential-file permission repair.
   Tighten production environment files to `0600` with the correct service owner;
   ensure that owner can still read them before restarting.

## S3 tool usage

With AWS CLI credentials authorized for the selected bucket:

```bash
python3 deploy/require-s3-https.py --bucket devicepulse --region ap-south-1
python3 deploy/require-s3-https.py --bucket devicepulse --region ap-south-1 --apply
```

The first command previews. Applying saves `before.json` in a private temporary
directory, checks for intervening policy edits, preserves existing statements,
and reads the policy back to verify it. Keep the printed backup if rollback is
needed; use it only after ensuring no later policy edits would be overwritten.
