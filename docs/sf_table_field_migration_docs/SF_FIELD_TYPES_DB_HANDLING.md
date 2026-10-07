# Salesforce Special Field Types — Handling in New Database

This document covers every Salesforce field type that needs special handling when
migrating to a relational database. For each type, multiple options are listed with
the recommended option clearly marked.

---

## Group 1 — Fields Salesforce controls (you cannot write to them)

Salesforce owns the value. DM never writes these today. The DB writer must skip them
on write. They need a separate strategy to stay accurate on read.

---

### 1.1 Formula Field

**What it is:** A field whose value is calculated automatically by Salesforce from
other fields on the same record. Example: `Opportunity_Record_Type_Formula__c`
computed from `Business_Type__c` + `Product_Type__c`.

**Problem:** No auto-calculation in a relational DB. A plain column goes stale the
moment a source field changes.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Generated column** | PostgreSQL `GENERATED ALWAYS AS (expression) STORED` — DB computes the value automatically whenever source columns change | Stays in sync automatically, just like SF. No application code needed | Formula must be translated from SF formula syntax to SQL. `ALTER TABLE` needed to change the formula | ✅ **Yes — for simple formulas** |
| Plain stored column, updated by writer | Application writes the computed value explicitly whenever source fields change | Simple to implement now | Can go stale if source fields are updated from outside DM. Extra update step needed | For complex formulas only |
| View / computed at query time | SQL view or subquery computes the value on every read | Always accurate | Performance hit on every read. Cannot be indexed directly | Only if formula is rarely read |

**Action in DB writer:** Add formula fields to a **skip list** — never attempt to
INSERT or UPDATE them. Salesforce rejects writes to formula fields; the DB should
behave the same way.

**Example from your mappings:** `<Lead.Opportunity_Record_Type_Formula__c>` is read
by ESA and passed to Actico/PostBureau. It must exist as a readable column in the DB
but must never be written directly.

---

### 1.2 Roll-Up Summary Field

**What it is:** A field on a parent record that aggregates values from child records
(COUNT, SUM, MAX, MIN). Example: a Lead's `No_of_re_decisioning__c` counting related
Offer records.

**Problem:** The value depends on rows in another table. A plain column goes stale
whenever a child row is inserted, updated, or deleted.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Application-level update** | After DM inserts or updates child rows, it recalculates and updates the parent's roll-up column in the same DB transaction | Full control. Works naturally with DM's existing transaction model | Logic lives in application code. Must be done consistently every time children change | ✅ **Yes — start here** |
| DB trigger | PostgreSQL trigger on the child table fires on INSERT/UPDATE/DELETE and updates the parent column automatically | Automatic. Catches all writes, even from outside DM | Harder to debug. Trigger logic can cause surprises in transactions | ✅ **Yes — add later for writes from outside DM** |
| Compute at query time | SQL `SELECT COUNT(*)` / `SUM()` subquery when the parent is read | Always accurate | Slow for large child sets. Cannot be indexed | Only for rarely-read roll-ups |

**Action in DB writer:** Skip roll-up fields on direct write (same as formula). After
inserting child rows, fire the recalculation as the last step in the same transaction.

---

### 1.3 Auto Number Field

**What it is:** SF automatically generates a formatted sequential number when a record
is created. Example: `APP-000001`, `APP-000002`. You cannot set or override this value.

**Problem:** A plain `VARCHAR` column with no sequence will have gaps or duplicates.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **PostgreSQL SEQUENCE + format function** | `CREATE SEQUENCE app_seq; DEFAULT 'APP-' \|\| LPAD(nextval('app_seq')::text, 6, '0')` | Exact equivalent of SF auto-number. Guaranteed unique and sequential | Sequence format must match SF's pattern | ✅ **Yes** |
| UUID | Use a UUID as the PK instead | Simpler. No sequence to manage | Loses the human-readable format that downstream systems may depend on | Only if the formatted number is not used externally |
| Copy from SF during migration | Read the existing SF value and store it as-is | Keeps continuity for existing records | New records after migration need the sequence anyway | Do both — copy existing, then use sequence for new |

