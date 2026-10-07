package utility

import (
	"context"
	"decision-manager/internal/app/constants"
	"decision-manager/internal/app/models/decision_manager_models"
	"decision-manager/internal/app/types"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	commoninit "decision-manager/internal/app/init"

	jsoniter "github.com/json-iterator/go"

	"github.com/google/uuid"
)

func UUIDFromString(input string) (u uuid.UUID, err error) {
	err = u.UnmarshalText([]byte(input))
	return
}

func ConvertFloatToString(amount float64) string {
	stringVal := strconv.FormatFloat(amount, 'f', 2, 64)
	return stringVal
}

func FormatDateFromCurrentToNewFormat(date string, currentFormat string, newFormat string) string {
	currentDate, _ := time.Parse(currentFormat, date)
	formattedDate := currentDate.Format(newFormat)
	return formattedDate
}

func GetRequestClientIpAddress(headers map[string]string) string {
	var ipAddress = headers[constants.ForwardedForHeaderKey]
	ipAddress = strings.TrimSpace(ipAddress)
	if len(ipAddress) > 0 {
		ipAddresses := strings.Split(ipAddress, ",")
		if len(ipAddresses) >= 2 {
			return strings.TrimSpace(ipAddresses[1])
		}
		return strings.TrimSpace(ipAddress)
	}
	ipAddress = headers[constants.RealIpHeaderKey]
	ipAddress = strings.TrimSpace(ipAddress)
	if len(ipAddress) > 0 {
		return ipAddress
	}
	return strings.TrimSpace(headers[constants.RemoteAddressHeaderKey])
}

func ConcatinateList(list []string, differentiator string) string {
	return strings.Join(list, differentiator)
}

// ParseSequenceString converts a sequence string like "{1,2;5}" into an array of integers [1,2,5]
func ParseSequenceString(sequenceStr string) []int {
	// Remove curly braces if present
	sequenceStr = strings.Trim(sequenceStr, "{}")

	// Replace semicolons with commas for uniform splitting
	sequenceStr = strings.ReplaceAll(sequenceStr, ";", ",")

	// Split by comma
	parts := strings.Split(sequenceStr, ",")

	result := make([]int, 0, len(parts))

	for _, part := range parts {
		// Trim whitespace
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Convert to integer
		num, err := strconv.Atoi(part)
		if err == nil {
			result = append(result, num)
		}
	}

	return result
}

// ParseNestedSequenceString converts a sequence string like "{1,2,3;5,6;7}" into a nested array
// where semicolons separate the sub-arrays: [[1,2,3], [5,6], [7]]
func ParseNestedSequenceString(sequenceStr string) [][]int {
	sequenceStr = strings.Trim(sequenceStr, "{}")
	if sequenceStr == "" {
		return nil
	}

	groups := strings.Split(sequenceStr, ";")
	result := make([][]int, 0, len(groups))

	for _, group := range groups {
		nums := make([]int, 0, 4)
		start := 0
		for i := 0; i <= len(group); i++ {
			if i == len(group) || group[i] == ',' {
				if start < i {
					numStr := group[start:i]
					if num, err := strconv.Atoi(strings.TrimSpace(numStr)); err == nil {
						nums = append(nums, num)
					}
				}
				start = i + 1
			}
		}
		if len(nums) > 0 {
			result = append(result, nums)
		}
	}

	return result
}

// Pre-compiled regex pattern for better performance
var objectFieldPattern = regexp.MustCompile(`<([A-Za-z0-9_]+)\.([A-Za-z0-9_]+)>`)

// ExtractObjectsFromSingleConfig extracts object fields from a single service config
// This allows for processing configs one at a time in the same loop as other operations
func ExtractObjectsFromSingleConfig(config *decision_manager_models.ServiceSfdcFieldMappingResponse, result map[string]map[string]struct{}) {
	if config.RequestBody == nil {
		return
	}

	var data interface{}
	if err := unmarshalFastSafe(config.RequestBody, &data); err != nil {
		return
	}

	extractFromInterface(data, result)
}

