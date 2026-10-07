# Salesforce to Relational Database — Field Types Migration Guide

This document is a complete reference for every Salesforce field type that needs
special handling when migrating to a relational database (PostgreSQL or equivalent).
It is written from a general migration perspective — not tied to any specific
application or service.

For each field type:
- What it is and how Salesforce handles it
- What problem it creates in a relational DB
- All available options with pros and cons
- The recommended option clearly marked with ✅

---

## Groups at a Glance

| Group | Field Types Covered |
|---|---|
| 1 — SF-controlled fields | Formula, Roll-Up Summary, Auto Number, System Timestamps, System User Fields |
| 2 — Constrained value fields | Restricted Picklist, Dependent Picklist, Multi-Select Picklist |
| 3 — Relationship fields | Lookup, Master-Detail, Self-Referential, Polymorphic Lookup, External ID |
| 4 — Numeric and financial fields | Currency, Percent, Number |
| 5 — Date and time fields | Date, DateTime, Time |
| 6 — Text fields | Text, Text Area, Long Text Area, Rich Text Area, Phone, Email, URL |
| 7 — Boolean fields | Checkbox |
| 8 — Binary and sensitive fields | Encrypted Field, File / Attachment |
| 9 — Spatial fields | Geolocation |


---

## Group 1 — Salesforce-Controlled Fields

Salesforce owns the value for these fields. You cannot write to them via the API.
Any attempt to INSERT or UPDATE them will be rejected by Salesforce. In the new DB,
these fields need a separate strategy to stay accurate.

---

### 1.1 Formula Field

**What it is in Salesforce:**
A read-only field whose value Salesforce calculates automatically from other fields
on the same record every time the record is read. The formula is defined in
Salesforce metadata. You never write to it — Salesforce rejects any attempt.

Examples: a field that concatenates first name + last name, a field that checks
whether a score is above a threshold and returns a category label, a field that
computes a ratio from two numeric fields.

**Why it is a problem in a relational DB:**
A plain database column has no concept of automatic recalculation. If you store
the computed value as a column, it will go stale the moment any of the source
fields change — unless something explicitly recomputes and updates it.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **PostgreSQL generated column** | `col NUMERIC GENERATED ALWAYS AS (source_a / source_b) STORED` — the DB recomputes the value automatically whenever any source column on the same row changes | Always in sync. Zero application code. Identical to SF formula behaviour | Formula must be translated from Salesforce formula language to SQL. Changing the formula requires `ALTER TABLE`. Cannot reference other tables — only columns on the same row | ✅ **Yes — for formulas that only use columns from the same table** |
| Application computes and writes | The writer calculates the value in code and explicitly writes the result column on every INSERT or UPDATE of the source fields | Works for any formula complexity, including cross-table logic | Goes stale if source fields are updated by a different writer that does not know about this formula. Requires discipline across all writers | Use for complex formulas that span multiple tables |
| SQL View | Define a database view that computes the formula as a SELECT expression | Always accurate on read. No storage needed | Cannot be indexed directly on the computed column. Adds a join or subquery overhead on every read | Use when the field is rarely queried and never needs indexing |
| Scheduled recomputation job | A background job periodically recomputes and updates all formula columns | Simple to implement | Value can be stale between job runs. Not suitable for fields used in real-time decisions | Only for reporting fields where slight staleness is acceptable |

**Migration action:**
- Identify all formula fields using Salesforce Metadata API (`FieldType = Formula`).
- Add them to a **skip list** in your write layer — never write them directly.
- For each formula field, decide which option above fits its complexity.
- Translate the Salesforce formula syntax to SQL if using a generated column.


---

### 1.2 Roll-Up Summary Field

**What it is in Salesforce:**
A field on a **parent** record that automatically aggregates values from its
**child** records using COUNT, SUM, MIN, or MAX. Salesforce recalculates it
automatically whenever a child record is created, updated, or deleted.

Examples: a Lead with a COUNT of related Offers, a parent account with a SUM of
all child invoice amounts, a master record with the MAX date from child records.

**Why it is a problem in a relational DB:**
The value depends on rows in a different table. A plain column on the parent has
no awareness of child table changes — it goes stale the moment a child is added,
updated, or deleted.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Application-level update in the same transaction** | After inserting or updating child rows, the writer explicitly recalculates the aggregate and updates the parent column in the same DB transaction | Simple. Full control. Consistent — parent is always updated by the same code that changes children | Only catches writes made through this writer. Misses updates from other services or direct DB access | ✅ **Yes — start here** |
| **Database trigger on the child table** | A PostgreSQL trigger fires on INSERT, UPDATE, and DELETE of child rows and automatically updates the parent column | Automatic. Catches every write regardless of which application or service made it | Harder to debug. Trigger failures can silently roll back the child write. Not portable across DB engines | ✅ **Yes — add this once multiple writers exist** |
| Compute at query time (subquery) | Do not store the value. Use `SELECT COUNT(*) FROM child WHERE parent_id = ?` whenever the parent is read | Always accurate. No sync needed | Performance degrades as child count grows. Cannot be indexed. Slows every parent read | Only for aggregates that are very rarely queried |
| Materialised view with refresh | A materialised view stores the aggregate and is refreshed periodically or on demand | Good query performance after refresh | Value is stale between refreshes. Refresh can be slow on large tables | Only for reporting/analytics — not for operational fields |

**Migration action:**
- Identify all roll-up summary fields using Salesforce Metadata API (`FieldType = Summary`).
- Add them to the **skip list** — never write directly.
- Implement the application-level update first.
- Add a DB trigger later to cover writes from outside the main application.


---

### 1.3 Auto Number Field

**What it is in Salesforce:**
Salesforce automatically assigns a sequential, formatted number to every new record.
The format is defined in metadata (e.g. `APP-{000000}`). You cannot set or override
this value — Salesforce controls it entirely.

Examples: `Application_Id__c` = `APP-000001`, `Case-0042`, `INV-2026-00891`.

