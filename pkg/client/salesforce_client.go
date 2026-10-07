package client

import (
	"context"
	"database/sql"
	"decision-manager/internal/app/db/repository"
	commoninit "decision-manager/internal/app/init"
	"decision-manager/internal/app/utility"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dmi-infotech/common-modules/go/contracts"
)

// Singleton variables for SalesforceClient
var (
	salesforceClient ISalesforceClient
	salesforceOnce   sync.Once
	// Reuse HTTP client to keep connections warm for auth calls.
	salesforceAuthHTTPClient = &http.Client{Timeout: 30 * time.Second}
)

// maxDifferentNodesPerGraph is the Salesforce Composite Graph limit: max distinct (objectType|method) per graph,
// and also the maximum total distinct node types allowed in a single composite/graph payload (across all graphs).
// See: https://developer.salesforce.com/docs/atlas.en-us.api_rest.meta/api_rest/resources_composite_graph_limits.htm
const maxDifferentNodesPerGraph = 15

// maxSubrequestsPerPayload is the Salesforce Composite Graph payload limit:
// total subrequests across all graphs in one payload must be <= 500.
const maxSubrequestsPerPayload = 500

// maxGraphsPerPayload is the Salesforce Composite Graph payload limit:
// maximum number of graphs in one payload is 75.
const maxGraphsPerPayload = 75

// Performance optimization: Pre-compiled regex and object pools
var (
	dependencyRegex = regexp.MustCompile(`@\{([^.}]+)`)
	// refReplacementRe matches full @{refId.field} for substituting with resolved id in multi-call
	refReplacementRe = regexp.MustCompile(`@\{([^}]+)\}`)
	stringPool       = sync.Pool{
		New: func() interface{} {
			return make([]string, 0, 8) // Pre-allocate for typical dependency count
		},
	}
	nodeMapPool = sync.Pool{
		New: func() interface{} {
			return make(map[string]*DependencyNode, 64) // Pre-allocate for typical request count
		},
	}
	visitedMapPool = sync.Pool{
		New: func() interface{} {
			return make(map[string]bool, 64)
		},
	}
	requestSlicePool = sync.Pool{
		New: func() interface{} {
			return make([]utility.SFRequest, 0, 64)
		},
	}
)

// SalesforceService manages the Salesforce client
type SalesforceService struct{}

// InitializeSalesforceClient initializes the Salesforce client
func (s *SalesforceService) InitializeSalesforceClient(db *sql.DB) {
	salesforceOnce.Do(func() {
		// Use the global repository that was initialized in main.go
		salesforceClient = NewSalesforceClient()
	})
}

// GetSalesforceClient returns the Salesforce client instance
func (s *SalesforceService) GetSalesforceClient() ISalesforceClient {
	// Ensure the client is initialized before returning it
	if salesforceClient == nil {
		log := commoninit.GetLogger()
		log.Warnw("Salesforce client was accessed before initialization, initializing now")
		s.InitializeSalesforceClient(nil)
	}
	return salesforceClient
}

type ISalesforceClient interface {
	UpsertSalesforceObjects(ctx context.Context, sfRequest []utility.SFRequest) (map[string]map[string]interface{}, error)
}

type SalesforceClient struct {
	apiClient         contracts.APIClient
	queryObjectRepo   repository.IQueryObjectRepository
	accessToken       string
	tokenExpiry       time.Time
	tokenRefreshMutex sync.Mutex
}

type SalesforceAuthResponse struct {
	AccessToken string `json:"access_token"`
	InstanceURL string `json:"instance_url"`
	TokenType   string `json:"token_type"`
	IssuedAt    string `json:"issued_at"`
	Signature   string `json:"signature"`
	ExpiresIn   int    `json:"expires_in"`
}

type UpsertCompositeRequest struct {
	AllOrNone          bool                `json:"allOrNone"`
	CollateSubrequests bool                `json:"collateSubrequests"`
	CompositeRequest   []utility.SFRequest `json:"compositeRequest"`
}

type GraphRequest struct {
	GraphId          string              `json:"graphId"`
	CompositeRequest []utility.SFRequest `json:"compositeRequest"`
}

type CompositeGraphRequest struct {
	Graphs []GraphRequest `json:"graphs"`
}

type GraphPayloadMetrics struct {
	GraphCount             int
	TotalRequests          int
	UniqueNodeTypes        int
	MaxRequestsInGraph     int
	GraphsAtRequestCap500  int
	GraphsOverRequestCap500 int
}

type DependencyNode struct {
	Request   utility.SFRequest
	Children  []*DependencyNode
	Parents   []*DependencyNode
	Processed bool
}

type DependencyChain struct {
	RootNodes []*DependencyNode
	AllNodes  []*DependencyNode
}

// chainRemainingSegments holds segments for round 2, 3, ... per chain that exceeded maxDifferentNodesPerGraph.
// chainRemainingSegments[chainIndex][segmentIndex] = requests for that segment (segmentIndex 0 = round 2, etc.)
type chainRemainingSegments [][][]utility.SFRequest

type NodeInfo struct {
	ObjectType string
	Method     string
}

type GraphCompositeResponse struct {
	Graphs []GraphResponse `json:"graphs"`
}

type GraphResponse struct {
	GraphId       string            `json:"graphId"`
	GraphResponse CompositeResponse `json:"graphResponse"`
	IsSuccessful  bool              `json:"isSuccessful"`
}

type CompositeResponse struct {
	CompositeResponse []SubResponse `json:"compositeResponse"`
}

type SubResponse struct {
	Body            interface{}       `json:"body"`
	HTTPHeaders     map[string]string `json:"httpHeaders"`
	HTTPStatusCode  int               `json:"httpStatusCode"`
	ReferenceID     string            `json:"referenceId"`
	ReferenceIDType string            `json:"referenceIdType"`
}

type SalesforceQueryResponse struct {
	Done           bool                     `json:"done"`
	Records        []map[string]interface{} `json:"records"`
	TotalSize      int                      `json:"totalSize"`
	NextRecordsURL string                   `json:"nextRecordsUrl,omitempty"`
}

