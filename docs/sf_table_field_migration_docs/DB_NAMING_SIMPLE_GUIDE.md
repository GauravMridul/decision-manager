# New Database — Naming Choice: Simple Comparison Guide

Two options for naming tables and columns in the new database:

- **Option A — Keep Salesforce names:** (`Audit_Log__c`, `Type__c`, `Contact__c`, `MailingCity`)
- **Option B — Use clean names:** (`audit_log`, `type`, `contact_id`, `mailing_city`)

---

## How Decision Manager (DM) works today

```
Request comes in
      │
      ▼
Fetch partner service sequence   ← from postgres (partner_service_mapping table)
      │
      ▼
Call ESA to run all vendor APIs  ← ESA returns all vendor responses in one JSON
      │
      ▼
Fetch service_sfdc_field_mapping ← from postgres + Redis cache
      │
      ▼
Run PreProcessings               ← parse CRIF/bureau raw responses into reusable sub-trees
      │
      ▼
Interpolate all templates        ← fill in all ((values)), <object.field>, {{expressions}}
      │                             Result: a plain list of "what to save"
      ▼
Write to Salesforce (Composite)  ← send the filled list to SF
      │
      ▼
Call Apex callback               ← notify SF that decision is done
      │
      ▼
Save audit log to MongoDB        ← async, stores full request + response for replay
```

The key step is **Interpolate all templates** — by the time this finishes, the
`service_sfdc_field_mapping` config has been fully resolved into a plain list of records to
save. That resolved list is what needs to go to the new DB.

---

## How ESA works today

```
Request comes in
      │
      ▼
Read query_object_relationship_map   ← tells ESA which SF objects to read and how
      │
      ▼
Scan service_configuration           ← find all <Object.Field> tokens in API configs
      │
      ▼
Build SOQL and query Salesforce      ← fetch all needed records from SF
      │
      ▼
Store results in memory (masterDTO)  ← keyed by SF object/field name
      │
      ▼
Call vendor APIs                     ← resolve <Object.Field> tokens from masterDTO
      │
      ▼
Return all vendor responses to DM
```

The key step is **Store results in memory** — results are stored using SF field names as
keys. Every `<Object.Field>` token in every vendor API call resolves against these keys.

---

## Changes required in BOTH options

These must be done regardless of naming choice.

