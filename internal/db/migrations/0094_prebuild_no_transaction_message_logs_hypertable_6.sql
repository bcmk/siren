-- Prebuild step 6: vacuum the copies, so the swap needs no downtime vacuum.

-- Stats and the visibility map follow the tables through the rename.
-- Freezing now spares the whole copied history an anti-wraparound vacuum later,
-- as every chunk took its rows in the copy's narrow xid window.
vacuum (freeze, analyze) sent_message_log_new, received_message_log_new;
