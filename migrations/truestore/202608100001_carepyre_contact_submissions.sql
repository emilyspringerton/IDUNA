-- CarePyre landing page (carepyre.org) "Contact Us" form. Public, unauthenticated,
-- CORS-scoped + rate-limited at the handler (see carepyre_contact.go) -- same shape
-- as the mailing-list subscribe endpoint, minus the vault/Mailchimp machinery, since
-- this isn't a marketing-consent flow. Plaintext at rest like every other operational
-- table in this store (chat_messages, redgarden tickets, etc.) -- not the mailing
-- list's never-at-rest-unencrypted posture, which is a founder-directed policy
-- specific to okemily.com marketing signups, not a blanket IDUNA rule.
CREATE TABLE IF NOT EXISTS carepyre_contact_submissions (
    id         INTEGER  PRIMARY KEY AUTOINCREMENT,
    name       VARCHAR(120) NOT NULL,
    email      VARCHAR(254) NOT NULL,
    message    VARCHAR(4000) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_carepyre_contact_submissions_created_at ON carepyre_contact_submissions(created_at);
