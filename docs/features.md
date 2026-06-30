# Skrá — User Guide

What Skrá can do and how to use it.
This describes the app as it stands; for architecture and rationale see [`architecture.md`](architecture.md), and for running it see [`operations.md`](operations.md).

## Accounts and roles

Every account has a global role: **admin** or **user**.

- **Admins** manage all accounts and can see every address book.
  Account management lives at `/admin/users` — create, edit, reset a password, or delete an account, with guards against deleting yourself or the last admin.
- **Users** see only the books they own or have been granted access to.

The very first admin is created once from the command line (`skra create-admin`); every account after that is created by an admin in the web UI.

Your own settings are on the **account page** (`/account`): edit your email (the username is fixed), change your password, review the books you belong to and your access level, and set your appearance and language.

## Address books and access

Contacts are organized into **address books**, each with an owner.
Access to a book is granted per user at one of two levels:

- **Viewer** — read the contacts.
- **Manager** — edit contacts, import, and create share links.

A manager can grant access to an existing user by username, or create a new account scoped to that book (never an admin).
Admins implicitly have access to every book.

## Contacts

A contact holds rich, multi-value details: given and family name, any number of typed emails, phones, and postal addresses, links, organization, title, a birthday, and a note, plus a photo.

- **Adding and editing:** multi-value fields use repeatable rows — a round **+** button adds a row, and each row has a remove control.
  Uploading a photo runs it through the image pipeline automatically (orientation fixed, metadata stripped, downscaled).
- **Birthdays:** tick **no year** to record a month and day without a year (the year field disappears); untick it to bring the year back.
- **The list view** offers a search box (substring match over name, organization, email, and phone), sorting (first name, last name, age, or postal code + country) with an ascending/descending toggle, and a choice of page size — including "all".
  Your sort and page-size choices are saved and applied automatically.

## Landing page

The dashboard shows two things across the books you can see: the **upcoming birthdays** (the next few, with the age each person turns when the birth year is known) and the most **recently added contacts**.

## Sharing

A manager can share a whole book or a single contact through a link, in one of three modes:

- **Authenticated** — only signed-in users can view it.
- **Public link** — anyone with the link can view it; the link is an unguessable capability URL.
- **PIN-gated link** — anyone can open it after entering the correct PIN; failed attempts are throttled.

Every link supports an expiry, a maximum number of uses, and revocation.
Shared pages are read-only, are marked `noindex`, and load no third-party resources.

## Import and export

- **Import (vCard `.vcf`):** upload a file, review a dry-run preview (counts of new, duplicate, and malformed cards), then commit.
  Duplicates — matched by UID, then email — can be skipped or imported as new.
  Requires a manager grant on the target book.
  One bad card never aborts the batch.
- **Create-and-import:** an admin can create a new address book and import a file into it in a single step from the books overview.
- **Export:** a whole book or a single contact as vCard (photos embedded), or a whole book as CSV (sanitized against spreadsheet formula injection).

## Personalization

- **Appearance:** choose a mode (light / dark / system), a palette flavor, and an accent.
  The choice is saved to your account and a header toggle switches it instantly; signed-out visitors get a per-device choice.
- **Language and formats:** choose `en-US`, `de-DE`, or `en-DK` (English text with European formats).
  Interface text, numbers, dates, and address line order all follow the locale.
  Until you pick one, Skrá follows your browser's language preference.
- **Accessibility:** a high-contrast option (also detected automatically from your operating-system setting), reduced motion when your system requests it, full keyboard operation, and screen-reader-friendly labels throughout.

## Maps and printing

- Each postal address links out to **OpenStreetMap**, **Google Maps**, and **directions**, opening in a new tab.
  Nothing is embedded, so following a link is the only thing that discloses the address.
- The contact overview and the contact detail page have a clean **print / PDF** layout that strips the app chrome and renders black on white.