**Why it is a problem in a relational DB:**
A plain `VARCHAR` column has no built-in sequence. Without a sequence, you either
generate duplicates, have gaps, or need external coordination.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **PostgreSQL SEQUENCE + column default** | `CREATE SEQUENCE app_seq START 1;` then `DEFAULT 'APP-' \|\| LPAD(nextval('app_seq')::TEXT, 6, '0')` on the column | Exact equivalent. Guaranteed unique and sequential. No application code needed for new records | Sequence format must match Salesforce's pattern exactly. Sequence resets are destructive | ✅ **Yes** |
| UUID as primary key | Replace the auto-number with a UUID (`gen_random_uuid()`) | Zero coordination needed. Globally unique | Loses the human-readable format. Downstream systems that display or search by the formatted number will be affected | Only if the formatted number is never exposed to users or external systems |
| Application generates the value | The writer generates the next number by querying the current MAX and incrementing | Simple | Race condition under concurrent inserts — two writers can generate the same number | Never for concurrent systems |

**Migration action:**
- For existing records: copy the Salesforce auto-number value into the DB column as-is to preserve continuity.
- For new records after migration: use a PostgreSQL SEQUENCE starting from `MAX(existing) + 1`.
- Add the formatted number as a `UNIQUE` constraint since Salesforce guarantees uniqueness.


---

### 1.4 System Timestamp Fields (CreatedDate, LastModifiedDate, SystemModstamp)

**What they are in Salesforce:**
Salesforce sets these automatically on every record. `CreatedDate` is set once on
insert. `LastModifiedDate` is updated on every change. `SystemModstamp` is updated
even when an automated process (workflow, trigger) changes the record without a
user action. You cannot write to any of them via the API.

**Why they are a problem in a relational DB:**
Nothing in a plain relational DB sets these automatically unless you configure it.
If your write layer tries to insert them, it will succeed (unlike Salesforce which
rejects the attempt) — which means wrong timestamps can be written if the logic
is not correct.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **DB column defaults + update trigger** | `created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`. `updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()` with a trigger: `NEW.updated_at = NOW()` on every UPDATE | Automatic. Always accurate. Zero application code | Trigger required for `updated_at` on PostgreSQL (unlike MySQL which has `ON UPDATE`) | ✅ **Yes** |
| Application sets explicitly | Writer calls `NOW()` and passes the value on every INSERT/UPDATE | Simple. No trigger needed | Can be forgotten by any writer that does not follow the convention. Inconsistent across services | Acceptable only if a single writer is guaranteed |
| Preserve Salesforce original value | During migration, store SF's `CreatedDate` and `LastModifiedDate` values as-is | Preserves historical audit trail | After migration, new records need the DB-generated value anyway — so both approaches are needed | Do for migration batch only. Use DB default for new records |

**Migration action:**
- Add these columns to the **skip list** for normal write operations.
- During initial data migration: populate `created_at` from SF `CreatedDate` and `updated_at` from SF `LastModifiedDate`.
- After migration: DB defaults and trigger handle all new records.


---

### 1.5 System User Fields (CreatedById, LastModifiedById, OwnerId)

**What they are in Salesforce:**
Salesforce sets `CreatedById` and `LastModifiedById` automatically to the ID of the
user or API client that performed the action. `OwnerId` defaults to the creating
user but can be changed. All three store 18-character Salesforce User record IDs.

**Why they are a problem in a relational DB:**
Salesforce User IDs (`005...`) are specific to Salesforce. In the new DB there may
be no User table, or users may be managed by a separate identity service (e.g.
Auth0, internal IAM). Referential integrity across systems is difficult.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Store as plain `VARCHAR`, populate from request context** | Keep the SF User ID or replace with the calling service's identifier (service name, API key ID, user UUID from your identity system) | Simple. No FK needed. Works before a User table exists | No DB-level referential integrity | ✅ **Yes — during migration** |
| FK to a local users table | Migrate all Salesforce users to a `users` table first. Then enforce FK | Full relational integrity | Requires complete user migration before any record migration. Circular dependency risk | Yes — once user management is in the new DB |
| Omit the columns | Do not store created-by or modified-by | Simpler schema | Loses the audit trail of who created or changed each record. Not recommended for regulated systems | Never for systems with compliance requirements |

**Migration action:**
- Store the SF User ID as-is during migration so the audit trail is preserved.
- For new records after migration: store the identifier from your own identity/auth system.


---

## Group 2 — Constrained Value Fields

---

### 2.1 Restricted Picklist

**What it is in Salesforce:**
A picklist field where only values from a predefined list are accepted. Salesforce
rejects any insert or update that contains a value not in the list — at the API level,
before the record is even saved.

Examples: `Status` = `Open` / `Closed` / `In Progress`. `Credit_Decision__c` =
`Pre Approved` / `Reject`.

**Why it is a problem in a relational DB:**
A plain `VARCHAR` column accepts any string. An invalid value can be written silently,
and data quality degrades over time without any error.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`CHECK` constraint** | `ALTER TABLE leads ADD CONSTRAINT chk_credit_decision CHECK (credit_decision IN ('Pre Approved', 'Reject', NULL))` | Enforced at DB level — no application can bypass it. Visible in the schema. Adding a new value is a simple `ALTER TABLE` | Removing a value requires a migration. Must include `NULL` explicitly if the field is nullable | ✅ **Yes** |
| PostgreSQL `ENUM` type | `CREATE TYPE decision_enum AS ENUM ('Pre Approved', 'Reject')`. Column type is the enum | Strongly typed. Slightly more storage efficient than VARCHAR | Adding a value requires `ALTER TYPE` which can lock the table on large datasets. Harder to manage across environments | Acceptable for small, very stable lists |
| Reference / lookup table (FK) | A separate `picklist_values` table holds allowed values. The main column is a FK to it | Most flexible — add new values by inserting a row, no DDL migration needed | Adds a table and a JOIN. Slightly more complex queries | Best for picklists that change frequently or are managed by non-developers |
| Application-level validation only | The writer validates the value in code before the INSERT | Simple to implement | No DB-level protection. Any direct DB access, admin tool, or another service can bypass it | Never as the only guard |

