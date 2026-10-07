package utility

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	commoninit "decision-manager/internal/app/init"

	"github.com/dmi-infotech/common-modules/go/contracts"
)

// Global token cache with mutex for thread safety
var (
	tokenCache = struct {
		sync.RWMutex
		token  string
		expiry time.Time
	}{}
	// Reuse auth HTTP client/transport across calls for connection pooling.
	sfExceptionAuthHTTPClient = &http.Client{
		Timeout: 15 * time.Second, // Reduced timeout for faster failure
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	// Object pools for memory optimization
	stringBuilderPool = sync.Pool{
		New: func() interface{} {
			return &strings.Builder{}
		},
	}
)

type SFExceptionLogger struct {
	apiClient contracts.APIClient
}

type ExceptionLogRequest struct {
	ClassName   string `json:"Class_Name__c"`
	MethodName  string `json:"Method_Name__c"`
	ContactID   string `json:"Contact__c"`
	Object      string `json:"Object__c"`
	RecordID    string `json:"Record_Id__c"`
	Description string `json:"Description__c"`
	LeadID      string `json:"Lead__c"`
	LogTime     string `json:"Log_Time__c"`
}

const (
	sfExceptionClassError   = "decision_manager_trigger_decision_service_error"
	sfExceptionClassWarning = "decision_manager_trigger_decision_service_warning"
)

func NewSFExceptionLogger() *SFExceptionLogger {
	return &SFExceptionLogger{
		apiClient: commoninit.GetAPIClient(),
	}
}

// LogSFExceptions creates exception logs for all graph request errors with optimized performance.
// It now creates two consolidated logs:
//   - one for real errors (failed sub-requests, callback failures, service issues, etc.)
//   - one for warnings (requests that were intentionally not sent to Salesforce, e.g. empty body/object resolved to null)
func (logger *SFExceptionLogger) LogSFExceptions(ctx context.Context, valueJson map[string]interface{}, sfCompositeResponse map[string]map[string]interface{}) error {
	log := commoninit.GetLogger(ctx)

	// Early return if no composite response
	if len(sfCompositeResponse) == 0 {
		return nil
	}

	// Extract lead and contact IDs from valueJson (optimized)
	leadID, contactID := logger.extractLeadAndContactOptimized(valueJson)
	log.Infow("Extracted IDs from transformed valueJson", "leadID", leadID, "contactID", contactID)

	// Split composite response into error and warning buckets.
	errorResponses, warningResponses := logger.splitErrorsAndWarnings(sfCompositeResponse)

	if len(errorResponses) == 0 && len(warningResponses) == 0 {
		log.Info("No errors or warnings found in SF composite response, skipping exception logging")
		return nil
	}

	// Get cached Salesforce access token
	accessToken, err := logger.getCachedSalesforceAccessToken(ctx)
	if err != nil {
		log.Errorw("Failed to get Salesforce access token for exception logging", "error", err)
		return err
	}

	var (
		firstErr error
		wg       sync.WaitGroup
		mu       sync.Mutex
	)

	captureErr := func(e error) {
		if e == nil {
			return
		}
		mu.Lock()
		if firstErr == nil {
			firstErr = e
		}
		mu.Unlock()
	}

	// Create consolidated exception log for real errors (if any).
	if len(errorResponses) > 0 {
		wg.Add(1)
		commoninit.StartTrackedGoroutine(func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					captureErr(fmt.Errorf("panic in error exception log goroutine: %v", r))
					log.Errorw("Recovered panic while logging Salesforce error exceptions",
						"panic", r,
						"stackTrace", string(debug.Stack()))
				}
			}()
			err := logger.createSingleConsolidatedExceptionLogFromRawResponse(
				ctx,
				accessToken,
				leadID,
				contactID,
				errorResponses,
				"Salesforce Composite Response Errors - All SF errors mapped to reference ID:",
				sfExceptionClassError,
			)
			captureErr(err)
		})
	}

	// Create consolidated exception log for warnings (if any).
	if len(warningResponses) > 0 {
		wg.Add(1)
		commoninit.StartTrackedGoroutine(func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					captureErr(fmt.Errorf("panic in warning exception log goroutine: %v", r))
					log.Errorw("Recovered panic while logging Salesforce warning exceptions",
						"panic", r,
						"stackTrace", string(debug.Stack()))
				}
			}()
			err := logger.createSingleConsolidatedExceptionLogFromRawResponse(
				ctx,
				accessToken,
				leadID,
				contactID,
				warningResponses,
				"Salesforce Composite Response Warnings - Requests not sent to Salesforce mapped to reference ID:",
				sfExceptionClassWarning,
			)
			captureErr(err)
		})
	}

	// Wait with a deadline so a hung Salesforce call cannot block the caller
	// indefinitely. Use the request context deadline if set; otherwise cap at 30s.
	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()

	timeout := 30 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if remaining := time.Until(dl); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-waitDone:
		if !timer.Stop() {
			<-timer.C
		}
	case <-timer.C:
		log.Errorw("Exception logging goroutines did not finish within timeout", "timeoutSeconds", timeout.Seconds())
	}

	return firstErr
}

