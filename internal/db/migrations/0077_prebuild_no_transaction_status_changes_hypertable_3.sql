-- Prebuild step 3: run the copy, one committed week at a time.

-- Alone in its file, and so outside a transaction: the procedure commits per week.
call convert_status_changes();
