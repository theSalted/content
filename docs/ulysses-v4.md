# Ulysses v4 route compatibility

Ulysses42 was observed requesting `GET /ghost/api/v4/admin/site/` during connection.
The previous router returned404. The v4 Admin alias now reaches the existing
bounded HTML post/image handlers; optional trailing slashes on Admin routes are
normalized without a redirect. This does not implement all Ghost4 resources.

Versioned JWT audiences remain isolated: `/v2/admin/`, `/v4/admin/`, and `/admin/`.
Signature, key ID, expiry and existing one-second iat rounding checks remain.
`Accept-Version: v4.0` is accepted by this bounded profile. No staff identity,
owner-password fallback, user/settings endpoint or broader permission is implied.

Tests cover observed site discovery, synthetic draft creation, authenticated
reads and a cross-family rejection matrix. Production read-only checks verified
both primary/fallback hosts and existing public content after deployment. Native
Ulysses connection/publishing success remains a separate real-app test.

Primary sources:
- [Ghost4 API mounts](https://raw.githubusercontent.com/TryGhost/Ghost/v4.48.0/core/server/web/api/app.js)
- [Ghost4 audience validation](https://raw.githubusercontent.com/TryGhost/Ghost/v4.48.0/core/server/services/auth/api-key/admin.js)
- [Ghost Admin API](https://docs.ghost.org/admin-api/)