// extractLeadAndContactOptimized - optimized version with direct map access
// Note: valueJson is the transformed ESA response where data is flattened to root level
func (logger *SFExceptionLogger) extractLeadAndContactOptimized(valueJson map[string]interface{}) (string, string) {
	leadID := ""
	contactID := ""

	// After TransformESAResponse, lead and contact are at root level (not under "data")
	if lead, ok := valueJson["lead"].(map[string]interface{}); ok {
		if id, ok := lead["Id"].(string); ok {
			leadID = id
		}
	}

	if contact, ok := valueJson["contact"].(map[string]interface{}); ok {
		if id, ok := contact["Id"].(string); ok {
			contactID = id
		}
	}

	return leadID, contactID
}

// BuildConsolidatedDescriptionFromRawResponse builds description directly from sfCompositeResponse
// using the provided header as the first line.
func (logger *SFExceptionLogger) BuildConsolidatedDescriptionFromRawResponse(sfCompositeResponse map[string]map[string]interface{}, header string) string {
	// Use string builder for efficient concatenation
	sb := stringBuilderPool.Get().(*strings.Builder)
	defer func() {
		sb.Reset()
		stringBuilderPool.Put(sb)
	}()

	sb.WriteString(header)
	sb.WriteString("\n")

	// Dump all errors from sfCompositeResponse as raw JSON mapped to reference IDs
	for refID, response := range sfCompositeResponse {
		// Check if this is an error (success = false or nil)
		if success, ok := response["success"]; ok {
			if successBool, isBool := success.(bool); (isBool && !successBool) || success == nil {
				sb.WriteString("Reference ID: ")
				sb.WriteString(refID)
				sb.WriteString(" | Response: ")

				// Dump the raw response as JSON
				if responseBytes, err := json.Marshal(response); err == nil {
					sb.Write(responseBytes)
				} else {
					sb.WriteString("Unable to serialize response")
				}
				sb.WriteString("\n")
			}
		}
	}

	return sb.String()
}

// submitExceptionLogRequest posts a single Exception_Log__c record using the given access token.
func (logger *SFExceptionLogger) submitExceptionLogRequest(ctx context.Context, accessToken string, exceptionLog ExceptionLogRequest) error {
	log := commoninit.GetLogger(ctx)

	sfBaseURL := commoninit.GetConfigString("salesforce.baseURL")
	apiVersion := commoninit.GetConfigString("salesforce.apiVersion")
	if apiVersion == "" {
		apiVersion = "v58.0"
	}
	exceptionURL := fmt.Sprintf("%s/services/data/%s/sobjects/Exception_Log__c", sfBaseURL, apiVersion)

	httpRequest, err := logger.apiClient.CreateJSONRequest(ctx, http.MethodPost, exceptionURL, accessToken, exceptionLog)
	if err != nil {
		log.Errorw("Failed to create exception log HTTP request", "error", err)
		return err
	}
	log.Infow("Creating exception log request",
		"url", httpRequest.URL.String(),
		"method", httpRequest.Method,
		"headers", httpRequest.Header,
		"exceptionLog", exceptionLog)

	statusCode, responseBody, err := logger.apiClient.RestExecute(ctx, httpRequest)
	if err != nil {
		log.Errorw("Failed to execute exception log request", "error", err, "statusCode", statusCode)
		if statusCode == http.StatusUnauthorized {
			invalidateTokenCache()
		}
		return err
	}

	if statusCode == http.StatusUnauthorized {
		invalidateTokenCache()
		log.Warnw("Token invalidated due to 401 from exception log API", "statusCode", statusCode)
		return fmt.Errorf("exception log request returned 401: token invalidated for retry on next call")
	}

	if statusCode == http.StatusCreated || statusCode == http.StatusOK {
		log.Infow("Exception log created successfully",
			"statusCode", statusCode,
			"leadId", exceptionLog.LeadID)
		return nil
	}

	log.Errorw("Exception log creation failed",
		"statusCode", statusCode,
		"response", responseBody)
	return fmt.Errorf("exception log creation failed with status code %d: %s", statusCode, responseBody)
}

