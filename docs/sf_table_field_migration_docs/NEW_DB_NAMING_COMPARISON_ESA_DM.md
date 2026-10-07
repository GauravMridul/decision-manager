# New Primary DB — Naming Strategy Comparison (ESA + Decision Manager)

> **Scope:** This document covers every dimension on which the naming choice for the new
> primary DB affects **ESA** (the read engine) and **Decision Manager / DM** (the write
> engine), using the actual code and configuration as the baseline.

- **Scenario A — Same SF names:** new DB keeps Salesforce API names verbatim
  (`Audit_Log__c`, `Type__c`, `MailingCity`, `Id`). FK columns keep SF names
  (`Contact__c`, `Lead__c`, `Multibureau__c`).
- **Scenario B — Renamed:** snake_case + `__c` removed (`audit_log`, `type`,
  `mailing_city`, `id`). FK columns renamed (`contact_id`, `lead_id`,
  `multibureau_id`).

> **One-line verdict:** The hard, structural work is identical in both scenarios. Scenario
> B adds one extra, pervasive, permanent layer: a per-field **name-translation dictionary**
> plus re-keying read results back to SF names at every boundary. Scenario A avoids that
> layer entirely.

---

## 0. How the systems use names today

### 0.1 ESA — READ engine

`ExtractObjectsFieldsAndConditions()` scans every `service_configuration` row
(request\_body / headers / url / additional\_config) for `<Object.Field>` tokens.
`QuerySalesforceObjectsWithConditions()` joins the extracted set with
`query_object_relationship_map` and builds literal SOQL:

```
SELECT <fields> FROM <query_object>
WHERE <query_relation> = '<customerId>'
<additional_conditions>
```

Results land in `masterDTO.Data[lowercase(object)]` and are resolved back into vendor
requests by exact (case-insensitive) SF field name.

### 0.2 DM — WRITE engine

`serviceSfdcFieldMappingFetch()` loads and merges `service_sfdc_field_mapping.request_body`
rows. `ApplyPreprocessings()` runs all `preProcessing` rules (parse CRIF JSON, alias
subtrees into `_pp*` keys). `InterpolateValuesWithArrayExpansion()` resolves all
`((path))`, `<object.field>`, `{{expr}}`, `arrayPath` expansions, `lookup` joins, and
`merge` logic against the full `valueJson` (ESA response + `_pp*` keys). The result is
`sfCompositeSubRequestArray` — a fully-resolved `[]SFRequest` where every template is gone.

`UpsertSalesforceObjects()` sends that array to the SF Composite Graph API. DM then:
- Calls the Apex decision callback (`CallDecisionCallbackApex`).
- Logs the full audit record (`SFCompositeSubRequestArray` + `SFCompositeResponse` +
  preprocessing summary) to MongoDB asynchronously.

**Key fact in both apps:** the token in config == the physical SF name. Any DB naming that
diverges from SF names breaks that identity everywhere the token appears.

---

## 1. ESA (READ) — scenario comparison