---

### 1.4 System Timestamps (CreatedDate, LastModifiedDate, SystemModstamp)

**What they are:** SF sets these automatically. You cannot write to them via the API.

**Problem:** If you store these as plain columns and try to write them, SF rejects it.
In the new DB, nothing sets them automatically unless you configure it.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **DB defaults + trigger** | `created_at TIMESTAMP DEFAULT NOW()`, `updated_at TIMESTAMP DEFAULT NOW()` with an `ON UPDATE` trigger | Automatic. Always accurate | `updated_at` requires a trigger or application discipline | ✅ **Yes** |
| Application sets the value | Writer explicitly sets `NOW()` on insert/update | Simple | Can be forgotten. Inconsistent if multiple writers exist | Acceptable as a start |
| Copy from SF response | Store SF's original timestamps when migrating existing data | Preserves history | New records after migration need DB-generated values | Do for migration only |

**Action in DB writer:** Skip these fields on write. Let DB defaults handle them.

---

### 1.5 System User Fields (CreatedById, LastModifiedById)

**What they are:** SF sets these to the ID of the user or API client that made the
change. Read-only via the API.

**Problem:** SF uses 18-char User record IDs. In the new DB there may be no User table.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Store as plain VARCHAR** | Keep the SF User ID or a service name string | Simple. No FK constraint needed | No referential integrity | ✅ **Yes — during migration** |
| FK to a users table | Create a `users` table and enforce FK | Proper relational design | Requires migrating all SF users to the new DB first | Later, once user management is in the new DB |
| Omit entirely | Do not store these fields | Simpler schema | Lose audit trail of who made each change | Not recommended |

---

## Group 2 — Fields with value constraints

---

### 2.1 Restricted Picklist

**What it is:** Only values from a predefined list are allowed. SF rejects any other
value at the API level. Example: `Credit_Decision__c` can only be `Pre Approved` or
`Reject`.

**Problem:** A plain `VARCHAR` accepts any string. Bad values can silently enter the DB.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **CHECK constraint** | `CHECK (credit_decision IN ('Pre Approved', 'Reject', NULL))` | Enforced at DB level. Visible in schema. Easy to add new values via `ALTER TABLE` | Requires a migration to add/remove values | ✅ **Yes** |
| PostgreSQL ENUM | `CREATE TYPE credit_decision_enum AS ENUM ('Pre Approved', 'Reject')` | Strongly typed | Adding a value requires `ALTER TYPE` — more disruptive than CHECK | Acceptable, but CHECK is more flexible |
| Reference table (FK) | `picklist_values` table with allowed values. FK constraint on the column | Most flexible — add values by inserting a row, no schema migration | Extra table. More complex queries | Best for picklists that change frequently |
| Application-level only | Validate in the DB writer before INSERT | Simple | No DB-level protection. Direct inserts bypass it | Not recommended as the only guard |

---

### 2.2 Dependent Picklist

**What it is:** The allowed values in a child picklist depend on the selected value of
a parent picklist. Example: `Sub_Program__c` values depend on `Program_Type__c`.

**Problem:** A DB CHECK constraint on the child field alone cannot express "this value
is only valid when the parent has this value."

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Application-level validation** | DB writer checks: given the parent value, is the child value in the allowed set? | Full control. Can replicate exact SF dependency matrix | Logic lives in application code | ✅ **Yes** |
| DB CHECK with all valid child values | CHECK constraint lists every valid child value regardless of parent | Simple | Does not enforce the dependency — any child value is accepted with any parent | Only as a secondary safety net |
| Reference table with parent-child pairs | `(program_type, sub_program)` allowed combinations stored in a table. CHECK via FK | Fully enforced at DB level | More complex schema and queries | Good for large dependency matrices |

---

## Group 3 — Relationship fields

---

### 3.1 Lookup Relationship