// NewSalesforceClient creates a new Salesforce client instance
func NewSalesforceClient() *SalesforceClient {
	log := commoninit.GetLogger()

	// Log the Salesforce configuration (without sensitive values)
	log.Info("Initializing Salesforce client loginURL: ", commoninit.GetConfigString("salesforce.loginURL"), " baseURL: ", commoninit.GetConfigString("salesforce.baseURL"), " hasUsername: ", commoninit.GetConfigString("salesforce.username") != "", " hasPassword: ", commoninit.GetConfigString("salesforce.password") != "", " hasSecurityToken: ", commoninit.GetConfigString("salesforce.securityToken") != "", " hasClientID: ", commoninit.GetConfigString("salesforce.clientID") != "", " hasClientSecret: ", commoninit.GetConfigString("salesforce.clientSecret") != "")

	client := &SalesforceClient{
		apiClient: commoninit.GetAPIClient(),
		// Don't try to get the repository immediately - it might not be initialized yet
		// We'll get it lazily when needed
	}

	// Try to get an access token, but don't fail initialization if it doesn't work
	ctx := context.Background()
	_, err := client.getSalesforceAccessToken(ctx)
	if err != nil {
		log.Warnw("Failed to initialize Salesforce access token, will retry on first API call", "error", err)
	} else {
		log.Info("Successfully initialized Salesforce client with valid access token")
	}

	return client
}

func (c *SalesforceClient) UpsertSalesforceObjects(ctx context.Context, sfRequest []utility.SFRequest) (map[string]map[string]interface{}, error) {
	log := commoninit.GetLogger(ctx)

	accessToken, err := c.getSalesforceAccessToken(ctx)
	if err != nil {
		log.Errorw("Failed to get Salesforce access token", "error", err)
		return nil, err
	}

	log.Info("SF Access Token obtained successfully")
	result := make(map[string]map[string]interface{})

	compositeGraphRequest, remainingSegments, emptyBodyRequests, deferredGraphBatches, err := c.buildUpsertCompositeRequest(ctx, sfRequest)
	if err != nil {
		log.Errorw("Failed to build Salesforce composite graph request", "error", err)
		return nil, err
	}
	log.Info("SF compositeGraphRequest", "compositeGraphRequest", compositeGraphRequest.Graphs)

	if len(compositeGraphRequest.Graphs) == 0 {
		log.Info("All requests had empty bodies, skipping Salesforce API call")
		result := make(map[string]map[string]interface{}, len(emptyBodyRequests))
		for _, emptyRequest := range emptyBodyRequests {
			result[emptyRequest.RefID] = map[string]interface{}{
				"success":     nil,
				"errors":      []interface{}{},
				"message":     "Empty Body",
				"referenceId": emptyRequest.RefID,
				"statusCode":  nil,
				"description": "Request had empty body and was not sent to Salesforce",
				"callPhase":   "not_sent_empty_body",
				"callSequence": 0,
			}
		}
		return result, nil
	}

	compositeEndpoint := commoninit.GetConfigString("salesforce.compositeEndpoint")
	if compositeEndpoint == "" {
		compositeEndpoint = "/services/data/{apiVersion}/composite/graph"
	}
	apiVersion := commoninit.GetConfigString("salesforce.apiVersion")
	if apiVersion == "" {
		apiVersion = "v64.0"
	}
	compositeEndpoint = strings.Replace(compositeEndpoint, "{apiVersion}", apiVersion, -1)
	compositeURL := fmt.Sprintf("%s%s", commoninit.GetConfigString("salesforce.baseURL"), compositeEndpoint)

	refMap := make(map[string]string, 64)
	callSequence := 0

	// Round 1: execute first payload
	callSequence++
	logCompositePayloadMetrics(log, "round_1_initial", compositeGraphRequest)
	graphResponse, err := c.executeCompositeGraphRequest(ctx, compositeURL, accessToken, compositeGraphRequest)
	if err != nil {
		return nil, err
	}
	totalResponses := 0
	for _, g := range graphResponse.Graphs {
		totalResponses += len(g.GraphResponse.CompositeResponse)
	}
	result = make(map[string]map[string]interface{}, totalResponses+len(emptyBodyRequests)+64)
	mergeGraphResponseIntoResult(&graphResponse, result, refMap, "round_1_initial", callSequence)

	// Deferred payloads contain first-segment graphs that were pushed out only due to payload-level limits.
	// Execute these before round-2+ segments so refMap has all parent IDs for subsequent segment substitution.
	for batchIdx, batch := range deferredGraphBatches {
		accessToken, err = c.getSalesforceAccessToken(ctx)
		if err != nil {
			log.Errorw("Failed to get Salesforce access token for deferred payload", "batch", batchIdx+1, "error", err)
			return nil, err
		}
		nextGraphs := make([]GraphRequest, 0, len(batch))
		for _, g := range batch {
			nextGraphs = append(nextGraphs, GraphRequest{
				GraphId:          g.GraphId,
				CompositeRequest: replaceRefsInRequests(g.CompositeRequest, refMap),
			})
		}
		nextPayload := &CompositeGraphRequest{Graphs: nextGraphs}
		logCompositePayloadMetrics(log, fmt.Sprintf("deferred_batch_%d", batchIdx+1), nextPayload)
		log.Infow("Composite Graph deferred payload", "batch", batchIdx+1, "graphCount", len(nextGraphs))
		callSequence++
		graphResponse, err = c.executeCompositeGraphRequest(ctx, compositeURL, accessToken, nextPayload)
		if err != nil {
			return nil, err
		}
		phase := fmt.Sprintf("deferred_batch_%d", batchIdx+1)
		mergeGraphResponseIntoResult(&graphResponse, result, refMap, phase, callSequence)
	}

	// Rounds 2+ (Option A multi-call): execute remaining segments with refs resolved from previous rounds.
	// This runs after deferred first-segment batches so cross-round substitutions can resolve parent IDs safely.
	maxRounds := 1
	for _, segs := range remainingSegments {
		if n := len(segs) + 1; n > maxRounds {
			maxRounds = n
		}
	}
	for round := 1; round < maxRounds; round++ {
		accessToken, err = c.getSalesforceAccessToken(ctx)
		if err != nil {
			log.Errorw("Failed to get Salesforce access token for multi-call round", "round", round+1, "error", err)
			return nil, err
		}
		nextGraphs := make([]GraphRequest, 0, len(remainingSegments))
		for chainIdx, segs := range remainingSegments {
			if round-1 >= len(segs) {
				continue
			}
			segmentRequests := replaceRefsInRequests(segs[round-1], refMap)
			if len(segmentRequests) == 0 {
				continue
			}
			nextGraphs = append(nextGraphs, GraphRequest{
				GraphId:          fmt.Sprintf("graph_r%d_c%d", round+1, chainIdx+1),
				CompositeRequest: segmentRequests,
			})
		}
		if len(nextGraphs) == 0 {
			break
		}
		nextPayload := &CompositeGraphRequest{Graphs: nextGraphs}
		logCompositePayloadMetrics(log, fmt.Sprintf("round_%d_multicall", round+1), nextPayload)
		log.Infow("Composite Graph multi-call round", "round", round+1, "graphCount", len(nextGraphs))
		callSequence++
		graphResponse, err = c.executeCompositeGraphRequest(ctx, compositeURL, accessToken, nextPayload)
		if err != nil {
			return nil, err
		}
		phase := fmt.Sprintf("round_%d_multicall", round+1)
		mergeGraphResponseIntoResult(&graphResponse, result, refMap, phase, callSequence)
	}

	for _, emptyRequest := range emptyBodyRequests {
		result[emptyRequest.RefID] = map[string]interface{}{
			"success":     nil,
			"errors":      []interface{}{},
			"message":     "Empty Body",
			"referenceId": emptyRequest.RefID,
			"statusCode":  nil,
			"description": "Request had empty body and was not sent to Salesforce",
			"callPhase":   "not_sent_empty_body",
			"callSequence": 0,
		}
		log.Infow("Added empty body response", "referenceId", emptyRequest.RefID)
	}

	return result, nil
}