| # | Touch-point | Scenario A — same names | Scenario B — snake_case + no `__c` |
|---|---|---|---|
| E1 | `<Object.Field>` extraction (`ObjectFieldPattern`) in `service_configuration` | **No change** — tokens match DB names directly | **No change to tokens**; resolved values must be re-keyed (E8) |
| E2 | `query_object` → table / `FROM` clause | **No change** (`ES_Contact__c` stays `ES_Contact__c`) | **Translate** object→table (`ES_Contact__c`→`es_contact`) |
| E3 | `query_relation` (`contact__r.Lead__c`, `Multibureau__r.Contact__r.Lead__c`, `Contact_Id__r.Lead__c`) | **Translate `__r` traversal → JOIN/FK**. A relational DB has no SOQL relationship traversal regardless of naming | **Translate `__r` → JOIN/FK AND rename leaf FK columns** |
| E4 | `additional_fields` (extra SELECT columns, e.g. `Campaign__r.Bscore__c`) | **No change** to names | **Translate** each field→column |
| E5 | `additional_conditions` (`WHERE Name = <contact.mailingcity>`, `AND Bureau__c='CRIF' ORDER BY CreatedDate DESC LIMIT 1`, `LAST_N_DAYS:30`) | **Translate SOQL semantics** to SQL (`LIMIT`, `ORDER BY`, `LAST_N_DAYS`, null handling, case-insensitive matching) — field names unchanged | **Translate semantics AND rename fields** inside the clause |
| E6 | SOQL builder + cross-object composite refs | Replace with **SQL builder for DB-sourced objects** (needed in both) | Same, plus column-name translation |
| E7 | Dependency ordering (`computeDependencyLevels`) | **Reusable**; execute ordered SQL/joins instead of composite queries | Same |
| E8 | `masterDTO.Data[...]` keys + `<Object.Field>` / `<Object[cond].field>` / `\|\|` fallback chain resolution | Rows already carry SF field names → **resolve unchanged** | **Re-key DB rows back to SF field names** before storing, or every downstream `<Object.Field>`, conditional filter, and DM write token breaks |
| E9 | Record shape `{records:[...]}`, single-vs-array, `processCompositeResponse`, `evaluateSimpleCondition`, `navigateValue` | **Reproduce shape** from SQL rows (needed in both) | Same, plus column→field renaming per record |
| E10 | `((ServiceName.path))` vendor-response refs; pure `{{expr}}`; `preProcessing _pp*` keys | **No change** — not SF field names | **No change** — not SF field names |
| E11 | ARRAY transforms over vendor responses (`telephoneNumber->...`) | **No change** — vendor response field names | **No change** — vendor response field names |
| E12 | `object_label` aliases (`MB_Crif`, `Pin_crif`, `lab_MB_Crif`) in `query_object_relationship_map` | **No change** — these are config-side aliases, not DB column names | **No change** — aliases are independent of DB names |
| E13 | `filterConditions.selectWhen` (`{{((current.Type__c)) == 'BankAA'}}`) in `service_sfdc_field_mapping` | **No change** — expression compares in-memory SF-named values | **No change if masterDTO stays SF-named** (translate only at DB edge) |

**ESA — required in BOTH A and B:** E3, E5 semantics, E6/E7, E9, per-object source routing (§3).
**ESA — extra only in B:** E2, E4, E5 field renames, E8 re-keying, per-field column maps.

---

## 2. Decision Manager (WRITE) — scenario comparison

### 2.1 The core problem: `service_sfdc_field_mapping.request_body` is SF-composite format

Every row in `service_sfdc_field_mapping` encodes a **Salesforce Composite Graph** sub-request:

```json
{
  "url":         "/services/data/v64.0/sobjects/Multibureau_Data__c",
  "method":      "POST",
  "referenceId": "CRIF_Multibureau_Data_Post",
  "body":        { "Bureau__c": "CRIF", "Contact__c": "0039H00000MKbOXQA1", ... }
}
```

and child rows reference parent rows via SF composite syntax:

```json
{
  "url":  "/services/data/v64.0/sobjects/MultiBureau_AccountList__c",
  "body": { "Multibureau__c": "@{CRIF_Multibureau_Data_Post.id}", ... }
}
```

**The `request_body` column must not be changed.** The `InterpolateValuesWithArrayExpansion`
+ `ApplyPreprocessings` pipeline is tightly coupled to this structure. Any format change
risks breaking the SF write path and invalidates the global Redis cache
(`GenerateRedisKeyForAllServiceSfdcFieldMapping`).

**Solution:** Add a parallel `db_request_body` column (see §2.3) that uses a DB-native
format processed by the same interpolation engine on the same `valueJson`.

### 2.2 DM touch-point comparison

