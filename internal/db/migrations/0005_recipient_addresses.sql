ALTER TABLE recipient_profiles ADD COLUMN addresses TEXT NOT NULL DEFAULT '[]';

CREATE TABLE recipient_addresses (
  address TEXT PRIMARY KEY NOT NULL
);
