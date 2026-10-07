### Decision Manager configuration guide for Salesforce composite upserts

This document explains all configurations required to transform ESA “processSequence” responses into Salesforce Composite requests using `service_sfdc_field_mapping.request_body`. It documents only features implemented in the code, so you can configure the system without reading the source.

### 1) High-level flow
- POST to `/decision-manager/v1/trigger-decision` with a valid `X-Api-Key`.
- Service calls ESA and receives a response.
- ESA response is normalized into an internal map (valueJson).
- The service loads one or more rows from `service_sfdc_field_mapping` for services present in valueJson and merges all `request_body` arrays.
- Placeholders are interpolated, array templates are expanded, and the final Composite request is sent to Salesforce.

### 2) Where to configure
- Table: `public.service_sfdc_field_mapping`
- Column: `request_body` (JSON array; see schema below)
- Row selection: rows are selected by `service_name` matching keys present in the normalized ESA response (valueJson).
- Multiple rows for the same service are allowed; their `request_body` arrays are merged (see section 6).

### 3) request_body schema
Each `request_body` is a JSON array of sub-requests. Supported fields per sub-request:

- `url` (string)
- `method` (string; e.g., `POST`, `PATCH`)
- `referenceId` (string; unique within a single Composite call)
- `body` (object; map of field → value; supports templating)
- `merge` (string; `"true"` or `"false"`, case-insensitive)
- `arrayPath` (string; optional; enables array expansion; see section 5)
- `preProcessing` (array; optional; transforms valueJson before interpolation; see section 3.1)

Example:

```json
[
  {
    "url": "/services/data/v64.0/sobjects/Audit_Log__c",
    "method": "POST",
    "referenceId": "NTCModel_Audit_Log__c_Post",
    "merge": "false",
    "body": {
      "Type__c": "((NTCModel.serviceName))",
      "StartTime__c": "((NTCModel.startTime))",
      "EndTime__c": "((NTCModel.endTime))",
      "Response__c": "{{serializeJson(((NTCModel.response)))}}"
    }
  }
]
```

### 3.1) preProcessing (transform valueJson before interpolation)

`preProcessing` is an optional array on any sub-request that runs ESA-response transformations BEFORE any `arrayPath` resolution or body templating. The most common use case is converting a stringified-JSON field (e.g. bureau `raw_response`) into an actual object so that `arrayPath` and `((path))` references can traverse it.

Each rule has two fields:

- `expr` (string): a govaluate expression (or a `((path))` alias) producing the value to store. All custom functions and operators available in `body`/`url` templates are available here.
- `storeAs` (string): the top-level key under which the result is written into valueJson. MUST start with the reserved `_pp` prefix (e.g. `_ppCrifRaw`, `_ppCrifInquiries`). Anything outside this namespace is rejected.

Schema:

```json
"preProcessing": [
  { "expr": "{{ ... }} or ((path))", "storeAs": "_ppXxx" }
]
```

#### Why the `_pp` prefix?
Original ESA top-level keys never start with `_pp`. Reserving this namespace lets the interpolation engine know that these keys hold preprocessed trees that should NOT be recursively flattened into the govaluate parameter map. Preprocessed trees are still reachable through `((path))` syntax (and through `getPath` for use inside expressions) but the heavy flattening cost is avoided.

#### Execution semantics
- Rules from ALL sub-requests in a request batch are collected into a single deduplicated list (by `storeAs`, first occurrence wins). Declaring the same rule on multiple sub-requests is therefore safe and cost-free.
- Rules apply BEFORE any `InterpolateValuesWithArrayExpansion` work — every sub-request that references a `_pp*` key sees it already materialised.
- Order is resolved via a multi-pass fixpoint: a rule whose expression depends on a `_pp*` key not yet defined is deferred until a later pass. The engine loops until either every rule succeeds or a full pass makes zero progress.
- Original ESA data in valueJson is never overwritten; only NEW `_pp*` top-level keys are produced.