| # | Touch-point | Scenario A — same names | Scenario B — snake_case + no `__c` |
|---|---|---|---|
| D1 | `url` → `/sobjects/Audit_Log__c` (+ optional `/<id>`) | Object token already == table name — parse + use directly | **Translate** object→table after `extractObjectType()` |
| D2 | `extractObjectType()` splitting on `/sobjects/` | **Reusable as-is** | Parsed token ≠ table → **translate after parse** |
| D3 | `body` field keys (`Type__c`, `EndTime__c`, `PAN_ID__c`, `Score__c`) | **No change** — keys are already column names | **Translate** every key→column |
| D4 | SF record refs in body (`"Contact__c": "<contact.Id>"`, `"Lead__c": "<lead.Id>"`) | **No change** — carried from ESA read; same names | **Translate** FK column name (value = ID, which is separate) |
| D5 | `@{CIBIL_Multibureau_Data_Post.id}` parent→child graph wiring | **Replace with `parentRef`/`parentForeignKey`** in `db_request_body` (needed in both — `@{Ref.id}` is SF-API-specific) | Same, plus FK column name translation |
| D6 | `extractDependencies()` + `buildDependencyGraph()` + `topologicalSort()` | **Reuse for DB INSERT order**; capture generated PK into refMap; inject into child FK | Same |
| D7 | `arrayPath` (1 response → N child rows after `InterpolateValuesWithArrayExpansion`) | **No structural change** — already expanded before DB writer sees it; insert N rows | Same |
| D8 | `lookup` (key-based join across two service-response arrays) | **No change** — resolved before DB writer | Same |
| D9 | `merge: "true"` (multiple sub-requests → single row) | **No change** — merged before DB writer | Same |
| D10 | `filterConditions.selectWhen` (conditional array-item filtering) | **No change** — resolved before DB writer | Same |
| D11 | `condition` field (per-sub-request boolean guard, e.g. Actico\_kyc CPV patch) | **No change** — evaluated before DB writer | Same |
| D12 | `preProcessing` `_pp*` keys (parse CRIF JSON, alias subtrees) | **No change** — resolved before DB writer | Same |
| D13 | HTTP method → DB operation: `POST`→INSERT, `PATCH /id`→UPDATE/UPSERT | **Map** (needed in both) | Same |
| D14 | Static booleans and typed values (`"PreApproval_Completed__c": true`, `{{… ? true : ''}}`) | **Typing + "empty=skip" rule** (needed in both) | Same, plus column rename |
| D15 | `"merge": "false"` with multiple rows targeting same object (e.g. multiple Audit\_Log inserts) | Each row → independent INSERT (needed in both) | Same |
| D16 | Apex decision callback (`CallDecisionCallbackApex`) after composite write | **No change** — SF-only; fires after SF write succeeds | **No change** — separate concern |
| D17 | MongoDB audit log (`SFCompositeSubRequestArray` + `SFCompositeResponse` + preprocessing) | **Existing log is the natural outbox** for DB replay/retry (needed in both) | Same |
| D18 | Redis cache for `service_sfdc_field_mapping` and `partner_service_mapping` | **No change** — cached by service name, unaffected by DB naming | **No change** |
| D19 | `db_request_body` (new nullable column) format | Uses DB table/column names directly — **no translation needed** | Contains renamed column names — **dictionary applied only here** |

**DM — required in BOTH A and B:** D5/D6 (parent→child via refMap), D13 (method mapping),
D14 (typing / empty=skip), ID strategy (§3.4), dual-write mechanism (§3.9), `db_request_body`
column (§2.3).
**DM — extra only in B:** D1/D2 post-parse object translation, D3/D4 field renames, D19
dictionary.

### 2.3 The `db_request_body` column (key design decision)

Do **not** modify `request_body`. Add a new nullable column:

```sql
ALTER TABLE service_sfdc_field_mapping
  ADD COLUMN db_request_body JSONB;
```

`db_request_body` uses a DB-native format:

```json
[
  {
    "table":        "audit_log",
    "operation":    "insert",
    "referenceId":  "CRIF_Multibureau_Data_Post",
    "fields": {
      "bureau":      "CRIF",
      "contact_id":  "<contact.Id>",
      "response":    "{{truncateRaw(serializeJson(((CRIF.response))),0,131000)}}"
    }
  },
  {
    "table":           "multi_bureau_account_list",
    "operation":       "insert",
    "referenceId":     "CRIF_AccountList_Post",
    "parentRef":       "CRIF_Multibureau_Data_Post",
    "parentForeignKey":"multibureau_id",
    "arrayPath":       "_ppCriftradelines",
    "fields": {
      "account_type": "((current.ACCT-TYPE))",
      "overdue_amt":  "{{stringToDouble((current.OVERDUE-AMT))}}"
    }
  }
]
```

- `table` / `fields` — DB-native names (Scenario A: same as SF; Scenario B: renamed).
- `operation` — `insert` / `update` / `upsert` (explicit, not inferred from URL).
- `parentRef` + `parentForeignKey` — replaces `@{Ref.id}`; DB-native and explicit.
- `arrayPath`, `{{expr}}`, `((path))` — **identical syntax** to `request_body`; same
  `InterpolateValuesWithArrayExpansion` processes `db_request_body` on the same `valueJson`.
- Nullable — if null for a service row, that service writes to SF only. Migrate
  incrementally, service by service.
- For Scenario A: `table` = SF object name minus `/sobjects/`, fields = SF field names.
  Zero dictionary needed.
