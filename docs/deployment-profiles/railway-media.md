# Private S3-compatible media on Railway

This optional deployment profile uses one private bucket dedicated to the CMS. Core uses the standard S3 operations described in [media storage](../media.md); no Railway SDK is required.

Configure endpoint, signing region, bucket, URL style and bucket-scoped read/write credentials only in the CMS runtime. Do not place these credentials in the browser, ActivityPub processes or repository. Environment-specific bucket instances are distinct storage: copying or retiring one requires object/metadata verification and a recoverable backup. Never delete a logical bucket across environments by accident.

The CMS proxy determines whether an object belongs to currently published public content. Unattached, draft and private media require owner authorization; the bucket stays private. A public CDN must check current visibility before returning cached bytes. HEAD performs this check from PostgreSQL metadata without downloading S3 bytes and is always no-store.

The application quota counts pending and ready reservations atomically. It is not a provider billing cap. Keep independent backups and account for both bucket storage and service egress. Provider pricing, scope and regions should be checked when deploying.

See [Railway storage documentation](https://docs.railway.com/storage-buckets). No concrete account identifiers or credentials are required by this profile.