// executeCompositeGraphRequest sends one Composite Graph request and returns the parsed response.
// Handles 401 by refreshing the token and retrying once.
// Retries up to 2 times on transient 429 (rate limit) and 503 (service unavailable)
// with exponential back-off.
func (c *SalesforceClient) executeCompositeGraphRequest(ctx context.Context, compositeURL, accessToken string, payload *CompositeGraphRequest) (GraphCompositeResponse, error) {
	log := commoninit.GetLogger(ctx)

	const maxTransientRetries = 2
	var statusCode int
	var responseBody string
	var err error

	for attempt := 0; ; attempt++ {
		httpRequest, reqErr := c.apiClient.CreateJSONRequest(ctx, http.MethodPost, compositeURL, accessToken, payload)
		if reqErr != nil {
			log.Errorw("Failed to create Salesforce API request", "error", reqErr)
			return GraphCompositeResponse{}, reqErr
		}

		statusCode, responseBody, err = c.apiClient.RestExecute(ctx, httpRequest)

		if statusCode == http.StatusUnauthorized {
			log.Warnw("Received 401 from Salesforce, refreshing token", "originalError", err)
			if rl := utility.GetSFRetryLog(ctx); rl != nil {
				rl.Events = append(rl.Events, utility.SFRetryEvent{
					Attempt:    attempt + 1,
					StatusCode: http.StatusUnauthorized,
					Reason:     "token_refresh",
				})
			}
			c.tokenRefreshMutex.Lock()
			c.tokenExpiry = time.Time{}
			c.tokenRefreshMutex.Unlock()
			accessToken, err = c.getSalesforceAccessToken(ctx)
			if err != nil {
				log.Errorw("Failed to refresh Salesforce access token", "error", err)
				return GraphCompositeResponse{}, err
			}
			httpRequest, reqErr = c.apiClient.CreateJSONRequest(ctx, http.MethodPost, compositeURL, accessToken, payload)
			if reqErr != nil {
				return GraphCompositeResponse{}, reqErr
			}
			statusCode, responseBody, err = c.apiClient.RestExecute(ctx, httpRequest)
		}

		if (statusCode == http.StatusTooManyRequests || statusCode == http.StatusServiceUnavailable) && attempt < maxTransientRetries {
			backoff := time.Duration(1<<uint(attempt)) * time.Second // 1s, 2s
			log.Warnw("Transient Salesforce error, retrying", "statusCode", statusCode, "attempt", attempt+1, "backoffMs", backoff.Milliseconds())
			if rl := utility.GetSFRetryLog(ctx); rl != nil {
				rl.Events = append(rl.Events, utility.SFRetryEvent{
					Attempt:    attempt + 1,
					StatusCode: statusCode,
					BackoffMs:  backoff.Milliseconds(),
					Reason:     "transient_sf_error",
				})
			}
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return GraphCompositeResponse{}, ctx.Err()
			case <-timer.C:
			}
			continue
		}

		break
	}

	if err != nil {
		log.Errorw("Failed to execute Salesforce API request", "error", err, "statusCode", statusCode)
		return GraphCompositeResponse{}, err
	}
	if statusCode != http.StatusOK {
		log.Errorw("Salesforce API request failed", "statusCode", statusCode, "response", responseBody)
		return GraphCompositeResponse{}, fmt.Errorf("salesforce API request failed with status code %d: %s", statusCode, responseBody)
	}
	var graphResponse GraphCompositeResponse
	if err := json.Unmarshal([]byte(responseBody), &graphResponse); err != nil {
		log.Errorw("Failed to parse Salesforce graph response", "error", err)
		return GraphCompositeResponse{}, err
	}
	return graphResponse, nil
}