**Migration action:**
- Pull the list of allowed values per picklist field from Salesforce Metadata API.
- Apply a `CHECK` constraint for each restricted picklist field on the corresponding DB column.
- Include `NULL` in the allowed list if the field is not mandatory in Salesforce.

---

### 2.2 Dependent Picklist

**What it is in Salesforce:**
A picklist whose allowed values depend on the currently selected value of another
picklist field (the "controlling field"). Salesforce enforces the dependency — you
cannot select a child value that is not valid for the current parent value.

Example: `Sub_Program__c` values depend on `Program_Type__c`. If Program Type is
`Consumer Loan`, Sub Program can only be `CL-A` or `CL-B`.

**Why it is a problem in a relational DB:**
A simple `CHECK` constraint on the child column cannot express conditional rules
like "this value is only valid when the parent has value X."

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Application-level validation** | Before INSERT/UPDATE, look up the dependency matrix (stored as config or a table) and check: given the parent value, is the child value in the allowed set? | Full control. Can replicate the exact SF dependency matrix | Logic lives in application code. Must be maintained when the matrix changes | ✅ **Yes** |
| Dependency matrix reference table | A table `(controlling_value, dependent_value)` stores all valid pairs. Application validates by querying this table | Matrix managed in DB, not code. Non-developers can update it | Requires a JOIN or lookup query on every write | ✅ **Yes — use alongside application validation for auditability** |
| DB `CHECK` with all valid child values (no dependency) | `CHECK (sub_program IN ('CL-A', 'CL-B', 'PL-A', ...))` — lists every child value regardless of parent | Catches completely invalid values | Does NOT enforce the parent-child dependency. `CL-A` is accepted even if parent is `Personal Loan` | Use only as a secondary safety net |
| Partial composite CHECK | `CHECK ((program_type = 'Consumer Loan' AND sub_program IN ('CL-A','CL-B')) OR (program_type = 'Personal Loan' AND sub_program IN ('PL-A','PL-B')))` | Full enforcement at DB level | Becomes extremely complex with many combinations. Very hard to maintain | Only for very small, stable dependency matrices (2-3 pairs) |


---

### 2.3 Multi-Select Picklist

**What it is in Salesforce:**
A picklist that allows the user to select multiple values at once. Salesforce stores
them internally as a single semicolon-delimited string: `"Value1;Value2;Value3"`.

Example: `Services_required_for_reevaluation__c` = `"CIBIL;CRIF;PAN"`.

**Why it is a problem in a relational DB:**
There is no native multi-value column in standard SQL. A plain `VARCHAR` stores
the semicolon string but makes individual-value queries unreliable (`LIKE '%CIBIL%'`
would match `'CIBIL_PLUS'` too).

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`TEXT` column, keep semicolon format** | Store `"Value1;Value2;Value3"` exactly as Salesforce returns it | Zero transformation. Migration is direct copy. Easiest to start with | Querying for a specific value requires `LIKE` or `string_to_array()`. Risk of false positives with LIKE | ✅ **Yes — for migration phase** |
| **PostgreSQL `TEXT[]` array column** | Convert semicolon string to a PostgreSQL array `ARRAY['Value1','Value2','Value3']` before storing | Clean individual-value queries: `'Value1' = ANY(column)`. Can be indexed with GIN index | Requires conversion on write (split by `;`) and re-join on read if the old format is needed elsewhere | ✅ **Yes — better long-term design** |
| Junction / association table | A separate table with one row per selected value: `(record_id, value)` | Fully normalised. Supports FK to picklist table | Overkill for most use cases. Adds a table and JOIN for every read | Only if individual values need their own attributes (timestamps, metadata) |

**Migration action:**
- Start with `TEXT` column (semicolon format) to make migration simple.
- After migration, convert to `TEXT[]` array if queries on individual values are needed.
- Add a GIN index on the array column for query performance: `CREATE INDEX ON table USING GIN(column)`.


---

## Group 3 — Relationship Fields

Relationship fields store the ID of a related record. How tightly Salesforce couples
the relationship determines what constraints you apply in the DB.

---

### 3.1 Lookup Relationship

**What it is in Salesforce:**
A loosely coupled relationship between two objects. The child record can exist even
if the parent is deleted (Salesforce clears the field to null on parent deletion by
default, but the child record remains). The field stores the 18-character SF ID of
the related record.

Example: `Contact__c` on `ES_Contact__c` — the ES Contact can exist even if the
Contact is deleted.

**Why it is a problem in a relational DB:**
A standard FK constraint in a relational DB prevents inserting a child if the parent
does not exist and by default prevents deleting a parent if children reference it.
This is stricter than Salesforce's Lookup behaviour.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Nullable FK, `ON DELETE SET NULL`** | FK column is nullable. When parent is deleted, child's FK column is set to NULL automatically | Closest match to SF Lookup behaviour — child survives parent deletion. FK integrity enforced on insert | Parent must exist at insert time. Slightly different from SF which allows inserts referencing non-existent parents in some edge cases | ✅ **Yes** |
| Nullable FK, no cascade (`ON DELETE RESTRICT`) | FK column is nullable. Parent deletion is blocked if any child references it | Prevents accidental parent deletion | Parent cannot be deleted while children exist — stricter than SF | Use when you want stricter data quality than SF |
| Plain `VARCHAR`, no FK constraint | Store the ID as a plain string. No referential integrity | Exact match for SF edge cases where references may be to IDs from other systems | Orphaned records possible. No DB-level integrity | Use only during migration or for cross-system references |

---

### 3.2 Master-Detail Relationship

**What it is in Salesforce:**
A tightly coupled parent-child relationship. The child record **cannot exist without
its parent** — the parent FK field is required and cannot be null. When the parent
record is deleted, Salesforce **automatically deletes all child records** (cascade
delete). The child's sharing and security also inherit from the parent.

Examples: `MultiBureau_AccountList__c` is a Master-Detail child of
`Multibureau_Data__c`. Deleting the bureau record deletes all account list rows.

