-- Denormalized birthday for querying upcoming birthdays (BDAY otherwise lives
-- only in vcard_raw). Stored normalized as YYYY-MM-DD, with year 0000 when the
-- source birthday has no year. Empty string means "no birthday"; NULL means
-- "not yet backfilled from vcard_raw".
ALTER TABLE contacts ADD COLUMN birthday TEXT;
CREATE INDEX idx_contacts_birthday ON contacts(address_book_id, birthday);
