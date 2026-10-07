package controller

import (
	"decision-manager/internal/app/constants"
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

// Health Check Controller
type HealthController struct{}

// Status handles GET request and returns version of the application
// @Summary Health Check API
// @Description Returns version of the application
// @Tags Decision Manager
// @Accept json
// @Produce json
// @Success 200 {object} map[string]string
// @Router /health [get]
func (h HealthController) Status(c *gin.Context) {

	version, err := os.ReadFile(constants.GetDeploymentVersionPath)
	if err != nil {
		fmt.Println("failed to read file deployedVersion")
	}
	c.JSON(http.StatusOK,
		gin.H{"Version": string(version)})
	return
}