// mergeGraphResponseIntoResult writes all subresponses from graphResponse into result.
// Each success entry gets its own empty errors slice so downstream code cannot mutate a shared backing array.
func mergeGraphResponseIntoResult(graphResponse *GraphCompositeResponse, result map[string]map[string]interface{}, refMap map[string]string, callPhase string, callSequence int) {
	if graphResponse == nil {
		return
	}
	log := commoninit.GetLogger()
	for _, graph := range graphResponse.Graphs {
		for _, subResponse := range graph.GraphResponse.CompositeResponse {
			referenceId := subResponse.ReferenceID
			// Treat 200 OK, 201 Created and 204 No Content as successful responses.
			// 204 is especially important for PATCH requests that legitimately return no body.
			isSuccess := subResponse.HTTPStatusCode == http.StatusOK ||
				subResponse.HTTPStatusCode == http.StatusCreated ||
				subResponse.HTTPStatusCode == http.StatusNoContent
			if !isSuccess {
				result[referenceId] = map[string]interface{}{
					"success":     false,
					"errors":      subResponse.Body,
					"statusCode":  subResponse.HTTPStatusCode,
					"referenceId": referenceId,
					"graphId":     graph.GraphId,
					"callPhase":   callPhase,
					"callSequence": callSequence,
				}
				continue
			}
			if refMap != nil && subResponse.Body != nil {
				if bodyMap, ok := subResponse.Body.(map[string]interface{}); ok {
					if id, ok := bodyMap["id"].(string); ok && id != "" {
						refMap[referenceId] = id
					} else if id, ok := bodyMap["Id"].(string); ok && id != "" {
						refMap[referenceId] = id
					}
				}
			}
			if subResponse.Body == nil {
				result[referenceId] = map[string]interface{}{
					"success": true, "errors": []interface{}{}, "graphId": graph.GraphId,
					"callPhase": callPhase, "callSequence": callSequence,
				}
				continue
			}
			if bodyMap, ok := subResponse.Body.(map[string]interface{}); ok {
				bodyMap["success"] = true
				bodyMap["errors"] = []interface{}{}
				bodyMap["graphId"] = graph.GraphId
				bodyMap["callPhase"] = callPhase
				bodyMap["callSequence"] = callSequence
				result[referenceId] = bodyMap
				continue
			}
			bodyBytes, marshalErr := json.Marshal(subResponse.Body)
			if marshalErr != nil {
				log.Errorw("mergeGraphResponseIntoResult: failed to marshal sub-response body",
					"referenceId", referenceId, "graphId", graph.GraphId, "error", marshalErr)
				result[referenceId] = map[string]interface{}{
					"success": false, "errors": []interface{}{marshalErr.Error()}, "graphId": graph.GraphId,
					"callPhase": callPhase, "callSequence": callSequence,
				}
				continue
			}
			responseData := make(map[string]interface{})
			if unmarshalErr := json.Unmarshal(bodyBytes, &responseData); unmarshalErr != nil {
				log.Errorw("mergeGraphResponseIntoResult: failed to unmarshal sub-response body",
					"referenceId", referenceId, "graphId", graph.GraphId, "error", unmarshalErr)
				result[referenceId] = map[string]interface{}{
					"success": false, "errors": []interface{}{unmarshalErr.Error()}, "graphId": graph.GraphId,
					"callPhase": callPhase, "callSequence": callSequence,
				}
				continue
			}
			responseData["success"] = true
			responseData["errors"] = []interface{}{}
			responseData["graphId"] = graph.GraphId
			responseData["callPhase"] = callPhase
			responseData["callSequence"] = callSequence
			result[referenceId] = responseData
		}
	}
}

func (c *SalesforceClient) buildUpsertCompositeRequest(ctx context.Context, sfRequest []utility.SFRequest) (*CompositeGraphRequest, chainRemainingSegments, []utility.SFRequest, [][]GraphRequest, error) {
	log := commoninit.GetLogger(ctx)

	if len(sfRequest) == 0 {
		return nil, nil, nil, nil, errors.New("no valid subrequests could be created")
	}

	// Filter out requests with empty bodies and track them for final response
	validRequests := make([]utility.SFRequest, 0, len(sfRequest))
	emptyBodyRequests := make([]utility.SFRequest, 0)

	for _, request := range sfRequest {
		if len(request.Body) > 0 {
			validRequests = append(validRequests, request)
		} else {
			log.Warnw("Filtering out request with empty body - will return 'Empty Body' response",
				"referenceId", request.RefID, "url", request.URL)
			emptyBodyRequests = append(emptyBodyRequests, request)
		}
	}

	// Build dependency graph and identify chains with valid requests
	dependencyChains, independentRequests := buildDependencyGraph(ctx, validRequests)

	// Group independent requests by object type
	independentGroups := groupIndependentRequests(ctx, independentRequests)

	// Allocate requests to graphs; chains over maxDifferentNodesPerGraph are split (remaining for follow-up calls)
	graphs, remainingSegments := allocateToGraphs(ctx, dependencyChains, independentGroups)

	// Enforce per-payload limits:
	// - max 15 distinct node types across graphs
	// - max 500 total subrequests across graphs
	firstBatch, deferredBatches := splitGraphsByTotalNodeLimit(graphs, maxDifferentNodesPerGraph, maxSubrequestsPerPayload, maxGraphsPerPayload)

	compositeGraphRequest := &CompositeGraphRequest{
		Graphs: firstBatch,
	}

	log.Info("Composite Graph Request Created",
		"graphCount", len(compositeGraphRequest.Graphs),
		"totalRequests", func() int {
			total := 0
			for _, graph := range compositeGraphRequest.Graphs {
				total += len(graph.CompositeRequest)
			}
			return total
		}(),
		"emptyBodyRequests", len(emptyBodyRequests),
		"chainsWithRemainingSegments", len(remainingSegments),
		"deferredPayloadCount", len(deferredBatches))

	return compositeGraphRequest, remainingSegments, emptyBodyRequests, deferredBatches, nil
}