func buildESAProcessSequenceTimeoutDescription(responseBody string) string {
	header := "External Service Adapter process-sequence exceeded maximum processing time (HTTP 504).\nESA response body:\n"

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(responseBody), &parsed); err != nil {
		return header + responseBody
	}
	pretty, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return header + responseBody
	}
	return header + string(pretty)
}

// LogESAProcessSequenceTimeout writes one Exception_Log__c for ESA HTTP 504 (process-sequence deadline).
// Lead__c and Record_Id__c use the trigger-request Lead ID; Object__c is Lead; Contact__c is empty.
func (logger *SFExceptionLogger) LogESAProcessSequenceTimeout(ctx context.Context, leadID string, esaResponseBody string) error {
	if strings.TrimSpace(leadID) == "" {
		return fmt.Errorf("LogESAProcessSequenceTimeout: leadID is required")
	}
	accessToken, err := logger.getCachedSalesforceAccessToken(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	exceptionLog := ExceptionLogRequest{
		ClassName:   sfExceptionClassError,
		MethodName:  "decision_manager_sf_exception_logger",
		ContactID:   "",
		Object:      "Lead",
		RecordID:    leadID,
		Description: buildESAProcessSequenceTimeoutDescription(esaResponseBody),
		LeadID:      leadID,
		LogTime:     now.Format("2006-01-02T15:04:05.000+0000"),
	}
	return logger.submitExceptionLogRequest(ctx, accessToken, exceptionLog)
}

// splitErrorsAndWarnings walks the composite response and classifies entries into:
//   - errors: real Salesforce/composite failures (success == false or other non-warning states)
//   - warnings: requests that were not sent to Salesforce, such as empty-body requests
func (logger *SFExceptionLogger) splitErrorsAndWarnings(sfCompositeResponse map[string]map[string]interface{}) (map[string]map[string]interface{}, map[string]map[string]interface{}) {
	errors := make(map[string]map[string]interface{})
	warnings := make(map[string]map[string]interface{})

	for refID, response := range sfCompositeResponse {
		success, hasSuccess := response["success"]
		if !hasSuccess {
			// If success flag is missing, treat as error to be safe.
			errors[refID] = response
			continue
		}

		// Explicit success → neither error nor warning.
		if successBool, isBool := success.(bool); isBool && successBool {
			continue
		}

		// At this point, success is either false or nil.
		// Detect known warning cases where the request was intentionally not sent to Salesforce.
		if success == nil {
			message, _ := response["message"].(string)
			description, _ := response["description"].(string)

			// These values are set in UpsertSalesforceObjects for empty-body requests.
			isEmptyBodyMessage := strings.EqualFold(message, "Empty Body")
			isEmptyBodyDescription := strings.Contains(strings.ToLower(description), "request had empty body") &&
				strings.Contains(strings.ToLower(description), "not sent to salesforce")

			if isEmptyBodyMessage || isEmptyBodyDescription {
				warnings[refID] = response
				continue
			}
		}

		// All remaining non-success cases are treated as errors.
		errors[refID] = response
	}

	return errors, warnings
}

// createSingleConsolidatedExceptionLogFromRawResponse creates one API call with raw sfCompositeResponse data
// and uses the provided header and class name when building the request.
func (logger *SFExceptionLogger) createSingleConsolidatedExceptionLogFromRawResponse(ctx context.Context, accessToken, leadID, contactID string, sfCompositeResponse map[string]map[string]interface{}, header string, className string) error {
	// Get the first error's reference ID as the Object field (as specified in requirements)
	var firstRefID string
	for refID, response := range sfCompositeResponse {
		if success, ok := response["success"]; ok {
			if successBool, isBool := success.(bool); (isBool && !successBool) || success == nil {
				firstRefID = refID
				break
			}
		}
	}

	// Build consolidated description directly from raw sfCompositeResponse
	description := logger.BuildConsolidatedDescriptionFromRawResponse(sfCompositeResponse, header)

	// Create single exception log request
	now := time.Now().UTC()
	exceptionLog := ExceptionLogRequest{
		ClassName:   className,
		MethodName:  "decision_manager_sf_exception_logger",
		ContactID:   contactID,
		Object:      firstRefID,  // Reference ID of first error as specified
		RecordID:    leadID,      // Same as Lead ID as specified
		Description: description, // All SF errors from sfCompositeResponse mapped to reference IDs
		LeadID:      leadID,
		LogTime:     now.Format("2006-01-02T15:04:05.000+0000"),
	}

	return logger.submitExceptionLogRequest(ctx, accessToken, exceptionLog)
}

// CallDecisionCallbackApex invokes the Apex REST callback API and returns status and response body
func (logger *SFExceptionLogger) CallDecisionCallbackApex(ctx context.Context, leadID string, callbackType string) (int, string, error) {
	log := commoninit.GetLogger(ctx)

	// Acquire Salesforce access token (cached)
	accessToken, err := logger.getCachedSalesforceAccessToken(ctx)
	if err != nil {
		log.Errorw("Failed to get Salesforce access token for Apex callback", "error", err)
		return 0, "", err
	}

	// Build Apex REST URL
	sfBaseURL := commoninit.GetConfigString("salesforce.baseURL")
	// Ensure no trailing slash
	sfBaseURL = strings.TrimSuffix(sfBaseURL, "/")
	apexURL := sfBaseURL + "/services/apexrest/decisioncallbackapi"

	// Prepare payload
	payload := struct {
		LeadID       string `json:"leadId"`
		CallbackType string `json:"callbackType"`
	}{
		LeadID:       leadID,
		CallbackType: callbackType,
	}

	// Create and execute request
	httpRequest, err := logger.apiClient.CreateJSONRequest(ctx, http.MethodPost, apexURL, accessToken, payload)
	if err != nil {
		log.Errorw("Failed to create Apex callback HTTP request", "error", err)
		return 0, "", err
	}

	statusCode, responseBody, execErr := logger.apiClient.RestExecute(ctx, httpRequest)
	if execErr != nil {
		log.Errorw("Failed to execute Apex callback HTTP request", "error", execErr, "statusCode", statusCode)
	}
	if statusCode == http.StatusUnauthorized {
		invalidateTokenCache()
		log.Warnw("Token invalidated due to 401 from Apex callback", "statusCode", statusCode)
	}

	return statusCode, responseBody, execErr
}

// invalidateTokenCache clears the cached token so the next call re-authenticates.
// Should be called when a 401 is received from Salesforce.
func invalidateTokenCache() {
	tokenCache.Lock()
	tokenCache.token = ""
	tokenCache.expiry = time.Time{}
	tokenCache.Unlock()
}

// getCachedSalesforceAccessToken gets access token with caching to avoid repeated auth calls
func (logger *SFExceptionLogger) getCachedSalesforceAccessToken(ctx context.Context) (string, error) {
	// Check cache first (read lock)
	tokenCache.RLock()
	if tokenCache.token != "" && time.Now().Before(tokenCache.expiry) {
		token := tokenCache.token
		tokenCache.RUnlock()
		return token, nil
	}
	tokenCache.RUnlock()

	// Need to refresh token (write lock)
	tokenCache.Lock()
	defer tokenCache.Unlock()

	// Double-check after acquiring write lock (another goroutine might have updated it)
	if tokenCache.token != "" && time.Now().Before(tokenCache.expiry) {
		return tokenCache.token, nil
	}

	// Get new token
	token, err := logger.requestSalesforceTokenOptimized(ctx)
	if err != nil {
		return "", err
	}

	// Cache token with 50-minute expiry (10 minutes before actual expiry)
	tokenCache.token = token
	tokenCache.expiry = time.Now().Add(50 * time.Minute)

	return token, nil
}

// requestSalesforceTokenOptimized - optimized version with connection reuse and reduced allocations
func (logger *SFExceptionLogger) requestSalesforceTokenOptimized(ctx context.Context) (string, error) {
	log := commoninit.GetLogger(ctx)

	// Get Salesforce configuration (cache these if called frequently)
	sfLoginURL := commoninit.GetConfigString("salesforce.loginURL")
	sfBaseURL := commoninit.GetConfigString("salesforce.baseURL")

	// Construct login URL if not specified
	if sfLoginURL == "" {
		if sfBaseURL == "" {
			sfLoginURL = "https://login.salesforce.com/services/oauth2/token"
		} else {
			sfBaseURL = strings.TrimSuffix(sfBaseURL, "/")
			sfLoginURL = sfBaseURL + "/services/oauth2/token"
		}
	}

	// Double-check that the loginURL ends with the correct path
	if !strings.HasSuffix(sfLoginURL, "/services/oauth2/token") {
		log.Warnw("Login URL doesn't end with /services/oauth2/token, might not be correct",
			"configuredLoginURL", sfLoginURL)
		if !strings.Contains(sfLoginURL, "/services/oauth2/") {
			// The URL doesn't even have the OAuth2 path, so we'll append it
			if strings.HasSuffix(sfLoginURL, "/") {
				sfLoginURL = sfLoginURL + "services/oauth2/token"
			} else {
				sfLoginURL = sfLoginURL + "/services/oauth2/token"
			}
			log.Info("Adjusted login URL to include OAuth path adjustedLoginURL: ", sfLoginURL)
		}
	}

	// // Ensure proper OAuth path
	// if !strings.HasSuffix(sfLoginURL, "/services/oauth2/token") {
	// 	if !strings.Contains(sfLoginURL, "/services/oauth2/") {
	// 		if strings.HasSuffix(sfLoginURL, "/") {
	// 			sfLoginURL = sfLoginURL + "services/oauth2/token"
	// 		} else {
	// 			sfLoginURL = sfLoginURL + "/services/oauth2/token"
	// 		}
	// 	}
	// }

	// Get credentials
	username := commoninit.GetConfigString("salesforce.username")
	password := commoninit.GetConfigString("salesforce.password")
	securityToken := commoninit.GetConfigString("salesforce.securityToken")
	clientID := commoninit.GetConfigString("salesforce.clientID")
	clientSecret := commoninit.GetConfigString("salesforce.clientSecret")

	if username == "" || password == "" || clientID == "" || clientSecret == "" {
		return "", fmt.Errorf("missing required Salesforce credentials for exception logging")
	}

	formData := url.Values{
		"grant_type":    {"password"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"username":      {username},
		"password":      {password + securityToken},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sfLoginURL, strings.NewReader(formData.Encode()))
	if err != nil {
		log.Errorw("Failed to create auth request for exception logging", "error", err)
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := sfExceptionAuthHTTPClient.Do(req)
	if err != nil {
		log.Errorw("Failed to execute auth request for exception logging", "error", err)
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Errorw("Auth request failed for exception logging", "statusCode", resp.StatusCode)
		return "", fmt.Errorf("auth request failed with status code %d", resp.StatusCode)
	}

	// Parse response directly without intermediate allocation
	var authResponse struct {
		AccessToken string `json:"access_token"`
		InstanceURL string `json:"instance_url"`
		TokenType   string `json:"token_type"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&authResponse); err != nil {
		log.Errorw("Failed to parse auth response for exception logging", "error", err)
		return "", err
	}

	log.Info("Successfully obtained Salesforce access token for exception logging")
	return authResponse.AccessToken, nil
}
