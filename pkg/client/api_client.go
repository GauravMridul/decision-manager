package client

//to be removed

// import (
// 	"context"
// 	"decision-manager/internal/app/constants"
// 	"decision-manager/internal/app/utility"
// 	"decision-manager/pkg/logger"
// 	"encoding/json"
// 	"errors"
// 	"io"
// 	"io/ioutil"
// 	"net/http"
// 	"net/url"
// 	"strconv"
// 	"strings"
// 	"time"

// 	commoninit "decision-manager/internal/app/init"
// )

// type IApiClient interface {
// 	RestExecute(ctx context.Context, r *http.Request) (int, string, error)
// 	RestExecuteWithRetriesAndTimeout(ctx context.Context, r *http.Request, maxRetryCount int, timeout int) (int, string, error)
// 	CreateJSONRequest(ctx context.Context, httpMethod, absoluteURL, accessToken string, requestBody interface{}) (*http.Request, error)
// 	CreateJSONRequestWithHeaders(ctx context.Context, httpMethod, absoluteURL string, headers map[string]string, requestBody interface{}) (*http.Request, error)
// 	CreateJSONRequestWithUrlEncodedHeaders(ctx context.Context, httpMethod, absoluteURL string, headers map[string]string) (*http.Request, error)
// 	RestExecuteWithTimeOut(ctx context.Context, request *http.Request, timeOutInSeconds int) (int, string, error)
// 	RestExecuteWithRetries(ctx context.Context, request *http.Request, maxRetryCount int) (int, string, error)
// }

// type ApiClient struct {
// }

// // NewApiClient :
// func NewApiClient() *ApiClient {
// 	service := &ApiClient{}
// 	return service
// }

// // RestExecute : An abstraction to call a rest API
// func (a ApiClient) RestExecute(ctx context.Context, request *http.Request) (int, string, error) {

// 	log := logger.GetLogger(ctx)
// 	restExecuteTimeoutInSeconds := commoninit.GetConfigString("RestExecuteTimeoutInSeconds")
// 	restExecuteTimeoutInSecondsInt, _ := strconv.Atoi(restExecuteTimeoutInSeconds)
// 	client := &http.Client{Timeout: time.Duration(restExecuteTimeoutInSecondsInt) * time.Second}
// 	log.Debugw("Invoking API. ", "URL: ", request.URL.String(), "Method: ", request.Method)

// 	requestedTime := time.Now()
// 	response, respErr := client.Do(request)
// 	if respErr != nil || response == nil {
// 		log.Errorw("Error or nil response while invoking API", "URI", request.URL.String(), "Method", request.Method, "Response", response)
// 		return http.StatusInternalServerError, "", respErr
// 	}

// 	responseTime := time.Since(requestedTime)
// 	log.Infow("Received response from Resource: ", "URL: ", request.URL.String(), "Method: ", request.Method, " StatusCode: ", response.Status, " Duration: ", responseTime)

// 	defer response.Body.Close()

// 	if response.StatusCode == 401 {
// 		log.Warnw("Unauthorized resource.", "request ", request, " URL: ", request.URL.String(), " Method:", request.Method)
// 		// this error message is specific and maybe used for retrying with fresh token
// 		return response.StatusCode, "", errors.New("Unauthorized resource")
// 	}

// 	responseData, err := io.ReadAll(response.Body)
// 	if err != nil {
// 		log.Errorw("Unable to read response body", "Error : ", err)
// 		return response.StatusCode, "", errors.New("Unable to read response body")
// 	}

// 	responseString := string(responseData)
// 	return response.StatusCode, responseString, nil
// }

// // CreateJSONRequest : Creates a JSON HTTP request
// func (a ApiClient) CreateJSONRequest(ctx context.Context, httpMethod, absoluteURL, accessToken string, requestBody interface{}) (*http.Request, error) {

// 	log := logger.GetLogger(ctx)
// 	headers := make(map[string]string, 1)
// 	headers["Authorization"] = "Bearer " + accessToken
// 	// headers[constants.CorrelationId], _ = correlation.FromContext(ctx)
// 	headers[constants.ForwardedForHeaderKey] = utility.ContextForwardedIP(ctx)
// 	log.Infow("Creating JSON Request. ", "URL: ", absoluteURL, "Method: ", httpMethod, "ClientIP : ", headers[constants.ForwardedForHeaderKey])

// 	u, urlErr := url.ParseRequestURI(absoluteURL)
// 	if urlErr != nil {
// 		log.Errorw("Invalid zest api request path.", "ResourcePath:", absoluteURL)
// 		return nil, urlErr
// 	}

// 	httpMethod = strings.ToUpper(httpMethod)

// 	requestJSONBody := ""
// 	if httpMethod != "GET" && requestBody != nil {
// 		jsonValue, merr := json.Marshal(requestBody)
// 		if merr != nil {
// 			log.Errorw("Unable to marshal request body.", "RequestURL: ", u.String(), "Method: ", httpMethod)
// 			return nil, errors.New("Unable to marshal request body")
// 		}
// 		requestJSONBody = string(jsonValue)
// 	}

