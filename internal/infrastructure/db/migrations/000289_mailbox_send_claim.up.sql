-- When a campaign pass last took this mailbox for a send, so two campaigns
-- sharing it cannot both send inside its minimum gap.
ALTER TABLE email_accounts ADD COLUMN IF NOT EXISTS send_claimed_at timestamptz;
