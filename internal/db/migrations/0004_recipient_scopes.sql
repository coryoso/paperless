ALTER TABLE routing_examples ADD COLUMN recipient_scope TEXT NOT NULL DEFAULT 'unknown';
CREATE INDEX routing_examples_scope ON routing_examples(sender, recipient, recipient_scope, document_type, folder);

CREATE TABLE recipient_profiles (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  scope TEXT NOT NULL CHECK (scope IN ('personal', 'sole_proprietor', 'gbr', 'organization', 'unknown')),
  aliases TEXT NOT NULL DEFAULT '[]',
  folder_prefix TEXT NOT NULL DEFAULT '',
  UNIQUE(name, scope)
);
