-- Prebuild step 2: run the conversion, one committed chunk at a time.

-- Alone in its file, and so outside a transaction: the procedure commits per chunk.
call convert_status_changes();