func (c *SalesforceClient) getSalesforceAccessToken(ctx context.Context) (string, error) {
	log := commoninit.GetLogger(ctx)

	// Use a mutex to prevent concurrent token refreshes
	c.tokenRefreshMutex.Lock()
	defer c.tokenRefreshMutex.Unlock()

	// Check if valid token
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	log.Info("Obtaining new Salesforce access token")

	// Get the configured login URL
	sfBaseURL := commoninit.GetConfigString("salesforce.baseURL")
	sfLoginURL := commoninit.GetConfigString("salesforce.loginURL")

	// If loginURL is not specified, construct it from baseURL
	if sfLoginURL == "" {
		if sfBaseURL == "" {
			sfLoginURL = "https://login.salesforce.com/services/oauth2/token"
		} else {
			// Ensure the URL doesn't end with a slash
			sfBaseURL = strings.TrimSuffix(sfBaseURL, "/")
			// Construct the token URL correctly
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

	username := commoninit.GetConfigString("salesforce.username")
	password := commoninit.GetConfigString("salesforce.password")
	securityToken := commoninit.GetConfigString("salesforce.securityToken")
	clientID := commoninit.GetConfigString("salesforce.clientID")
	clientSecret := commoninit.GetConfigString("salesforce.clientSecret")

	if username == "" || password == "" || clientID == "" || clientSecret == "" {
		return "", errors.New("missing required Salesforce credentials in configuration")
	}

	formData := url.Values{}
	formData.Set("grant_type", "password")
	formData.Set("client_id", clientID)
	formData.Set("client_secret", clientSecret)
	formData.Set("username", username)
	formData.Set("password", password+securityToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sfLoginURL, strings.NewReader(formData.Encode()))
	if err != nil {
		log.Errorw("Failed to create auth request", "error", err)
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := salesforceAuthHTTPClient.Do(req)
	if err != nil {
		log.Errorw("Failed to execute auth request", "error", err)
		return "", err
	}
	defer resp.Body.Close()

	// Read response body into a variable
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Errorw("Failed to read response body", "error", err)
		return "", err
	}
	bodyString := string(bodyBytes)

	// Log response headers for debugging
	log.Info("Auth response headers statusCode: ", resp.StatusCode, " contentType: ", resp.Header.Get("Content-Type"), " contentLength: ", resp.Header.Get("Content-Length"))

	if resp.StatusCode != http.StatusOK {
		log.Errorw("Auth request failed", "statusCode", resp.StatusCode, "response", bodyString)
		return "", fmt.Errorf("auth request failed with status code %d", resp.StatusCode)
	}

	// Check for HTML response (which would cause JSON parsing to fail)
	if strings.HasPrefix(strings.TrimSpace(bodyString), "<") {
		// Log the first 200 chars of the response to see what we're getting
		previewLength := 200
		if len(bodyString) < previewLength {
			previewLength = len(bodyString)
		}
		responsePreview := bodyString[:previewLength]

		log.Errorw("Received HTML instead of JSON from Salesforce",
			"contentType", resp.Header.Get("Content-Type"),
			"responsePreview", responsePreview,
			"loginURL", sfLoginURL)

		return "", fmt.Errorf("received HTML instead of JSON from Salesforce - check credentials and endpoint URL")
	}

	// Log successful response without trying to log resp.Body directly
	log.Info("Auth request successful statusCode: ", resp.StatusCode)

	// Use the already read body for JSON decoding
	var authResponse SalesforceAuthResponse
	if err := json.Unmarshal(bodyBytes, &authResponse); err != nil {
		// If unmarshal fails, log more context about the response
		log.Errorw("Failed to parse auth response",
			"error", err,
			"contentType", resp.Header.Get("Content-Type"),
			"responseStart", bodyString[:min(100, len(bodyString))])
		return "", err
	}

	// token expiry (default = 1 hour)
	expiresIn := authResponse.ExpiresIn
	if expiresIn == 0 {
		expiresIn = 3600 // Default to 1 hour
	}

	// Set 5 minutes buffer before actual expiry
	buffer := 5 * time.Minute

	c.accessToken = authResponse.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(expiresIn)*time.Second - buffer)

	log.Info("Successfully obtained Salesforce access token expiresAt: ", c.tokenExpiry, " instanceURL: ", authResponse.InstanceURL, " tokenType: ", authResponse.TokenType)

	return c.accessToken, nil
}

// min/max for int are available as Go 1.23 builtins; removed custom shadowing versions.

// extractDependencies extracts parent reference IDs from @{referenceId.field} patterns
// Optimized version with object pooling and reduced allocations
func extractDependencies(request utility.SFRequest) []string {
	// Get pooled slice for dependencies
	dependencies := stringPool.Get().([]string)
	dependencies = dependencies[:0] // Reset length but keep capacity
	defer stringPool.Put(dependencies)

	seen := make(map[string]bool, 8)
	addMatches := func(text string) {
		if text == "" {
			return
		}
		matches := dependencyRegex.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			if len(match) > 1 && !seen[match[1]] {
				seen[match[1]] = true
				dependencies = append(dependencies, match[1])
			}
		}
	}

	// Dependencies can exist in body and URL (e.g., /sobjects/X/@{Parent.id})
	bodyBytes, err := json.Marshal(request.Body)
	if err == nil {
		addMatches(string(bodyBytes))
	}
	addMatches(request.URL)

	if len(dependencies) == 0 {
		return nil
	}
	result := make([]string, len(dependencies))
	copy(result, dependencies)
	return result
}