#### Failure isolation (graceful degradation)
A misconfigured or failing preprocessing rule never aborts the request. Specifically:

- **Bad expression / custom-function error / malformed JSON** → the failing rule's `storeAs` is set to `nil` in valueJson, the underlying error is logged with `storeAs`, `expr` and the root cause, and the next rule continues.
- **Invalid `storeAs` (wrong prefix or empty) or empty `expr`** → the rule is logged as an error and skipped entirely. Remaining rules still run.
- **Cycle or genuinely missing `_pp*` dependency** → after the fixpoint stalls, every stuck rule is logged with the specific `_pp*` keys it was waiting on, and its `storeAs` is set to `nil`. The pipeline keeps going.
- **Caller-driven context cancellation** is the ONLY hard failure: the call returns the cancellation error so the entire request can short-circuit.

Downstream `((_pp*))` lookups against a nil-stored key resolve to empty (same as any other missing field), so a failed rule cleanly degrades the affected sub-request fields rather than blocking unrelated ones.

#### MongoDB audit trail
Every request that runs preprocessing also writes two fields into the `decision-manager` mongo log entry for that request:

- `preprocessingTimeMs` (int64) — total wall-clock duration of the stage.
- `preprocessing` (object) — structured summary with `ruleCount`, `succeeded`, `failed`, `unresolved`, `skippedInvalid`, `duplicates`, `passes`, `cancelled`, a `rules` array (`storeAs`, `expr`, `status`, `pass`, `durationMs`, optional `error`, optional `missingPpKeys`), and a `values` map containing every produced `_pp*` key. Status is one of `success | failed | unresolved | skipped`.

This lets you replay or diagnose preprocessing without grepping application logs. `values` shares references with the parsed trees, so the mongo entry grows by roughly the parsed-tree footprint.

