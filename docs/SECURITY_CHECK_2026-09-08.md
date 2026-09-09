# DevicePulse security configuration check — 8 September 2026

Scope: read-only checks of https://devicepulse.waybeyond.tech/, the running local
API, workspace configuration, and the MongoDB/S3 resources configured in the
local API environment. The production server environment was not available;
its database and bucket cannot independently be confirmed to match local configuration.
No security settings, data, accounts, or deployments were changed.

## Verified protections

| Check | Evidence |
| --- | --- |
| Production HTTPS | HTTPS homepage and `/api/health` returned 200. Certificate hostname validation succeeded; certificate expires 22 November 2026. The connection negotiated TLS 1.3 with TLS_AES_256_GCM_SHA384. This was not an exhaustive TLS protocol/cipher scan. |
| HTTP redirect | HTTP homepage returned 301 to the HTTPS homepage. |
| S3 default encryption | Configured bucket `devicepulse` reports `SSEAlgorithm: AES256` (SSE-S3). A sampled browser-history object's HEAD metadata also reports AES256. No object body was downloaded. This is a sample, not an inventory of every object. |
| S3 public access | All four bucket public-access-block flags are true. Bucket policy status reports `IsPublic: false`. |
| MongoDB TLS | The configured Atlas SRV connection has TLS enabled, certificate validation enabled, and completed an authenticated ping. No application records were read. |
| Local response encryption | Local `/api/auth/me` with a valid ephemeral public key returned 401 with `application/octet-stream` and `X-DevicePulse-Encryption: devicepulse-response-v1`. |

MongoDB Atlas documents AES-256 encryption of cluster storage and snapshots by
default. This provides a provider-backed at-rest baseline for the configured
Atlas deployment; customer-managed keys, Atlas project network access, backups,
and production's selected cluster were not inspected through the Atlas control plane.
See [Atlas security documentation](https://www.mongodb.com/docs/atlas/setup-cluster-security/).
For S3's distinction between storage encryption and application encryption, see
[AWS SSE-S3 documentation](https://docs.aws.amazon.com/AmazonS3/latest/userguide/UsingServerSideEncryption.html).

## Findings and remaining checks

1. **Production response encryption is not active.** `/api/auth/me` with a valid
   encryption public key returned plaintext 401 (`text/plain; charset=utf-8`)
   with no encryption protocol header. The local server returns encrypted 401
   for the equivalent request. Deploy the updated API and dashboard together,
   then repeat an authenticated request check. Plaintext JSON over HTTPS is
   still encrypted in transit; this finding concerns the requested additional layer.

2. **Production Secure login cookie is unverified.** Local `api/.env` omits
   `COOKIE_SECURE`, so this local configuration defaults to false. This is not
   proof of production's environment. Inspect production's login response for
   `Secure; HttpOnly; SameSite=Lax`, or verify `COOKIE_SECURE=true` in the API
   process environment. No login was performed and no user credentials were requested.

3. **Local sensitive files have overly broad file modes.**
   `agent/data/registration.json`, `api/.env`, and `agent/.env` are mode `0644`;
   `agent/data` is `0755`. The registration file holds the plaintext API key,
   and the API environment contains credentials. Effective access also depends
   on parent-directory permissions and ACLs. Restrict sensitive files to `0600`
   and ensure the intended service account owns them. The agent's
   `os.WriteFile(..., 0600)` does not tighten permissions on an existing file;
   fix that persistence path as well as existing files. Local credential encryption
   or OS keychain storage remains a separate improvement.

4. **No HSTS header observed.** The HTTPS homepage and API health response did
   not include `Strict-Transport-Security`. Configure HSTS at the production
   reverse proxy after confirming the intended domain scope and HTTPS availability.
   HTTP-to-HTTPS redirection already works.

5. **Bucket policy does not explicitly deny insecure transport.** The inspected
   policy has principal-scoped allows and no deny using `aws:SecureTransport=false`.
   Public access is blocked, and the checked SDK/API access used HTTPS. Other IAM
   or organization policies were not inspected, so this is a missing bucket-level
   safeguard, not proof that an HTTP request would currently succeed. Add an
   appropriately scoped deny for non-TLS bucket/object requests.

6. **Direct production API-port exposure is inconclusive.** HTTP and HTTPS
   connections to the production hostname on port 8000 timed out from this
   machine. This does not prove the firewall/security group is correctly configured.
   Locally, ports 8000 and 3000 listen on wildcard addresses, and Docker Compose
   publishes both ports on all interfaces. For a production reverse-proxy setup,
   verify that the backend port is restricted to the intended internal path.

An unused `NEXT_PUBLIC_DASHBOARD_TOKEN` is set in local dashboard configuration;
its value was not found in the current built browser JavaScript. Remove obsolete
public secret configuration to prevent future accidental use. No secret values
were included in this report.

This is a targeted configuration check, not a penetration test or a complete
security certification. The remaining production cookie/server-configuration
checks need an authenticated response or deployment access.
