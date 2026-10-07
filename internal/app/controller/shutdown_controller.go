package controller

import (
	"net/http"
	"os"
	"syscall"
	"time"

	"decision-manager/internal/app/constants"
	commoninit "decision-manager/internal/app/init"

	"github.com/gin-gonic/gin"
)

// ShutdownController provides testing endpoints related to service shutdown.
type ShutdownController struct{}

// TriggerGracefulShutdown schedules a SIGTERM to the current process after
// returning an HTTP response, allowing graceful shutdown path testing.
func (s ShutdownController) TriggerGracefulShutdown(c *gin.Context) {
	enabled := commoninit.GetConfigBool(constants.ServerShutdownTestEndpointEnabledKey, false)
	if !enabled {
		c.JSON(http.StatusForbidden, gin.H{
			"status":  "disabled",
			"message": "graceful shutdown test endpoint is disabled by configuration",
		})
		return
	}

	env := commoninit.GetConfigString(constants.Environment, constants.DevEnvironment)
	if env == constants.Production {
		c.JSON(http.StatusForbidden, gin.H{
			"status":  "forbidden",
			"message": "graceful shutdown test endpoint is not allowed in production",
		})
		return
	}

	delayMs := commoninit.GetConfigInt(
		constants.ServerShutdownTestEndpointDelayMsKey,
		constants.DefaultShutdownTestEndpointDelayMs,
	)
	if delayMs < 0 {
		delayMs = 0
	}

	log := commoninit.GetLogger(c.Request.Context())
	pid := os.Getpid()
	delay := time.Duration(delayMs) * time.Millisecond

	c.JSON(http.StatusAccepted, gin.H{
		"status":      "accepted",
		"message":     "graceful shutdown scheduled",
		"signal":      "SIGTERM",
		"pid":         pid,
		"delay_ms":    delayMs,
		"environment": env,
	})

	go func() {
		time.Sleep(delay)
		process, err := os.FindProcess(pid)
		if err != nil {
			log.Errorw("Failed to find current process for shutdown test", "pid", pid, "error", err)
			return
		}
		if err := process.Signal(syscall.SIGTERM); err != nil {
			log.Errorw("Failed to signal graceful shutdown for testing", "pid", pid, "error", err)
			return
		}
		log.Infow("Triggered graceful shutdown test signal", "signal", "SIGTERM", "pid", pid, "delay_ms", delayMs)
	}()
}
