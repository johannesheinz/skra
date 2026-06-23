-- Denormalized sort keys for the contact list (name components and the primary
-- address location live only in vcard_raw otherwise). Populated on write and
-- backfilled once from vcard_raw at startup. Empty string means "known empty";
-- NULL means "not yet backfilled". Age sorting reuses the birthday column.
ALTER TABLE contacts ADD COLUMN given_name TEXT;
ALTER TABLE contacts ADD COLUMN family_name TEXT;
ALTER TABLE contacts ADD COLUMN postal_code TEXT;
ALTER TABLE contacts ADD COLUMN country TEXT;
CREATE INDEX idx_contacts_given ON contacts(address_book_id, given_name);
CREATE INDEX idx_contacts_family ON contacts(address_book_id, family_name);
CREATE INDEX idx_contacts_location ON contacts(address_book_id, country, postal_code);