- For Scenario B: `table` and `fields` use renamed names. The dictionary is applied only
  when writing `db_request_body` rows — nowhere else.

---

## 3. Changes required in BOTH scenarios (naming-independent)

These are unavoidable because **a relational DB is not Salesforce**, not because of names.

1. **Per-object source routing.**  
   A `source` flag (SF | DB | BOTH) so ESA reads and DM writes hit the right store. For
   ESA: add a column to `query_object_relationship_map`. For DM: `db_request_body` being
   non-null is the write-routing signal.

2. **Relationship traversal → JOIN/FK.**  
   `contact__r.Lead__c`, `Multibureau__r.Contact__r.Lead__c`, `Contact_Id__r.Lead__c`
   have no SQL equivalent. Even with identical names, these become multi-table JOINs/FK
   filters per object.

3. **SOQL → SQL semantic translation.**  
   `ORDER BY CreatedDate DESC LIMIT 1`, `LAST_N_DAYS:30`, null/`''` handling,
   case-insensitive string matching. These appear in `additional_conditions` across all
   objects and must be translated one-for-one.

4. **ID strategy: SF Id as DB primary key.**  
   `<contact.Id>`, `<lead.Id>`, `Lead__c: "<lead.Id>"` values are 18-char SF Ids.
   Use the SF Id as the DB PK (or store it as a unique column). Avoids an xref table,
   keeps child FK wiring identical in both stores, and lets existing templates resolve
   unchanged. Under the `db_request_body` design, the DB writer runs after
   `UpsertSalesforceObjects` returns `sfCompositeResponse` (which contains the generated
   SF Ids per referenceId). Those Ids are injected into the `refMap` and into child FK
   fields before DB INSERT.

5. **Typed columns + "empty = skip" semantics.**  
   SF composite omits `''`/unresolved fields silently. Many DM mappings emit `''`
   intentionally (`{{… ? true : ''}}`). Against typed SQL columns, the DB writer must
   **drop the column** (not write `''`/null) and **cast** `"true"`/`"123"`/date strings
   to typed values. This is naming-independent.

6. **Parent→child write as a DB transaction.**  
   The existing `extractDependencies()` + `buildDependencyGraph()` + `topologicalSort()`
   in DM gives the correct insertion order. The DB writer wraps each dependency chain in
   one transaction: INSERT parent → capture generated PK (or use injected SF Id) → inject
   into children's FK column. No read-back needed when using SF-first strategy (§3.4).

7. **Record-shape parity on ESA read.**  
   SQL results must be returned as `{records:[...]}` / single-object / empty exactly as
   `processCompositeResponse` produces, so `evaluateSimpleCondition`, `navigateValue`,
   and conditional filters (`<Object[cond].field>`) keep working.

8. **Method → DB operation mapping.**  
   `POST` → INSERT, `PATCH /sobjects/Object/<id>` → UPDATE by PK,
   `PATCH /sobjects/Object/@{Ref.id}` → UPDATE by refMap-injected PK. `merge: true` rows
   are already merged before the DB writer sees them.

9. **Dual-write mechanism + consistency model.**  
   Two stores, no distributed transaction. Recommended: write SF first (unchanged path),
   capture `sfCompositeResponse`, then fire the DB writer in a tracked goroutine with
   retries — same pattern as the existing `initAsyncDMLogWriter`. The MongoDB
   `DecisionManagerLog` (`SFCompositeSubRequestArray` + `SFCompositeResponse`) is a
   natural **outbox** for replay/backfill. Apex callback is SF-only and fires after SF
   write; no DB equivalent needed.

10. **CDC / sync for non-DM writers.**  
    DM dual-write only covers decisioning-originated records. The new DB needs
    CDC / Platform Events / ETL for records created by the UI, Apex, and other
    integrations (e.g. `consolidated_external_data__c`, `Contact`, `Lead` updates
    from KYC flow).

11. **Intra-request read-after-write safety.**  
    The following objects are **written by DM and read back by ESA in the same flow**
    (incl. CIBIL/CRIF 30-day dedupe via `multibureau_consolidate_data__c.pre_execution.
    pick_response_from`):  
    `ES_Contact__c`, `Multibureau_Data__c`, `Multibureau_Consolidate_Data__c`,
    `MultiBureau_AccountList__c`, `PhoneList__c`, `AddressList__c`, `EnquiryList__c`,
    `IdList__c`, `ScoreList__c`, `Credit_Vision_Detail__c`, `consolidated_external_data__c`,
    `Offer__c`, `A_Score__c`, `Income_Model__c`, `Contact` (field patches), `Lead`
    (decision patches).  
    Keep read + write of these on the **same store** until sync is proven, or make their
    DB write synchronous before the Apex callback returns.

