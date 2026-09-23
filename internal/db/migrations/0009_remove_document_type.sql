DROP INDEX idx_routing_sender_type;
DROP INDEX routing_examples_scope;
ALTER TABLE routing_examples DROP COLUMN document_type;
CREATE INDEX routing_examples_identity ON routing_examples(sender, recipient, recipient_scope, folder);
UPDATE jobs SET classification_json = json_remove(classification_json, '$.document_type')
 WHERE json_valid(classification_json);
