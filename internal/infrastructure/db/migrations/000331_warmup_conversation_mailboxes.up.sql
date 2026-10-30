CREATE TABLE warmup_conversation_mailboxes (
    conversation_id uuid NOT NULL REFERENCES warmup_conversations(id) ON DELETE CASCADE,
    email_account_id uuid NOT NULL REFERENCES email_accounts(id) ON DELETE CASCADE,
    task_id uuid REFERENCES tasks(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, email_account_id),
    UNIQUE (task_id, email_account_id)
);

CREATE INDEX warmup_conversation_mailboxes_account_idx
    ON warmup_conversation_mailboxes (email_account_id);