A repository-level size guard runs immediately before insert: if the marshaled BSON exceeds 15 MiB (1 MiB below MongoDB's hard 16 MiB doc limit), `preprocessing.values` is dropped and `preprocessing.valuesStripped: true` is set on the way out, so the rest of the audit record still lands in mongo. A `Warn` log fires whenever this happens carrying before/after sizes + `customerId` for correlation. The check reuses the marshal output via `bson.Raw`, so in-budget docs pay exactly one `bson.Marshal` pass — same work the driver would do anyway, net overhead ≈ 0.

#### CRIF example (stringified JSON inside ESA response)

The CRIF service returns its credit report under `CRIF.response.body.raw_response` as a string containing JSON followed by an HTML representation. Preprocessing parses the JSON portion once and exposes it as `_ppCrifRaw`, then walks into nested subtrees with cheap path aliases.

```json
{
  "url": "/services/data/v64.0/sobjects/MultiBureau_EnquiryList__c",
  "method": "POST",
  "referenceId": "CRIF_EnquiryList_Post",
  "merge": "false",
  "preProcessing": [
    {
      "expr": "{{stringToJson(jsonPart(((CRIF.response.body.raw_response))))}}",
      "storeAs": "_ppCrifRaw"
    },
    {
      "expr": "((_ppCrifRaw.CIR-REPORT-FILE.REPORT-DATA.STANDARD-DATA))",
      "storeAs": "_ppCrifStd"
    },
    {
      "expr": "((_ppCrifStd.INQUIRY-HISTORY))",
      "storeAs": "_ppCrifInquiries"
    }
  ],
  "arrayPath": "_ppCrifInquiries",
  "body": {
    "LENDER_TYPE__c":                 "((current.LENDER-TYPE))",
    "Multibureau__c":                 "@{CRIF_Multibureau_Data_Post.id}",
    "Inquiry_Type__c":                "((current.LOAN-TYPE))",
    "Date_Reported__c":               "{{formatDate(current_INQUIRY_DT,'YYYY-MM-DD')}}",
    "Enquiry_Amount__c":              "((current.AMOUNT))",
    "OWNERSHIP_TYPE__c":              "((current.OWNERSHIP-TYPE))",
    "Enquiry_Purpose__c":             "((current.LOAN-TYPE))",
    "CREDIT_INQUIRY_STAGE__c":        "((current.CREDIT-INQUIRY-STAGE))",
    "CREDIT_INQ_PURPS_TYPE__c":       "((current.CREDIT-INQ-PURPS-TYPE))",
    "Reporting_Member_Short_Name__c": "((current.LENDER-NAME))"
  }
}
```

Sibling CRIF requests in the same `request_body` array can simply reference the produced keys without redeclaring any rule:

```json
{
  "url": "/services/data/v64.0/sobjects/MultiBureau_Tradeline__c",
  "method": "POST",
  "referenceId": "CRIF_Tradeline_Post",
  "merge": "false",
  "arrayPath": "_ppCrifStd.TRADELINES",
  "body": { "...": "..." }
}
```

#### Failure log messages (rule isolated, request continues)
All of the following appear in the request log as `Error` entries; the request itself proceeds with the failing `_pp*` key set to nil.

- `preProcessing: skipping rule with invalid storeAs (...)` — `storeAs` does not start with `_pp` or has nothing after the prefix.
- `preProcessing: skipping rule with empty expr` — `expr` was missing or whitespace-only.
- `preProcessing rule failed; storing nil and continuing` — the expression itself raised an error (invalid JSON parsed by `stringToJson`, malformed govaluate syntax, custom-function error, etc.). The log entry carries `storeAs`, `expr` and the root-cause `error`.
- `preProcessing rule unresolved (cycle or missing _pp* dependency); storing nil and continuing` — a `_pp*` reference in this rule never resolves; either two rules reference each other or a referenced key is not declared anywhere. The log entry carries the precise list of missing keys.


### 4) Templating features (body and url)
You can mix three kinds of placeholders:

- Dot-paths: `((Some.Path[0].field))`
  - Reads from the normalized ESA response map (valueJson)
  - Supports arrays via `[index]`
  - If the resolved value is `nil` or an empty string, the field is omitted from the body

- Expressions: `{{ expression }}`
  - Evaluated via govaluate against valueJson
  - Supported functions:
    - `min`, `max`, `concat`, `pow`, `sqrt`, `abs`, `round`, `ceil`, `floor`
    - `serializeJson(value)` → JSON string
    - `serializeJsonPretty(value)` → pretty JSON string
    - `arrayToString(array)` → e.g., `a,b,c` (handles empty arrays)
    - `delimetterSwap(value, outputDelimiter)` → normalizes `,`, `;`, `|` to `outputDelimiter`
    - `delimetterSwap(value, outputDelimiter, sourceDelimiter)` → replaces only `sourceDelimiter` with `outputDelimiter`
    - `jsonPart(responseBody)` → content before `<html` (case-insensitive)
    - `htmlPart(responseBody)` → content from `<html` onward (case-insensitive)
    - `stringToJson(value)` → parses a stringified JSON value into an object/array (streams via JSON decoder so trailing non-JSON content is tolerated). Useful in `preProcessing` to materialise bureau payloads. Already-parsed values pass through unchanged.
    - `getPath(data, dotPath)` → walks a parsed object/array using a dot path (e.g. `getPath(_ppCrifRaw, 'CIR-REPORT-FILE.HEADER-SEGMENT.STATUS')`). Escape hatch for reaching INTO `_pp*` preprocessed trees from inside an expression, since `_pp*` keys are stored as references but not recursively flattened into the param map.
    - `findByField(array, fieldName, fieldValue [, extractField])` → searches an array of objects for the **first** element where `element[fieldName] == fieldValue` (case-insensitive). Returns the matched element, or if the optional `extractField` argument is given, returns that sub-field of the matched element instead. Returns nil when no match is found (field is skipped, not an error). Primarily used in `preProcessing` to select a typed sub-section from arrays like CRIF `DEMOGS.VARIATIONS`. Example: `findByField(_ppCrifVariations, 'TYPE', 'ADDRESS-VARIATIONS', 'VARIATION')` returns the `VARIATION` array from the `ADDRESS-VARIATIONS` entry.
    - `uploadToS3(content, extension, filePath, bucketName, region)` → uploads content and returns S3 URL
    - `stringToInt(value)` → strips all characters except digits `0-9` and the **first** `.`, parses as float64, then truncates to `int`. Non-string numerics accepted directly. Returns `0` for nil, empty, or all-alpha input. **Cannot be used in govaluate arithmetic** (`+`, `-`, etc.) — use `stringToDouble` instead when arithmetic is needed.
    - `intToString(value)` → converts an integer or any numeric type to its string form. Returns `""` for nil input.
    - `stringToDouble(value)` → strips all characters except digits `0-9` and the **first** `.`, then parses as `float64`. Non-string numerics accepted directly. Returns `0` for nil, empty, or all-alpha input. Result is `float64` and can be used in arithmetic expressions.
    - `doubleToString(value)` → converts a `float64` or numeric value to string with no unnecessary trailing zeros (`10.0` → `"10"`, `3.14` → `"3.14"`). Returns `""` for nil input.
  - If an expression references a missing value, the field is skipped (not an error)
  - Inside expressions, do not use `<contact.Id>` / `<lead.Id>`; use `((contact.Id))` / `((lead.Id))`

**Type conversion quick reference:**

| Input string | `stringToInt` result | `stringToDouble` result |
|---|---|---|
| `"42000"` | `42000` | `42000` |
| `"42,000"` | `42000` | `42000` |
| `"20,000/weekly"` | `20000` | `20000` |
| `"₹1,00,000"` | `100000` | `100000` |
| `"1,234.56"` | `1234` | `1234.56` |
| `"1,234.56/month"` | `1234` | `1234.56` |
| `"3.14"` | `3` | `3.14` |
| `"85.5%"` | `85` | `85.5` |
| `"abc"` / nil / `""` | `0` | `0` |

Examples:
```json
{
  "Salary_Int__c":   "{{stringToInt(((lead.Annual_Income__c)))}}",
  "Salary_Str__c":   "{{intToString(((lead.Salary_Int__c)))}}",
  "Rate_Double__c":  "{{stringToDouble(((loan.Interest_Rate__c)))}}",
  "Rate_Label__c":   "{{doubleToString(((loan.Rate_Double__c)))}}",
  "Chained_Int__c":  "{{intToString(stringToInt(((lead.Raw_Income__c))))}}",
  "Chained_Dbl__c":  "{{doubleToString(stringToDouble(((loan.Raw_Rate__c))))}}",
  "Total__c":        "{{stringToDouble(((loan.Principal__c))) + stringToDouble(((loan.Fee__c)))}}",
  "Score_Adj__c":    "{{stringToDouble(((bureau.rawScore__c))) * 1.1}}"
}
```

> **Note:** `stringToInt` returns a Go `int` which govaluate cannot use in arithmetic. Use `stringToDouble` when the result must participate in `+`, `-`, `*`, or `/` operators.
>
> **Known limitation:** A string prefix that itself contains a dot (e.g. `"Rs. 5,000"`) will have that dot treated as the decimal separator, yielding `0`. Pre-clean the value with `left`/`substring` to remove the prefix first.

- Angle brackets: `<Some.Path.to.value>`
  - Works in both `url` and string fields of `body`
  - Inlines the resolved value; if unresolved, the original text is kept

Validation while interpolating:
- A field is added only if the resolved value is non-nil and not an empty string (for string types). Otherwise it is omitted.

Example for mixed JSON+HTML bureau payload:

```json
{
  "Response__c": "{{truncateRaw(jsonPart(((CRIF.response.body.raw_response))), 0, 130000)}}",
  "Credit_Bureau_pdf__c": "{{uploadToS3(htmlPart(((CRIF.response.body.raw_response))), '.html', concat('Crif_PDF/', ((contact.Id)), '/', formatDate(now(), 'YYYY-MM-DD_HH:mm:ss')), 'example-bucket', 'ap-south-1')}}"
}
```

S3 runtime config keys:

```properties
# No region key required for uploadToS3; pass region in expression argument.
# Example: uploadToS3(..., 'example-bucket', 'ap-south-1')
```

S3 credentials are sourced from AWS default credential chain (IRSA/service-account role in K8s).
Static `aws.accessKey` / `aws.secretKey` are not required for this implementation.

### 4.1) Detailed function guide: `delimetterSwap`
Use this function to normalize delimiter-separated text fields before writing to Salesforce.

Function signatures:
- `delimetterSwap(value, outputDelimiter)`
- `delimetterSwap(value, outputDelimiter, sourceDelimiter)`

Argument behavior:
- `value`: input string to transform.
- `outputDelimiter`: delimiter to use in the final result (for example `;` or `|`).
- `sourceDelimiter` (optional):
  - If provided, only this delimiter is split/replaced.
  - If omitted, the function treats `,`, `;`, and `|` as source delimiters.

Normalization rules:
- Trims spaces around each token.
- Removes empty tokens.
- Keeps original token order.
- Does not deduplicate repeated values.

Examples:

1) Your Manual Credit Review use case (comma to semicolon):