**Why it is a problem in a relational DB:**
A plain FK column in SQL is nullable by default and does not cascade deletes unless
explicitly configured. Missing this configuration means orphaned child rows remain
after parent deletion — violating the relationship semantics.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`NOT NULL` FK + `ON DELETE CASCADE`** | FK column is `NOT NULL`. `ON DELETE CASCADE` automatically deletes all children when the parent is deleted | Exact equivalent of SF Master-Detail. Enforces both "child requires parent" and "delete parent → delete children" | Parent must be inserted before child. Cascade deletes are irreversible | ✅ **Yes** |
| `NOT NULL` FK, `ON DELETE RESTRICT` | Child cannot exist without parent. Parent deletion is blocked if children exist | Prevents accidental data loss from cascade | Manual cleanup of children required before parent can be deleted | Use when cascade delete is too risky for the data |
| `NOT NULL` FK, `ON DELETE NO ACTION` | Same as RESTRICT in PostgreSQL | Default FK behaviour | Same as RESTRICT | Not recommended — use CASCADE or RESTRICT explicitly |

---

### 3.3 Hierarchical / Self-Referential Lookup

**What it is in Salesforce:**
A lookup to another record of the **same object type**, creating a tree hierarchy.
SF uses this for the User object (`ReportsToId`). Custom objects can also have
self-lookups.

Example: A `Category__c` object where each category can have a parent category.
`Parent_Category__c` points back to `Category__c`.

**Why it is a problem in a relational DB:**
A self-referential FK can create circular references (A → B → A) which would
make tree traversal infinite.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Nullable self-referential FK + cycle guard** | `parent_id FK REFERENCES same_table(id) ON DELETE SET NULL`. Application checks for cycles before insert | Standard SQL pattern for hierarchies. `NULL` = root of tree | Application must prevent circular references | ✅ **Yes** |
| PostgreSQL recursive CTE for traversal | Use `WITH RECURSIVE` for tree queries | Efficient tree traversal | Query complexity — but this is a read concern, not a write concern | Use for reading, not for write design |
| Plain `VARCHAR`, no FK | Store parent ID as a plain string | No cycle detection needed at DB level | No referential integrity. Orphaned nodes possible | Only as a fallback |


---

### 3.4 Polymorphic Lookup

**What it is in Salesforce:**
A single lookup field that can reference records from **multiple different object
types**. The field stores the ID of the related record, and Salesforce also stores
the object type separately.

Examples: `WhatId` on a Task or Event can point to a Lead, an Opportunity, or a
custom object. `WhoId` can point to a Contact or a Lead.

**Why it is a problem in a relational DB:**
A standard FK column can only reference one table. A polymorphic reference — one
field pointing to multiple different tables — has no direct SQL equivalent.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Two columns: ID + type** | `related_id VARCHAR, related_type VARCHAR` (e.g. `'Lead'`, `'Opportunity'`). No FK constraint on `related_id` | Simple. Exactly matches how Salesforce stores it internally | No DB-level referential integrity | ✅ **Yes** |
| Separate nullable FK per target type | `lead_id FK NULL, opportunity_id FK NULL` — only one is populated at a time | Full FK integrity per target | Column count grows with each new target type. Queries must check all columns | Only if the number of target types is small (2-3) and fixed |
| Generic entity table | All referenced records share a single `entities` table with a `type` discriminator | Single FK. Works for unlimited types | Requires all referenced objects to share one table — major schema change | Only for new greenfield designs |

---

### 3.5 External ID Field

**What it is in Salesforce:**
A field marked as "External ID" in Salesforce metadata. Salesforce uses it as a
unique key for **upsert operations** — insert the record if no match exists, update
it if a match is found. It is also used for cross-system deduplication and for
referencing records from external systems without knowing the SF internal ID.

Examples: `Application_Id__c`, `Partner_Reference_Id__c`.

**Why it is a problem in a relational DB:**
A plain column with no unique constraint allows duplicate values. Without uniqueness
enforcement, upsert logic in the application is not safe under concurrent writes.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`UNIQUE` constraint + `INSERT ... ON CONFLICT DO UPDATE`** | `CREATE UNIQUE INDEX ON table(external_id_col)`. Upsert: `INSERT INTO table (...) VALUES (...) ON CONFLICT (external_id_col) DO UPDATE SET ...` | Exact equivalent of SF External ID upsert. Atomic and safe under concurrency. Idempotent — safe to retry | None | ✅ **Yes** |
| `UNIQUE` constraint, application handles conflict | Unique index, but writer catches the duplicate key error and does an UPDATE instead | Familiar pattern | Two round trips (try INSERT, catch error, do UPDATE). Race condition window between the two | Acceptable, but `ON CONFLICT` is better |
| Plain column, application deduplication | Application queries first, then decides insert or update | Simple | Race condition — two concurrent writes can both find no record and both insert, creating duplicates | Not recommended |


---

## Group 4 — Numeric and Financial Fields

---

### 4.1 Currency Field

**What it is in Salesforce:**
Stores a monetary amount. In multi-currency orgs, each record also has a
`CurrencyIsoCode` field (e.g. `INR`, `USD`). The Currency field itself stores a
numeric value; the ISO code is stored separately.

Examples: `Amount_in_Rs__c`, `Processing_Fee__c`, `Eligible_Loan_Amount__c`.

**Why it is a problem in a relational DB:**
Using floating-point types (`FLOAT`, `DOUBLE PRECISION`) for financial values
introduces binary rounding errors. `0.1 + 0.2` in floating point is not exactly
`0.3`. This causes incorrect totals, rounding discrepancies, and audit failures.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`NUMERIC(18,2)` or `DECIMAL(18,2)`** | Fixed-point exact storage. `18` = total digits, `2` = decimal places | No rounding errors. Industry standard for financial amounts. Supported by every DB engine | Slightly more storage than FLOAT | ✅ **Yes** |
| `BIGINT` storing paise/cents | Store `10050` to represent ₹100.50 (multiply by 100) | Zero rounding error. Fast integer arithmetic | All application code must multiply/divide by 100. Errors if convention is not followed consistently | Acceptable for very high-throughput systems where precision is critical |
| `FLOAT` / `DOUBLE PRECISION` | Native floating-point | Smaller storage. Faster arithmetic | Rounding errors in totals and comparisons. Never acceptable for financial data | Never |

