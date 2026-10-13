-- Gateway-owned warmup follows its enrolled schedule, not an inherited Cloud workday.
UPDATE email_account_behavior b
SET enabled = false, updated_at = NOW()
FROM email_accounts ea, pool_link_mailboxes m, pool_link_instances i
WHERE b.email_account_id = ea.id
  AND m.email_account_id = ea.id AND m.instance_id = i.id
  AND ea.organization_id = i.organization_id
  AND NOT m.managed AND i.revoked_at IS NULL
  AND b.enabled AND b.created_at = b.updated_at
  AND (b.daily_limit_min, b.daily_limit_max) IN ((28, 32), (30, 45))
  AND b.hourly_limit_min = 5 AND b.hourly_limit_max = 9
  AND b.gap_min_seconds = 90 AND b.gap_max_seconds = 420
  AND b.work_start_min = 543 AND b.work_start_max = 567
  AND b.work_end_min = 1038 AND b.work_end_max = 1076
  AND b.lunch_enabled AND b.lunch_earliest = 720 AND b.lunch_latest = 810
  AND b.lunch_min_minutes = 30 AND b.lunch_max_minutes = 60
  AND b.weekdays = 31;
