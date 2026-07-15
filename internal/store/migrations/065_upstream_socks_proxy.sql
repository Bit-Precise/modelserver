-- Add per-upstream outbound proxy selection. Existing upstreams retain the
-- historical http.ProxyFromEnvironment behavior.

ALTER TABLE upstreams
    ADD COLUMN proxy_mode TEXT NOT NULL DEFAULT 'environment',
    ADD COLUMN socks_proxy_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN socks_proxy_username TEXT NOT NULL DEFAULT '',
    ADD COLUMN socks_proxy_password_encrypted BYTEA;

ALTER TABLE upstreams
    ADD CONSTRAINT upstreams_proxy_mode_check
    CHECK (proxy_mode IN ('environment', 'direct', 'socks5'));
