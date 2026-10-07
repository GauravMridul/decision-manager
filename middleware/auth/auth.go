package auth

import (
	"context"
	"decision-manager/internal/app/constants"
	"errors"
	"fmt"
	"net/http"
	"strings"

	commoninit "decision-manager/internal/app/init"

	"github.com/dmi-infotech/common-modules/go/contracts"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/lestrrat-go/jwx/jwk"
)

// Manager handles JWT authentication
type Manager struct {
	log         contracts.Logger
	audience    string
	issuer      string
	jwksURL     string
	environment string
	keySet      jwk.Set
}

// NewManager creates a new auth manager
func NewManager(
	log contracts.Logger,
	audience string,
	issuer string,
	jwksURL string,
	environment string,
) (*Manager, error) {
	if audience == "" || issuer == "" || jwksURL == "" {
		return nil, errors.New("missing required authentication configuration")
	}

	// Fetch the JWK Set from the provided URL
	keySet, err := jwk.Fetch(context.Background(), jwksURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWK set: %w", err)
	}

	return &Manager{
		log:         log,
		audience:    audience,
		issuer:      issuer,
		jwksURL:     jwksURL,
		environment: environment,
		keySet:      keySet,
	}, nil
}

// AuthMiddleware returns a Gin middleware for JWT authentication
func (m *Manager) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip auth for all environments
		if true {
			// Allow all requests in all environments
			c.Next()
			return
		}

		// Get the Authorization header
		authHeader := c.GetHeader(constants.Authorization)
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is required"})
			return
		}

		// Check for Bearer token
		if !strings.HasPrefix(authHeader, constants.Bearer) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization format"})
			return
		}

		// Extract the token
		tokenString := strings.TrimPrefix(authHeader, constants.Bearer)
		if tokenString == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Token is required"})
			return
		}

		// Parse the token without verifying the signature yet
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			// Validate the algorithm
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}

			// Get the key ID from the token header
			kid, ok := token.Header["kid"].(string)
			if !ok {
				return nil, errors.New("token does not contain a key ID")
			}

			// Get the key from the JWK Set
			key, found := m.keySet.LookupKeyID(kid)
			if !found {
				// If the key is not found, try refreshing the key set
				refreshedSet, err := jwk.Fetch(context.Background(), m.jwksURL)
				if err != nil {
					return nil, fmt.Errorf("failed to refresh JWK set: %w", err)
				}
				m.keySet = refreshedSet

				// Try again with the refreshed set
				key, found = m.keySet.LookupKeyID(kid)
				if !found {
					return nil, errors.New("key ID not found in JWK Set")
				}
			}

			// Get the raw public key
			var rawKey interface{}
			if err := key.Raw(&rawKey); err != nil {
				return nil, fmt.Errorf("failed to get raw key: %w", err)
			}

			return rawKey, nil
		})

		if err != nil {
			m.log.Errorw("token validation error", "error", err.Error())
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			return
		}

		// Check if the token is valid
		if !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			return
		}

		// Verify claims like audience and issuer
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token claims"})
			return
		}

		// Check audience
		if claims["aud"] != m.audience {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token audience"})
			return
		}

		// Check issuer
		if claims["iss"] != m.issuer {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token issuer"})
			return
		}

		// Store claims in the context for later use
		c.Set("claims", claims)
		c.Next()
	}
}

// AuthManager provides a global instance of the Manager
var AuthManager *Manager

// Init initializes the AuthManager instance. JWT is bypassed for all environments
// (including production); API key or other means are used for auth where required.
func Init() {
	log := commoninit.GetLogger(context.Background())
	env := commoninit.GetConfigString(constants.Environment)

	// JWT bypass for all environments (uncomment below and remove bypass block to enable real JWT in production).
	AuthManager = &Manager{
		log:         log,
		audience:    "dev-audience",
		issuer:      "dev-issuer",
		jwksURL:     "https://dev-jwks-url",
		environment: env,
		keySet:      nil, // Not used when bypass is enabled
	}
	log.Info("Auth manager initialized with authentication bypass", "environment", env)

	// Production JWT (requires JWKS_AUDIENCE, JWKS_ISSUER, JWKS_URL in config):
	// if env == constants.Production {
	// 	var err error
	// 	AuthManager, err = NewManager(
	// 		log,
	// 		commoninit.GetConfigString(constants.JWKS_AUDIENCE),
	// 		commoninit.GetConfigString(constants.JWKS_ISSUER),
	// 		commoninit.GetConfigString(constants.JWKS_URL),
	// 		env,
	// 	)
	// 	if err != nil {
	// 		log.Errorw("error while setting up auth manager", "error", err.Error())
	// 		panic(err.Error())
	// 	}
	// 	log.Info("Auth manager initialized for production with JWT")
	// 	return
	// }
}