**What it is:** A loosely coupled relationship. The child record can exist even if the
parent is deleted. Stores the parent's 18-char SF ID. Example: `Contact__c` on
`ES_Contact__c`.

**Problem:** SF allows orphaned records (parent deleted, child remains). A strict DB FK
constraint rejects inserts when parent does not exist.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Nullable FK, no cascade** | FK column, nullable, no `ON DELETE CASCADE` | Matches SF behaviour. Orphans allowed | No referential integrity enforcement | ✅ **Yes — matches SF semantics** |
| FK with `SET NULL on delete` | FK + `ON DELETE SET NULL` | Child survives parent deletion. FK still enforced on insert | Parent must exist at insert time | Good if you want looser SF-like behaviour with some integrity |
| Strict FK | FK + `ON DELETE RESTRICT` | Full referential integrity | Breaks on parent delete, unlike SF | Only if you want stricter data quality than SF |

---

### 3.2 Master-Detail Relationship

**What it is:** A tightly coupled parent-child relationship. The child cannot exist
without its parent. Deleting the parent deletes all children (cascade delete). Example:
`MultiBureau_AccountList__c` → `Multibureau_Data__c`.

**Problem:** Must enforce that child cannot be orphaned, and parent deletion removes
all children.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`NOT NULL` FK + `ON DELETE CASCADE`** | FK column is NOT NULL. `ON DELETE CASCADE` removes children when parent is deleted | Exact equivalent of SF Master-Detail | Requires parent to always exist before child insert — which is already guaranteed by DM's topological sort | ✅ **Yes** |
| `NOT NULL` FK, no cascade | Reject orphaned children but do not auto-delete | Prevents orphans | Manual cleanup needed on parent delete | Only if cascade delete is not wanted |

---

### 3.3 Self-Referential (Hierarchical) Lookup

**What it is:** A lookup to another record of the same type. SF uses this for User
hierarchies (`ReportsToId`). Custom objects can also have self-lookups.

**Problem:** Self-referential FKs can create infinite loops if not handled carefully.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Self-referential FK, nullable** | FK points to the same table. `NULL` = top of hierarchy | Standard SQL pattern | Circular references possible if not guarded | ✅ **Yes, with application-level cycle check** |
| No FK constraint, plain ID column | Store parent ID as plain VARCHAR | No cycle risk | No referential integrity | Only as a fallback |

---

### 3.4 Polymorphic Lookup (WhatId / WhoId)

**What it is:** A single field that can point to records from multiple different
object types. Example: `WhatId` on a Task can point to a Lead or an Opportunity.

**Problem:** A single FK column cannot reference two different tables.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **ID column + type column** | `what_id VARCHAR, what_type VARCHAR` (e.g. `'Lead'`, `'Opportunity'`). No FK constraint | Simple. Matches SF's own storage model | No DB-level referential integrity | ✅ **Yes** |
| Separate FK per type | `lead_id FK, opportunity_id FK` — only one populated | Full FK integrity | Schema gets wide as types grow | Only if the number of target types is small and fixed |

---

## Group 4 — Fields with special value formats

---

### 4.1 Checkbox (Boolean)

**What it is:** Stores `true` or `false`. But in your DM mappings, the pattern
`{{condition ? true : ''}}` is used — an empty string means "do not change this
field", not `false`.

**Problem:** `''` (empty string) must be treated as "skip this column", not as
`false`. Writing `''` to a boolean column causes a type error.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Skip empty, cast non-empty** | In DB writer: if value is `''` → skip column entirely. If `"true"` → `true`. If `"false"` → `false` | Matches SF behaviour exactly. Already the rule for all empty fields | Must be consistent across all boolean columns | ✅ **Yes** |
| Treat `''` as `false` | Write `false` when value is empty string | Simple | Incorrect — overwrites the existing DB value when SF would have left it unchanged | Never do this |

**Examples from your mappings:**
- `"PreApproval_Completed__c": true` → write `true`
- `"AA_Required__c": "{{...!='TRUE')?true:''}}"` → write `true` or skip
- `"summary_flag__c": true` → always write `true`

