package types

// SFRequest represents the final output struct for Salesforce requests
// NOTE: This struct should NOT include the Merge field - it's only for internal processing
type SFRequest struct {
	URL    string                 `json:"url" bson:"url"`
	Method string                 `json:"method" bson:"method"`
	RefID  string                 `json:"referenceId" bson:"referenceId"`
	Body   map[string]interface{} `json:"body" bson:"body"`
}

// SFRequestDB represents the database/input struct - keeps additional fields for processing
type SFRequestDB struct {
	URL              string                 `json:"url" bson:"url"`
	Method           string                 `json:"method" bson:"method"`
	RefID            string                 `json:"referenceId" bson:"referenceId"`
	Body             map[string]interface{} `json:"body" bson:"body"`
	ArrayPath        string                 `json:"arrayPath" bson:"arrayPath"`
	Lookup           *LookupConfig          `json:"lookup,omitempty" bson:"lookup,omitempty"`
	Merge            string                 `json:"merge,omitempty" bson:"merge,omitempty"`
	FilterConditions *FilterConditions      `json:"filterConditions,omitempty" bson:"filterConditions,omitempty"`
	PreProcessing    []PreprocessingConfig  `json:"preProcessing,omitempty" bson:"preProcessing,omitempty"`
}

// PreprocessingConfig defines one transformation applied to valueJson before
// any arrayPath resolution or body interpolation. The Expr is a govaluate
// expression (e.g. "{{stringToJson(jsonPart(((CRIF.response.body.raw_response))))}}"
// or a "((path))" alias) whose result is stored at the top of valueJson under
// StoreAs. StoreAs MUST start with the reserved "_pp" prefix so the param-map
// flattener can skip recursing into the (potentially large) parsed tree.
type PreprocessingConfig struct {
	Expr    string `json:"expr" bson:"expr"`
	StoreAs string `json:"storeAs" bson:"storeAs"`
}

// PreprocessingRuleStatus enumerates terminal outcomes for a single rule.
// Used inside PreprocessingRuleResult so mongo readers can filter by status
// (e.g. dashboards alerting on any non-"success" entries).
const (
	PreprocessingRuleStatusSuccess    = "success"
	PreprocessingRuleStatusFailed     = "failed"
	PreprocessingRuleStatusUnresolved = "unresolved"
	PreprocessingRuleStatusSkipped    = "skipped"
)

// PreprocessingRuleResult captures the outcome of one preprocessing rule for
// observability (mongo log / dashboards). The Value field is intentionally
// kept at the top of PreprocessingSummary.Values map (not here) to avoid
// duplicating potentially-large parsed trees per rule entry.
type PreprocessingRuleResult struct {
	StoreAs    string `bson:"storeAs" json:"storeAs"`
	Expr       string `bson:"expr,omitempty" json:"expr,omitempty"`
	Status     string `bson:"status" json:"status"`
	Pass       int    `bson:"pass,omitempty" json:"pass,omitempty"`
	DurationMs int64  `bson:"durationMs,omitempty" json:"durationMs,omitempty"`
	// Error is populated for "failed" / "unresolved" rules; empty otherwise.
	Error string `bson:"error,omitempty" json:"error,omitempty"`
	// MissingPpKeys is populated for "unresolved" rules.
	MissingPpKeys []string `bson:"missingPpKeys,omitempty" json:"missingPpKeys,omitempty"`
}

// PreprocessingSummary is the structured outcome of ApplyPreprocessings,
// persisted into the mongo decision-manager log so operators can replay /
// audit exactly what the preprocessing stage produced and how long it took
// without having to grep through application logs.
type PreprocessingSummary struct {
	DurationMs     int64                     `bson:"durationMs" json:"durationMs"`
	RuleCount      int                       `bson:"ruleCount" json:"ruleCount"`
	Succeeded      int                       `bson:"succeeded" json:"succeeded"`
	Failed         int                       `bson:"failed" json:"failed"`
	Unresolved     int                       `bson:"unresolved" json:"unresolved"`
	SkippedInvalid int                       `bson:"skippedInvalid" json:"skippedInvalid"`
	Duplicates     int                       `bson:"duplicates" json:"duplicates"`
	Passes         int                       `bson:"passes" json:"passes"`
	Cancelled      bool                      `bson:"cancelled,omitempty" json:"cancelled,omitempty"`
	Rules          []PreprocessingRuleResult `bson:"rules,omitempty" json:"rules,omitempty"`
	// ValuesStripped is set to true when the mongo size guard removed the
	// Values payload because the audit doc would otherwise exceed the BSON
	// 16 MiB limit. Counters and per-rule outcomes in Rules remain intact;
	// only the parsed "_pp*" trees are gone (re-derivable from
	// EsaResponseBody if a deep dive is ever needed).
	ValuesStripped bool `bson:"valuesStripped,omitempty" json:"valuesStripped,omitempty"`
	// Values is the storeAs -> produced value map (only includes rules that
	// were actually attempted; skipped-invalid rules are recorded in Rules
	// but not here). Sharing references with valueJson is safe -- downstream
	// readers do not mutate either side.
	Values map[string]interface{} `bson:"values,omitempty" json:"values,omitempty"`
}

// LookupConfig describes optional O(1) key-based joins during array expansion.
// When configured, the mapper indexes fromArrayPath by lookupKeyPath and exposes
// a matched record in the current item context under alias "as".
type LookupConfig struct {
	As             string `json:"as" bson:"as"`
	FromArrayPath  string `json:"fromArrayPath" bson:"fromArrayPath"`
	CurrentKeyPath string `json:"currentKeyPath" bson:"currentKeyPath"`
	LookupKeyPath  string `json:"lookupKeyPath" bson:"lookupKeyPath"`
}

// FilterConditions controls array element inclusion/exclusion during expansion.
type FilterConditions struct {
	SkipWhen   string `json:"skipWhen,omitempty" bson:"skipWhen,omitempty"`
	SelectWhen string `json:"selectWhen,omitempty" bson:"selectWhen,omitempty"`
}