**Multi-currency note:** If the Salesforce org uses multi-currency, store
`CurrencyIsoCode` as a separate `VARCHAR(3)` column alongside the amount column.

---

### 4.2 Percent Field

**What it is in Salesforce:**
Stored as a plain number where `25.5` means 25.5%. Salesforce only adds the `%`
symbol in the UI — the API returns and accepts the raw number.

Examples: `Down_Payment_percent__c` = `20.0`, `Processing_Fee_Percentage__c` = `2.5`.

**Why it is a problem in a relational DB:**
The risk is not a DB type problem — it is a convention problem. If one part of the
system stores `25.5` and another assumes it should be `0.255` (i.e. divides by 100),
calculations will be wrong by a factor of 100.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`NUMERIC(6,2)` storing 0–100 scale** | Store `25.50` to mean 25.5%. Match Salesforce API convention exactly | Consistent with SF. No conversion needed | Application must document the scale convention clearly | ✅ **Yes** |

---

### 4.3 Number Field (Integer and Decimal)

**What it is in Salesforce:**
A numeric field with configurable precision (total digits) and scale (decimal places).
`Length = 18, Decimal = 0` = integer. `Length = 16, Decimal = 2` = two decimal places.

Examples: `Bureau_Score__c` (integer), `Fraud_Probability__c` (decimal), `Age1__c` (integer).

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`INTEGER` for scale=0, `NUMERIC(p,s)` for scale>0** | Match the SF field's precision and scale exactly | Exact equivalent. No data loss | Need to check SF metadata for precision/scale per field | ✅ **Yes** |
| `BIGINT` for all integers | Use 64-bit integer | Avoids overflow for large numbers | Slightly larger storage than `INTEGER` for small numbers | Acceptable |
| `FLOAT` / `DOUBLE` | Floating-point | Simpler | Rounding errors on financial or score values | Never for scores, financials, or exact counts |


---

## Group 5 — Date and Time Fields

---

### 5.1 Date Field

**What it is in Salesforce:**
Stores a calendar date with no time component. SF API returns and accepts it as
`YYYY-MM-DD`.

Examples: `PAN_Dob__c`, `Date_Closed__c`, `DL_LL_Issue_date__c`, `Contact.Birthdate`.

**Why it is a problem in a relational DB:**
If you store a Date value in a DateTime/Timestamp column, it gains a false time
component (`00:00:00`). Date comparisons and display then look wrong.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`DATE` column** | PostgreSQL `DATE` type. Stores `YYYY-MM-DD` exactly. No time component | Exact match. Lightweight storage (4 bytes) | None | ✅ **Yes** |
| `TIMESTAMPTZ` for all date-related columns | Use timestamp for everything | Simpler — one type for all | False time component on date-only fields. Confusing for dates like birthdate or document expiry | Acceptable only if you never need date-only comparison |
| `TEXT` | Store the ISO string `"2026-01-15"` | Zero casting | Cannot sort or compare dates correctly without casting. No date arithmetic | Not recommended |

---

### 5.2 DateTime Field

**What it is in Salesforce:**
Stores a date and time with timezone awareness. SF API returns and accepts it in
ISO 8601 format: `2026-07-15T14:30:00.000+0530`.

Examples: `PreApproved_Date__c`, `CreatedDate`, `KYC_Decision_Date__c`.

**Why it is a problem in a relational DB:**
Storing a DateTime without timezone information means all timestamps are ambiguous
— you do not know whether `14:30:00` is IST, UTC, or something else.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`TIMESTAMP WITH TIME ZONE` (`TIMESTAMPTZ`)** | Stores the moment in time unambiguously. PostgreSQL converts to UTC internally and can return in any timezone | No ambiguity. Correct ordering and arithmetic across timezones | Application must handle timezone display explicitly | ✅ **Yes** |
| `TIMESTAMP WITHOUT TIME ZONE` | Stores the literal date-time as given, with no timezone | Simpler if all data is in one timezone | Ambiguous in multi-timezone systems. Cannot compare correctly across timezones | Only if the entire system is guaranteed single-timezone |
| `TEXT` | Store the ISO string | Zero casting | No time arithmetic. Sorting is string sort, not time sort | Not recommended |

---

### 5.3 Time Field

**What it is in Salesforce:**
Stores a time of day with no date component. Returned as `HH:MM:SS.sssZ`.

Example: `TimeStamp__c` in some audit objects (though in your context this is often
stored as a full DateTime).

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`TIME WITH TIME ZONE`** | PostgreSQL native time type | Exact match for Salesforce Time fields | Rarely needed — most SF Time fields are actually DateTime in practice | ✅ **Yes — if the SF field is genuinely Time type** |
| `TIMESTAMPTZ` with a fixed date | Store as a full timestamp using a fixed epoch date (e.g. `1970-01-01`) | Reuses the same type as DateTime columns | Storing a fake date is semantically wrong | Only if the DB must treat time and datetime uniformly |


---

## Group 6 — Text Fields

---

### 6.1 Text Field (Short)

**What it is in Salesforce:**
A plain text field with a maximum length defined in metadata (up to 255 characters).

Examples: `Name`, `Status`, `Type__c`, `Bureau__c`.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`VARCHAR(n)` matching SF max length** | `VARCHAR(255)` or match the exact SF field length | Enforces the same length limit as SF | Requires looking up the SF field length per field | ✅ **Yes** |
| `TEXT` | No length limit | Never causes truncation errors | Does not replicate SF's length enforcement | Acceptable if length enforcement is not needed |

---

### 6.2 Long Text Area and Rich Text Area

**What it is in Salesforce:**
Long Text Area: plain text up to 131,072 characters.
Rich Text Area: HTML-formatted text up to 131,072 characters.
Both are used for storing large text like API responses, rejection reasons, and
audit payloads.

