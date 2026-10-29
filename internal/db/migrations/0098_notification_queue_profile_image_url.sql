-- The streamer's profile image, shown where image_url fails.
alter table notification_queue add column profile_image_url text not null default '';