12. **Shadow / diff testing.**  
    For each object being migrated: read from both SF and DB, diff the `masterDTO.Data`
    snapshot and the resolved composite body, cut over only when diffs are clean for N
    consecutive requests.

13. **`filterConditions.selectWhen` and `condition` fields in `request_body`.**  
    These are evaluated by the interpolation engine before the DB writer runs. The DB
    writer only sees already-filtered, already-expanded rows. No special handling needed
    for either naming scenario.

14. **`preProcessing` (`_pp*` keys).**  
    Parsed and materialised by `ApplyPreprocessings` before interpolation. The DB writer
    sees already-resolved field values. No special handling needed for either naming
    scenario. The MongoDB audit log includes the full preprocessing summary for replay.

15. **`lookup` join in `db_request_body`.**  
    The same `lookup` config block works in `db_request_body` because
    `InterpolateValuesWithArrayExpansion` processes it before the DB writer. No change
    needed in the DB writer.

---

## 4. Extra changes ONLY in Scenario B (renamed)

Everything below is **zero work in Scenario A** and **permanent ongoing work in Scenario B**.

1. **Explicit name-translation dictionary.**  
   Per object: `sf_object → table`. Per field: `sf_field → column` (with passthrough
   default). Must be **reviewed and hand-curated** — auto `strip __c + to_snake_case`
   is unsafe (see Appendix A). The dictionary is applied only in two places:
   - Writing rows to `db_request_body` (author-time, one-off per service).
   - ESA DB adapter: re-keying SELECT results back to SF names before `masterDTO.Data`.

2. **Standard-field rename surface is large.**  
   `Id`, `Name`, `Email`, `Phone`, `MailingCity`, `MailingState`, `MailingStreet`,
   `MailingPostalCode`, `OtherPostalCode`, `CreatedDate`, `Birthdate`, `Status`,
   `OwnerId`, `RecordType` all appear in `additional_conditions`, body refs, and ARRAY
   transforms. In Scenario A these are untouched.

3. **ESA result re-keying (E8 above).**  
   After every SQL SELECT, each row's columns must be re-keyed back to SF field names
   before entering `masterDTO.Data`. Without this, **every** `<Object.Field>`,
   `<Object[cond].field>`, `||` fallback, and ARRAY transform breaks silently.

4. **DM `db_request_body` authoring cost.**  
   Every new service mapping requires authoring two parallel rows: `request_body` (SF
   composite) and `db_request_body` (DB native with renamed fields). In Scenario A,
   `db_request_body` field names are copied from `request_body`; no dictionary lookup.

5. **FK column renames.**  
   `Contact__c`→`contact_id`, `Multibureau__c`→`multibureau_id`, `Lead__c`→`lead_id`.
   In Scenario A FK columns keep SF names and the `db_request_body` body values are
   unchanged.

6. **Ongoing drift risk.**  
   Every new field in any of the three config tables must be added to the dictionary.
   A missing entry silently drops that column on read or that field on write. In
   Scenario A, new fields "just work."

---

## 5. Side-by-side summary

| Dimension | Scenario A — same names | Scenario B — snake_case + no `__c` |
|---|---|---|
| `request_body` rewritten? | No | No |
| `db_request_body` authoring | Copy field names from `request_body` | Author with renamed fields (dictionary lookup) |
| `query_object_relationship_map` changes | source flag only | source flag + object/field rename |
| Relationship→JOIN (E3) | Required | Required |
| SOQL→SQL semantics (E5) | Required | Required |
| Source routing / dual-write / ID strategy / typing / consistency | Required | Required |
| Object→table translation | Not needed | **Required** |
| Field→column translation (incl. standard fields) | Not needed | **Required (large surface)** |
| ESA result re-keying | Not needed | **Required** |
| `db_request_body` FK column rename | Not needed | **Required** |
| Auto-transform safe? | N/A | **No** — needs reviewed dictionary (see Appendix A) |
| New-field maintenance | Automatic | **Dictionary entry + `db_request_body` update each time** |
| Net extra effort vs A | baseline | **+ dictionary + re-keying + authoring overhead + drift risk** |

---

## 6. Recommendation

