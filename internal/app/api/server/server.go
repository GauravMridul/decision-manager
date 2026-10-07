package server

//to be removed

// import (
// 	"decision-manager/internal/app/api/router"
// 	"decision-manager/internal/app/constants"
// 	"decision-manager/pkg/logger"
// 	"strings"

// 	commoninit "decision-manager/internal/app/init"

// 	"github.com/gin-gonic/gin"
// )

// var Router *gin.Engine

// func Init() {
// 	r := router.NewRouter()
// 	port := commoninit.GetConfigString(constants.ServerPort)
// 	// Add debugging to see the actual port value
// 	logger.GetLogger().Infof("Server port from config: '%s'", port)

// 	// Default to :8080 if port is empty
// 	if port == "" {
// 		port = "8080"
// 		logger.GetLogger().Info("Using default port :8080")
// 	}

// 	// Make sure port starts with colon
// 	if !strings.HasPrefix(port, ":") {
// 		port = ":" + port
// 	}
// 	logger.GetLogger().Infof("Starting server on port %s", port)
// 	err := r.Run(port)
// 	if err != nil {
// 		logger.GetLogger().Error("Server not able to startup with error: ", err)
// 	}
// }