---

### 4.2 Date vs DateTime

**What it is:** `Date` stores `YYYY-MM-DD`. `DateTime` stores
`YYYY-MM-DDTHH:mm:ss.SSSZ` with timezone. They are different types in SF.

**Problem:** Storing a DateTime value in a Date column truncates the time. Storing a
Date in a DateTime column adds a wrong time component.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Match SF type exactly** | `DATE` column for SF Date fields. `TIMESTAMP WITH TIME ZONE` for SF DateTime fields | Exact equivalent. No data loss | Requires knowing which SF fields are Date vs DateTime | ✅ **Yes** |
| Store everything as `TIMESTAMP WITH TIME ZONE` | Use timestamp for all date-related columns | Simpler — no need to differentiate | Wastes storage for plain dates. Confusing for date-only fields | Acceptable if you do not want to differentiate |
| Store as `TEXT` | Keep the ISO string as-is | Zero casting errors | No date arithmetic. Cannot sort or filter correctly | Not recommended |

**Examples from your mappings:**
- `"formatDate(now(),'YYYY-MM-DDTHH:mm:ss.SSSZ')"` → `TIMESTAMP WITH TIME ZONE`
- `"PAN_Dob__c"`, `"Date_Closed__c"`, `"date_of_birth"` → `DATE`

---

### 4.3 Long Text Area / Rich Text Area

**What it is:** Long Text Area allows plain text up to 131,072 characters. Rich Text
Area allows HTML-formatted text up to 131,072 characters. Examples: `Response__c`,
`All_rejection_reasons__c`, CIBIL/CRIF raw bureau responses.

**Problem:** PostgreSQL `TEXT` has no practical size limit, but Salesforce has a hard
cap. Your existing DM mappings already use `truncateRaw(..., 130000)` to stay under
the SF limit. The DB must store whatever DM writes — which is already truncated.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`TEXT` column, keep truncateRaw in DM** | PostgreSQL `TEXT`. DM's `truncateRaw` call stays in the mapping so both SF and DB get the same value | Simple. Consistent between SF and DB | | ✅ **Yes** |
| `TEXT` column, remove truncation | Store full untruncated value in DB | DB has more complete data | SF and DB would then have different data. Inconsistency | Only if DB is the system of record and full data is needed |
| `VARCHAR(131072)` | Enforce the SF limit at DB level | Matches SF exactly | PostgreSQL `TEXT` is already fine. Hard limit can cause insert errors if limit changes | Not needed |

---

### 4.4 Multi-Select Picklist

**What it is:** A picklist that allows multiple values to be selected simultaneously.
SF stores them as a semicolon-separated string: `"Value1;Value2;Value3"`.

**Problem:** A plain string column makes querying for a specific value awkward
(`LIKE '%Value1%'` has false positives).

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`TEXT` column, keep semicolon format** | Store `"Value1;Value2;Value3"` as-is | Zero change from SF. Simple | Querying individual values needs `LIKE` or `string_to_array` | ✅ **Yes — during migration, for simplicity** |
| PostgreSQL `TEXT[]` array | Store as `ARRAY['Value1','Value2','Value3']` | Clean queries using `= ANY(column)` | DM must convert the semicolon string to an array before storing. Reads back as array not string | ✅ **Yes — better long-term** |
| Separate junction table | One row per selected value | Fully normalised | Overkill for most picklist use cases | Only if individual values need their own metadata |

---

### 4.5 Currency Field

**What it is:** Stores a monetary amount. In multi-currency SF orgs, each record also
has a `CurrencyIsoCode` field. Examples: `Amount_in_Rs__c`, `Processing_Fee__c`,
`Eligible_Loan_Amount__c`.

**Problem:** Floating-point types (`FLOAT`, `DOUBLE`) have rounding errors with
financial data.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`DECIMAL(18,2)` or `NUMERIC(18,2)`** | Fixed-point exact storage | No rounding errors. Industry standard for financial values | Slightly more storage than FLOAT | ✅ **Yes** |
| `FLOAT` / `DOUBLE PRECISION` | Floating-point storage | Smaller storage | Rounding errors in financial calculations | Never for currency |