```json
{
  "Manual_Credit_Review__c": "{{delimetterSwap(((kycOutput.Manual_Credit_Review)), ';')}}"
}
```

Input:
`" , FCU Queue - PAN Aadhar not linked , FCU Queue_YOB Mismatch"`

Output:
`"FCU Queue - PAN Aadhar not linked;FCU Queue_YOB Mismatch"`

2) Normalize mixed delimiters (`,`, `;`, `|`) into semicolon:

```json
{
  "Reason__c": "{{delimetterSwap(((SomeService.response.body.reasons)), ';')}}"
}
```

Input:
`"A , B; C | D"`

Output:
`"A;B;C;D"`

3) Selective replacement only for semicolon:

```json
{
  "Reason__c": "{{delimetterSwap(((SomeService.response.body.reasons)), '|', ';')}}"
}
```

Input:
`"A ; B ; C"`

Output:
`"A|B|C"`

4) Selective replacement only for comma (semicolons remain inside token):

```json
{
  "Reason__c": "{{delimetterSwap(((SomeService.response.body.reasons)), ';', ',')}}"
}
```

Input:
`"A, B; C"`

Output:
`"A;B; C"`

Recommended mapping pattern for SF text fields:
- Use `delimetterSwap(..., ';')` when Salesforce field values should be semicolon-separated.
- Use third argument when you need strict control and only one source delimiter should be transformed.

