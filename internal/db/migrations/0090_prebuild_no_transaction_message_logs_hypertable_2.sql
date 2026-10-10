-- Prebuild step 2: run the copy, 10 committed weeks at a time.

-- Alone in its file, and so outside a transaction: the procedure commits per step.
call convert_message_logs();