// 	request, requestError := http.NewRequest(httpMethod, u.String(), strings.NewReader(requestJSONBody))

// 	if requestError != nil {
// 		log.Errorw("Unable to create request.", "URL: ", u.String(), "ErrorMessage: ", requestError.Error())
// 		return nil, requestError
// 	}

// 	for k, v := range headers {
// 		request.Header.Add(k, v)
// 	}
// 	request.Header.Add("Content-Type", "application/json")
// 	request.Header.Add("Content-Length", strconv.Itoa(len(requestJSONBody)))

// 	return request, nil
// }

// func (a ApiClient) CreateJSONRequestWithHeaders(ctx context.Context, httpMethod, absoluteURL string, headers map[string]string, requestBody interface{}) (*http.Request, error) {

// 	log := logger.GetLogger(ctx)
// 	headers[constants.ForwardedForHeaderKey] = utility.ContextForwardedIP(ctx)

// 	log.Infow("Creating JSON Request. ", "URL: ", absoluteURL, "Method: ", httpMethod, "ClientIP : ", headers[constants.ForwardedForHeaderKey])

// 	u, urlErr := url.ParseRequestURI(absoluteURL)
// 	if urlErr != nil {
// 		log.Errorw("Invalid zest api request path.", "ResourcePath: ", absoluteURL)
// 		return nil, urlErr
// 	}

// 	httpMethod = strings.ToUpper(httpMethod)

// 	requestJSONBody := ""
// 	if httpMethod != "GET" && requestBody != nil {
// 		jsonValue, merr := json.Marshal(requestBody)
// 		if merr != nil {
// 			log.Errorw("Unable to marshal request body. ", " RequestURL: ", u.String(), " Method: ", httpMethod)
// 			return nil, errors.New("Unable to marshal request body")
// 		}
// 		requestJSONBody = string(jsonValue)
// 	}

// 	request, requestError := http.NewRequest(httpMethod, u.String(), strings.NewReader(requestJSONBody))

// 	if requestError != nil {
// 		log.Errorw("Unable to create request.", "URL: ", u.String(), "ErrorMessage: ", requestError.Error())
// 		return nil, requestError
// 	}

// 	for k, v := range headers {
// 		request.Header.Add(k, v)
// 	}
// 	request.Header.Add("Content-Type", "application/json")
// 	request.Header.Add("Content-Length", strconv.Itoa(len(requestJSONBody)))

// 	return request, nil
// }

// // RestExecuteWithRetriesAndTimeout : An abstraction to call a rest API
// func (a ApiClient) RestExecuteWithRetriesAndTimeout(ctx context.Context, request *http.Request, maxRetryCount int, timeout int) (int, string, error) {

// 	log := logger.GetLogger(ctx)
// 	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
// 	log.Infow("Invoking API. ", "URL: ", request.URL.String(), "Method: ", request.Method)

// 	var response *http.Response
// 	var respErr error
// 	if maxRetryCount <= 0 {
// 		maxRetryCount = 1
// 	}
// 	for i := 0; i < maxRetryCount; i++ {
// 		requestedTime := time.Now()
// 		response, respErr = client.Do(request)
// 		responseTime := time.Since(requestedTime)
// 		if respErr == nil && response.StatusCode < 500 {
// 			log.Infow("Received response from Resource: ", "URL: ", request.URL.String(), "Method: ", request.Method, " StatusCode: ", response.Status, " Duration: ", responseTime)
// 			break
// 		}
// 	}

// 	// All the retries exhausted and it is still failing
// 	if respErr != nil {
// 		log.Errorw("Retries exhausted and still Error while invoking API", "URI", request.URL.String(), "Method", request.Method, "Error", respErr)
// 		return http.StatusInternalServerError, constants.EmptyString, respErr
// 	}

// 	defer response.Body.Close()

// 	if response.StatusCode == http.StatusUnauthorized {
// 		log.Errorw("Unauthorized resource.", "request ", request, " URL: ", request.URL.String(), " Method:", request.Method)
// 		return response.StatusCode, constants.EmptyString, errors.New("Unauthorized resource")
// 	}

// 	responseData, err := ioutil.ReadAll(response.Body)
// 	if err != nil {
// 		log.Errorw("Unable to read response body", "Error : ", err)
// 		return response.StatusCode, constants.EmptyString, errors.New("Unable to read response body")
// 	}

// 	responseString := string(responseData)
// 	return response.StatusCode, responseString, nil
// }

// func (a ApiClient) CreateJSONRequestWithUrlEncodedHeaders(ctx context.Context, httpMethod, absoluteURL string, headers map[string]string) (*http.Request, error) {

// 	log := logger.GetLogger(ctx)
// 	u, urlErr := url.ParseRequestURI(absoluteURL)
// 	if urlErr != nil {
// 		log.Warnw("Invalid zest api request path.", "ResourcePath: ", absoluteURL)
// 		return nil, urlErr
// 	}
// 	urlStr := u.String()
// 	httpMethod = strings.ToUpper(httpMethod)
// 	requestBody := url.Values{}