| # | What needs to change | Where | Why |
|---|---|---|---|
| 1 | Add `db_request_body` column to `service_sfdc_field_mapping` table | DM — postgres schema | The existing `request_body` is in Salesforce Composite format (has `/services/data/v64.0/sobjects/...` URLs, `@{Ref.id}` links). A new DB-native column is needed alongside it. The existing column stays untouched. |
| 2 | Write a DB writer that reads `db_request_body` and does INSERT/UPDATE | DM — new code | After interpolation produces the resolved list, a DB writer must apply it to the new DB using the same logic flow. |
| 3 | Replace `@{ParentRef.id}` with explicit `parentRef` + `parentForeignKey` in `db_request_body` | DM — new config format | `@{Ref.id}` is Salesforce Composite API syntax only. The DB writer needs to know explicitly: "this child's FK column points to this parent". |
| 4 | DB writer must insert parent before child rows | DM — new code | DM already has `buildDependencyGraph` + `topologicalSort` that figures out the correct order. The DB writer reuses this ordering and wraps it in a single DB transaction. |
| 5 | Generate IDs up front (UUIDs) before writing | DM — new code | SF generates its own IDs after you POST. With a real DB you can generate a UUID before writing, so the child already knows the parent's ID. No read-back needed. |
| 6 | Skip any field whose value is blank | DM — new code | SF Composite silently ignores blank values. Many mappings intentionally produce `''` (e.g. `{{condition ? true : ''}}`). A real DB column will reject or misstore a blank. The DB writer must drop the column if value is empty. |
| 7 | Cast string values to correct DB types | DM — new code | All values from the mapping are strings. The DB writer must convert `"true"` → boolean, `"123.45"` → number, `"2026-01-01"` → date before saving. |
| 8 | Add a `source` flag per object in `query_object_relationship_map` | ESA — postgres schema | ESA needs to know which objects to read from SF and which from the new DB. Without this flag it can only read from one place. |
| 9 | Replace Salesforce relationship paths with SQL JOINs | ESA — new code | Paths like `contact__r.Lead__c` and `Multibureau__r.Contact__r.Lead__c` are SOQL-only. Even with identical column names, a real DB needs an explicit JOIN or FK filter to navigate relationships. |
| 10 | Translate SOQL query syntax to SQL | ESA — new code | `ORDER BY CreatedDate DESC LIMIT 1`, `LAST_N_DAYS:30`, and similar clauses in `additional_conditions` are Salesforce-specific. Each must be written as equivalent SQL. |
| 11 | Reproduce the same record shape that ESA expects | ESA — new code | ESA expects results as `{records: [...]}` or a single object. SQL results must be shaped the same way before being stored in memory, otherwise every `<Object.Field>` resolution breaks. |
| 12 | Dual-write consistency — what happens if one store fails | DM — new code | Writing to two stores is not atomic. The MongoDB audit log (already stores the full resolved list + SF response) acts as a natural replay log. A retry mechanism is needed so the two stores don't silently drift. |
| 13 | Apex callback stays SF-only | DM — no change needed | The `CallDecisionCallbackApex` step after the SF write is SF-specific. There is no equivalent needed for the new DB. |
| 14 | Redis cache is unaffected | DM + ESA — no change | Redis caches `service_sfdc_field_mapping` and `partner_service_mapping` by name. Naming in the DB does not affect the cache key or cached content. |
| 15 | Keep in-flow objects on one store until tested | DM + ESA — process | Some objects are written by DM and immediately read back by ESA in the same request (e.g. `ES_Contact__c`, `Multibureau_Data__c`, `Offer__c`, `consolidated_external_data__c`). If you write to the new DB but ESA still reads from SF, results will not match. Keep these on the same store until shadow testing is done. |

---

## Option A — Keep Salesforce names

### Extra work on top of the common changes above

| # | What | Where | Detail |
|---|---|---|---|
| A1 | Write `db_request_body` rows | DM — config/data | Field names in `db_request_body` are copied almost directly from the existing `request_body`. `"Type__c": "((value))"` stays `"Type__c": "((value))"`. Only the URL/method structure changes to `table`/`operation`. |
| A2 | Nothing else | — | Because DB column names match SF field names, no translation is needed anywhere in the read or write path. |

### Example — what `db_request_body` looks like in Option A

| Existing `request_body` (SF format) | New `db_request_body` (DB format, Option A) |
|---|---|
| `"url": "/services/data/v64.0/sobjects/Audit_Log__c"` | `"table": "Audit_Log__c"` |
| `"method": "POST"` | `"operation": "insert"` |
| `"referenceId": "CRIF_Multibureau_Data_Post"` | `"referenceId": "CRIF_Multibureau_Data_Post"` |
| `"body": { "Type__c": "CRIF", "Contact__c": "<contact.Id>" }` | `"fields": { "Type__c": "CRIF", "Contact__c": "<contact.Id>" }` |
| `"body": { "Multibureau__c": "@{CRIF_Multibureau_Data_Post.id}" }` | `"parentRef": "CRIF_Multibureau_Data_Post", "parentForeignKey": "Multibureau__c"` |

---

## Option B — Use clean names (snake_case, no `__c`)

### Extra work on top of the common changes above

