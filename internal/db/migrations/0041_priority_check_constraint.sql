-- Remap old priority values to new constants:
-- old > 0 (high) → 0 (PriorityHigh),
-- old 0 (low) → 1 (PriorityLow)
update notification_queue
set priority = case when priority > 0 then 0 else 1 end;

alter table notification_queue
add constraint chk_notification_queue_priority
check (priority in (0, 1));

alter table sent_message_log
add constraint chk_sent_message_log_priority
check (priority in (0, 1));