---

### 4.6 Percent Field

**What it is:** Stored as a plain number (e.g. `25.5` for 25.5%). SF adds the `%`
symbol only in the UI. Examples: `Down_Payment_percent__c`,
`Processing_Fee_Percentage__c`.

**Problem:** None really — it is a plain number. Risk is treating it as a 0–1 decimal
(`0.255`) instead of 0–100.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`DECIMAL(6,2)`** | Store the raw SF value (e.g. `25.5`) | Matches what SF API returns | None | ✅ **Yes** |

---

### 4.7 Phone Field

**What it is:** Plain text but SF normalises formatting on save (strips brackets,
dashes). Examples: `MobilePhone`, `Phone`.

**Problem:** Inconsistent formats entering the DB if not normalised.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`VARCHAR(20)` + normalise before write** | Strip special chars and spaces before storing. Your DM mappings already use `cleanSpecialChars` + `remove_spaces` — apply the same before DB insert | Consistent format. Already done in DM | | ✅ **Yes** |
| Store raw as-is | No normalisation | Simple | Inconsistent formats make deduplication and querying hard | Not recommended |

---

### 4.8 Email Field

**What it is:** Plain text. SF lowercases on save and can enforce uniqueness.
Examples: `Email`, `Official_Email__c`.

**Problem:** Case inconsistency (`JOHN@GMAIL.COM` vs `john@gmail.com` treated as
different).

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`VARCHAR(254)` + `LOWER()` before write** | Lowercase the email before storing. Add a unique index if SF had the unique flag | Consistent. Matches SF behaviour | | ✅ **Yes** |
| Store as-is | No normalisation | Simple | Case inconsistency. Deduplication fails | Not recommended |

---

### 4.9 Encrypted Field

**What it is:** SF Classic Encryption or Shield Platform Encryption. Sensitive data
like Aadhaar numbers, SSNs are stored encrypted. SF decrypts on read based on the
user's permission set.

**Problem:** If you store these in plain text in the new DB, you lose the security
guarantee that SF was providing.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Application-level encryption** | Encrypt before storing, decrypt after reading. Use AES-256. Key managed separately (AWS KMS, Vault) | Strongest. Key is independent of DB | Application complexity. Key rotation needed | ✅ **Yes** |
| PostgreSQL `pgcrypto` | `pgp_sym_encrypt()` / `pgp_sym_decrypt()` inside the DB | DB handles encryption | Key stored in or near DB — less separation | Acceptable if KMS is not available |
| Transparent Data Encryption (TDE) | DB-level encryption at rest (RDS default) | Zero application change | Does not protect against compromised DB credentials — only against disk theft | Use as a minimum baseline, not as the only control |
| Plain text | No encryption | Simplest | Regulatory risk (PDPA, RBI guidelines). Not acceptable for Aadhaar/PAN | Never for regulated fields |

---

### 4.10 Geolocation Field

**What it is:** A compound field with two sub-fields: `Field__Latitude__s` and
`Field__Longitude__s`. SF stores and queries these as a pair. Examples:
`Latitude__c`, `Longitude__c` on Contact (though in your mappings these are stored
as separate custom fields, not as a compound geo field).

**Problem:** SF's compound field syntax (`__Latitude__s`, `__Longitude__s`) does not
exist in a relational DB.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **Two separate `DECIMAL(9,6)` columns** | `latitude DECIMAL(9,6)`, `longitude DECIMAL(9,6)` | Simple. Matches how your mappings already treat them as separate fields | No built-in geo query support | ✅ **Yes — for your current use case** |
| PostgreSQL `POINT` or PostGIS `GEOMETRY` | Native geo type. Supports distance queries, indexing | Powerful geo queries | Requires PostGIS extension. Overkill if you only store values | Use if geo queries (distance, radius) are needed |