### 5) Array expansion (`arrayPath`)
Use when you need one request per element of an array in valueJson.

- Add `arrayPath` to a sub-request, e.g., `"arrayPath": "NTCModel.response.body.body"`
- The template expands to N requests (one per array element)
- Context available inside an expanded item:
  - `current`: the array element object (or value)
  - `index`: element index (`0, 1, 2, ...`)
- Dot-paths that start with `arrayPath` are automatically transformed to reference `current`
- `referenceId` handling in arrays:
  - If it contains `{{index}}`, it gets replaced
  - Otherwise the system appends `_<index>` automatically

Example:

```json
{
  "url": "/services/data/<salesforce.apiVersion>/sobjects/A_Score__c",
  "method": "POST",
  "referenceId": "NTCModel_A_Score__c_Post_{{index}}",
  "merge": "false",
  "arrayPath": "NTCModel.response.body.body",
  "body": {
    "Data__c": "((current.Data__c))",
    "Lead__c": "((current.leadid))",
    "Type__c": "((current.type))",
    "Decile__c": "((current.Credit_bucket))",
    "Acquisition_Probability_of_default__c": "((current.Underwriting_score))"
  }
}
```

Behavior controls (config keys in your service config file):
- `arrayProcessing.memoryPoolThreshold` (int, default 20)
- `arrayProcessing.enableMemoryPool` (bool, default true)
- `arrayProcessing.fallbackToSingleElement` (bool, default true)
- `arrayProcessing.skipInvalidArrayPaths` (bool, default true)