// 	for k, v := range headers {
// 		requestBody.Set(k, v)
// 	}

// 	request, requestError := http.NewRequest(httpMethod, urlStr, strings.NewReader(requestBody.Encode()))
// 	if requestError != nil {
// 		log.Warnw("Unable to create request.", "URL: ", urlStr, "ErrorMessage: ", requestError.Error())
// 		return nil, requestError
// 	}

// 	request.Header.Add("Content-Type", "application/x-www-form-urlencoded")
// 	request.Header.Add("Content-Length", strconv.Itoa(len(requestBody)))

// 	return request, nil
// }

// // RestExecuteWithTimeOut : An abstraction to call a rest API with input Timeout
// func (a ApiClient) RestExecuteWithTimeOut(ctx context.Context, request *http.Request, timeOutInSeconds int) (int, string, error) {

// 	log := logger.GetLogger(ctx)
// 	client := &http.Client{Timeout: time.Duration(timeOutInSeconds) * time.Second}
// 	log.Debugw("Invoking API. ", "URL: ", request.URL.String(), "Method: ", request.Method)

// 	requestedTime := time.Now()
// 	response, respErr := client.Do(request)
// 	if respErr != nil || response == nil {
// 		log.Warnw("Error or nil response while invoking API", "URI", request.URL.String(), "Method", request.Method, "Response", response)
// 		return http.StatusInternalServerError, constants.EmptyString, respErr
// 	}

// 	responseTime := time.Since(requestedTime)
// 	log.Infow("Received response from Resource: ", "URL: ", request.URL.String(), "Method: ", request.Method, " StatusCode: ", response.Status, " Duration: ", responseTime)

// 	defer response.Body.Close()

// 	if response.StatusCode == http.StatusUnauthorized {
// 		log.Warnw("Unauthorized resource.", "request ", request, " URL: ", request.URL.String(), " Method:", request.Method)
// 		// this error message is specific and maybe used for retrying with fresh token
// 		return response.StatusCode, constants.EmptyString, errors.New("Unauthorized resource")
// 	}

// 	responseData, err := ioutil.ReadAll(response.Body)
// 	if err != nil {
// 		log.Warnw("Unable to read response body", "Error : ", err)
// 		return response.StatusCode, constants.EmptyString, errors.New("Unable to read response body")
// 	}

// 	responseString := string(responseData)
// 	return response.StatusCode, responseString, nil
// }

// // RestExecuteWithRetries : An abstraction to call a rest API
// func (a ApiClient) RestExecuteWithRetries(ctx context.Context, request *http.Request, maxRetryCount int) (int, string, error) {

// 	log := logger.GetLogger(ctx)
// 	restExecuteTimeoutInSeconds := commoninit.GetConfigString("RestExecuteTimeoutInSeconds")
// 	restExecuteTimeoutInSecondsInt, _ := strconv.Atoi(restExecuteTimeoutInSeconds)
// 	client := &http.Client{Timeout: time.Duration(restExecuteTimeoutInSecondsInt) * time.Second}
// 	log.Infow("Invoking API. ", "URL: ", request.URL.String(), "Method: ", request.Method)

// 	var response *http.Response
// 	var respErr error
// 	if maxRetryCount <= 0 {
// 		maxRetryCount = 1
// 	}
// 	for i := 0; i < maxRetryCount; i++ {
// 		requestedTime := time.Now()
// 		response, respErr = client.Do(request)
// 		responseTime := time.Since(requestedTime)
// 		if respErr == nil && response.StatusCode < 500 {
// 			log.Infow("Received response from Resource: ", "URL: ", request.URL.String(), "Method: ", request.Method, " StatusCode: ", response.Status, " Duration: ", responseTime)
// 			break
// 		}
// 		log.Infow("Retrying for resource: ", "URL: ", request.URL.String())
// 	}

// 	// All the retries exhausted and it is still failing
// 	if respErr != nil {
// 		log.Errorw("Retries exhausted and still Error while invoking API", "URI", request.URL.String(), "Method", request.Method, "Error", respErr)
// 		return http.StatusInternalServerError, constants.EmptyString, respErr
// 	}

// 	defer response.Body.Close()

// 	if response.StatusCode == http.StatusUnauthorized {
// 		log.Errorw("Unauthorized resource.", "request ", request, " URL: ", request.URL.String(), " Method:", request.Method)
// 		return response.StatusCode, constants.EmptyString, errors.New("Unauthorized resource")
// 	}

// 	responseData, err := ioutil.ReadAll(response.Body)
// 	if err != nil {
// 		log.Errorw("Unable to read response body", "Error : ", err)
// 		return response.StatusCode, constants.EmptyString, errors.New("Unable to read response body")
// 	}

// 	responseString := string(responseData)
// 	return response.StatusCode, responseString, nil
// }