**Why it is a problem in a relational DB:**
PostgreSQL `TEXT` has no practical size limit. But Salesforce has a hard cap.
During migration the data is within the SF limit, but after migration your system
could store data larger than SF would accept — creating inconsistency if you ever
need to write back to SF.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`TEXT` column, enforce truncation in application** | Store as `TEXT`. The writer truncates at the SF limit (e.g. 130,000 chars) before storing so both systems always hold the same value | Simple. Consistent between SF and DB. No DB-level limit needed | Truncation logic must be in every writer | ✅ **Yes** |
| `TEXT` column, no truncation | Store the full value — DB is the system of record | DB holds complete data | SF and DB diverge. Cannot write back to SF without truncating | Only when DB is the sole system of record |
| `VARCHAR(131072)` | Enforce SF's limit at DB level | Matches SF exactly | Hard limit can cause insert errors if the SF limit ever increases. PostgreSQL `TEXT` is simpler | Not needed |

**Note for Rich Text:** If you need to strip HTML tags for plain-text search, store
both a `rich_text TEXT` (HTML) and a `plain_text TEXT` (stripped) column, or use
PostgreSQL full-text search with the HTML stripped at insert time.

---

### 6.3 Phone Field

**What it is in Salesforce:**
A text field where SF applies light formatting. The API returns and accepts phone
numbers as strings. SF does not enforce a format but the UI normalises display.

Examples: `MobilePhone`, `Phone`, `HomePhone`.

**Why it is a problem in a relational DB:**
Phone numbers arrive in many formats: `+91-9876543210`, `(987) 654-3210`,
`9876543210`. If stored as-is, the same phone number appears as different values,
making deduplication and search unreliable.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`VARCHAR(20)` + normalise before write** | Strip all non-digit characters and leading country codes consistently before storing. Store `9876543210` | Consistent. Deduplication works. Search works | Normalisation logic must be in every writer | ✅ **Yes** |
| Store raw as received | No normalisation | Zero transformation | Same number stored in multiple formats. Deduplication fails | Not recommended |

---

### 6.4 Email Field

**What it is in Salesforce:**
A text field where SF lowercases the value on save. SF can also enforce uniqueness
per object if the unique flag is set.

Examples: `Email`, `Official_Email__c`.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`VARCHAR(254)` + `LOWER()` before write + UNIQUE index if SF had unique flag** | Lowercase the email before storing. `VARCHAR(254)` matches the RFC 5321 max email length | Consistent case. Deduplication works. Matches SF behaviour | None | ✅ **Yes** |
| Store as-is | No normalisation | Simple | `John@Gmail.com` and `john@gmail.com` treated as different records | Not recommended |

---

### 6.5 URL Field

**What it is in Salesforce:**
A text field where SF validates that the value looks like a URL. The API returns
and accepts URL strings without any special encoding.

Examples: `Monnai_URL__c`, `Credit_Bureau_pdf__c`.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`TEXT` column** | Store as plain text. Optionally add URL format validation in the writer | Simple | No built-in validation at DB level | ✅ **Yes** |
| `VARCHAR(2048)` | Cap at a practical URL length | Matches browser and common URL length limits | PostgreSQL `TEXT` is fine. Hard limit can fail on very long S3/presigned URLs | Acceptable |


---

## Group 7 — Boolean Fields

---

### 7.1 Checkbox (Boolean)

**What it is in Salesforce:**
Stores `true` or `false`. In Salesforce there is no NULL — an unchecked checkbox
is `false`, not null. However, the Salesforce API and many integration patterns use
an empty string `''` to mean "do not change this field" (not the same as `false`).

Examples: `PreApproval_Completed__c`, `AA_Required__c`, `summary_flag__c`,
`Finbox_Required__c`.

**Why it is a problem in a relational DB:**
The empty string convention (`''` = skip) is Salesforce-specific. If you write `''`
to a `BOOLEAN` column in PostgreSQL, it throws a type error. If you convert `''` to
`false`, you silently overwrite an existing `true` value — which is incorrect.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`BOOLEAN` + skip empty string** | DB column is `BOOLEAN`. In the writer: if value is `''` → skip the column entirely (do not include it in the INSERT/UPDATE). If `"true"` → `true`. If `"false"` → `false` | Correct. Matches SF API semantics. Existing values are not overwritten when empty | Every writer must follow the empty-string-skip rule consistently | ✅ **Yes** |
| `BOOLEAN NOT NULL DEFAULT FALSE` | Same as above but not nullable | Closer to SF (SF checkbox is never null) | Must handle the `''`-skip rule before reaching the DB | ✅ **Yes — use `NOT NULL DEFAULT FALSE` for SF checkboxes that are never null in SF** |
| Convert `''` to `false` | Treat empty string as `false` and write it | Simple | Incorrect — overwrites `true` values when SF intended "leave unchanged" | Never |
| `SMALLINT` (0/1) | Use integer instead of boolean | Compatible with systems that do not support native boolean | Less expressive. No direct boolean comparison | Only for compatibility with legacy systems |

**Key rule table:**

| Value received from writer | Action in DB |
|---|---|
| `true` or `"true"` | Write `TRUE` |
| `false` or `"false"` | Write `FALSE` |
| `''` (empty string) | Skip this column — do not include in INSERT/UPDATE |
| `null` | Write `NULL` (only if column is nullable) |


---

## Group 8 — Binary and Sensitive Fields

---

### 8.1 Encrypted Field (Classic Encryption / Shield)

**What it is in Salesforce:**
Salesforce Classic Encryption masks a field's value (shows `XXXXX` to users without
the encryption key). Shield Platform Encryption encrypts at rest at the platform
level. Both protect sensitive data like national IDs, financial account numbers,
biometric references.

Examples: Aadhaar numbers, PAN (in some configurations), bank account numbers,
passport numbers.