---

### 4.11 External ID Field

**What it is:** A field marked as "External ID" in SF. SF uses it for upsert-by-key
operations (insert if not exists, update if exists). Also used for de-duplication.
Examples: `Application_Id__c`.

**Problem:** In a plain DB table, there is nothing enforcing uniqueness on this
column.

| Option | How it works | Pros | Cons | Recommended? |
|---|---|---|---|---|
| **`UNIQUE` constraint + use as upsert key** | `CREATE UNIQUE INDEX ON table(external_id)`. Use `INSERT ... ON CONFLICT (external_id) DO UPDATE` | Exact equivalent of SF External ID upsert. Safe for retries | | ✅ **Yes** |
| Plain column, no constraint | No uniqueness enforced | Simple | Duplicates possible. Cannot use as upsert key | Not recommended |

---

## Master Summary Table

| SF Field Type | DB Column Type | Write behaviour | Read behaviour | Key rule |
|---|---|---|---|---|
| Formula | Generated column or plain column | **Skip on write** | Computed by DB or pre-computed | Add to skip list |
| Roll-up Summary | Plain column | **Skip on write**, update via trigger or app after child changes | Plain read | Add to skip list + recalc after child write |
| Auto Number | `VARCHAR` with sequence default | **Skip on write** | Plain read | DB sequence generates value |
| CreatedDate / LastModifiedDate | `TIMESTAMP WITH TIME ZONE DEFAULT NOW()` | **Skip on write** | Plain read | DB default sets value |
| CreatedById / LastModifiedById | `VARCHAR` | Write service/user identifier | Plain read | Skip SF user ID format |
| Restricted Picklist | `VARCHAR` + CHECK constraint | Validate before write | Plain read | CHECK constraint enforces list |
| Dependent Picklist | `VARCHAR` + app validation | Validate parent+child pair | Plain read | App-level dependency matrix |
| Lookup | Nullable FK column | Write ID value | Plain read | No cascade delete |
| Master-Detail | `NOT NULL` FK + CASCADE | Write ID value | Plain read | `ON DELETE CASCADE` |
| Polymorphic Lookup | ID `VARCHAR` + type `VARCHAR` | Write both ID and type | Plain read | No FK constraint |
| Checkbox | `BOOLEAN` | `''` → skip, `"true"` → true, `"false"` → false | Plain read | Empty string = skip, not false |
| Date | `DATE` | Cast `YYYY-MM-DD` string to DATE | Plain read | Do not mix with DateTime |
| DateTime | `TIMESTAMP WITH TIME ZONE` | Cast ISO string to TIMESTAMPTZ | Plain read | Use `SSSZ` format from DM |
| Long Text / Rich Text | `TEXT` | Truncate at 130,000 chars (already done via `truncateRaw`) | Plain read | Keep `truncateRaw` in DM |
| Multi-select Picklist | `TEXT` or `TEXT[]` | Keep semicolon format or convert to array | Use `= ANY()` for array | Decide format once, be consistent |
| Currency | `DECIMAL(18,2)` | Cast string to decimal | Plain read | Never use FLOAT |
| Percent | `DECIMAL(6,2)` | Cast string to decimal | Plain read | Store as 0–100, not 0–1 |
| Phone | `VARCHAR(20)` | Normalise (strip special chars + spaces) | Plain read | Same normalisation as DM templates |
| Email | `VARCHAR(254)` | Lowercase before storing | Plain read + unique index if unique in SF | Consistent case |
| Encrypted | `TEXT` (encrypted) | Encrypt before write | Decrypt after read | Use AES-256 + KMS |
| Geolocation | Two `DECIMAL(9,6)` columns | Write lat and long separately | Plain read | PostGIS if geo queries needed |
| External ID | `VARCHAR` + UNIQUE index | `INSERT ... ON CONFLICT DO UPDATE` | Plain read | Use as upsert key |
| Auto Number | `VARCHAR` + sequence | Skip on write | Plain read | Sequence generates formatted value |
