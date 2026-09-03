ALTER TABLE users
    ADD CONSTRAINT users_normalized_email_matches_email
    CHECK (normalized_email = lower(btrim(email)));