**Why it is a problem in a relational DB:**
If you store these in plain text in the new DB, you lose the security guarantee that
Salesforce was providing. This is also a regulatory concern — in India, Aadhaar
storage is governed by UIDAI guidelines; PAN by Income Tax Act rules; financial data
by RBI guidelines.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Application-level encryption (AES-256 + KMS)** | The writer encrypts the value using AES-256 before storing. The key is managed by an external key management service (AWS KMS, HashiCorp Vault, Azure Key Vault). The reader decrypts after fetching | Strongest separation — the DB stores ciphertext; the key is never in the DB. Key rotation is possible without data migration | Application complexity. Key management overhead. Decryption needed on every read | ✅ **Yes — for regulated fields (Aadhaar, PAN, account numbers)** |
| PostgreSQL `pgcrypto` extension | `pgp_sym_encrypt(value, key)` on write, `pgp_sym_decrypt(value, key)` on read. Key is passed by the application | DB handles the crypto operation | The key must be passed by the application anyway. If the application is compromised, the key is exposed | Acceptable when an external KMS is not available |
| Transparent Data Encryption (TDE) | Cloud DB service encrypts all data at rest automatically (AWS RDS default, Azure SQL default) | Zero application code. Always on | Protects only against disk theft / storage compromise. Does NOT protect against compromised DB credentials or application-layer attacks | Use as a minimum baseline. Not sufficient alone for regulated sensitive fields |
| Column-level DB encryption | Some DB engines support column-level encryption natively | DB manages the key | Key is inside or near the DB — weaker separation than KMS | Use only if KMS is not available |
| Plain text | No encryption | Simplest | Regulatory risk. Unacceptable for Aadhaar, PAN, biometric data | Never for regulated fields |

**Tokenisation alternative:** For fields that are only compared for equality (e.g.
deduplication on Aadhaar) but never displayed, consider **tokenisation** — store a
hash (`SHA-256(value + salt)`) instead of the value. The token is useless to an
attacker but allows exact-match lookup.

---

### 8.2 File / Attachment (ContentDocument, Attachment)

**What it is in Salesforce:**
Salesforce Files (`ContentDocument` / `ContentVersion`) and legacy Attachments store
binary files linked to records. The file content is stored in Salesforce's blob
storage. The API provides a download URL; the binary is fetched separately.

Examples: Bureau PDF reports, KYC documents, face-match images.

**Why it is a problem in a relational DB:**
Storing large binary files directly in a relational DB is inefficient — it bloats
table size, degrades query performance, and makes backup/restore slow.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Object storage (S3, GCS, Azure Blob) + URL in DB** | Store the file in object storage. Store the URL/key in a `TEXT` column in the DB | Industry standard. Cheap storage. Files served directly from object storage. DB stays lean | Requires managing object storage access control separately | ✅ **Yes** |
| PostgreSQL `BYTEA` column | Store binary content directly in the DB | Everything in one system | Degrades DB performance. Backup size explodes. Not suitable for large files | Only for very small files (< 1 KB thumbnails) |
| PostgreSQL Large Objects (`lo`) | Postgres-specific large object storage alongside the DB | Better than `BYTEA` for large files | Still in the DB. Backup and replication complexity | Not recommended |

**Note:** Your existing mappings already use S3 URLs (`uploadToS3(...)` in CRIF
mapping, `Monnai_URL__c`). The pattern is already correct — store the S3 URL,
not the binary.


---

## Group 9 — Spatial Fields

---

### 9.1 Geolocation Field

**What it is in Salesforce:**
A compound field that stores a latitude/longitude pair. In the Salesforce API, the
compound field splits into two sub-fields using the suffixes `__Latitude__s` and
`__Longitude__s`. Salesforce supports geo-distance queries in SOQL using
`DISTANCE()` and `GEOLOCATION()`.

Examples: `Dealer_latitude__c` / `Dealer_longitude__c` on Dealer_Info__c,
`Latitude__c` / `Longitude__c` on Contact.

**Why it is a problem in a relational DB:**
The `__Latitude__s` / `__Longitude__s` compound field syntax does not exist in
SQL. The SOQL `DISTANCE()` function has no automatic equivalent.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Two separate `NUMERIC(9,6)` columns** | `latitude NUMERIC(9,6)`, `longitude NUMERIC(9,6)`. Store the raw decimal degree values (e.g. `28.613939`, `77.209023`) | Simple. Direct mapping from SF API sub-fields. No extension needed. Good for storing and reading values | No native geo query support. Distance calculation must be done in application code using Haversine formula | ✅ **Yes — if geo queries are not needed** |
| **PostgreSQL `POINT` type** | `location POINT`. Store as `POINT(longitude, latitude)` | Single column for the pair. Supports basic geometric operators | Limited query support without PostGIS. Coordinate order can be confusing (PostGIS uses lon/lat, not lat/lon) | ✅ **Yes — if simple point storage and distance queries are needed** |
| **PostGIS `GEOMETRY(Point, 4326)`** | Install PostGIS extension. `location GEOMETRY(Point, 4326)` where SRID 4326 = WGS84 (standard GPS coordinate system) | Industry standard for geo data. Full geo query support: distance, radius search, containment, spatial indexing | Requires PostGIS extension. More complex setup | ✅ **Yes — if geo queries (distance between two points, radius search) are needed** |
| Store as JSON `{"lat": 28.6, "lng": 77.2}` | `JSONB` column | Flexible | No native geo query support. Harder to index | Not recommended |

**Choosing between the options:**

| Use case | Recommended option |
|---|---|
| Just store and display lat/lng | Two separate `NUMERIC(9,6)` columns |
| Calculate distance in application code | Two separate `NUMERIC(9,6)` columns |
| Query "find all records within 10km of this point" | PostGIS `GEOMETRY(Point, 4326)` |
| Query "sort by distance from a reference point" | PostGIS `GEOMETRY(Point, 4326)` |


---

## Master Summary Table

