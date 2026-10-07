# Decision Manager Database Configuration Guide

## Table of Contents
1. [Overview](#overview)
2. [Core Configuration Structure](#core-configuration-structure)
3. [Field Mapping Configuration](#field-mapping-configuration)
4. [Preprocessing Configuration](#preprocessing-configuration)
5. [Expression Syntax](#expression-syntax)
6. [Array Expansion](#array-expansion)
7. [Merge Logic](#merge-logic)
8. [Service Sequence Configuration](#service-sequence-configuration)
9. [Complete Examples](#complete-examples)
10. [Best Practices](#best-practices)
11. [Troubleshooting](#troubleshooting)

## Overview

The Decision Manager uses database configurations to define how external service responses are mapped to Salesforce objects. This guide covers all configuration options with practical examples.

## Core Configuration Structure

### Basic Request Template
```json
{
  "url": "/services/data/v64.0/sobjects/ObjectName__c",
  "method": "POST|PATCH|PUT",
  "referenceId": "unique_reference_identifier",
  "body": {
    "Field__c": "value_or_expression"
  },
  "merge": "true|false",
  "arrayPath": "path.to.array.in.response",
  "lookup": {
    "as": "joinedAlias",
    "fromArrayPath": "OtherService.response.body.results",
    "currentKeyPath": "gstid",
    "lookupKeyPath": "gstin"
  },
  "preProcessing": [
    { "expr": "{{ ... }} or ((path))", "storeAs": "_ppXxx" }
  ]
}
```

### Field Descriptions

| Field | Required | Description | Example |
|-------|----------|-------------|---------|
| `url` | Yes | Salesforce API endpoint | `/services/data/v64.0/sobjects/Contact` |
| `method` | Yes | HTTP method | `POST`, `PATCH`, `PUT` |
| `referenceId` | Yes | Unique identifier for this request | `ServiceName_Object_Method` |
| `body` | Yes | Field mappings for Salesforce object | `{"Name": "John Doe"}` |
| `merge` | No | Whether to merge multiple requests | `"true"` or `"false"` |
| `arrayPath` | No | Path to array for expansion | `"response.body.items"` |
| `lookup` | No | Key-based join config for array expansion | `{"as":"gstJoin","fromArrayPath":"OtherService.response.body.results","currentKeyPath":"gstid","lookupKeyPath":"gstin"}` |
| `preProcessing` | No | Transforms run BEFORE interpolation; produces `_pp*` keys in valueJson (see [Preprocessing Configuration](#preprocessing-configuration)) | `[{"expr":"{{stringToJson(jsonPart(((CRIF.response.body.raw_response))))}}","storeAs":"_ppCrifRaw"}]` |

## Field Mapping Configuration

### Static Values
```json
{
  "Type__c": "Static Value",
  "Status__c": "Active"
}
```

### Dynamic Values from Service Response

#### 1. Dot Path Notation `((path.to.value))`
```json
{
  "Score__c": "((CreditService.response.body.score))",
  "Risk_Level__c": "((CreditService.response.body.riskCategory))"
}
```

#### 2. Salesforce Variable Notation `<object.field>`
```json
{
  "Contact__c": "<contact.Id>",
  "Lead__c": "<lead.Id>",
  "Account__c": "<account.Id>"
}
```

#### 3. Expression Evaluation `{{expression}}`
```json
{
  "Full_Name__c": "{{concat(firstName, ' ', lastName)}}",
  "Total_Score__c": "{{creditScore + behaviorScore}}",
  "Serialized_Data__c": "{{serializeJson(((ServiceName.response.body)))}}"
}
```

**Important:** Inside `{{...}}` expressions, do not use angle-bracket variables like `<contact.Id>` or `<lead.Id>`.  
Use dot-path style with double brackets, e.g. `((contact.Id))`, `((lead.Id))`.

## Preprocessing Configuration

The optional `preProcessing` array on any sub-request runs ESA-response transformations BEFORE any `arrayPath` resolution or body templating happens. It is the right place to:

- Parse a stringified JSON field (e.g. a bureau `raw_response`) into an actual object so `arrayPath` and `((path))` references can traverse it.
- Build short aliases for deeply-nested subtrees that several sibling requests will reuse.
- Apply any reusable transformation that you do not want to repeat in every body template.

### Rule Schema

```json
"preProcessing": [
  { "expr": "...", "storeAs": "_ppXxx" }
]
```

| Field | Required | Description |
|-------|----------|-------------|
| `expr` | Yes | A govaluate expression (`{{ ... }}`) or a path alias (`(( ... ))`). All custom functions and operators available in body/url templates are available here. |
| `storeAs` | Yes | Top-level key under which the result is written into valueJson. MUST start with the reserved `_pp` prefix (e.g. `_ppCrifRaw`, `_ppCrifInquiries`). |

### Why the `_pp` Prefix is Reserved

The interpolation engine treats top-level keys starting with `_pp` specially: it stores the reference (so `getPath` and `serializeJson` can still see them) but does NOT recursively flatten them into the govaluate parameter map. Without this, a parsed bureau payload would add hundreds-to-thousands of flat parameter entries to every interpolation, ballooning memory and CPU per request. Reserving the namespace makes the optimisation safe by construction.

Any `storeAs` not starting with `_pp` is rejected up-front with a clear error.

### Execution Semantics

- Rules from ALL sub-requests in a request batch are collected into one ordered list, deduplicated by `storeAs` (first declaration wins). Declaring the same rule on multiple sibling sub-requests is therefore safe and zero-cost.
- All rules apply BEFORE any `InterpolateValuesWithArrayExpansion` work. Every sub-request that uses a `_pp*` key sees it already materialised.
- Order is resolved via a multi-pass fixpoint. If a rule depends on another `_pp*` key not yet present, it is deferred to the next pass.
- Original ESA data in valueJson is NEVER overwritten; only NEW `_pp*` top-level keys are produced.

### Failure Isolation (graceful degradation)

A misconfigured or failing preprocessing rule **never aborts the request**. Specifically:

- **Bad expression / custom-function error / invalid JSON** → the failing rule's `storeAs` is set to `nil`, the underlying error is logged with `storeAs`, `expr`, and the root cause, and the next rule runs.
- **Invalid `storeAs` prefix or empty `expr`** → the rule is logged and skipped entirely. Remaining rules still run.
- **Cycle or genuinely missing `_pp*` dependency** → after the fixpoint stalls, every stuck rule is logged with the precise list of `_pp*` keys it was waiting on, and its `storeAs` is set to `nil`. Pipeline continues.
- **Context cancellation** is the only hard failure: the whole request short-circuits with the cancellation error.

Downstream `((_pp*))` lookups against a nil-stored key behave the same as any other missing field (resolve to empty). A single misconfigured preprocessing rule therefore degrades only the SF fields that depended on it, never sibling sub-requests.

### Audit Trail in MongoDB

Every request that runs preprocessing writes two extra fields into its `decision-manager` mongo log entry:

| Field | Type | Contents |
|-------|------|----------|
| `preprocessingTimeMs` | int64 | Total wall-clock duration of the preprocessing stage. |
| `preprocessing` | object | Full structured summary; see schema below. |

`preprocessing` schema:

```jsonc
{
  "durationMs":      123,               // total time
  "ruleCount":       3,                 // rules attempted (after dedup, after invalid-skip)
  "succeeded":       2,
  "failed":          1,
  "unresolved":      0,                 // cycle / missing dep, nil-stored
  "skippedInvalid":  0,                 // bad storeAs / empty expr
  "duplicates":      0,                 // duplicate storeAs dropped
  "passes":          1,                 // fixpoint passes used
  "cancelled":       false,             // true if ctx cancelled mid-flight
  "rules": [                            // per-rule outcomes, in execution order
    {
      "storeAs":    "_ppCrifRaw",
      "expr":       "{{stringToJson(jsonPart(((CRIF.response.body.raw_response))))}}",
      "status":     "success",          // success | failed | unresolved | skipped
      "pass":       1,
      "durationMs": 12
    },
    {
      "storeAs":      "_ppOther",
      "expr":         "{{stringToJson(((Bad.path)))}}",
      "status":       "failed",
      "pass":         1,
      "durationMs":   2,
      "error":        "stringToJson: invalid JSON: unexpected end of JSON input"
    }
  ],
  "values": {                           // every produced "_pp*" key, post-eval
    "_ppCrifRaw": { /* parsed JSON tree */ },
    "_ppOther":   null
  }
}
```

This audit trail lets you replay or diagnose any preprocessing run without re-executing the upstream ESA call. Notes:

- `values` references the same parsed trees that downstream interpolation consumed, so the mongo entry grows by roughly the parsed-tree footprint. For bureau-grade responses (KB-MB) this is fine.
- **Automatic size guard**: just before the doc reaches MongoDB the repository checks the marshaled BSON size against `MaxMongoDocSizeBytes` (currently 15 MiB, 1 MiB below the BSON hard limit). If the doc exceeds the soft limit the repository drops `preprocessing.values` entirely and sets `preprocessing.valuesStripped: true` on the way out, so the rest of the audit record (counters, per-rule outcomes, ESA request / response, SF composite) still lands in mongo. Stripped trees are re-derivable from `esaResponseBody`.
- A `Warn` log line is emitted whenever this strip fires, carrying the before/after byte counts and `customerId` for correlation. If the doc is still over the limit after stripping (heavy non-preprocessing payloads), an `Error` is logged and the insert is still attempted so the failure mode is mongo's well-defined oversize rejection, not a silent drop.
- The size check reuses the marshal output via `bson.Raw`, so the in-budget common path costs exactly one `bson.Marshal` pass — the same work the driver would have done internally. Net overhead: ≈ 0.
- `rules[].error` carries the root cause already unwrapped from the internal `MissingParameterError` wrapper, so the message is the actual govaluate / custom-function error.
- When a rule is skipped at config-validation time, `rules[].pass` and `rules[].durationMs` are absent (BSON `omitempty`) since the rule never reached evaluation.

### New Custom Functions Useful in Preprocessing

| Function | Description |
|----------|-------------|
| `stringToJson(value)` | Parses a stringified JSON value (object or array) into a Go map/slice. Streams via JSON decoder so trailing non-JSON content (e.g. an HTML report appended after the JSON) is tolerated. Already-parsed values pass through unchanged. |
| `getPath(data, dotPath)` | Walks a parsed object/array using a dot path. Escape hatch for reaching INTO a `_pp*` preprocessed tree from inside an expression. Example: `getPath(_ppCrifRaw, 'CIR-REPORT-FILE.HEADER-SEGMENT.STATUS')`. |
| `findByField(array, fieldName, fieldValue [, extractField])` | Searches an array of objects for the **first** element where `element[fieldName] == fieldValue` (case-insensitive). Returns the matched element, or if `extractField` is given, returns that sub-field instead. Returns nil when no match is found. Example: `findByField(_ppCrifVariations, 'TYPE', 'ADDRESS-VARIATIONS', 'VARIATION')` returns the `VARIATION` array from the ADDRESS-VARIATIONS entry. |
| `selectByFieldSorted(array, matchFieldPath, matchValue, sortFieldPath, order, extractFieldPath)` | Filters an array of objects by an optional **nested** field (`matchFieldPath == matchValue`, case-insensitive; pass `''`/`''` to disable), drops elements whose `sortFieldPath` key is null/empty, then returns `extractFieldPath` (nested path; `''` = whole element) from the element that wins the sort. `order` is `asc` (earliest/smallest) or `desc` (latest/largest). Sort key type auto-detected (number → date → string). Accepts an array, a single object, or spread objects, so it covers both the `records: [...]` and `records: {...}` shapes with one call. Returns nil when nothing matches or all keys are null — ideal as the condition in a ternary that falls back to `uploadToS3`/`now()`. Example: `selectByFieldSorted(((multibureau_consolidate_data__c.records)), 'Multibureau__r.Bureau__c', 'CRIF', 'Multibureau_Date__c', 'desc', 'Multibureau__r.Credit_Bureau_pdf__c')`. |
| `jsonPart(responseBody)` | (existing) Returns content before `<html` (case-insensitive). Often chained as `stringToJson(jsonPart(...))` for mixed JSON+HTML payloads. |

### Worked Example: CRIF Stringified Response

The CRIF service returns its credit report under `CRIF.response.body.raw_response` as a string containing valid JSON followed by an HTML report. Preprocessing parses the JSON portion once, exposes the parsed tree as `_ppCrifRaw`, and aliases nested subtrees for cheap downstream access:

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
  "body": { "Account__c": "((current.ACCOUNT-NUMBER))" }
}
```

### Common Patterns

- Parse stringified JSON once, alias children for arrayPath:
  ```json
  [
    {"expr":"{{stringToJson(jsonPart(((Service.response.body.raw_response))))}}","storeAs":"_ppParsed"},
    {"expr":"((_ppParsed.someArray))","storeAs":"_ppItems"}
  ]
  ```
- Pull a deeply-nested string for use inside a body expression (`getPath` reaches into a `_pp*` tree without flattening it):
  ```json
  "Status__c": "{{toLower(getPath(_ppCrifRaw, 'CIR-REPORT-FILE.HEADER-SEGMENT.STATUS'))}}"
  ```

### Failure Log Entries (rule isolated, request continues)

All entries below are emitted at `Error` level. None of them abort the request — the failing rule's `storeAs` is set to `nil` and the rest of the pipeline keeps running. Inspect logs (filter by `storeAs`) to diagnose.

| Log message | Cause | Fix |
|-------------|-------|-----|
| `preProcessing: skipping rule with invalid storeAs ...` | `storeAs` does not start with `_pp` or has nothing after the prefix. | Rename it to `_ppSomething`. |
| `preProcessing: skipping rule with empty expr` | Missing or whitespace-only `expr`. | Provide an expression. |
| `preProcessing rule failed; storing nil and continuing` (with `error` field) | Expression itself raised an error (e.g. `stringToJson: invalid JSON`, malformed govaluate syntax, custom-function error). | Verify the source value, syntax and function arguments; chain `jsonPart` for mixed JSON+HTML payloads. |
| `preProcessing rule unresolved (cycle or missing _pp* dependency); storing nil and continuing` (with `missingPpKeys`) | Rule references `_pp*` keys not produced by any rule, or two rules reference each other. | Ensure the producing rule exists and its `storeAs` matches the reference exactly. |

## Expression Syntax

The Decision Manager uses the **govaluate** library for expression evaluation, supporting mathematical operations, logical comparisons, and custom functions.

### Basic Expression Structure
```json
{
  "Field Name": "{{expression_here}}"
}
```

### Govaluate Built-in Operators

#### Arithmetic Operators
```json
{
  "Addition__c": "{{value1 + value2}}",//eg. Addition__c is a SF field name
  "Subtraction__c": "{{value1 - value2}}",
  "Multiplication__c": "{{value1 * value2}}",
  "Division__c": "{{value1 / value2}}",
  "Modulus__c": "{{value1 % value2}}",
  "Exponentiation__c": "{{value1 ** value2}}"
}
```

#### Comparison Operators
```json
{
  "Equal__c": "{{value1 == value2}}",
  "Not_Equal__c": "{{value1 != value2}}",
  "Greater_Than__c": "{{value1 > value2}}",
  "Greater_Equal__c": "{{value1 >= value2}}",
  "Less_Than__c": "{{value1 < value2}}",
  "Less_Equal__c": "{{value1 <= value2}}"
}
```

#### Logical Operators
```json
{
  "And_Logic__c": "{{condition1 && condition2}}",
  "Or_Logic__c": "{{condition1 || condition2}}",
  "Not_Logic__c": "{{!condition}}"
}
```

#### Ternary Operator (Conditional)
```json
{
  "Status__c": "{{score > 700 ? 'APPROVED' : 'REJECTED'}}",
  "Risk_Level__c": "{{score > 800 ? 'LOW' : score > 600 ? 'MEDIUM' : 'HIGH'}}",
  "Grade__c": "{{marks >= 90 ? 'A' : marks >= 80 ? 'B' : marks >= 70 ? 'C' : 'F'}}"
}
```

#### Bitwise Operators
```json
{
  "Bitwise_And__c": "{{value1 & value2}}",
  "Bitwise_Or__c": "{{value1 | value2}}",
  "Bitwise_Xor__c": "{{value1 ^ value2}}",
  "Left_Shift__c": "{{value << 2}}",
  "Right_Shift__c": "{{value >> 2}}"
}
```

#### String Operations
```json
{
  "String_Concat__c": "{{firstName + ' ' + lastName}}",
  "String_Match__c": "{{name =~ 'John.*'}}",  
  "String_Not_Match__c": "{{name !~ 'Admin.*'}}"
}
```

### Custom Functions Available

#### Mathematical Functions
```json
{
  "Min_Value__c": "{{min(score1, score2, score3)}}",
  "Max_Value__c": "{{max(score1, score2, score3)}}",
  "Power_Value__c": "{{pow(base, exponent)}}",
  "Square_Root__c": "{{sqrt(value)}}",
  "Absolute_Value__c": "{{abs(negativeValue)}}",
  "Rounded_Value__c": "{{round(decimalValue)}}",
  "Ceiling_Value__c": "{{ceil(decimalValue)}}",
  "Floor_Value__c": "{{floor(decimalValue)}}"
}
```

**Function Details:**
- `min(a, b, c, ...)` - Returns minimum value from arguments
- `max(a, b, c, ...)` - Returns maximum value from arguments  
- `pow(base, exponent)` - Returns base raised to exponent power
- `sqrt(value)` - Returns square root of value
- `abs(value)` - Returns absolute value
- `round(value)` - Rounds to nearest integer
- `ceil(value)` - Rounds up to nearest integer
- `floor(value)` - Rounds down to nearest integer

#### String Functions
```json
{
  "Concatenated__c": "{{concat('Hello', ' ', 'World', '!')}}",
  "Full_Name__c": "{{concat(firstName, ' ', middleName, ' ', lastName)}}",
  "Address__c": "{{concat(street, ', ', city, ', ', state, ' ', zipCode)}}",
  "Lower_Name__c": "{{toLower(((contact.EmploymentType__c)))}}",
  "Upper_Name__c": "{{toUpper(((lead.Sourcing_Program__c)))}}",
  "Response_Safe__c": "{{truncate(((CIBIL.response.body)), 1300)}}",
  "Response_Raw_Cut__c": "{{truncateRaw(((CIBIL.response.body)), 0, 1300)}}"
}
```

**Function Details:**
- `concat(arg1, arg2, arg3, ...)` - Concatenates all arguments into a single string
- `toLower(value)` - Converts value to lowercase string
- `toUpper(value)` - Converts value to uppercase string
- `stringToInt(value)` - Strips all non-digit characters (keeps `0-9` and the first `.`), parses as float64, then truncates to integer. Also accepts numeric types directly. Returns `0` for nil, empty, or all-alpha input.
- `intToString(value)` - Converts an integer (or any numeric type) to its string representation. Falls back to a general string conversion for non-numeric inputs. Returns `""` for nil input.
- `stringToDouble(value)` - Strips all non-digit characters (keeps `0-9` and the first `.`), parses as float64. Also accepts numeric types directly. Returns `0` for nil, empty, or all-alpha input.
- `doubleToString(value)` - Converts a `float64` (or any numeric type) to its string form with no unnecessary trailing zeros (e.g. `3.14` → `"3.14"`, `10.0` → `"10"`). Falls back to a general string conversion for non-numeric inputs. Returns `""` for nil input.
- `truncate(data, maxChars)` - Type-preserving truncation; recursively truncates string values in objects/arrays and keeps non-string types unchanged
- `truncate(data, start, end)` - Backward-compatible raw character slice mode (same behavior as `truncateRaw(data, start, end)`)
- `truncateRaw(data, start, end)` - Raw character slice on string form of input; may produce invalid/truncated JSON
- `truncateRaw(data, end)` - Convenience form equivalent to `truncateRaw(data, 0, end)`
- `jsonPart(responseBody)` - Returns content before `<html` (used for bureau JSON extraction from mixed JSON+HTML payloads)
- `htmlPart(responseBody)` - Returns content from `<html` onwards (used to isolate embedded HTML/PDF payloads)
- `stringToJson(value)` - Parses a stringified JSON value into an object/array. Uses a streaming JSON decoder so trailing non-JSON content (e.g. an HTML page after the JSON document) is tolerated. Already-parsed values pass through unchanged. Most useful inside `preProcessing`.
- `getPath(data, dotPath)` - Walks a parsed object/array using a dot path. Escape hatch for reaching INTO `_pp*` preprocessed trees from inside an expression (since `_pp*` keys are stored as references but not recursively flattened into the param map). Supports `[index]` array access.
- `findByField(array, fieldName, fieldValue [, extractField])` - Searches an array of objects for the first element where `element[fieldName] == fieldValue` (case-insensitive). Returns the matched element, or if `extractField` is given, returns that sub-field instead. Returns nil when no match is found (field is skipped, not an error).
- `uploadToS3(content, extension, filePath, bucketName, region)` - Uploads content to S3 and returns the public object URL

**Detailed behavior for split/upload/parse functions:**

| Function | Arguments | Return type | Working |
|---|---|---|---|
| `jsonPart(responseBody)` | 1 arg | string | Finds first `<html` (case-insensitive). Returns text before it. If `<html` is absent, returns full input. |
| `htmlPart(responseBody)` | 1 arg | string | Finds first `<html` (case-insensitive). Returns text from `<html...` onward. If absent, returns empty string. |
| `stringToJson(value)` | 1 arg | object/array/scalar | Decodes the first JSON value in the string. Empty input -> nil. Non-string input -> returned unchanged (idempotent). Invalid JSON -> error. |
| `getPath(data, dotPath)` | 2 args | any | Returns the value at `dotPath` inside `data`. Returns nil if the path is missing or `data` is the wrong type. |
| `findByField(array, fieldName, fieldValue [, extractField])` | 3–4 args | object/array/nil | Searches `array` for the first element where `element[fieldName] == fieldValue` (case-insensitive). With 3 args returns the matched element (map). With 4 args returns `element[extractField]` from the matched element. Returns nil on no match or if `array` is not a slice. |
| `selectByFieldSorted(array, matchFieldPath, matchValue, sortFieldPath, order, extractFieldPath)` | 6 args | any/nil | Filters `array` (array, single object, or spread objects) by nested `matchFieldPath == matchValue` (case-insensitive; empty match args disable filtering), drops elements with null/empty `sortFieldPath`, then returns nested `extractFieldPath` (empty = whole element) from the sort winner. `order`: `asc` = smallest/earliest, else largest/latest. Sort key auto-typed number→date→string. Stable on ties. Returns nil when nothing survives — use as a ternary condition with a fallback. |
| `uploadToS3(content, extension, filePath, bucketName, region)` | 5 args | string | Uploads `content` into S3 key generated from `filePath` + `extension`, and returns generated URL. On upload/config error returns empty string (field gets skipped by interpolation rules). |
| `stringToInt(value)` | 1 arg | int | Strips all characters except digits `0-9` and the **first** `.`, parses as float64, then truncates to int. Non-string numerics are accepted directly. Returns `0` for nil, empty, or all-alpha input. **Note:** `stringToInt` returns a Go `int`; it cannot be used directly in govaluate arithmetic (`+`, `-`, `*`). Use `stringToDouble` when the result must participate in arithmetic. |
| `intToString(value)` | 1 arg | string | Converts an integer or numeric value to its string form. Falls back to `fmt.Sprintf("%v", value)` for non-numeric types. Returns `""` for nil input. |
| `stringToDouble(value)` | 1 arg | float64 | Strips all characters except digits `0-9` and the **first** `.`, then parses as float64. Non-string numerics are accepted directly. Returns `0` for nil, empty, or all-alpha input. Returns `float64` so the result can be used in arithmetic expressions. |
| `doubleToString(value)` | 1 arg | string | Converts a `float64` or numeric value to its string form with no unnecessary trailing zeros (e.g. `3.14` → `"3.14"`, `10.0` → `"10"`). Returns `""` for nil input. |

**Type Conversion Examples:**

```json
{
  "Salary_Int__c":    "{{stringToInt(((lead.Annual_Income__c)))}}",
  "Salary_Str__c":    "{{intToString(((lead.Salary_Int__c)))}}",
  "Rate_Double__c":   "{{stringToDouble(((loan.Interest_Rate__c)))}}",
  "Rate_Label__c":    "{{doubleToString(((loan.Rate_Double__c)))}}",
  "Chained_Int__c":   "{{intToString(stringToInt(((lead.Raw_Income__c))))}}",
  "Chained_Dbl__c":   "{{doubleToString(stringToDouble(((loan.Raw_Rate__c))))}}",
  "Total_Amount__c":  "{{stringToDouble(((loan.Principal__c))) + stringToDouble(((loan.Interest__c))))}}",
  "Score_Bumped__c":  "{{stringToDouble(((bureau.score__c))) + 50}}"
}
```

**Input → output quick reference:**

| Input string | `stringToInt` | `stringToDouble` |
|---|---|---|
| `"42000"` | `42000` | `42000` |
| `"42,000"` | `42000` | `42000` |
| `"20,000/weekly"` | `20000` | `20000` |
| `"₹1,00,000"` | `100000` | `100000` |
| `"1,234.56"` | `1234` | `1234.56` |
| `"1,234.56/month"` | `1234` | `1234.56` |
| `"3.14"` | `3` | `3.14` |
| `"85.5%"` | `85` | `85.5` |
| `"abc"` | `0` | `0` |
| `""` / nil | `0` | `0` |

**Known limitation — prefix containing a dot (e.g. `"Rs. 5,000"`):**
The stripping logic keeps the **first** `.` it encounters, even if it belongs to an abbreviation like `Rs.` The result is `".5000"` which parses as `0.5` → `stringToInt` returns `0`.
Workaround: Pre-clean the string with `left` or `substring` to remove the prefix, or use `stringToDouble` and compare > 0.

**Arithmetic note:**
`stringToInt` returns a Go `int`. govaluate arithmetic operators (`+`, `-`, `*`, `/`) require `float64`. Use `stringToDouble` when the parsed value must participate in arithmetic:
```json
{
  "Total__c": "{{stringToDouble(((loan.Principal__c))) + stringToDouble(((loan.Fee__c)))}}",
  "Score__c":  "{{stringToDouble(((bureau.rawScore__c))) * 1.1}}"
}
```

**`uploadToS3` argument details:**
- `content`: generally use `htmlPart(...)`
- `extension`: `'.html'` recommended (`'html'` also supported)
- `filePath`: folder + filename path; dynamic expressions supported
- `bucketName`: bucket key for config lookup (`s3.buckets.<bucketName>.*`)
- `region`: AWS region for target bucket (for example `ap-south-1`)

**Truncation Examples:**
```json
{
  "Audit_Response__c": "{{truncate(((SomeService.response.body)), 1300)}}",
  "Audit_Response_Raw__c": "{{truncateRaw(((SomeService.response.body)), 0, 1300)}}",
  "Audit_Response_Raw_Short__c": "{{truncateRaw(((SomeService.response.body)), 1300)}}",
  "Name_Short__c": "{{truncate(((SomeService.response.body.customerName)), 50)}}"
}
```

**Truncation Notes:**
- Use `truncate(...)` when you want safer type-preserving behavior.
- Use `truncateRaw(...)` when you need pure character cutting and do not care about JSON validity.
- `truncateRaw` uses deterministic JSON string representation for `map[string]interface{}` inputs.

**CRIF Mixed Response Example:**
```json
{
  "Response__c": "{{truncateRaw(jsonPart(((CRIF.response.body.raw_response))), 0, 130000)}}",
  "Credit_Bureau_pdf__c": "{{uploadToS3(htmlPart(((CRIF.response.body.raw_response))), '.html', concat('Crif_PDF/', ((contact.Id)), '/', formatDate(now(), 'YYYY-MM-DD_HH:mm:ss')), 'example-bucket', 'ap-south-1')}}"
}
```

**Example output behavior:**
- Input: `{"statusCode":200,"body":{"ok":true}}<html><body>PDF</body></html>`
- `jsonPart(...)` => `{"statusCode":200,"body":{"ok":true}}`
- `htmlPart(...)` => `<html><body>PDF</body></html>`

#### Array Functions
```json
{
  "Array_String__c": "{{arrayToString(arrayField)}}",
  "UAN_List__c": "{{arrayToString(((MobileUANService.response.body.data.result)), 'uan')}}",
  "Item_List__c": "{{arrayToString(((Service.response.body.items)))}}"
}
```

**Function Details:**
- `arrayToString(array)` - Converts array to comma-separated string
- `arrayToString(array, fieldName)` - If array contains objects, extracts `fieldName` from each element before joining
- Supports multiple array types: `[]interface{}`, `[]string`, `[]int`, `[]float64`, etc.
- Handles empty arrays gracefully (returns empty string)

#### Filter + Sort + Select Function
```json
{
  "Latest_CRIF_PDF__c": "{{selectByFieldSorted(((multibureau_consolidate_data__c.records)), 'Multibureau__r.Bureau__c', 'CRIF', 'Multibureau_Date__c', 'desc', 'Multibureau__r.Credit_Bureau_pdf__c')}}"
}
```

`selectByFieldSorted(array, matchFieldPath, matchValue, sortFieldPath, order, extractFieldPath)` — filters an array of objects by an (optional) nested field equal to a value, then returns a field from the single element that **wins** a sort on another nested field. It is the generic "pick the latest / earliest / max / min matching element and pull a value out of it" helper.

**Arguments:**

| Arg | Description |
|---|---|
| `array` | The element set. Accepts a `[]` array of objects, a **single object** (treated as a one-element set), or govaluate-spread object elements. All three shapes are handled, so it works whether a Salesforce child relationship arrives as `records: [ ... ]` or as a single `records: { ... }` record. |
| `matchFieldPath` | Dot path (supports `a.b` and `a.b[0].c`) to the field used to filter. Pass `''` to disable filtering. |
| `matchValue` | Value that `matchFieldPath` must equal (case-insensitive string comparison). Pass `''` to disable filtering. |
| `sortFieldPath` | Dot path to the sort key. Elements whose key is **null or empty are dropped** (this yields "latest NOT-NULL" semantics). The key type is auto-detected across the surviving candidates: numeric if every key parses as a number, else chronological if every key parses as a date, else lexical string. |
| `order` | `asc` → winner is the smallest / earliest. Anything else (`desc` is the intended default) → winner is the largest / latest. |
| `extractFieldPath` | Dot path returned from the winning element. Pass `''` to return the whole element. |

**Returns:** the extracted value from the winning element, or `nil` when nothing matches, the array is empty/missing, or every candidate's sort key is null/empty. Ties keep the first element in input order (stable). Because it returns `nil` in the "nothing found" case, a downstream ternary can fall back — e.g. to `uploadToS3(...)` or `now()`.

**Notes:**
- Both `matchFieldPath` and `extractFieldPath` accept deep nested paths (e.g. `Multibureau__r.Credit_Bureau_pdf__c`), unlike `findByField` whose match/extract fields are top-level only.
- Non-object entries in the array are ignored defensively.
- Handles the array-vs-single-record shape internally, so **no `records != ''` ternary is needed** to cover both payload shapes — one call works for both.

**Example — latest CRIF PDF with HTML fallback (single expression handles array or single record):**
```json
{
  "Credit_Bureau_pdf__c": "{{selectByFieldSorted(((multibureau_consolidate_data__c.records)), 'Multibureau__r.Bureau__c', 'CRIF', 'Multibureau_Date__c', 'desc', 'Multibureau__r.Credit_Bureau_pdf__c') != '' ? selectByFieldSorted(((multibureau_consolidate_data__c.records)), 'Multibureau__r.Bureau__c', 'CRIF', 'Multibureau_Date__c', 'desc', 'Multibureau__r.Credit_Bureau_pdf__c') : uploadToS3(htmlPart(((CRIF.response.body.raw_response))), '.html', concat('Crif_PDF/', ((contact.Id)), '/', formatDate(now(), 'YYYY-MM-DD_HH:mm:ss')), 'example-bucket', 'ap-south-1')}}"
}
```

**Example — compute once in `preProcessing`, reference from the body:**
```json
{
  "preProcessing": [
    { "expr": "{{selectByFieldSorted(((multibureau_consolidate_data__c.records)), 'Multibureau__r.Bureau__c', 'CRIF', 'Multibureau_Date__c', 'desc', 'Multibureau__r.Credit_Bureau_pdf__c')}}", "storeAs": "_ppLatestCrifPdf" }
  ],
  "body": {
    "Credit_Bureau_pdf__c": "{{((_ppLatestCrifPdf)) != '' ? ((_ppLatestCrifPdf)) : uploadToS3(htmlPart(((CRIF.response.body.raw_response))), '.html', concat('Crif_PDF/', ((contact.Id)), '/', formatDate(now(), 'YYYY-MM-DD_HH:mm:ss')), 'example-bucket', 'ap-south-1')}}"
  }
}
```

#### List Membership Functions
```json
{
  "Is_Priority__c": "{{inList(((lead.Sourcing_Program__c)), 'offline store', 'qro2o')}}",
  "Has_Account_Type__c": "{{inListField(((CIBIL.response.body.consumerCreditData[0].accounts)), 'accountType', 12, 14, 23)}}"
}
```

**Function Details:**
- `inList(valueOrArray, option1, option2, ...)` - Returns true if value (or any value in an array of primitives) matches an option
- `inListField(arrayOfObjects, fieldName, option1, option2, ...)` - Returns true if any object in the array has `fieldName` matching an option

#### JSON Serialization Functions
```json
{
  "JSON_Data__c": "{{serializeJson(((ServiceName.response.body)))}}",
  "Pretty_JSON__c": "{{serializeJsonPretty(((ServiceName.response.body)))}}",
  "Complex_Object__c": "{{serializeJson(complexObjectVariable)}}",
  "Formatted_Response__c": "{{serializeJsonPretty(((CreditService.response)))}}"
}
```

**Function Details:**
- `serializeJson(object)` - Converts object to compact JSON string
- `serializeJsonPretty(object)` - Converts object to formatted JSON string with indentation
- Works with any object type: maps, arrays, nested structures
- Returns error if object cannot be serialized

### Complex Expression Examples

#### Multi-Condition Logic
```json
{
  "Approval_Status__c": "{{creditScore > 750 && income > 50000 && debtRatio < 0.4 ? 'AUTO_APPROVED' : creditScore > 650 && income > 30000 ? 'MANUAL_REVIEW' : 'REJECTED'}}",
  "Risk_Category__c": "{{fraudScore > 0.8 || blacklistFlag == true ? 'HIGH_RISK' : fraudScore > 0.5 ? 'MEDIUM_RISK' : 'LOW_RISK'}}",
  "Loan_Eligibility__c": "{{age >= 21 && age <= 65 && employmentType == 'SALARIED' && workExperience >= 2 ? 'ELIGIBLE' : 'NOT_ELIGIBLE'}}"
}
```

#### Mathematical Calculations
```json
{
  "EMI_Amount__c": "{{round(principal * (interestRate/12) * pow(1 + interestRate/12, tenure) / (pow(1 + interestRate/12, tenure) - 1))}}",
  "Total_Interest__c": "{{round((emiAmount * tenure) - principal)}}",
  "Processing_Fee__c": "{{min(max(principal * 0.02, 1000), 10000)}}",
  "Final_Score__c": "{{round((creditScore * 0.4) + (incomeScore * 0.3) + (stabilityScore * 0.3))}}"
}
```

#### String Manipulations
```json
{
  "Customer_Code__c": "{{concat('CUST_', leadId, '_', substring(panNumber, 0, 4))}}",
  "Full_Address__c": "{{concat(addressLine1, addressLine2 != '' ? ', ' + addressLine2 : '', ', ', city, ', ', state, ' - ', pincode)}}",
  "Reference_Number__c": "{{concat('REF', year(now()), month(now()), day(now()), '_', randomId)}}"
}
```

#### Array Processing
```json
{
  "Product_List__c": "{{arrayToString(((ProductService.response.body.products)), ' | ')}}",
  "Score_Summary__c": "{{concat('Scores: ', arrayToString(((ScoreService.response.body.scores)), ', '), ' (Average: ', round(sum(((ScoreService.response.body.scores))) / length(((ScoreService.response.body.scores)))), ')')}}",
  "Error_Messages__c": "{{arrayToString(((ValidationService.response.body.errors)), '; ')}}"
}
```

#### JSON Data Handling
```json
{
  "Raw_Credit_Data__c": "{{serializeJson(((CreditBureau.response.body)))}}",
  "Formatted_Profile__c": "{{serializeJsonPretty(((ProfileService.response.body.profile)))}}",
  "Decision_Log__c": "{{serializeJson(map('timestamp', now(), 'decision', approvalStatus, 'score', finalScore, 'factors', decisionFactors))}}"
}
```

### Variable References in Expressions

#### Service Response Variables
Variables are automatically created from service responses with underscore notation:
```json
{
  "Credit_Score__c": "{{CreditService_response_body_score}}",
  "Risk_Level__c": "{{FraudService_response_body_riskLevel}}",
  "Decision__c": "{{ActicoService_response_body_decision}}"
}
```

#### Nested Object Access
```json
{
  "Deep_Value__c": "{{ServiceName_response_body_level1_level2_targetField}}",
  "Array_Item__c": "{{ServiceName_response_body_items_0_name}}",
  "Complex_Path__c": "{{ServiceName_response_body_customer_profile_address_zipCode}}"
}
```

#### Special Variables
```json
{
  "Current_Item__c": "{{current_fieldName}}",  // In array expansion context
  "Array_Index__c": "{{index}}",              // Current array index
  "Lead_Reference__c": "{{lead_Id}}",         // From Salesforce lead object
  "Contact_Reference__c": "{{contact_Id}}"    // From Salesforce contact object
}
```

### Handling Spaces in Field Names
For fields with spaces like "Data Dictionary", the system automatically converts them:
```json
{
  "Data_Dictionary__c": "{{serializeJson(((SamsungFraudModel.response.body.Data Dictionary)))}}"
}
```

## Array Expansion

### Basic Array Expansion
When a service returns an array, use `arrayPath` to create multiple Salesforce records:

```json
{
  "url": "/services/data/v64.0/sobjects/Offer__c",
  "method": "POST",
  "referenceId": "PFAPRCalculation_Offer__c_POST",
  "arrayPath": "PFAPRCalculation.response.body.offers",
  "body": {
    "Type__c": "Proposed Offer",
    "Amount__c": "((current.amount))",
    "Tenor__c": "((current.tenor))",
    "APR__c": "((current.apr))",
    "Lead__c": "<lead.Id>"
  },
  "merge": "true"
}
```

### Array Context Variables
- `((current.fieldName))` - Current array item field
- `((index))` - Current array index (0-based)

### Array Lookup Join (O(n + m))
Use `lookup` when you need to enrich each `current` element from another service array by key match.

```json
{
  "url": "/services/data/v64.0/sobjects/Contact_Tax_Detail__c",
  "method": "POST",
  "referenceId": "KarzaGSTNonOTP_Contact_Tax_Detail__c_Post",
  "arrayPath": "KarzaGSTNonOTP.response.body.results",
  "lookup": {
    "as": "gstJoin",
    "fromArrayPath": "OtherService.response.body.results",
    "currentKeyPath": "gstid",
    "lookupKeyPath": "gstin"
  },
  "body": {
    "GST_number__c": "((current.gstid))",
    "Email_for_principal_place_of_business__c": "((gstJoin.Email_for_principal_place))",
    "Nature_of_business_carried_out_at_princi__c": "((gstJoin.Nature_of_business_carried))"
  },
  "merge": "false"
}
```

**How it works:**
- Builds an in-memory index from `fromArrayPath` using `lookupKeyPath` (m records)
- Iterates `arrayPath` (`current`) and resolves `currentKeyPath` per item (n records)
- Looks up the matched object in O(1) and exposes it as `((<as>.<field>))`

**Behavior Notes:**
- Matching is case-insensitive and whitespace-trimmed (`trim + upper`)
- If no match is found, alias (e.g. `gstJoin`) is not populated for that item
- Unresolved joined fields are omitted from request body (same as other unresolved dot-path fields)
- If duplicate keys exist in lookup array, first occurrence wins (warning logged)
- `currentKeyPath` and `lookupKeyPath` can be relative field names (e.g. `gstid`) or `current.*` style (e.g. `current.gstid`)
- `lookup.as` must be a simple alias without spaces, dots, or brackets

### Lookup Examples (Latest Behavior)

#### Example A: Relative key paths
```json
{
  "arrayPath": "KarzaGSTNonOTP.response.body.results",
  "lookup": {
    "as": "gstJoin",
    "fromArrayPath": "GSTMaster.response.body.results",
    "currentKeyPath": "gstid",
    "lookupKeyPath": "gstin"
  },
  "body": {
    "GST_number__c": "((current.gstid))",
    "Trade_Name__c": "((gstJoin.tradeName))"
  }
}
```

#### Example B: `current.*` style key paths
```json
{
  "arrayPath": "KarzaGSTNonOTP.response.body.results",
  "lookup": {
    "as": "gstJoin",
    "fromArrayPath": "GSTMaster.response.body.results",
    "currentKeyPath": "current.gstid",
    "lookupKeyPath": "current.gstin"
  },
  "body": {
    "GST_number__c": "((current.gstid))",
    "Constitution__c": "((gstJoin.constitutionOfBusiness))"
  }
}
```

#### Example C: Case/space normalization in keys
If `current.gstid` is `" 29ABCDE1234F1Z5 "` and lookup key is `"29abcde1234f1z5"`, they match.

#### Example D: Duplicate lookup keys
If lookup source has duplicate `gstin`, first item wins; later duplicates are ignored.

### Example Service Response
```json
{
  "PFAPRCalculation": {
    "response": {
      "body": {
        "offers": [
          {"amount": 10000, "tenor": 6, "apr": 12.5},
          {"amount": 15000, "tenor": 12, "apr": 11.8},
          {"amount": 20000, "tenor": 18, "apr": 11.2}
        ]
      }
    }
  }
}
```

This creates 3 Salesforce records with auto-generated reference IDs:
- `PFAPRCalculation_Offer__c_POST_0` (first offer)
- `PFAPRCalculation_Offer__c_POST_1` (second offer)  
- `PFAPRCalculation_Offer__c_POST_2` (third offer)

**Note**: The system automatically appends `_0`, `_1`, `_2` etc. to the original `referenceId` for each array item.

## Merge Logic

### When to Use Merge
Use `"merge": "true"` when you want to combine multiple requests into a single Salesforce record.

### Merge Behavior

#### Same Reference ID Merging
```json
[
  {
    "referenceId": "Contact_Update_PATCH",
    "merge": "true",
    "body": {"Phone": "123-456-7890"}
  },
  {
    "referenceId": "Contact_Update_PATCH", 
    "merge": "true",
    "body": {"Email": "john@example.com"}
  }
]
```
Result: Single request with `{"Phone": "123-456-7890", "Email": "john@example.com"}`

#### Array Expansion Merging
```json
{
  "referenceId": "Service_Object_POST",
  "arrayPath": "service.response.array",
  "merge": "true",
  "body": {
    "Field1__c": "((current.field1))",
    "Field2__c": "((current.field2))"
  }
}
```
All expanded items merge into: `Service_Object_POST_0_merged`

#### Cross-Reference ID Merging
Different reference IDs with same URL, method, and `merge: true`:
```json
[
  {
    "url": "/services/data/v64.0/sobjects/Contact",
    "method": "PATCH",
    "referenceId": "Service1_Contact_Update",
    "merge": "true",
    "body": {"Phone": "123-456-7890"}
  },
  {
    "url": "/services/data/v64.0/sobjects/Contact",
    "method": "PATCH", 
    "referenceId": "Service2_Contact_Update",
    "merge": "true",
    "body": {"Email": "john@example.com"}
  }
]
```
Result: Single merged request

### Merge Priority
1. **Same base reference ID** (highest priority)
2. **Same URL + Method + merge=true** (cross-reference merging)
3. **Array expansion items** with same base reference

## Service Sequence Configuration

### Partner Service Mapping
```json
{
  "partnerId": "partner123",
  "programName": "Personal Loan",
  "serviceSequenceString": "{1,2,3;4,5;6}",
  "services": [
    {
      "sequenceId": 1,
      "serviceName": "CreditBureau",
      "isActive": true
    },
    {
      "sequenceId": 2, 
      "serviceName": "FraudDetection",
      "isActive": true
    }
  ]
}
```

### Sequence String Format
- `{1,2,3;4,5;6}` - Groups: [1,2,3], [4,5], [6]
- Services in same group run in parallel
- Groups run sequentially

## Complete Examples

### Example 1: Simple Contact Update
```json
{
  "url": "/services/data/v64.0/sobjects/Contact/<contact.Id>",
  "method": "PATCH",
  "referenceId": "CreditService_Contact_Update",
  "body": {
    "Credit_Score__c": "((CreditService.response.body.score))",
    "Risk_Category__c": "((CreditService.response.body.riskLevel))",
    "Last_Updated__c": "{{formatDate(((CreditService.response.timestamp)), 'YYYY-MM-DD')}}"
  },
  "merge": "false"
}
```

### Example 2: Array Expansion with Merge
```json
{
  "url": "/services/data/v64.0/sobjects/Offer__c",
  "method": "POST", 
  "referenceId": "LoanCalculator_Offers_POST",
  "arrayPath": "LoanCalculator.response.body.offers",
  "body": {
    "Lead__c": "<lead.Id>",
    "Type__c": "Loan Offer",
    "Amount__c": "((current.principal))",
    "Interest_Rate__c": "((current.rate))",
    "Tenure_Months__c": "((current.tenure))",
    "Monthly_EMI__c": "((current.emi))",
    "Processing_Fee__c": "{{current.principal * 0.02}}",
    "Total_Interest__c": "{{(current.emi * current.tenure) - current.principal}}"
  },
  "merge": "true"
}
```

### Example 3: Complex Fraud Detection
```json
{
  "url": "/services/data/v64.0/sobjects/Fraud_Details__c",
  "method": "POST",
  "referenceId": "FraudService_Details_POST", 
  "body": {
    "Contact__c": "<contact.Id>",
    "Lead__c": "<lead.Id>",
    "Risk_Score__c": "((FraudService.response.body.riskScore))",
    "Risk_Level__c": "((FraudService.response.body.riskLevel))",
    "Fraud_Indicators__c": "{{arrayToString(((FraudService.response.body.indicators)), '; ')}}",
    "Model_Version__c": "((FraudService.response.body.modelVersion))",
    "Raw_Response__c": "{{serializeJson(((FraudService.response.body)))}}",
    "Decision__c": "{{FraudService_response_body_riskScore > 0.7 ? 'HIGH_RISK' : 'LOW_RISK'}}",
    "Processed_Date__c": "{{formatDate(now(), 'YYYY-MM-DD HH:mm:ss')}}"
  },
  "merge": "false"
}
```

### Example 4: Multi-Service Audit Log
```json
{
  "url": "/services/data/v64.0/sobjects/Audit_Log__c",
  "method": "POST",
  "referenceId": "MultiService_Audit_POST",
  "body": {
    "Lead__c": "<lead.Id>",
    "Service_Name__c": "{{concat(((CreditService.serviceName)), ' + ', ((FraudService.serviceName)))}}",
    "Start_Time__c": "((CreditService.startTime))",
    "End_Time__c": "((FraudService.endTime))",
    "Total_Duration__c": "{{FraudService_endTime - CreditService_startTime}}",
    "Credit_Response__c": "{{serializeJson(((CreditService.response)))}}",
    "Fraud_Response__c": "{{serializeJson(((FraudService.response)))}}",
    "Status__c": "{{CreditService_response_statusCode == 200 && FraudService_response_statusCode == 200 ? 'SUCCESS' : 'FAILED'}}"
  },
  "merge": "false"
}
```

## Best Practices

### 1. Reference ID Naming Conventions

#### Standard Format
**Pattern**: `{ServiceName}_{Object}_{Method}`

**Examples**:
- `CreditBureau_Contact_PATCH`
- `FraudDetection_Fraud_Details__c_POST`
- `LoanCalculator_Offer__c_POST`
- `ActicoPreBureau_Audit_Log__c_POST`

#### Array Expansion Reference IDs
When using `arrayPath`, the system automatically generates reference IDs by appending array indices:

**Original**: `PFAPRCalculation_Offer__c_POST`
**Generated**:
- `PFAPRCalculation_Offer__c_POST_0` (first array item)
- `PFAPRCalculation_Offer__c_POST_1` (second array item)  
- `PFAPRCalculation_Offer__c_POST_2` (third array item)
- ... and so on

#### Merged Reference IDs
When merge logic combines requests, reference IDs get `_merged` suffix:

**Single Item Merge**: `ServiceName_Object_Method_merged`
**Array Expansion Merge**: `ServiceName_Object_Method_0_merged` (uses first item's ID as base)


### 2. Field Mapping Guidelines
- Use static values for constant fields
- Use dot notation for simple field access
- Use expressions for calculations and transformations
- Always include required Salesforce fields

### 3. Error Handling
- Provide fallback values for optional fields
- Use conditional expressions for dynamic logic
- Test expressions with sample data

### 4. Performance Optimization
- Use merge strategically to reduce API calls
- Group related updates in single requests
- Avoid unnecessary array expansions
- Prefer `lookup` for cross-array matching instead of nested/manual matching patterns

### 5. Merge Strategy
```json
{
  "merge": "true",  // Use for combining related data
  "merge": "false"  // Use for independent records
}
```

## Runtime and S3 Configuration

Use these runtime keys in config/properties files:

```properties
# Shutdown endpoint
server.shutdown.test_endpoint.enabled=true
server.shutdown.test_endpoint.delay_ms=200

# Telemetry
telemetry.enabled=true
telemetry.interval_seconds=60
telemetry.memstats.enabled=true
telemetry.cache_sample.enabled=false
telemetry.cache_sample.interval_seconds=300
telemetry.cache_sample.max_entries=10
```

S3 keys required for `uploadToS3(...)`:

```properties
# No region required in config; pass region in expression argument
# Example: uploadToS3(..., 'example-bucket', 'ap-south-1')
```

Credentials are resolved via AWS default provider chain (for example IRSA/service-account role in Kubernetes).
Do not configure static `aws.accessKey` / `aws.secretKey` for this flow.

Notes:
- URL contains region once in host (`bucket.s3.<region>.amazonaws.com`).
- Returned URL format is public style, but actual browser visibility depends on S3 read policy.

## Troubleshooting

### Common Issues

#### 1. Expression Parsing Errors
**Error**: `Cannot transition token types from VARIABLE [Service_response_body_Data] to VARIABLE [Dictionary]`

**Solution**: Fields with spaces are auto-converted
```json
// This works automatically
"Data_Field__c": "{{serializeJson(((Service.response.body.Data Dictionary)))}}"
```

#### 2. Missing Field Values
**Error**: Field not populated in Salesforce

**Solutions**:
- Check dot path syntax: `((Service.response.body.field))`
- Verify service response structure
- Use debug logging to trace values

#### 3. Array Expansion Issues
**Error**: Array not expanding correctly

**Solutions**:
- Verify `arrayPath` points to actual array
- Check array structure in service response
- Use `((current.field))` for array item access

#### 5. Lookup Join Issues
**Error**: Joined fields like `((gstJoin.someField))` are empty

**Solutions**:
- Verify `lookup.fromArrayPath` points to an array
- Verify `lookup.currentKeyPath` and `lookup.lookupKeyPath` point to existing fields
- Ensure `lookup.as` is a valid alias (no spaces, dots, brackets)
- Confirm keys match after trim/case normalization
- Check for duplicate keys in lookup source (first match is used)

#### 4. Merge Not Working
**Error**: Expected merge not happening

**Solutions**:
- Ensure `"merge": "true"` is set
- Check URL and method match exactly
- Verify reference ID patterns for array expansion

#### 6. Preprocessing Issues
Preprocessing failures never abort the request — they are logged at `Error` level and the failing rule's `storeAs` is nil-stored so downstream `((_pp*))` lookups resolve to empty. To diagnose, search request logs by `storeAs`.

**Log**: `preProcessing: skipping rule with invalid storeAs ...`

**Solution**: Rename `storeAs` so it begins with `_pp` AND has at least one character after (e.g. `_ppCrifRaw`). The `_pp` namespace is reserved so the interpolation engine can skip recursive flattening of preprocessed trees into the govaluate param map. The bare `_pp` is rejected.

**Log**: `preProcessing rule unresolved (cycle or missing _pp* dependency); storing nil and continuing` with `missingPpKeys=[...]`

**Solutions**:
- Verify every `_pp*` key in `missingPpKeys` is declared by some rule in the same request batch (the producing rule's `storeAs` must match the reference exactly, including case).
- Check for accidental cycles: rule A's `expr` references `_ppB` while rule B's `expr` references `_ppA`.
- If a sibling sub-request was supposed to declare the producer, ensure that sub-request is actually loaded for the current ESA response (i.e. its parent `service_name` row was matched).

**Log**: `preProcessing rule failed; storing nil and continuing` with the underlying `error` field

**Solutions**:
- Read the `error` field; it carries the original govaluate / custom-function error message (parse error, `stringToJson: invalid JSON`, etc.).
- Confirm the source value is what you expect. For mixed JSON+HTML payloads use `stringToJson(jsonPart(...))`; `stringToJson` alone also tolerates trailing content via its streaming decoder.
- Validate the expression syntax in isolation by trying it in a body field first.

**Error**: `arrayPath` resolves to nil even though the source field looks correct.

**Solutions**:
- Confirm preprocessing actually ran. Look in logs for the `preprocessing_done` checkpoint and `preProcessing applied` debug entries.
- If the source is a stringified JSON field, you typically need a rule like `{"expr":"{{stringToJson(jsonPart(((Service.response.body.raw_response))))}}","storeAs":"_ppX"}` and then `"arrayPath":"_ppX.path.to.array"`. Pointing `arrayPath` at the raw stringified field will always fail.

#### 6. S3 Upload / Visibility Issues
**Error**: Uploaded URL returns `AccessDenied` or not reachable

**Solutions**:
- Ensure write permission for service credentials (`s3:PutObject`) on target bucket/prefix
- Ensure bucket region and configured region match
- For browser-openable links, enable public read policy/ACL or use pre-signed URL strategy
- If your infra has egress allowlist, allow S3 endpoint (`*.amazonaws.com` or specific regional host)

### Debug Logging
Enable debug logging to trace:
- Expression evaluation
- Field resolution
- Merge decisions
- Array expansion
- Lookup index build and join matching

### Validation Checklist
- [ ] All required Salesforce fields mapped
- [ ] Expression syntax correct
- [ ] Array paths valid
- [ ] Lookup paths/keys valid (if lookup is used)
- [ ] Reference IDs unique
- [ ] Merge logic appropriate
- [ ] URL endpoints correct
- [ ] HTTP methods valid

## Advanced Features

### 1. Conditional Logic
```json
{
  "Status__c": "{{score > 700 ? 'APPROVED' : 'REJECTED'}}",
  "Risk_Level__c": "{{score > 800 ? 'LOW' : score > 600 ? 'MEDIUM' : 'HIGH'}}"
}
```

### 2. Date Formatting
```json
{
  "Created_Date__c": "{{formatDate(((service.timestamp)), 'YYYY-MM-DD')}}",
  "Updated_Time__c": "{{formatDate(now(), 'YYYY-MM-DD HH:mm:ss')}}"
}
```

### 3. Array Processing
```json
{
  "Item_Count__c": "{{arrayLength(((service.response.items)))}}",
  "First_Item__c": "{{arrayGet(((service.response.items)), 0)}}",
  "Items_List__c": "{{arrayToString(((service.response.items)), ', ')}}"
}
```

### 4. Nested Object Access
```json
{
  "Deep_Field__c": "((Service.response.body.level1.level2.targetField))",
  "Array_Item__c": "((Service.response.body.items[0].name))"
}
```

This comprehensive guide covers all aspects of Decision Manager database configuration. Use it as a reference for implementing and troubleshooting your configurations.