Edge cases:
- If `arrayPath` is missing or points to non-array:
  - With `fallbackToSingleElement=true`, the template is processed as a single request using that value as `current`
  - Otherwise the template is skipped or errors based on configuration
- Empty array → generates zero requests

### 6) Merging across templates (`merge` flag)
When multiple `request_body` arrays exist across rows for the same service set, they are combined with the following rules:

- Grouping by base `referenceId`:
  - The base refID removes a numeric suffix like `_0`, `_1` and the suffix `_merged`
  - Multi-item groups sharing a base refID are grouped together

- Merge behavior:
  - If a group has multiple items and every item has `merge == "true"`, their bodies are merged into one request
    - Keys from later items overwrite earlier ones
    - The resulting `referenceId` becomes `<firstRefID>_merged`
  - If a group has a single item, or any item has `merge != "true"`, the items remain separate

- Cross-group merging for single items:
  - Single-item groups with `merge == "true"` may merge across groups if they share the same `url + method`; bodies are merged; `referenceId` becomes `<firstRefID>_merged`

Notes:
- Do not mix different HTTP methods for the same base refID and URL (it will not merge and logs a warning)
- Use `merge: "true"` when you split a target object into multiple configuration entries but want a single upsert

### 7) Filtering before Salesforce call
- Any sub-request with an empty body after interpolation is removed
- If all are empty, the Salesforce call is skipped and the service returns synthetic "Empty Body" responses for each `referenceId`

### 8) Salesforce client configuration keys
Provide the following keys in the service configuration:

- `salesforce.clientId`
- `salesforce.clientSecret`
- `salesforce.username`
- `salesforce.password`
- `salesforce.securityToken`
- `salesforce.loginURL`
- `salesforce.baseURL`
- `salesforce.apiVersion` (e.g., `v64.0`)
- Optional: `salesforce.compositeEndpoint` (defaults to `/services/data/{apiVersion}/composite/graph` with `{apiVersion}` replaced)

### 9) Trigger endpoint and authentication
- Endpoint: `POST /decision-manager/v1/trigger-decision`
- Headers:
  - `X-Api-Key`: must match config key `EsaApiKey`
  - `Authorization: Bearer <token>`: enforced only when `Environment == "production"` (validated against `JwksAudience`, `JwksIssuer`, `JwksUrl`)
- CORS: `AllowedOrigins` (comma-separated) governs CORS; methods: GET, POST, PUT, PATCH; headers: `*`
- Correlation ID: If `X-Correlation-ID` is missing, it is generated and echoed back

### 10) Redis caching for mapping fetch
- The combined `ServiceSfdcFieldMapping` result is cached by `sequenceId`
- Cache key: `decision-manager<Environment>serviceSfdcFieldMapping:<sequenceId>`
- On cache miss/unmarshal error → DB fetch → cache set
- Cache is provided via common-modules using `cache.*` config keys (host, port, password, database, enabled). If disabled/unavailable, processing works without cache.

### 11) Database and migrations
- `service_sfdc_field_mapping` holds your configuration per service with `request_body` (JSONB)
- Included Goose migrations are PostgreSQL-specific (use of `JSONB`, `BIGSERIAL`)
  - If using MySQL, either disable Goose migrations or provide MySQL-specific SQL migrations