| SF Field Type | Recommended DB Column Type | On Write | On Read | Key Rule |
|---|---|---|---|---|
| **Formula** | Generated column (`GENERATED ALWAYS AS`) or plain column | **Skip** — never write directly | Computed by DB automatically or pre-computed | Add to skip list. Translate formula to SQL |
| **Roll-Up Summary** | Plain column (`NUMERIC` or `INTEGER`) | **Skip** direct write. Recalculate after child changes | Plain read | Skip list + recalculate in same transaction as child write |
| **Auto Number** | `VARCHAR` with sequence default | **Skip** — DB sequence generates value | Plain read | `CREATE SEQUENCE`. Copy existing SF values during migration |
| **CreatedDate / LastModifiedDate** | `TIMESTAMPTZ DEFAULT NOW()` | **Skip** — DB default sets value | Plain read | Preserve original SF timestamps for migrated records |
| **CreatedById / LastModifiedById** | `VARCHAR` | Write calling service / user identifier | Plain read | Store SF User ID for migrated records. Switch to own IDs after migration |
| **Restricted Picklist** | `VARCHAR` + `CHECK` constraint | Validate value before write | Plain read | `CHECK (col IN ('val1','val2',NULL))` |
| **Dependent Picklist** | `VARCHAR` + application validation | Validate parent+child pair in code | Plain read | Store allowed pairs in a reference table |
| **Multi-Select Picklist** | `TEXT` (migration) → `TEXT[]` (target) | Keep semicolon format or convert to array | `= ANY(col)` for array queries | Decide format once. Add GIN index for array |
| **Lookup Relationship** | Nullable FK, `ON DELETE SET NULL` | Write the related record's ID | Plain read | No cascade delete — child survives parent deletion |
| **Master-Detail Relationship** | `NOT NULL` FK, `ON DELETE CASCADE` | Write the related record's ID | Plain read | Insert parent before child. Cascade delete is automatic |
| **Self-Referential Lookup** | Nullable self-referential FK | Write parent ID. Check for cycles | Recursive CTE for tree traversal | `NULL` = root of hierarchy |
| **Polymorphic Lookup** | `related_id VARCHAR` + `related_type VARCHAR` | Write both ID and type | Plain read | No FK constraint possible |
| **External ID** | `VARCHAR` + `UNIQUE` index | `INSERT ... ON CONFLICT (col) DO UPDATE` | Plain read | Atomic upsert. Safe for retries |
| **Currency** | `NUMERIC(18,2)` | Cast string to decimal | Plain read | Never use `FLOAT` for money |
| **Percent** | `NUMERIC(6,2)` | Store 0–100 scale as-is from SF API | Plain read | Do not divide by 100. SF stores `25.5` for 25.5% |
| **Number (integer)** | `INTEGER` or `BIGINT` | Cast string to integer | Plain read | Match SF field scale = 0 |
| **Number (decimal)** | `NUMERIC(p,s)` matching SF field definition | Cast string to decimal | Plain read | Match SF field precision and scale |
| **Date** | `DATE` | Cast `YYYY-MM-DD` string to `DATE` | Plain read | Never mix with DateTime columns |
| **DateTime** | `TIMESTAMPTZ` | Cast ISO 8601 string to `TIMESTAMPTZ` | Plain read | Always store with timezone. Use UTC internally |
| **Time** | `TIME WITH TIME ZONE` | Cast `HH:MM:SS.sssZ` string | Plain read | Rarely used in SF — verify field type |
| **Text (short)** | `VARCHAR(n)` matching SF length | Plain write | Plain read | Match SF max length from metadata |
| **Long Text Area** | `TEXT` | Truncate at SF limit (130,000 chars) in writer | Plain read | Keep truncation in writer so SF and DB hold the same value |
| **Rich Text Area** | `TEXT` | Truncate at SF limit. Strip HTML for search index | Plain read + optional plain-text search column | Consider storing HTML + stripped-text separately |
| **Phone** | `VARCHAR(20)` | Normalise: strip special chars and spaces | Plain read | Same normalisation everywhere for consistency |
| **Email** | `VARCHAR(254)` | `LOWER()` before storing. Add `UNIQUE` if SF field was unique | Plain read | Consistent case. Deduplication requires normalisation |
| **URL** | `TEXT` | Plain write. Optional format validation | Plain read | No special handling needed |
| **Checkbox (Boolean)** | `BOOLEAN` (`NOT NULL DEFAULT FALSE` if mandatory) | `''` → skip column. `"true"` → `TRUE`. `"false"` → `FALSE` | Plain read | Empty string = skip, not false. Most critical rule |
| **Encrypted Field** | `TEXT` (ciphertext) | Encrypt with AES-256 before storing | Decrypt after fetching | Use KMS for key management. Never plain text for Aadhaar / PAN |
| **File / Attachment** | `TEXT` (object storage URL) | Upload binary to S3/GCS. Store URL in DB | Fetch binary from object storage using URL | Never store binary in DB column |
| **Geolocation** | Two `NUMERIC(9,6)` columns or PostGIS `GEOMETRY(Point,4326)` | Write lat and lng separately | Plain read or geo query | Use PostGIS only if geo-distance queries are needed |

---

## Quick Reference — Fields That Need a Skip List

The following field types must **never be written directly** to the DB. Add them
to a skip list in your write layer:

| Field Type | Why skip on write |
|---|---|
| Formula | Salesforce computes it. DB uses generated column or pre-computation |
| Roll-Up Summary | Value comes from child aggregation, not a direct write |
| Auto Number | DB sequence generates it |
| CreatedDate, LastModifiedDate, SystemModstamp | DB default generates it |
| CreatedById, LastModifiedById | Set by DB context, not by the record payload |

---

## Quick Reference — Fields That Need Type Casting in the Writer

All Salesforce API values arrive as strings. The writer must cast before storing:

| Field Type | Cast to |
|---|---|
| Number (integer) | `INTEGER` / `BIGINT` |
| Number (decimal), Currency, Percent | `NUMERIC(p,s)` |
| Date | `DATE` |
| DateTime | `TIMESTAMPTZ` |
| Checkbox | `BOOLEAN` (with empty-string-skip rule) |
| Encrypted field | Encrypt first, then store as `TEXT` |

---

## Quick Reference — Fields That Need Normalisation Before Write

| Field Type | Normalisation |
|---|---|
| Phone | Strip all non-digit characters and spaces |
| Email | `LOWER()` — store in lowercase |
| Multi-Select Picklist | Optionally split semicolons into `TEXT[]` array |
| Long Text / Rich Text | Truncate at SF character limit |
| Encrypted Field | Encrypt value before storing |
