-- Prebuild step 5: copy the rows logged since step 2 stopped, shortening the cutover's tail.

-- Alone in its file, and so outside a transaction: the procedure commits per step.
call convert_chats();
