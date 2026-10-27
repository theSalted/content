# Optional PostgreSQL trust anchor

`supabase-ca.crt` is the public Supabase Root 2021 CA certificate, not a private key or project credential. SHA-256 fingerprint: `807025ad50d4ed219d2c9c7d299c004f824eb00cf7f65afef607d07b72e6cafa`. Subject and issuer: Supabase Inc / Supabase Root 2021 CA. Valid through 2031-04-26.

The CMS image provides it at `/etc/content/supabase-ca.crt` for existing verified-TLS deployments. It is not installed as a global trust root and is only used if the configured PostgreSQL DSN explicitly selects it. Other providers can use system roots or a mounted CA path. Always use `sslmode=verify-full` with the correct provider trust anchor; never disable verification to make a connection succeed.

Obtain and verify the current certificate through your database provider's connection settings before choosing it for a new installation.