// replaceRefsInValue recursively replaces @{refId.field} in string values with refMap[refId].
// Used for multi-call: later rounds get body/URL with actual IDs instead of references.
func replaceRefsInValue(v interface{}, refMap map[string]string) interface{} {
	if len(refMap) == 0 {
		return v
	}
	switch val := v.(type) {
	case string:
		return refReplacementRe.ReplaceAllStringFunc(val, func(match string) string {
			subs := refReplacementRe.FindStringSubmatch(match)
			if len(subs) < 2 {
				return match
			}
			refKey := subs[1] // e.g. "CIBIL_Multibureau_Data_Post.id"
			if dot := strings.IndexByte(refKey, '.'); dot > 0 {
				refId := refKey[:dot]
				if id, ok := refMap[refId]; ok {
					return id
				}
			}
			return match
		})
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, vv := range val {
			out[k] = replaceRefsInValue(vv, refMap)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, vv := range val {
			out[i] = replaceRefsInValue(vv, refMap)
		}
		return out
	default:
		return v
	}
}

// replaceRefsInRequests returns a copy of requests with body and URL updated using refMap (referenceId -> id).
func replaceRefsInRequests(requests []utility.SFRequest, refMap map[string]string) []utility.SFRequest {
	if len(refMap) == 0 {
		return requests
	}
	out := make([]utility.SFRequest, len(requests))
	for i := range requests {
		r := requests[i]
		out[i] = utility.SFRequest{
			URL:    r.URL,
			Method: r.Method,
			RefID:  r.RefID,
			Body:   nil,
		}
		if r.URL != "" {
			out[i].URL = refReplacementRe.ReplaceAllStringFunc(r.URL, func(match string) string {
				subs := refReplacementRe.FindStringSubmatch(match)
				if len(subs) < 2 {
					return match
				}
				refKey := subs[1]
				if dot := strings.IndexByte(refKey, '.'); dot > 0 {
					if id, ok := refMap[refKey[:dot]]; ok {
						return id
					}
				}
				return match
			})
		}
		if r.Body != nil {
			out[i].Body = replaceRefsInValue(r.Body, refMap).(map[string]interface{})
		}
	}
	return out
}

// extractObjectType extracts object type from Salesforce URL
// Optimized version using string operations instead of split
func extractObjectType(url string) string {
	// Pattern: /services/data/v64.0/sobjects/ObjectType__c
	// Find "/sobjects/" and extract the next segment
	sobjIndex := strings.Index(url, "/sobjects/")
	if sobjIndex == -1 {
		return ""
	}

	start := sobjIndex + 10 // len("/sobjects/")
	if start >= len(url) {
		return ""
	}

	// Find the next "/" or end of string
	end := strings.Index(url[start:], "/")
	if end == -1 {
		return url[start:]
	}

	return url[start : start+end]
}

// getNodeInfo creates NodeInfo from request
func getNodeInfo(request utility.SFRequest) NodeInfo {
	return NodeInfo{
		ObjectType: extractObjectType(request.URL),
		Method:     request.Method,
	}
}

// buildDependencyGraph builds dependency chains from SF requests
// Optimized version with object pooling and pre-allocation
func buildDependencyGraph(ctx context.Context, sfRequests []utility.SFRequest) ([]DependencyChain, []utility.SFRequest) {
	log := commoninit.GetLogger(ctx)

	// Get pooled map for nodes
	nodeMap := nodeMapPool.Get().(map[string]*DependencyNode)
	defer func() {
		// Clear map and return to pool
		for k := range nodeMap {
			delete(nodeMap, k)
		}
		nodeMapPool.Put(nodeMap)
	}()

	// Pre-allocate slices with known capacity
	allNodes := make([]*DependencyNode, 0, len(sfRequests))
	chains := make([]DependencyChain, 0, len(sfRequests)/4) // Estimate 25% will be in chains
	independent := make([]utility.SFRequest, 0, len(sfRequests))

	// Create nodes with pre-allocated slices
	for i := range sfRequests {
		node := &DependencyNode{
			Request:   sfRequests[i],
			Children:  make([]*DependencyNode, 0, 2), // Most nodes have 0-2 children
			Parents:   make([]*DependencyNode, 0, 1), // Most nodes have 0-1 parent
			Processed: false,
		}
		nodeMap[sfRequests[i].RefID] = node
		allNodes = append(allNodes, node)
	}

	// Build parent-child relationships
	for _, node := range allNodes {
		dependencies := extractDependencies(node.Request)
		for _, parentRefId := range dependencies {
			if parentNode, exists := nodeMap[parentRefId]; exists {
				node.Parents = append(node.Parents, parentNode)
				parentNode.Children = append(parentNode.Children, node)
			}
		}
	}

	// Identify dependency chains and independent nodes
	for _, node := range allNodes {
		if node.Processed {
			continue
		}

		// Check if node is part of any dependency (has parents or children)
		if len(node.Parents) == 0 && len(node.Children) == 0 {
			// Independent node
			independent = append(independent, node.Request)
			node.Processed = true
		} else {
			// Part of dependency chain - find the root and collect all connected nodes
			chain := collectDependencyChain(node)
			if len(chain.AllNodes) > 0 {
				chains = append(chains, chain)
			}
		}
	}

	log.Infow("Dependency analysis completed",
		"totalRequests", len(sfRequests),
		"dependencyChains", len(chains),
		"independentRequests", len(independent))

	return chains, independent
}

// collectDependencyChain collects all nodes in a dependency chain
// Optimized version with pooled objects and iterative approach
func collectDependencyChain(startNode *DependencyNode) DependencyChain {
	// Get pooled map for visited tracking
	visited := visitedMapPool.Get().(map[string]bool)
	defer func() {
		// Clear map and return to pool
		for k := range visited {
			delete(visited, k)
		}
		visitedMapPool.Put(visited)
	}()

	allNodes := make([]*DependencyNode, 0, 8)  // Pre-allocate for typical chain size
	rootNodes := make([]*DependencyNode, 0, 2) // Most chains have 1-2 roots

	// Use iterative DFS with stack to avoid recursion overhead
	stack := make([]*DependencyNode, 0, 16)
	stack = append(stack, startNode)

	for len(stack) > 0 {
		// Pop from stack
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if visited[node.Request.RefID] {
			continue
		}

		visited[node.Request.RefID] = true
		node.Processed = true
		allNodes = append(allNodes, node)

		// Check if this is a root node (no parents)
		if len(node.Parents) == 0 {
			rootNodes = append(rootNodes, node)
		}

		// Add connected nodes to stack
		for _, parent := range node.Parents {
			if !visited[parent.Request.RefID] {
				stack = append(stack, parent)
			}
		}
		for _, child := range node.Children {
			if !visited[child.Request.RefID] {
				stack = append(stack, child)
			}
		}
	}

	return DependencyChain{
		RootNodes: rootNodes,
		AllNodes:  allNodes,
	}
}

// topologicalSort sorts nodes in dependency order (parents before children)
// Optimized version using Kahn's algorithm for better performance
func topologicalSort(chain DependencyChain) []utility.SFRequest {
	// Get pooled slice for result
	result := requestSlicePool.Get().([]utility.SFRequest)
	result = result[:0] // Reset length but keep capacity
	defer requestSlicePool.Put(result)

	// Calculate in-degree for each node
	inDegree := make(map[*DependencyNode]int, len(chain.AllNodes))
	for _, node := range chain.AllNodes {
		inDegree[node] = len(node.Parents)
	}

	// Initialize queue with nodes that have no dependencies (in-degree 0)
	queue := make([]*DependencyNode, 0, len(chain.RootNodes))
	for _, node := range chain.AllNodes {
		if inDegree[node] == 0 {
			queue = append(queue, node)
		}
	}

	// Pre-allocate final result slice
	finalResult := make([]utility.SFRequest, 0, len(chain.AllNodes))

	// Process queue using Kahn's algorithm
	for len(queue) > 0 {
		// Dequeue
		node := queue[0]
		queue = queue[1:]

		// Add to result
		finalResult = append(finalResult, node.Request)

		// Update in-degrees of children
		for _, child := range node.Children {
			inDegree[child]--
			if inDegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}

	return finalResult
}

// countUniqueNodes counts unique object+method combinations
// Optimized version with pre-allocated map and direct string concatenation
func countUniqueNodes(requests []utility.SFRequest) int {
	// Use string keys instead of struct for better performance
	uniqueNodes := make(map[string]bool, len(requests))

	for _, request := range requests {
		objectType := extractObjectType(request.URL)
		key := objectType + "|" + request.Method
		uniqueNodes[key] = true
	}

	return len(uniqueNodes)
}

// countUniqueNodesAcrossGraphs returns the number of distinct (objectType|method) across all graphs.
// Used to enforce Salesforce's per-payload limit (max 15 node types in one composite/graph request).
func countUniqueNodesAcrossGraphs(graphs []GraphRequest) int {
	uniqueNodes := make(map[string]bool)
	for _, g := range graphs {
		for _, request := range g.CompositeRequest {
			objectType := extractObjectType(request.URL)
			key := objectType + "|" + request.Method
			uniqueNodes[key] = true
		}
	}
	return len(uniqueNodes)
}

// splitGraphsByTotalNodeLimit splits graphs so each payload respects both:
// - maxTotalUniqueNodes (distinct objectType|method across all graphs in payload)
// - maxTotalRequests (total subrequests across all graphs in payload)
// The first payload is returned as firstBatch; additional payloads are deferredBatches.
func splitGraphsByTotalNodeLimit(graphs []GraphRequest, maxTotalUniqueNodes int, maxTotalRequests int, maxGraphs int) (firstBatch []GraphRequest, deferredBatches [][]GraphRequest) {
	if len(graphs) == 0 {
		return nil, nil
	}
	if maxTotalUniqueNodes <= 0 && maxTotalRequests <= 0 && maxGraphs <= 0 {
		return graphs, nil
	}

	withinLimits := func(batch []GraphRequest, next GraphRequest) bool {
		if maxGraphs > 0 && len(batch)+1 > maxGraphs {
			return false
		}
		combined := make([]utility.SFRequest, 0, countRequestsAcrossGraphs(batch)+len(next.CompositeRequest))
		for _, prev := range batch {
			combined = append(combined, prev.CompositeRequest...)
		}
		combined = append(combined, next.CompositeRequest...)

		if maxTotalUniqueNodes > 0 {
			totalUnique := countUniqueNodes(combined)
			if totalUnique > maxTotalUniqueNodes {
				return false
			}
		}
		if maxTotalRequests > 0 {
			if len(combined) > maxTotalRequests {
				return false
			}
		}
		return true
	}

	batches := make([][]GraphRequest, 0, 4)
	current := make([]GraphRequest, 0, len(graphs))
	for _, g := range graphs {
		if len(current) == 0 || withinLimits(current, g) {
			current = append(current, g)
			continue
		}
		batches = append(batches, current)
		current = []GraphRequest{g}
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}

	if len(batches) == 0 {
		return nil, nil
	}

	firstBatch = batches[0]
	if len(batches) > 1 {
		deferredBatches = batches[1:]
	}
	return firstBatch, deferredBatches
}

func countRequestsAcrossGraphs(graphs []GraphRequest) int {
	total := 0
	for _, g := range graphs {
		total += len(g.CompositeRequest)
	}
	return total
}

func getGraphPayloadMetrics(payload *CompositeGraphRequest) GraphPayloadMetrics {
	metrics := GraphPayloadMetrics{}
	if payload == nil || len(payload.Graphs) == 0 {
		return metrics
	}

	metrics.GraphCount = len(payload.Graphs)
	metrics.UniqueNodeTypes = countUniqueNodesAcrossGraphs(payload.Graphs)
	for _, g := range payload.Graphs {
		reqCount := len(g.CompositeRequest)
		metrics.TotalRequests += reqCount
		if reqCount > metrics.MaxRequestsInGraph {
			metrics.MaxRequestsInGraph = reqCount
		}
		if reqCount == maxSubrequestsPerPayload {
			metrics.GraphsAtRequestCap500++
		}
		if reqCount > maxSubrequestsPerPayload {
			metrics.GraphsOverRequestCap500++
		}
	}
	return metrics
}

func logCompositePayloadMetrics(log contracts.Logger, phase string, payload *CompositeGraphRequest) {
	if log == nil {
		return
	}
	metrics := getGraphPayloadMetrics(payload)
	log.Infow("Composite graph payload metrics",
		"phase", phase,
		"graphCount", metrics.GraphCount,
		"totalRequests", metrics.TotalRequests,
		"uniqueNodeTypes", metrics.UniqueNodeTypes,
		"maxRequestsInGraph", metrics.MaxRequestsInGraph,
		"graphsAtRequestCap500", metrics.GraphsAtRequestCap500,
		"graphsOverRequestCap500", metrics.GraphsOverRequestCap500,
		"payloadGraphLimit", maxGraphsPerPayload,
		"payloadRequestLimit", maxSubrequestsPerPayload,
		"payloadDifferentNodeLimit", maxDifferentNodesPerGraph)
}

// canAddToGraph checks if requests can be added to graph without exceeding maxDifferentNodesPerGraph
// Optimized version that avoids slice allocation
func canAddToGraph(existingRequests []utility.SFRequest, newRequests []utility.SFRequest) bool {
	if len(existingRequests)+len(newRequests) > maxSubrequestsPerPayload {
		return false
	}

	// Pre-allocate map with estimated capacity
	uniqueNodes := make(map[string]bool, len(existingRequests)+len(newRequests))

	for _, request := range existingRequests {
		objectType := extractObjectType(request.URL)
		uniqueNodes[objectType+"|"+request.Method] = true
	}

	for _, request := range newRequests {
		objectType := extractObjectType(request.URL)
		uniqueNodes[objectType+"|"+request.Method] = true
	}

	return len(uniqueNodes) <= maxDifferentNodesPerGraph
}

// groupIndependentRequests groups independent requests by object type
func groupIndependentRequests(ctx context.Context, independent []utility.SFRequest) [][]utility.SFRequest {
	log := commoninit.GetLogger(ctx)

	// Group by object type
	objectGroups := make(map[string][]utility.SFRequest)
	for _, request := range independent {
		objectType := extractObjectType(request.URL)
		objectGroups[objectType] = append(objectGroups[objectType], request)
	}

	// Split groups that exceed graph limits
	result := [][]utility.SFRequest{}
	for objectType, requests := range objectGroups {
		if countUniqueNodes(requests) <= maxDifferentNodesPerGraph && len(requests) <= maxSubrequestsPerPayload {
			result = append(result, requests)
		} else {
			chunks := splitRequestsByGraphLimits(requests, maxDifferentNodesPerGraph, maxSubrequestsPerPayload)
			result = append(result, chunks...)
		}
		log.Infow("Grouped independent requests", "objectType", objectType, "requestCount", len(requests))
	}

	return result
}

// splitRequestsByGraphLimits splits requests into chunks respecting both:
// - maxNodes: max distinct node types per graph
// - maxRequests: max subrequests per graph
// It preserves input order so dependency topological order remains intact.
func splitRequestsByGraphLimits(requests []utility.SFRequest, maxNodes int, maxRequests int) [][]utility.SFRequest {
	chunks := [][]utility.SFRequest{}
	currentChunk := []utility.SFRequest{}

	for _, request := range requests {
		testChunk := append(currentChunk, request)
		withinNodeLimit := maxNodes <= 0 || countUniqueNodes(testChunk) <= maxNodes
		withinRequestLimit := maxRequests <= 0 || len(testChunk) <= maxRequests
		if withinNodeLimit && withinRequestLimit {
			currentChunk = testChunk
		} else {
			// Current chunk is full, start new one
			if len(currentChunk) > 0 {
				chunks = append(chunks, currentChunk)
			}
			currentChunk = []utility.SFRequest{request}
		}
	}

	// Add the last chunk
	if len(currentChunk) > 0 {
		chunks = append(chunks, currentChunk)
	}

	return chunks
}

// allocateToGraphs allocates requests to graphs respecting per-graph limits:
// - max different node types
// - max subrequests
// Chains that exceed either limit are split: first segment in first payload,
// remaining segments returned for sequential follow-up calls (Option A multi-call).
func allocateToGraphs(ctx context.Context, chains []DependencyChain, independentGroups [][]utility.SFRequest) ([]GraphRequest, chainRemainingSegments) {
	log := commoninit.GetLogger(ctx)

	graphs := make([]GraphRequest, 0, len(chains)+len(independentGroups))
	var remaining chainRemainingSegments
	graphCounter := 1

	for _, chain := range chains {
		sortedRequests := topologicalSort(chain)
		uniqueCount := countUniqueNodes(sortedRequests)

		if uniqueCount <= maxDifferentNodesPerGraph && len(sortedRequests) <= maxSubrequestsPerPayload {
			graph := GraphRequest{
				GraphId:          fmt.Sprintf("graph%d", graphCounter),
				CompositeRequest: sortedRequests,
			}
			graphs = append(graphs, graph)
			graphCounter++
			log.Infow("Created dependency chain graph",
				"graphId", graph.GraphId,
				"requestCount", len(sortedRequests),
				"uniqueNodes", uniqueCount)
			continue
		}

		// Chain exceeds graph limits: segment it; first segment in this payload, rest for follow-up calls
		segments := splitRequestsByGraphLimits(sortedRequests, maxDifferentNodesPerGraph, maxSubrequestsPerPayload)
		graph := GraphRequest{
			GraphId:          fmt.Sprintf("graph%d", graphCounter),
			CompositeRequest: segments[0],
		}
		graphs = append(graphs, graph)
		graphCounter++
		log.Infow("Created dependency chain graph (first segment, multi-call)",
			"graphId", graph.GraphId,
			"requestCount", len(segments[0]),
			"uniqueNodes", countUniqueNodes(segments[0]),
			"totalSegments", len(segments))

		if len(segments) > 1 {
			remaining = append(remaining, segments[1:])
		}
	}

	for _, group := range independentGroups {
		graph := GraphRequest{
			GraphId:          fmt.Sprintf("graph%d", graphCounter),
			CompositeRequest: group,
		}
		graphs = append(graphs, graph)
		graphCounter++
		log.Infow("Created independent group graph",
			"graphId", graph.GraphId,
			"requestCount", len(group),
			"uniqueNodes", countUniqueNodes(group))
	}

	log.Infow("Graph allocation completed", "totalGraphs", len(graphs), "chainsWithRemainingSegments", len(remaining))
	return graphs, remaining
}

// distributeToExistingGraphs distributes requests to graphs with available capacity
func distributeToExistingGraphs(graphs []GraphRequest, requests []utility.SFRequest) {
	for _, request := range requests {
		distributed := false
		for i := range graphs {
			if canAddToGraph(graphs[i].CompositeRequest, []utility.SFRequest{request}) {
				graphs[i].CompositeRequest = append(graphs[i].CompositeRequest, request)
				distributed = true
				break
			}
		}
		if !distributed {
			// If we can't distribute, add to first graph (let SF handle the error)
			graphs[0].CompositeRequest = append(graphs[0].CompositeRequest, request)
		}
	}
}