- **If snake_case is a preference only:** choose Scenario A. The hard work (relationships,
  routing, consistency, `db_request_body`) is identical. Skip the translation layer.
  Names like `Type__c` are "legacy-looking" but harmless. Rename after SF is
  decommissioned — at that point it's a single-store, well-scoped migration.
- **If snake_case is mandated:** choose Scenario B with **edge-only translation**. Keep
  SF names canonical everywhere in memory and in `request_body`. Apply the dictionary
  only at the DB read/write adapter boundary. Do not touch `service_sfdc_field_mapping.
  request_body`, `service_configuration`, or `query_object_relationship_map`.
- **In both cases:** start with externally-owned, slow-changing objects (`Postal_Code__c`,
  `City_Tier__c`, `Dealer_Info__c`, `Policy_Parameter__c`) where read-after-write
  consistency is not a concern. Keep in-flow objects (Appendix B) on one store until
  shadow testing is clean.

---

## Appendix A — fields where auto `strip __c + snake_case` is wrong or ambiguous

| SF name | Auto result | Problem |
|---|---|---|
| `PAN_ID__c` | `pan_i_d` or `pan_id` | Acronym — ambiguous split |
| `DPD1MWtDt` | `d_p_d1_m_wt_dt` | No `__c`; embedded numbers and caps |
| `HL_Amt__c` | `hl_amt` | Acronym prefix |
| `SMASUBDBTLSS_count18__c` | `s_m_a_s_u_b_d_b_t_l_s_s_count18` | All-caps prefix |
| `No_of_90_DPD_overall_in_L12M__c` | varies | Number + acronym mix |
| `overall_6_mob_10+_dpd_count` | error | `+` in path — not a valid column name |
| `"TotalWorkExperienceInMonths__c "` | error | Trailing space in actual field name |
| `MailingCity`, `OtherPostalCode`, `CreatedDate`, `Birthdate` | `mailing_city` etc. | Standard fields — no `__c`, but still renamed in B |
| `Customer Category` (acticoData response path) | `customer_category` | Space in path, not a column name |
| `DPD1MWtDt_CB_gt_5k` | varies | Underscore + numbers + operators in name |

---

## Appendix B — objects written by DM and read by ESA in-flow (intra-request consistency)

If DB write happens but DB read returns stale/empty for any of these before sync is proven,
decisioning will diverge from SF. Keep read + write on the same store or make DB write
synchronous for these during transition.

- **Written by DM, read by ESA same-flow:** `ES_Contact__c` (KarzaNameFetch, TartanVerification,
  PAN profile, Mobile Match, UAN, Infobib etc.), `Multibureau_Data__c` + all child tables
  (`MultiBureau_AccountList__c`, `PhoneList__c`, `AddressList__c`, `EnquiryList__c`,
  `IdList__c`, `ScoreList__c`, `Credit_Vision_Detail__c`), `Multibureau_Consolidate_Data__c`
  (30-day CIBIL/CRIF reuse via `pick_response_from`), `A_Score__c`, `Income_Model__c`,
  `Offer__c`, `Fraud_Details__c`, `Dedupe_Result__c`, `Negative_Scrub_List__c`.
- **Written by DM via PATCH, read by ESA same-flow:** `Contact` (scores, geo, match flags),
  `Lead` (credit decision, amounts, re-decisioning count), `consolidated_external_data__c`
  (KYC decision, face match, liveness).
- **Read by DM as input, written by other sources:** `Multibureau_Consolidate_Data__c`
  (checked via `pick_response_from` to reuse a recent bureau pull — requires CDC if this
  table is being migrated while other services still write to SF only).

---

## Appendix C — three config tables and their DB-migration surface

| Table | What it drives | Changes for source routing | Extra for Scenario B |
|---|---|---|---|
| `query_object_relationship_map` | ESA reads: object, relation, conditions, extra fields | Add `source` column (SF\|DB\|BOTH); translate `query_relation` `__r` paths → JOIN specs | Rename `query_object`, `additional_fields`, field names in `additional_conditions` |
| `service_configuration` | ESA: vendor API URLs, headers, request bodies; all `<Object.Field>` tokens | No structural change; `masterDTO.Data` re-keying at DB adapter layer handles everything | Re-keying at adapter edge is sufficient; tokens in config untouched |
| `service_sfdc_field_mapping` | DM writes: SF composite sub-requests | Add `db_request_body` nullable column | `db_request_body` contains renamed table/field names; `request_body` untouched |