| # | What | Where | Detail |
|---|---|---|---|
| B1 | Build and maintain a name dictionary | DM + ESA — config/data | Every SF object name and field name must be mapped to its DB equivalent. Example: `Audit_Log__c → audit_log`, `Type__c → type`, `MailingCity → mailing_city`. This cannot be automated safely — field names like `PAN_ID__c`, `DPD1MWtDt`, `HL_Amt__c` have acronyms and irregular casing that a simple "strip `__c` and lowercase" rule gets wrong. Every entry must be manually reviewed. |
| B2 | Write `db_request_body` rows with renamed fields | DM — config/data | Every field name in `db_request_body` must use the renamed DB column name. `"Type__c"` becomes `"type"`, `"Contact__c"` becomes `"contact_id"`. This must be done for every service mapping row. |
| B3 | Re-key DB results back to SF names after every read | ESA — new code | When ESA reads a row from the new DB, the columns come back as `type`, `contact_id` etc. Before those values can be used anywhere they must be converted back to `Type__c`, `Contact__c` etc. — because every `<Object.Field>` token, every expression, and every DM write template still uses SF names. If this step is missed nothing resolves and decisioning breaks silently. |
| B4 | Rename FK columns | DM + ESA | `Contact__c → contact_id`, `Multibureau__c → multibureau_id`, `Lead__c → lead_id` etc. must be consistent between what DM writes and what ESA reads. |
| B5 | Standard fields are also renamed — large surface | ESA — new code | Fields without `__c` also change: `Id → id`, `MailingCity → mailing_city`, `CreatedDate → created_date`, `Birthdate → birthdate`. These appear in `additional_conditions` clauses like `ORDER BY CreatedDate DESC LIMIT 1`. All must be in the dictionary. |
| B6 | Dictionary maintenance forever | DM + ESA — ongoing | Every time a new field is added to any service mapping or query config, the dictionary must also be updated. A missing entry silently drops that field on read or write with no error message. |

### Example — what `db_request_body` looks like in Option B

| Existing `request_body` (SF format) | New `db_request_body` (DB format, Option B) |
|---|---|
| `"url": "/services/data/v64.0/sobjects/Audit_Log__c"` | `"table": "audit_log"` |
| `"method": "POST"` | `"operation": "insert"` |
| `"referenceId": "CRIF_Multibureau_Data_Post"` | `"referenceId": "CRIF_Multibureau_Data_Post"` |
| `"body": { "Type__c": "CRIF", "Contact__c": "<contact.Id>" }` | `"fields": { "type": "CRIF", "contact_id": "<contact.Id>" }` |
| `"body": { "Multibureau__c": "@{CRIF_Multibureau_Data_Post.id}" }` | `"parentRef": "CRIF_Multibureau_Data_Post", "parentForeignKey": "multibureau_id"` |

---

## Side-by-side comparison

| | Option A (same names) | Option B (clean names) |
|---|---|---|
| Common changes required | Yes — all 15 items above | Yes — all 15 items above |
| Name dictionary needed | No | Yes — must be hand-reviewed |
| `db_request_body` authoring effort | Low — copy field names from existing mapping | High — rename every field using dictionary |
| ESA re-keying after DB read | Not needed | Required for every DB-sourced object |
| Standard fields (`Id`, `MailingCity`, `CreatedDate`) | No extra work | Must be in the dictionary too |
| Adding a new field later | Just add it — works automatically | Must add to dictionary + update `db_request_body` |
| Risk of silent data loss | Low | Higher — missing dictionary entry = field silently dropped |
| Schema appearance | Has `__c` suffixes | Clean snake_case |
| Can rename later (after SF removed) | Yes — easy one-time migration when only one system exists | Already done, but paid a higher cost during migration |

---

## Recommendation

Use **Option A** during the migration period.

The migration is the most complex and risky phase — you are running two systems at the
same time. Option A removes an entire layer of complexity (dictionary, re-keying, ongoing
maintenance) during exactly the period when you least want extra moving parts.

The `__c` names look like legacy Salesforce names, but they are harmless in a relational
database. Once Salesforce is fully removed and there is only one system left, rename the
columns then — it becomes a straightforward, low-risk database migration script with no
dual-write to worry about.
