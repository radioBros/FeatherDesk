-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: sqlite.
-- Trusted integrations (range 0150-0159, I-12).
-- ticket_messages.via_token_id: the API token (scope tickets:act_as_customer)
-- that posted this customer message on the customer's behalf. NULL for every
-- other message. Staff-facing APIs and webhooks expose it as the boolean
-- `via_integration`; customers never see it. Revoking a token keeps the row;
-- deleting it (user erasure) only clears the link.
ALTER TABLE ticket_messages ADD COLUMN via_token_id TEXT NULL REFERENCES api_tokens (id) ON DELETE SET NULL;
CREATE INDEX ticket_messages_via_token_idx ON ticket_messages (via_token_id) WHERE via_token_id IS NOT NULL;
