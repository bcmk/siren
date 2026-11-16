-- The bot ranks search matches itself and no longer calls levenshtein.
drop extension if exists fuzzystrmatch;
