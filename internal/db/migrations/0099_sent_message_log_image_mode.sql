-- 0 for a message without an image, 1 for a photo send by file id, 2 for a photo upload.
-- For simplicity, existing rows get 0 rather than null, though some of them were photos.
alter table sent_message_log add column image_mode integer not null default 0;

-- file_upload is true only since 2026-10-20, so older chunks are skipped, with a three-day margin.
update sent_message_log
set image_mode = 2
where timestamp >= 1792195200
and file_upload;

alter table sent_message_log drop column file_upload;
