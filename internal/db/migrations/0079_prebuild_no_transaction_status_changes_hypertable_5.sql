-- Prebuild step 5: vacuum analyze the copy, so the swap needs no downtime vacuum.

-- Stats and the visibility map follow the table through the rename.
vacuum analyze status_changes_new;
