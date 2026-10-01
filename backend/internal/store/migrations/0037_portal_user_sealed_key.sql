-- Store the envelope-encrypted plaintext for portal-provisioned keys so the
-- owner can reveal them again on /portal/key (show/hide), like the Bansos key.
-- NULL/empty for manually claimed keys: their plaintext is unrecoverable by
-- design, so those bindings stay masked-only.
ALTER TABLE portal_users ADD COLUMN sealed_wrapped_dek TEXT;
ALTER TABLE portal_users ADD COLUMN sealed_ciphertext TEXT;
