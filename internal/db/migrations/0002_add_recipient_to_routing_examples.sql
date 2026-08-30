ALTER TABLE routing_examples
ADD COLUMN recipient TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_routing_recipient ON routing_examples(recipient);