### 12) End-to-end example

Salesforce client (config file):

```json
{
  "salesforce": {
    "clientId": "...",
    "clientSecret": "...",
    "username": "...",
    "password": "...",
    "securityToken": "...",
    "loginURL": "https://test.salesforce.com/services/oauth2/token",
    "baseURL": "https://yourdomain.my.salesforce.com",
    "apiVersion": "v64.0",
    "compositeEndpoint": "/services/data/{apiVersion}/composite/graph"
  },
  "arrayProcessing": {
    "memoryPoolThreshold": 20,
    "enableMemoryPool": true,
    "fallbackToSingleElement": true,
    "skipInvalidArrayPaths": true
  }
}
```

Insert a configuration row (PostgreSQL example):

```sql
INSERT INTO service_sfdc_field_mapping (service_name, request_body, created_date, created_by)
VALUES (
  'NTCModel',
  '[
     {
       "url": "/services/data/v64.0/sobjects/Audit_Log__c",
       "method": "POST",
       "referenceId": "NTCModel_Audit_Log__c_Post",
       "merge": "false",
       "body": {
         "Type__c": "((NTCModel.serviceName))",
         "StartTime__c": "((NTCModel.startTime))",
         "EndTime__c": "((NTCModel.endTime))",
         "Response__c": "{{serializeJson(((NTCModel.response)))}}"
       }
     },
     {
       "url": "/services/data/v64.0/sobjects/A_Score__c",
       "method": "POST",
       "referenceId": "NTCModel_A_Score__c_Post",
       "merge": "false",
       "body": {
         "Data__c": "((NTCModel.response.body.body[0].Data__c))",
         "Lead__c": "((NTCModel.response.body.body[0].leadid))",
         "Type__c": "((NTCModel.response.body.body[0].type))",
         "Decile__c": "((NTCModel.response.body.body[0].Credit_bucket))",
         "Acquisition_Probability_of_default__c": "((NTCModel.response.body.body[0].Underwriting_score))"
       }
     }
   ]'::jsonb,
  now(),
  'configurator'
);
```

### 13) Troubleshooting
- A field didn’t populate:
  - Ensure the `((...))`/`{{...}}`/`<...>` selector resolves to a non-empty value; empty strings are omitted
- Array expansion didn’t occur:
  - Verify `arrayPath` exists and points to an array; use `fallbackToSingleElement=true` for graceful fallback
- Unexpected merge or duplicates:
  - Check `merge` flags and base `referenceId` naming; avoid mixing HTTP methods for the same base refID+URL
- Composite errors:
  - Confirm `salesforce.baseURL`, `salesforce.apiVersion`, and credentials
- Caching not used:
  - Ensure `cache.enabled=true` and connection details are correct; processing still works without cache
- Preprocessing log entries (all isolated — request continues, failing `_pp*` key is set to nil):
  - `preProcessing: skipping rule with invalid storeAs ...` — rename `storeAs` to begin with `_pp` and include at least one char after the prefix.
  - `preProcessing: skipping rule with empty expr` — provide an `expr`.
  - `preProcessing rule failed; storing nil and continuing` — read the `error` field; it carries the original govaluate / custom-function error.
  - `preProcessing rule unresolved (cycle or missing _pp* dependency); storing nil and continuing` — the `missingPpKeys` field lists what was waited on; ensure every referenced `_pp*` key is produced somewhere in the batch.
  - `arrayPath` pointing into a `_pp*` key returns nil — confirm the producing rule actually ran (check logs for the `preprocessing_done` checkpoint and `preProcessing applied` debug entries).

---

This guide documents exactly what is supported by the service today: the `request_body` schema, templating, expressions, array expansion, merging, request filtering, Salesforce client configuration, caching, and auth/trigger details. If you need help crafting selectors for a specific ESA response, provide a sample payload and target fields to derive precise `request_body` entries.