func ExtractObjectsAndFields(serviceConfigs []*decision_manager_models.ServiceSfdcFieldMappingResponse) map[string][]string {
	// Store object -> set of fields
	objectsWithFields := make(map[string]map[string]struct{})

	for _, config := range serviceConfigs {
		ExtractObjectsFromSingleConfig(config, objectsWithFields)
	}

	// Convert to map[string][]string with pre-allocated capacity
	result := make(map[string][]string, len(objectsWithFields))
	for obj, fieldSet := range objectsWithFields {
		fields := make([]string, 0, len(fieldSet))
		for field := range fieldSet {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		result[obj] = fields
	}

	return result
}

// Recursive function to traverse the JSON structure
func extractFromInterface(data interface{}, result map[string]map[string]struct{}) {
	switch v := data.(type) {
	case map[string]interface{}:
		for _, val := range v {
			extractFromInterface(val, result)
		}
	case []interface{}:
		for _, item := range v {
			extractFromInterface(item, result)
		}
	case string:
		matches := objectFieldPattern.FindAllStringSubmatch(v, -1)
		for _, match := range matches {
			if len(match) == 3 {
				obj, field := match[1], match[2]
				if _, ok := result[obj]; !ok {
					result[obj] = make(map[string]struct{})
				}
				result[obj][field] = struct{}{}
			}
		}
	}
}

// Converts Sequence String to Array , example: "{1,2,3;5,6;7}" to [1,2,3,5,6,7]
func ConvertSequenceStringToArray(input string) []int {
	result := make([]int, 0, 10) // Pre-allocate for reasonable size
	currentNum := 0

	for i := 0; i < len(input); i++ {
		char := input[i]
		if char >= '0' && char <= '9' {
			currentNum = currentNum*10 + int(char-'0')
		} else if char == ',' || char == ';' {
			result = append(result, currentNum)
			currentNum = 0
		}
	}

	// Don't forget the last number
	if currentNum > 0 {
		result = append(result, currentNum)
	}

	return result
}

func TransformESAResponse(esaResponseJSON string) (map[string]interface{}, error) {
	// Use json.RawMessage for partial parsing to avoid full unmarshaling
	var esaResponse struct {
		Data        json.RawMessage `json:"data"`
		EsaServices json.RawMessage `json:"esaServices"`
	}

	if err := jsonParser.Unmarshal([]byte(esaResponseJSON), &esaResponse); err != nil {
		return nil, fmt.Errorf("failed to parse ESA response: %v", err)
	}

	// Estimate capacity: assume ~5 data fields + ~4 services
	result := make(map[string]interface{}, 9)

	// Parse and copy data section efficiently
	if len(esaResponse.Data) > 0 {
		var data map[string]interface{}
		if err := jsonParser.Unmarshal(esaResponse.Data, &data); err == nil {
			// Direct assignment instead of loop (compiler optimized)
			for k, v := range data {
				result[k] = v
			}
		}
	}

	// Parse services once and project by serviceName.
	if len(esaResponse.EsaServices) > 0 {
		var services []map[string]interface{}
		if err := jsonParser.Unmarshal(esaResponse.EsaServices, &services); err == nil {
			for _, service := range services {
				serviceName, ok := service["serviceName"].(string)
				if !ok || serviceName == "" {
					continue
				}
				result[serviceName] = service
			}
		}
	}

	return result, nil
}

// func MergeServiceSfdcFieldMappingRequestBody(serviceMappings []*decision_manager_models.ServiceSfdcFieldMappingResponse) ([]types.SFRequest, error) {
// 	// Enhanced merge strategy with performance optimizations:
// 	// 1. Group by URL + Method + Base Reference ID
// 	// 2. Within each group, merge items with merge=true
// 	// 3. Keep merge=false items separate
// 	// 4. Support array expansion (ref1_0, ref1_1 → ref1_merged)

// 	type requestItem struct {
// 		URL         string                 `json:"url"`
// 		Method      string                 `json:"method"`
// 		ReferenceID string                 `json:"referenceId"`
// 		Body        map[string]interface{} `json:"body"`
// 		Merge       string                 `json:"merge"`
// 	}

// 	// Two-phase grouping strategy:
// 	// Phase 1: Group by exact RefIDBase (ref1 with ref1, ref2 with ref2)
// 	// Phase 2: Group remaining different RefIDs by URL+Method if merge=true

// 	// Optimized base reference ID extraction with string interning
// 	extractBaseRefID := func(refID string) string {
// 		// Fast path: check if already processed
// 		if !strings.Contains(refID, "_") {
// 			return refID // No underscore, return as-is
// 		}

// 		// Remove _merged suffix first (optimized)
// 		if strings.HasSuffix(refID, "_merged") {
// 			refID = refID[:len(refID)-7] // Avoid string allocation
// 		}

// 		// Handle array expansion: "ref1_0" → "ref1" (optimized)
// 		if idx := strings.LastIndexByte(refID, '_'); idx != -1 {
// 			suffix := refID[idx+1:]
// 			// Fast numeric check without strconv.Atoi
// 			if len(suffix) > 0 && len(suffix) <= 3 { // Reasonable numeric suffix length
// 				isNumeric := true
// 				for i := 0; i < len(suffix); i++ {
// 					if suffix[i] < '0' || suffix[i] > '9' {
// 						isNumeric = false
// 						break
// 					}
// 				}
// 				if isNumeric {
// 					return refID[:idx] // Return base without allocation
// 				}
// 			}
// 		}
// 		return refID
// 	}

// 	// Pre-calculate total capacity
// 	totalItems := 0
// 	for _, service := range serviceMappings {
// 		// Convert JSON to string before counting
// 		totalItems += strings.Count(string(service.RequestBody), `"url"`)
// 	}

// 	// Optimized: Pre-allocate slice with estimated capacity
// 	allItems := make([]requestItem, 0, totalItems)

// 	for _, service := range serviceMappings {
// 		var requestBodies []requestItem
// 		if err := json.Unmarshal(service.RequestBody, &requestBodies); err != nil {
// 			return nil, err
// 		}
// 		allItems = append(allItems, requestBodies...)
// 	}

// 	// Phase 1: Optimized grouping with pre-sized map
// 	phase1Groups := make(map[string][]requestItem, len(allItems)/4) // Estimate fewer unique RefIDs
// 	for _, item := range allItems {
// 		baseRefID := extractBaseRefID(item.ReferenceID)
// 		phase1Groups[baseRefID] = append(phase1Groups[baseRefID], item)
// 	}

// 	// Phase 2: Optimized cross-RefID merging using hash map lookup
// 	type finalGroup struct {
// 		items      []requestItem
// 		firstRefID string
// 	}

// 	finalGroups := make([]finalGroup, 0, len(phase1Groups)) // Pre-allocate with estimated size

// 	// Optimized: Reduce method validation overhead - only track when needed
// 	var methodTracker map[string]map[string]string
// 	enableMethodValidation := commoninit.GetLogger() != nil // Only validate if logger available
// 	if enableMethodValidation {
// 		methodTracker = make(map[string]map[string]string, len(phase1Groups))
// 	}

// 	// Optimization: Use hash map to track mergeable groups by URL+Method
// 	// Key: "URL|METHOD", Value: index in finalGroups
// 	mergeableGroupIndex := make(map[string]int, len(phase1Groups)/2)

// 	// First, process multi-item groups (same RefID base)
// 	for _, items := range phase1Groups {
// 		if len(items) > 1 {
// 			// Multiple items with same RefIDBase - group them together
// 			finalGroups = append(finalGroups, finalGroup{
// 				items:      items,
// 				firstRefID: items[0].ReferenceID,
// 			})
// 		}
// 	}

// 	// Then, process single-item groups for potential cross-RefID merging
// 	for baseRefID, items := range phase1Groups {
// 		if len(items) == 1 {
// 			// Single item - check if it can merge with other single items ONLY
// 			item := items[0]
// 			if strings.ToLower(item.Merge) == "true" {
// 				// Create merge key for O(1) lookup
// 				mergeKey := fmt.Sprintf("%s|%s", item.URL, item.Method)

// 				if existingGroupIdx, exists := mergeableGroupIndex[mergeKey]; exists {
// 					// Check if existing group is also from single items (not multi-item groups)
// 					existingGroup := &finalGroups[existingGroupIdx]
// 					if len(existingGroup.items) > 0 {
// 						// Only merge with other single-item groups, not multi-item groups
// 						firstExistingItem := existingGroup.items[0]
// 						existingBaseRefID := extractBaseRefID(firstExistingItem.ReferenceID)

// 						// Check if existing group was created from single items
// 						if originalItems, exists := phase1Groups[existingBaseRefID]; exists && len(originalItems) == 1 {
// 							// Safe to merge - both are from single-item groups
// 							existingGroup.items = append(existingGroup.items, item)
// 						} else {
// 							// Existing group is multi-item, create separate group
// 							finalGroups = append(finalGroups, finalGroup{
// 								items:      []requestItem{item},
// 								firstRefID: item.ReferenceID,
// 							})
// 						}
// 					}
// 				} else {
// 					// Create new group and register it for single items only
// 					newGroupIdx := len(finalGroups)
// 					finalGroups = append(finalGroups, finalGroup{
// 						items:      []requestItem{item},
// 						firstRefID: item.ReferenceID,
// 					})
// 					mergeableGroupIndex[mergeKey] = newGroupIdx
// 				}
// 			} else {
// 				// merge=false - keep separate
// 				finalGroups = append(finalGroups, finalGroup{
// 					items:      []requestItem{item},
// 					firstRefID: item.ReferenceID,
// 				})
// 			}
// 		}

// 		// Method validation logging
// 		for _, item := range items {
// 			urlRefKey := fmt.Sprintf("%s|%s", item.URL, baseRefID)
// 			if methodTracker[urlRefKey] == nil {
// 				methodTracker[urlRefKey] = make(map[string]string)
// 			}
// 			if _, exists := methodTracker[urlRefKey][item.Method]; !exists {
// 				methodTracker[urlRefKey][item.Method] = item.ReferenceID
// 				// Check if we already have a different method for this URL+RefIDBase
// 				if len(methodTracker[urlRefKey]) > 1 {
// 					if logger := commoninit.GetLogger(); logger != nil {
// 						methods := make([]string, 0, len(methodTracker[urlRefKey]))
// 						for method := range methodTracker[urlRefKey] {
// 							methods = append(methods, method)
// 						}
// 						logger.Warn(fmt.Sprintf("Different methods found for same URL+RefIDBase: url=%s, baseRefID=%s, methods=%v",
// 							item.URL, baseRefID, methods))
// 					}
// 				}
// 			}
// 		}
// 	}

// 	// Build final result from finalGroups
// 	result := make([]types.SFRequest, 0, len(finalGroups))

// 	for _, group := range finalGroups {
// 		if len(group.items) == 0 {
// 			continue
// 		}

// 		firstItem := group.items[0]

// 		// Check if all items in group have merge=true
// 		allMergeable := true
// 		for _, item := range group.items {
// 			if strings.ToLower(item.Merge) != "true" {
// 				allMergeable = false
// 				break
// 			}
// 		}

// 		if len(group.items) == 1 || !allMergeable {
// 			// Single item or contains non-mergeable items - keep separate
// 			for _, item := range group.items {
// 				result = append(result, types.SFRequest{
// 					URL:    item.URL,
// 					Method: item.Method,
// 					RefID:  item.ReferenceID,
// 					Body:   item.Body,
// 				})
// 			}
// 		} else {
// 			// Multiple mergeable items - merge them
// 			mergedBody := make(map[string]interface{})
// 			for _, item := range group.items {
// 				for k, v := range item.Body {
// 					mergedBody[k] = v // Later values overwrite earlier ones
// 				}
// 			}

// 			// Use first RefID + _merged for multiple items
// 			refID := group.firstRefID
// 			if len(group.items) > 1 {
// 				refID = fmt.Sprintf("%s_merged", group.firstRefID)
// 				// Log successful merge
// 				if logger := commoninit.GetLogger(); logger != nil {
// 					logger.Info(fmt.Sprintf("Merged requests: firstRefID=%s, mergeCount=%d, url=%s, method=%s",
// 						group.firstRefID, len(group.items), firstItem.URL, firstItem.Method))
// 				}
// 			}

// 			result = append(result, types.SFRequest{
// 				URL:    firstItem.URL,
// 				Method: firstItem.Method,
// 				RefID:  refID,
// 				Body:   mergedBody,
// 			})
// 		}
// 	}

// 	return result, nil
// }

var (
	jsonParser    = jsoniter.ConfigCompatibleWithStandardLibrary
	stringMapPool = sync.Pool{
		New: func() interface{} {
			return make(map[string]string, 8)
		},
	}
)

// Hot-path JSON helpers:
// - prefer jsoniter for throughput
// - fallback to encoding/json for compatibility edge-cases
func marshalFastSafe(v interface{}) ([]byte, error) {
	if data, err := jsonParser.Marshal(v); err == nil {
		return data, nil
	}
	return json.Marshal(v)
}

func unmarshalFastSafe(data []byte, v interface{}) error {
	if err := jsonParser.Unmarshal(data, v); err == nil {
		return nil
	}
	return json.Unmarshal(data, v)
}

func marshalIndentFastSafe(v interface{}, prefix, indent string) ([]byte, error) {
	if data, err := jsonParser.MarshalIndent(v, prefix, indent); err == nil {
		return data, nil
	}
	return json.MarshalIndent(v, prefix, indent)
}

// Combined ultra-fast approach. ctx optional; when provided, request-context enriched logger is used.
func FastMergeServiceSfdcFieldMappingRequestBody(ctx context.Context, serviceMappings []*decision_manager_models.ServiceSfdcFieldMappingResponse) ([]types.SFRequestDB, error) {
	log := commoninit.GetLogger(ctx)
	// Enhanced merge strategy - same logic as regular merge function
	type requestItem struct {
		URL              string                       `json:"url"`
		Method           string                       `json:"method"`
		ReferenceID      string                       `json:"referenceId"`
		Body             map[string]interface{}       `json:"body"`
		Merge            string                       `json:"merge"`
		ArrayPath        string                       `json:"arrayPath"`
		Lookup           *types.LookupConfig          `json:"lookup"`
		FilterConditions *types.FilterConditions      `json:"filterConditions"`
		PreProcessing    []types.PreprocessingConfig  `json:"preProcessing"`
		//for debugging merge priority
		ServiceName string `json:"-"`
	}

	// Composite key for proper grouping
	type mergeKey struct {
		URL       string
		Method    string
		RefIDBase string
	}

	type finalGroup struct {
		items      []requestItem
		firstRefID string
	}

	// Fixed base reference ID extraction - properly handle array expansion
	extractBaseRefID := func(refID string) string {
		// Fast path: check if already processed
		if !strings.Contains(refID, "_") {
			return refID // No underscore, return as-is
		}

		// Remove _merged suffix first (optimized)
		refID = strings.TrimSuffix(refID, "_merged")

		// Handle array expansion: "ref1_0" → "ref1"
		// Look for numeric suffix at the end (after last underscore)
		if idx := strings.LastIndexByte(refID, '_'); idx != -1 {
			suffix := refID[idx+1:]
			// Only treat as array expansion if it's purely numeric AND short
			if len(suffix) > 0 && len(suffix) <= 2 { // Array indices are typically 0-99
				isNumeric := true
				for i := 0; i < len(suffix); i++ {
					if suffix[i] < '0' || suffix[i] > '9' {
						isNumeric = false
						break
					}
				}
				if isNumeric {
					// This is a numeric array suffix, remove it
					return refID[:idx] // Return base without numeric suffix
				}
			}
		}
		return refID // Return as-is if not an array expansion
	}

	// Optimized: use an estimated initial capacity and grow as needed.
	allItems := make([]requestItem, 0, len(serviceMappings)*4)

	for _, service := range serviceMappings {
		var requestBodies []requestItem
		if err := jsonParser.Unmarshal(service.RequestBody, &requestBodies); err != nil {
			return nil, err
		}
		//for debugging merge priority
		for i := range requestBodies {
			requestBodies[i].ServiceName = service.ServiceName
			if log != nil {
				log.Info(fmt.Sprintf("FastMerge - After DB unmarshal: service=%s, item[%d]: refID=%s, merge='%s', arrayPath='%s'",
					service.ServiceName, i, requestBodies[i].ReferenceID, requestBodies[i].Merge, requestBodies[i].ArrayPath))
			}
		}
		allItems = append(allItems, requestBodies...)
	}

	// Phase 1: Optimized grouping with pre-sized map
	phase1Groups := make(map[string][]requestItem, len(allItems)/4) // Estimate fewer unique RefIDs
	orderedBaseRefIDs := make([]string, 0, len(allItems)/2)
	seenBaseRefIDs := make(map[string]struct{}, len(allItems)/2)
	for _, item := range allItems {
		baseRefID := extractBaseRefID(item.ReferenceID)
		phase1Groups[baseRefID] = append(phase1Groups[baseRefID], item)
		if _, exists := seenBaseRefIDs[baseRefID]; !exists {
			seenBaseRefIDs[baseRefID] = struct{}{}
			orderedBaseRefIDs = append(orderedBaseRefIDs, baseRefID)
		}
	}

	// Phase 2: Optimized cross-RefID merging using hash map lookup
	finalGroups := make([]finalGroup, 0, len(phase1Groups)) // Pre-allocate with estimated size

	// Optimized: Reduce method validation overhead - only track when needed
	var methodTracker map[string]map[string]string
	enableMethodValidation := log != nil // Only validate if logger available
	if enableMethodValidation {
		methodTracker = make(map[string]map[string]string, len(phase1Groups))
	}

	// Optimization: Use hash map to track mergeable groups by URL+Method
	// Key: "URL|METHOD", Value: index in finalGroups
	mergeableGroupIndex := make(map[string]int, len(phase1Groups)/2)

	// First, process multi-item groups (same RefID base) in stable order
	for _, baseRefID := range orderedBaseRefIDs {
		items := phase1Groups[baseRefID]
		if len(items) > 1 {
			// Multiple items with same RefIDBase - group them together
			finalGroups = append(finalGroups, finalGroup{
				items:      items,
				firstRefID: items[0].ReferenceID,
			})
		}
	}

	// Then, process single-item groups for potential cross-RefID merging in stable order
	for _, baseRefID := range orderedBaseRefIDs {
		items := phase1Groups[baseRefID]
		if len(items) == 1 {
			// Single item - check if it can merge with other single items ONLY
			item := items[0]
			if strings.ToLower(item.Merge) == "true" {
				// Create merge key for O(1) lookup
				mergeKey := fmt.Sprintf("%s|%s", item.URL, item.Method)

				if existingGroupIdx, exists := mergeableGroupIndex[mergeKey]; exists {
					// Check if existing group is also from single items (not multi-item groups)
					existingGroup := &finalGroups[existingGroupIdx]
					if len(existingGroup.items) > 0 {
						// Only merge with other single-item groups, not multi-item groups
						firstExistingItem := existingGroup.items[0]
						existingBaseRefID := extractBaseRefID(firstExistingItem.ReferenceID)

						// Check if existing group was created from single items
						if originalItems, exists := phase1Groups[existingBaseRefID]; exists && len(originalItems) == 1 {
							// Safe to merge - both are from single-item groups
							existingGroup.items = append(existingGroup.items, item)
						} else {
							// Existing group is multi-item, create separate group
							finalGroups = append(finalGroups, finalGroup{
								items:      []requestItem{item},
								firstRefID: item.ReferenceID,
							})
						}
					}
				} else {
					// Create new group and register it for single items only
					newGroupIdx := len(finalGroups)
					finalGroups = append(finalGroups, finalGroup{
						items:      []requestItem{item},
						firstRefID: item.ReferenceID,
					})
					mergeableGroupIndex[mergeKey] = newGroupIdx
				}
			} else {
				// merge=false - keep separate
				finalGroups = append(finalGroups, finalGroup{
					items:      []requestItem{item},
					firstRefID: item.ReferenceID,
				})
			}
		}

		// Optimized method validation logging - only when enabled
		if enableMethodValidation {
			for _, item := range items {
				urlRefKey := fmt.Sprintf("%s|%s", item.URL, baseRefID)
				if methodTracker[urlRefKey] == nil {
					methodTracker[urlRefKey] = make(map[string]string)
				}
				if _, exists := methodTracker[urlRefKey][item.Method]; !exists {
					methodTracker[urlRefKey][item.Method] = item.ReferenceID
					// Check if we already have a different method for this URL+RefIDBase
					if len(methodTracker[urlRefKey]) > 1 {
						methods := make([]string, 0, len(methodTracker[urlRefKey]))
						for method := range methodTracker[urlRefKey] {
							methods = append(methods, method)
						}
						log.Warn(fmt.Sprintf("FastMerge - Different methods found for same URL+RefIDBase: url=%s, baseRefID=%s, methods=%v",
							item.URL, baseRefID, methods))
					}
				}
			}
		}
	}

	// Build final result from finalGroups
	result := make([]types.SFRequestDB, 0, len(finalGroups))

	for _, group := range finalGroups {
		if len(group.items) == 0 {
			continue
		}

		firstItem := group.items[0]

		// Check if all items in group have merge=true
		allMergeable := true
		for _, item := range group.items {
			if strings.ToLower(item.Merge) != "true" {
				allMergeable = false
				break
			}
		}

		if len(group.items) == 1 || !allMergeable {
			// Single item or contains non-mergeable items - keep separate
			for _, item := range group.items {
				result = append(result, types.SFRequestDB{
					URL:              item.URL,
					Method:           item.Method,
					RefID:            item.ReferenceID,
					Body:             item.Body,
					ArrayPath:        item.ArrayPath,
					Lookup:           item.Lookup,
					Merge:            item.Merge, // Preserve merge field
					FilterConditions: item.FilterConditions,
					PreProcessing:    item.PreProcessing,
				})
			}
		} else {
			// Multiple mergeable items - merge them
			mergedBody := make(map[string]interface{})
			var winningServiceByField map[string]string
			var overwrittenFields map[string]struct{}
			if log != nil {
				winningServiceByField = stringMapPool.Get().(map[string]string)
				for k := range winningServiceByField {
					delete(winningServiceByField, k)
				}
				overwrittenFields = make(map[string]struct{}, 8)
			}
			for _, item := range group.items {
				for k, v := range item.Body {
					if winningServiceByField != nil {
						if _, exists := mergedBody[k]; exists {
							overwrittenFields[k] = struct{}{}
						}
						winningServiceByField[k] = item.ServiceName
					}
					mergedBody[k] = v
				}
			}

			// Use first RefID + _merged for multiple items
			refID := group.firstRefID
			if len(group.items) > 1 {
				refID = fmt.Sprintf("%s_merged", group.firstRefID)
				if log != nil {
					log.Info(fmt.Sprintf("FastMerge - Merged requests: firstRefID=%s, mergeCount=%d, url=%s, method=%s",
						group.firstRefID, len(group.items), firstItem.URL, firstItem.Method))
					if len(overwrittenFields) > 0 {
						winnerByField := make(map[string]string, len(overwrittenFields))
						for field := range overwrittenFields {
							winnerByField[field] = winningServiceByField[field]
						}
						log.Infow("FastMerge - Overlapping fields resolved by service order",
							"refID", group.firstRefID,
							"winningServiceByField", winnerByField)
					}
				}
			}

			// Return winningServiceByField to pool only AFTER all reads are done
			if winningServiceByField != nil {
				for k := range winningServiceByField {
					delete(winningServiceByField, k)
				}
				stringMapPool.Put(winningServiceByField)
			}

			// Collect preProcessing rules from all items in the merged group.
			// Dedup by storeAs (first occurrence wins) keeps the final list tight
			// without altering apply-order semantics handled later in ApplyPreprocessings.
			var mergedPreProcessing []types.PreprocessingConfig
			if cap(group.items) > 0 {
				var seenStoreAs map[string]struct{}
				for _, item := range group.items {
					if len(item.PreProcessing) == 0 {
						continue
					}
					if seenStoreAs == nil {
						seenStoreAs = make(map[string]struct{}, len(item.PreProcessing))
					}
					for _, pp := range item.PreProcessing {
						if _, dup := seenStoreAs[pp.StoreAs]; dup {
							continue
						}
						seenStoreAs[pp.StoreAs] = struct{}{}
						mergedPreProcessing = append(mergedPreProcessing, pp)
					}
				}
			}

			result = append(result, types.SFRequestDB{
				URL:              firstItem.URL,
				Method:           firstItem.Method,
				RefID:            refID,
				Body:             mergedBody,
				ArrayPath:        firstItem.ArrayPath,
				Lookup:           firstItem.Lookup,
				Merge:            firstItem.Merge, // Preserve merge field from first item
				FilterConditions: firstItem.FilterConditions,
				PreProcessing:    mergedPreProcessing,
			})
		}
	}

	return result, nil
}
